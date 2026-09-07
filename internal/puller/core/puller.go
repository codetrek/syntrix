// Package core owns durable source ingestion and local subscriptions.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/internal/puller/health"
	"github.com/syntrixbase/syntrix/internal/puller/normalizer"
	"github.com/syntrixbase/syntrix/internal/puller/recovery"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Backend struct {
	name             string
	client           *mongo.Client
	db               *mongo.Database
	config           config.PullerBackendConfig
	normalizer       *normalizer.Normalizer
	buffer           *buffer.Buffer
	maxRetainedBytes int64
	cancel           context.CancelFunc
	ready            chan struct{}
	readyOnce        sync.Once
	err              error
}

type Puller struct {
	cfg    config.Config
	logger *slog.Logger
	health *health.Checker

	// Registration cuts and post-sync publication share this coordinator lock.
	mu               sync.Mutex
	backends         map[string]*Backend
	sources          map[string]*Backend
	heads            *cursor.ProgressMarker
	subscribers      map[uint64]*subscription
	nextRegistration uint64
	started          bool
	stopping         bool
	cancel           context.CancelFunc
	stopOnce         sync.Once
	stopDone         chan struct{}
	failed           chan struct{}
	stopErr          error
	wg               sync.WaitGroup

	watchFunc         func(context.Context, *Backend, *slog.Logger) error
	openStream        func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error)
	earliestTimestamp func(context.Context, *mongo.Client) (primitive.Timestamp, error)
	currentTimestamp  func(context.Context, *mongo.Client) (primitive.Timestamp, error)
	retryDelay        time.Duration
}

type changeStream interface {
	Next(context.Context) bool
	Decode(any) error
	Err() error
	Close(context.Context) error
}

func New(cfg config.Config, logger *slog.Logger) *Puller {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Puller{
		cfg: cfg, logger: logger.With("component", "puller"), health: health.NewChecker(logger),
		backends: make(map[string]*Backend), sources: make(map[string]*Backend),
		heads: cursor.NewProgressMarker(), subscribers: make(map[uint64]*subscription),
		stopDone: make(chan struct{}), failed: make(chan struct{}), retryDelay: time.Second,
		openStream: openMongoChangeStream, earliestTimestamp: oldestOplogTimestamp, currentTimestamp: currentOperationTime,
	}
	p.watchFunc = p.watchChangeStream
	return p
}

func openMongoChangeStream(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
	return db.Watch(ctx, pipeline, opts)
}

func oldestOplogTimestamp(ctx context.Context, client *mongo.Client) (primitive.Timestamp, error) {
	var row struct {
		Timestamp primitive.Timestamp `bson:"ts"`
	}
	err := client.Database("local").Collection("oplog.rs").FindOne(ctx, bson.D{},
		options.FindOne().SetSort(bson.D{{Key: "$natural", Value: 1}}).SetProjection(bson.D{{Key: "ts", Value: 1}})).Decode(&row)
	if err != nil {
		return primitive.Timestamp{}, fmt.Errorf("read earliest retained oplog entry: %w", err)
	}
	if row.Timestamp.T == 0 {
		return primitive.Timestamp{}, errors.New("earliest oplog entry has no timestamp")
	}
	return row.Timestamp, nil
}

func currentOperationTime(ctx context.Context, client *mongo.Client) (primitive.Timestamp, error) {
	session, err := client.StartSession()
	if err != nil {
		return primitive.Timestamp{}, err
	}
	defer session.EndSession(ctx)
	if err := client.Database("admin").RunCommand(mongo.NewSessionContext(ctx, session), bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
		return primitive.Timestamp{}, fmt.Errorf("capture initial source operation time: %w", err)
	}
	timestamp := session.OperationTime()
	if timestamp == nil || timestamp.T == 0 {
		return primitive.Timestamp{}, errors.New("source returned no initial operation time")
	}
	return *timestamp, nil
}

func (p *Puller) SetRetryDelay(d time.Duration) { p.retryDelay = d }

func (p *Puller) AddBackend(name string, client *mongo.Client, dbName string, cfg config.PullerBackendConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.stopping || p.nextRegistration != 0 {
		return errors.New("backends must be configured before ingestion or subscriptions start")
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("invalid backend name %q", name)
	}
	if _, exists := p.backends[name]; exists {
		return fmt.Errorf("backend %q already exists", name)
	}
	if cfg.SourceID == "" {
		return errors.New("backend source_id is required")
	}
	if _, exists := p.sources[cfg.SourceID]; exists {
		return fmt.Errorf("duplicate source_id %q", cfg.SourceID)
	}
	if client == nil || dbName == "" {
		return errors.New("backend client and database are required")
	}
	maxSize, err := parseSize(p.cfg.Buffer.MaxSize)
	if err != nil {
		return err
	}
	collections := slices.Clone(cfg.Collections)
	slices.Sort(collections)
	collections = slices.Compact(collections)
	scope, err := json.Marshal(struct {
		Database    string   `json:"database"`
		Collections []string `json:"collections"`
	}{dbName, collections})
	if err != nil {
		return err
	}
	backend := &Backend{name: name, client: client, db: client.Database(dbName), config: cfg,
		normalizer: normalizer.New(), maxRetainedBytes: maxSize, ready: make(chan struct{})}
	buf, err := buffer.New(buffer.Options{
		Path: filepath.Join(p.cfg.Buffer.Path, name), SourceID: cfg.SourceID, SourceScope: string(scope),
		BatchSize: p.cfg.Buffer.BatchSize, BatchInterval: p.cfg.Buffer.BatchInterval,
		QueueSize: p.cfg.Buffer.QueueSize, QueueBytes: p.cfg.Buffer.QueueBytes, BatchBytes: p.cfg.Buffer.BatchBytes,
		Logger:   p.logger.With("backend", name),
		OnCommit: func(batch buffer.CommittedBatch) { p.publish(name, batch) },
		OnError:  func(err error) { p.failBackend(name, err) },
	})
	if err != nil {
		return fmt.Errorf("open backend %q event log: %w", name, err)
	}
	state, err := buf.State()
	if err != nil {
		return errors.Join(err, buf.Close(context.Background()))
	}
	backend.buffer = buf
	p.backends[name] = backend
	p.sources[cfg.SourceID] = backend
	p.heads.SetPosition(state.Position)
	p.health.RegisterBackend(name)
	return nil
}

// Start returns after every source has opened its first change stream.
func (p *Puller) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.started || p.stopping {
		p.mu.Unlock()
		return errors.New("puller has already started or stopped")
	}
	if len(p.backends) == 0 {
		p.mu.Unlock()
		return errors.New("no backends configured")
	}
	p.started = true
	lifetime, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	context.AfterFunc(lifetime, p.beginStop)
	backends := make([]*Backend, 0, len(p.backends))
	for _, backend := range p.backends {
		backendCtx, backendCancel := context.WithCancel(lifetime)
		backend.cancel = backendCancel
		backends = append(backends, backend)
		p.wg.Add(1)
		go p.runBackend(backendCtx, backend)
	}
	p.mu.Unlock()
	for _, backend := range backends {
		select {
		case <-backend.ready:
		case <-p.failed:
			err := p.Err()
			p.beginStop()
			return err
		case <-lifetime.Done():
			p.beginStop()
			if err := p.Err(); err != nil {
				return err
			}
			return lifetime.Err()
		}
	}
	if err := p.Err(); err != nil {
		p.beginStop()
		return err
	}
	return nil
}

func (p *Puller) beginStop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopping = true
		if p.cancel != nil {
			p.cancel()
		}
		for _, sub := range p.subscribers {
			p.terminateLocked(sub, context.Canceled)
		}
		backends := make([]*Backend, 0, len(p.backends))
		for _, backend := range p.backends {
			backends = append(backends, backend)
		}
		p.mu.Unlock()
		// The drain owns each buffer until synchronization finishes, even if a caller times out.
		go func() {
			p.wg.Wait()
			var closeErrors []error
			for _, backend := range backends {
				closeErrors = append(closeErrors, backend.buffer.Close(context.Background()))
			}
			p.mu.Lock()
			p.stopErr = errors.Join(append(closeErrors, p.errLocked())...)
			p.mu.Unlock()
			close(p.stopDone)
		}()
	})
}

func (p *Puller) Stop(ctx context.Context) error {
	p.beginStop()
	select {
	case <-p.stopDone:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Puller) runBackend(ctx context.Context, backend *Backend) {
	defer p.wg.Done()
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	maintenanceDone := make(chan struct{})
	go func() { defer close(maintenanceDone); p.maintain(maintenanceCtx, backend) }()
	defer func() { stopMaintenance(); <-maintenanceDone }()
	logger := p.logger.With("backend", backend.name)
	for ctx.Err() == nil {
		err := p.watchFunc(ctx, backend, logger)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = io.EOF
		}
		if recovery.IsHistoryLost(err) {
			terminal := p.backendError(backend, events.CodeContinuityLost, err)
			p.failBackend(backend.name, terminal)
			if markErr := backend.buffer.MarkDiscontinuous(context.Background(), terminal); markErr != nil {
				var domain *events.Error
				if !errors.As(markErr, &domain) || domain.Code != events.CodeContinuityLost {
					p.recordAdditionalFailure(backend, markErr)
				}
			}
			return
		}
		var domain *events.Error
		if errors.As(err, &domain) || !recovery.IsRetryable(err) {
			p.failBackend(backend.name, p.backendError(backend, events.CodeSourceUnavailable, err))
			return
		}
		p.health.RecordError(backend.name)
		logger.Warn("source interrupted; resuming from committed checkpoint", "error", err)
		timer := time.NewTimer(p.retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Puller) maintain(ctx context.Context, backend *Backend) {
	ticker := time.NewTicker(p.cfg.Cleaner.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := backend.buffer.Retain(ctx, now.Add(-p.cfg.Cleaner.Retention), backend.maxRetainedBytes); err != nil {
				if ctx.Err() == nil {
					p.failBackend(backend.name, p.backendError(backend, events.CodeStorageFailure, err))
				}
				return
			}
		}
	}
}

func (p *Puller) watchChangeStream(ctx context.Context, backend *Backend, logger *slog.Logger) (result error) {
	state, err := backend.buffer.Flush(ctx)
	if err != nil {
		return p.backendError(backend, events.CodeStorageFailure, err)
	}
	if len(state.ResumeToken) == 0 && state.StartAt == nil {
		timestampFunc := p.currentTimestamp
		if p.cfg.Bootstrap.Mode == "from_beginning" {
			timestampFunc = p.earliestTimestamp
		}
		timestamp, err := timestampFunc(ctx, backend.client)
		if err != nil {
			return p.backendError(backend, events.CodeSourceUnavailable, fmt.Errorf("establish source boundary: %w", err))
		}
		if err := backend.buffer.InitializeBoundary(ctx, nil, &timestamp); err != nil {
			return p.backendError(backend, events.CodeStorageFailure, err)
		}
		state, err = backend.buffer.State()
		if err != nil {
			return p.backendError(backend, events.CodeStorageFailure, err)
		}
	}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	if len(state.ResumeToken) != 0 {
		opts.SetResumeAfter(state.ResumeToken)
	} else {
		opts.SetStartAtOperationTime(state.StartAt)
	}
	stream, err := p.openStream(ctx, backend.db, p.buildWatchPipeline(backend.config), opts)
	if err != nil {
		return fmt.Errorf("open change stream: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result = errors.Join(result, stream.Close(closeCtx))
	}()
	backend.readyOnce.Do(func() { close(backend.ready) })
	logger.Debug("change stream ready")
	for stream.Next(ctx) {
		var raw normalizer.RawEvent
		if err := stream.Decode(&raw); err != nil {
			return p.backendError(backend, events.CodeSourceUnavailable, fmt.Errorf("decode source event: %w", err))
		}
		evt, err := backend.normalizer.Normalize(&raw)
		if err != nil {
			return p.backendError(backend, events.CodeSourceUnavailable, fmt.Errorf("normalize source event: %w", err))
		}
		evt.Backend = backend.name
		if err := backend.buffer.Enqueue(ctx, evt, raw.ResumeToken); err != nil {
			return p.backendError(backend, events.CodeStorageFailure, err)
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("read change stream: %w", err)
	}
	return ctx.Err()
}

func (p *Puller) buildWatchPipeline(cfg config.PullerBackendConfig) mongo.Pipeline {
	if len(cfg.Collections) == 0 {
		return nil
	}
	return mongo.Pipeline{{{Key: "$match", Value: bson.M{"ns.coll": bson.M{"$in": cfg.Collections}}}}}
}

func (p *Puller) backendError(backend *Backend, code events.ErrorCode, cause error) error {
	var domain *events.Error
	if errors.As(cause, &domain) {
		return cause
	}
	p.mu.Lock()
	head, _ := p.heads.GetPosition(backend.config.SourceID)
	p.mu.Unlock()
	return &events.Error{Code: code, Backend: backend.name, SourceID: backend.config.SourceID,
		Generation: head.Generation, CommittedThrough: head.Sequence, Cause: cause}
}

func (p *Puller) failBackend(name string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	backend := p.backends[name]
	if backend == nil || backend.err != nil {
		return
	}
	backend.err = err
	select {
	case <-p.failed:
	default:
		close(p.failed)
	}
	if backend.cancel != nil {
		backend.cancel()
	}
	p.health.FailBackend(name)
	for _, sub := range p.subscribers {
		p.terminateLocked(sub, err)
	}
}

func (p *Puller) recordAdditionalFailure(backend *Backend, err error) {
	p.mu.Lock()
	backend.err = errors.Join(backend.err, err)
	p.mu.Unlock()
}

func (p *Puller) Err() error { p.mu.Lock(); defer p.mu.Unlock(); return p.errLocked() }

func (p *Puller) errLocked() error {
	var errs []error
	for _, backend := range p.backends {
		if backend.err != nil {
			errs = append(errs, backend.err)
		}
	}
	return errors.Join(errs...)
}

func (p *Puller) HealthReport() health.Report {
	p.mu.Lock()
	stopping := p.stopping
	p.mu.Unlock()
	report := p.health.GetReport()
	if stopping {
		report.Status = health.StatusUnhealthy
	}
	return report
}

func (p *Puller) BackendNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.backends))
	for name := range p.backends {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func parseSize(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid buffer max_size %q", value)
	}
	size, err := strconv.ParseInt(value[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid buffer max_size %q: %w", value, err)
	}
	powers := map[string]int{"": 0, "B": 0, "KB": 1, "KiB": 1, "MB": 2, "MiB": 2, "GB": 3, "GiB": 3, "TB": 4, "TiB": 4}
	power, ok := powers[strings.TrimSpace(value[i:])]
	if !ok {
		return 0, fmt.Errorf("invalid buffer max_size unit %q", value[i:])
	}
	for range power {
		if size > (1<<63-1)/1024 {
			return 0, fmt.Errorf("buffer max_size overflows: %q", value)
		}
		size *= 1024
	}
	return size, nil
}
