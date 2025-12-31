// Package core implements the local puller service.
package core

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/events"
	"github.com/codetrek/syntrix/internal/puller/internal/buffer"
	"github.com/codetrek/syntrix/internal/puller/internal/checkpoint"
	"github.com/codetrek/syntrix/internal/puller/internal/normalizer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// EventHandler is a function that handles events from the change stream.
// Note: This type must match the signature in grpc.EventSource interface.
type EventHandler = func(ctx context.Context, backendName string, event *events.NormalizedEvent) error

// Backend represents a single MongoDB backend being watched.
type Backend struct {
	name       string
	client     *mongo.Client
	db         *mongo.Database
	config     config.PullerBackendConfig
	normalizer *normalizer.Normalizer
	tracker    *checkpoint.Tracker
	buffer     *buffer.Buffer

	// eventChan receives normalized events from the change stream
	eventChan chan *events.NormalizedEvent
}

// Puller is the main puller service that watches MongoDB change streams
// and distributes events to consumers.
type Puller struct {
	cfg      config.PullerConfig
	backends map[string]*Backend
	logger   *slog.Logger

	// eventHandler is called for each normalized event
	eventHandler EventHandler

	// wg tracks running goroutines
	wg sync.WaitGroup

	// cancel function to stop all backends
	cancel context.CancelFunc
}

// New creates a new Puller instance.
func New(cfg config.PullerConfig, logger *slog.Logger) *Puller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Puller{
		cfg:      cfg,
		backends: make(map[string]*Backend),
		logger:   logger.With("component", "puller"),
	}
}

// SetEventHandler sets the event handler for processing events.
func (p *Puller) SetEventHandler(handler EventHandler) {
	p.eventHandler = handler
}

// AddBackend adds a MongoDB backend to watch.
func (p *Puller) AddBackend(name string, client *mongo.Client, dbName string, cfg config.PullerBackendConfig) error {
	if _, exists := p.backends[name]; exists {
		return fmt.Errorf("backend %q already exists", name)
	}

	logger := p.logger.With("backend", name)
	db := client.Database(dbName)
	buf, err := buffer.New(buffer.Options{
		Path:          filepath.Join(p.cfg.Buffer.Path, name),
		Logger:        logger,
		BatchSize:     p.cfg.Buffer.BatchSize,
		BatchInterval: p.cfg.Buffer.BatchInterval,
		QueueSize:     p.cfg.Buffer.QueueSize,
	})
	if err != nil {
		return fmt.Errorf("failed to create buffer: %w", err)
	}

	policy := checkpoint.Policy{
		Interval:   p.cfg.Checkpoint.Interval,
		EventCount: p.cfg.Checkpoint.EventCount,
		OnShutdown: true,
	}

	p.backends[name] = &Backend{
		name:       name,
		client:     client,
		db:         db,
		config:     cfg,
		normalizer: normalizer.New(nil),
		tracker:    checkpoint.NewTracker(policy),
		buffer:     buf,
		eventChan:  make(chan *events.NormalizedEvent, 1000),
	}

	p.logger.Info("added backend", "name", name, "database", dbName)
	return nil
}

// Start starts watching all backends.
func (p *Puller) Start(ctx context.Context) error {
	if len(p.backends) == 0 {
		return fmt.Errorf("no backends configured")
	}

	ctx, p.cancel = context.WithCancel(ctx)

	for name, backend := range p.backends {
		p.wg.Add(1)
		go p.runBackend(ctx, name, backend)
	}

	p.logger.Info("puller started", "backends", len(p.backends))
	return nil
}

// Stop stops all backends gracefully.
func (p *Puller) Stop(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}

	// Wait for all backends to stop
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("puller stopped gracefully")
	case <-ctx.Done():
		p.logger.Warn("puller stop timed out")
		return ctx.Err()
	}

	return nil
}

// runBackend runs the change stream consumer for a single backend.
func (p *Puller) runBackend(ctx context.Context, name string, backend *Backend) {
	defer p.wg.Done()
	defer backend.buffer.Close()

	logger := p.logger.With("backend", name)
	logger.Info("starting backend")

	for {
		select {
		case <-ctx.Done():
			// Save final checkpoint before exit
			p.saveCheckpointOnShutdown(backend, logger)
			logger.Info("backend stopped")
			return
		default:
		}

		err := p.watchChangeStream(ctx, backend, logger)
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled, normal shutdown
				p.saveCheckpointOnShutdown(backend, logger)
				return
			}
			logger.Error("change stream error, reconnecting", "error", err)
			time.Sleep(time.Second) // Backoff before reconnect
		}
	}
}

// watchChangeStream watches the MongoDB change stream for a backend.
func (p *Puller) watchChangeStream(ctx context.Context, backend *Backend, logger *slog.Logger) error {
	// Load resume token if exists
	resumeToken, err := backend.buffer.LoadCheckpoint()
	if err != nil {
		logger.Warn("failed to load checkpoint", "error", err)
		// Continue without resume token
	}

	// Build watch pipeline for collection filtering
	pipeline := p.buildWatchPipeline(backend.config)

	// Configure watch options
	opts := options.ChangeStream().
		SetFullDocument(options.UpdateLookup)

	if resumeToken != nil {
		opts.SetResumeAfter(resumeToken)
		logger.Info("resuming from checkpoint")
	} else {
		logger.Info("starting fresh (no checkpoint)")
	}

	// Start watching at database level
	stream, err := backend.db.Watch(ctx, pipeline, opts)
	if err != nil {
		return fmt.Errorf("failed to open change stream: %w", err)
	}
	defer stream.Close(ctx)

	logger.Info("change stream opened")

	// Process events
	for stream.Next(ctx) {
		var raw normalizer.RawEvent
		if err := stream.Decode(&raw); err != nil {
			logger.Error("failed to decode event", "error", err)
			continue
		}

		// Normalize event
		evt, err := backend.normalizer.Normalize(&raw)
		if err != nil {
			logger.Error("failed to normalize event", "error", err)
			continue
		}

		shouldCheckpoint := backend.tracker.RecordEvent(raw.ResumeToken)
		if err := backend.buffer.WriteWithCheckpoint(evt, raw.ResumeToken, shouldCheckpoint); err != nil {
			logger.Error("failed to write event to buffer", "error", err)
			continue
		}
		if shouldCheckpoint {
			backend.tracker.MarkCheckpointed()
		}

		// Handle event after it is persisted
		if p.eventHandler != nil {
			if err := p.eventHandler(ctx, backend.name, evt); err != nil {
				logger.Error("failed to handle event", "error", err)
				// Continue processing other events
			}
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("change stream error: %w", err)
	}

	return nil
}

// saveCheckpointOnShutdown saves the current checkpoint for a backend on shutdown.
func (p *Puller) saveCheckpointOnShutdown(backend *Backend, logger *slog.Logger) {
	if !backend.tracker.ShouldCheckpointOnShutdown() {
		return
	}

	token := backend.tracker.LastToken()
	if err := backend.buffer.SaveCheckpoint(token); err != nil {
		logger.Error("failed to save checkpoint", "error", err)
		return
	}

	backend.tracker.MarkCheckpointed()
	logger.Debug("checkpoint saved on shutdown")
}

// buildWatchPipeline builds the MongoDB aggregation pipeline for collection filtering.
func (p *Puller) buildWatchPipeline(cfg config.PullerBackendConfig) mongo.Pipeline {
	if len(cfg.IncludeCollections) > 0 {
		return mongo.Pipeline{
			{{Key: "$match", Value: bson.M{
				"ns.coll": bson.M{"$in": cfg.IncludeCollections},
			}}},
		}
	}
	if len(cfg.ExcludeCollections) > 0 {
		return mongo.Pipeline{
			{{Key: "$match", Value: bson.M{
				"ns.coll": bson.M{"$nin": cfg.ExcludeCollections},
			}}},
		}
	}
	return nil // No filter, watch all collections
}

// BackendNames returns the names of all configured backends.
func (p *Puller) BackendNames() []string {
	names := make([]string, 0, len(p.backends))
	for name := range p.backends {
		names = append(names, name)
	}
	return names
}

// Subscribe subscribes to events from the puller with the given progress marker.
// Returns a channel of events.
func (p *Puller) Subscribe(ctx context.Context, consumerID string, after string) (<-chan *events.NormalizedEvent, error) {
	ch := make(chan *events.NormalizedEvent, 1000)

	// Set up event handler to send to channel
	p.SetEventHandler(func(ctx context.Context, backendName string, event *events.NormalizedEvent) error {
		select {
		case ch <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel full
			return nil
		}
	})

	return ch, nil
}
