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
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/puller/internal/buffer"
	"github.com/codetrek/syntrix/internal/puller/internal/flowcontrol"
	"github.com/codetrek/syntrix/internal/puller/internal/metrics"
	"github.com/codetrek/syntrix/internal/puller/internal/normalizer"
	"github.com/codetrek/syntrix/internal/puller/internal/recovery"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// EventHandler is a function that handles events from the change stream.
// Note: This type must match the signature in grpc.EventSource interface.
type EventHandler = func(ctx context.Context, backendName string, event *events.NormalizedEvent) error

// Backend represents a single MongoDB backend being watched.
type Backend struct {
	name            string
	client          *mongo.Client
	db              *mongo.Database
	config          config.PullerBackendConfig
	normalizer      *normalizer.Normalizer
	buffer          *buffer.Buffer
	gapDetector     *recovery.GapDetector
	recoveryHandler *recovery.Handler
	backpressure    *flowcontrol.BackpressureMonitor
	cleaner         *buffer.Cleaner

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

	// watchFunc is the function used to watch the change stream.
	// It can be replaced for testing purposes.
	watchFunc func(ctx context.Context, backend *Backend, logger *slog.Logger) error

	// openStream allows tests to inject a custom change stream implementation.
	openStream func(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error)

	// retryDelay is the time to wait before retrying after an error.
	retryDelay time.Duration

	// backpressureSlowDownDelay is the time to sleep when backpressure action is SlowDown.
	backpressureSlowDownDelay time.Duration

	// backpressurePauseDelay is the time to sleep when backpressure action is Pause.
	backpressurePauseDelay time.Duration
}

// New creates a new Puller instance.
func New(cfg config.PullerConfig, logger *slog.Logger) *Puller {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Puller{
		cfg:                       cfg,
		backends:                  make(map[string]*Backend),
		logger:                    logger.With("component", "puller"),
		retryDelay:                time.Second,
		backpressureSlowDownDelay: 100 * time.Millisecond,
		backpressurePauseDelay:    1 * time.Second,
	}
	p.watchFunc = p.watchChangeStream
	p.openStream = openMongoChangeStream
	return p
}

// changeStream defines the subset of mongo.ChangeStream used by the puller.
type changeStream interface {
	Next(context.Context) bool
	Decode(any) error
	Err() error
	Close(context.Context) error
}

func openMongoChangeStream(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
	return db.Watch(ctx, pipeline, opts)
}

// SetRetryDelay sets the retry delay for testing purposes.
func (p *Puller) SetRetryDelay(d time.Duration) {
	p.retryDelay = d
}

// SetBackpressureSlowDownDelay sets the slow down delay for testing purposes.
func (p *Puller) SetBackpressureSlowDownDelay(d time.Duration) {
	p.backpressureSlowDownDelay = d
}

// SetBackpressurePauseDelay sets the pause delay for testing purposes.
func (p *Puller) SetBackpressurePauseDelay(d time.Duration) {
	p.backpressurePauseDelay = d
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

	gapDetector := recovery.NewGapDetector(recovery.GapDetectorOptions{
		Logger: logger,
	})

	recoveryHandler := recovery.NewHandler(recovery.HandlerOptions{
		Checkpoint:           buf,
		MaxConsecutiveErrors: 10, // TODO: Make configurable
		Logger:               logger,
	})

	maxSize, err := parseSize(p.cfg.Buffer.MaxSize)
	if err != nil {
		logger.Warn("invalid buffer max size, using unlimited", "error", err)
		maxSize = 0
	}

	cleaner := buffer.NewCleaner(buffer.CleanerOptions{
		Buffer:    buf,
		Retention: p.cfg.Cleaner.Retention,
		MaxSize:   maxSize,
		Interval:  p.cfg.Cleaner.Interval,
		Logger:    logger,
	})

	bpMonitor := flowcontrol.NewBackpressureMonitor(flowcontrol.BackpressureOptions{
		PublishLatency: metrics.PublishLatency,
		QueueDepth:     metrics.QueueDepth,
	})

	p.backends[name] = &Backend{
		name:            name,
		client:          client,
		db:              db,
		config:          cfg,
		normalizer:      normalizer.New(nil),
		buffer:          buf,
		gapDetector:     gapDetector,
		recoveryHandler: recoveryHandler,
		backpressure:    bpMonitor,
		cleaner:         cleaner,
		eventChan:       make(chan *events.NormalizedEvent, 1000),
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

	backend.cleaner.Start(ctx)
	defer backend.cleaner.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("backend stopped")
			return
		default:
		}

		err := p.watchFunc(ctx, backend, logger)
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled, normal shutdown
				return
			}

			action := backend.recoveryHandler.HandleError(err)
			switch action {
			case recovery.ActionRestart:
				logger.Warn("restarting backend due to error", "error", err)
				if recErr := backend.recoveryHandler.RecoverFromResumeTokenError(ctx); recErr != nil {
					logger.Error("failed to recover from resume token error", "error", recErr)
					// If recovery fails, we might want to stop or retry.
					// For now, we'll retry which will likely trigger another error.
				}
				// Continue loop to restart watch
				time.Sleep(p.retryDelay)
			case recovery.ActionFatal:
				logger.Error("fatal error, stopping backend", "error", err)
				return
			case recovery.ActionReconnect:
				logger.Warn("transient error, reconnecting", "error", err)
				time.Sleep(p.retryDelay) // Backoff before reconnect
			case recovery.ActionNone:
				// Should not happen if err != nil
				logger.Warn("unknown error, reconnecting", "error", err)
				time.Sleep(p.retryDelay)
			}
		} else {
			// Reset error count on successful run (if watchChangeStream returns nil, it means it finished normally, e.g. context done)
			backend.recoveryHandler.ResetErrorCount()
		}
	}
}

// watchChangeStream watches the MongoDB change stream for a backend.
func (p *Puller) watchChangeStream(ctx context.Context, backend *Backend, logger *slog.Logger) error {
	// Load resume token if exists
	resumeToken, err := backend.buffer.LoadCheckpoint()
	if err != nil {
		logger.Warn("failed to load checkpoint from buffer", "error", err)
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
		if p.cfg.Bootstrap.Mode == "from_beginning" {
			opts.SetStartAtOperationTime(&primitive.Timestamp{T: 1, I: 1})
		}
	}

	// Start watching at database level
	stream, err := p.openStream(ctx, backend.db, pipeline, opts)
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
		evt.Backend = backend.name

		// Check for gaps
		backend.gapDetector.RecordEvent(evt)

		if err := backend.buffer.Write(evt, raw.ResumeToken); err != nil {
			logger.Error("failed to write event to buffer", "error", err)
			continue
		}

		// Handle event after it is persisted
		p.invokeHandlerWithBackpressure(ctx, backend, evt)
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("change stream error: %w", err)
	}

	return nil
}

func (p *Puller) invokeHandlerWithBackpressure(ctx context.Context, backend *Backend, evt *events.NormalizedEvent) {
	if p.eventHandler != nil {
		start := time.Now()
		if err := p.eventHandler(ctx, backend.name, evt); err != nil {
			p.logger.Error("failed to handle event", "error", err)
			// Continue processing other events
		}
		latency := time.Since(start)

		// Handle backpressure
		action := backend.backpressure.HandleBackpressure(latency)
		switch action {
		case flowcontrol.ActionSlowDown:
			time.Sleep(p.backpressureSlowDownDelay)
		case flowcontrol.ActionPause:
			p.logger.Warn("pausing due to high backpressure")
			metrics.BackpressureEvents.WithLabelValues(backend.name, "pause").Inc()
			time.Sleep(p.backpressurePauseDelay)
		}
	}
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

// Replay returns an iterator that replays events from the given progress marker.
// If the marker is empty, it replays from the beginning of the buffer.
func (p *Puller) Replay(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
	var iters []events.Iterator

	for name, backend := range p.backends {
		startID := ""
		if after != nil {
			eventID := after[name]
			if eventID != "" {
				ct, err := normalizer.ParseEventID(eventID)
				if err != nil {
					// Close already opened iterators
					for _, it := range iters {
						it.Close()
					}
					return nil, fmt.Errorf("invalid event ID %q for backend %q: %w", eventID, name, err)
				}
				startID = events.FormatBufferKey(ct, eventID)
			}
		}

		iter, err := backend.buffer.ScanFrom(startID)
		if err != nil {
			// Close already opened iterators
			for _, it := range iters {
				it.Close()
			}
			return nil, fmt.Errorf("failed to create iterator for backend %q: %w", name, err)
		}
		iters = append(iters, &backendInjectingIterator{
			Iterator:    iter,
			backendName: name,
		})
	}

	iter := NewMergeIterator(iters)
	if coalesce {
		return NewCoalescingIterator(iter, 100), nil
	}
	return iter, nil
}

type backendInjectingIterator struct {
	events.Iterator
	backendName string
}

func (i *backendInjectingIterator) Event() *events.NormalizedEvent {
	evt := i.Iterator.Event()
	if evt != nil && evt.Backend == "" {
		evt.Backend = i.backendName
	}
	return evt
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

func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	var size int64
	var unit string
	// Try to parse number and unit
	n, err := fmt.Sscanf(s, "%d%s", &size, &unit)
	if err != nil {
		// Try just number
		_, err = fmt.Sscanf(s, "%d", &size)
		if err != nil {
			return 0, fmt.Errorf("invalid size format: %s", s)
		}
		return size, nil
	}

	if n == 1 {
		return size, nil
	}

	switch unit {
	case "KB", "KiB":
		size *= 1024
	case "MB", "MiB":
		size *= 1024 * 1024
	case "GB", "GiB":
		size *= 1024 * 1024 * 1024
	case "TB", "TiB":
		size *= 1024 * 1024 * 1024 * 1024
	}
	return size, nil
}
