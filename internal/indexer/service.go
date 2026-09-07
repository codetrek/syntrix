package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/syntrixbase/syntrix/internal/indexer/config"
	"github.com/syntrixbase/syntrix/internal/indexer/encoding"
	"github.com/syntrixbase/syntrix/internal/indexer/manager"
	"github.com/syntrixbase/syntrix/internal/indexer/mem_store"
	"github.com/syntrixbase/syntrix/internal/indexer/persist_store"
	"github.com/syntrixbase/syntrix/internal/indexer/store"
	"github.com/syntrixbase/syntrix/internal/indexer/template"
	"github.com/syntrixbase/syntrix/internal/puller"
)

// service implements LocalService.
type service struct {
	cfg     config.Config
	logger  *slog.Logger
	manager *manager.Manager
	store   store.Store

	// Puller subscription
	pullerSvc puller.Service

	// State
	mu          sync.RWMutex
	running     bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	progress    string // last successfully applied progress marker
	terminalErr error
	closed      bool

	// Stats
	eventsApplied atomic.Int64
	lastEventTime atomic.Int64
}

// NewService creates a new Indexer service.
// pullerSvc can be nil if not subscribing to Puller (for testing).
func NewService(cfg config.Config, pullerSvc puller.Service, logger *slog.Logger) (LocalService, error) {
	if cfg.ConsumerID == "" {
		cfg.ConsumerID = "indexer"
	}
	if cfg.ProgressPath == "" {
		cfg.ProgressPath = "data/indexer/progress"
	}

	// Create store based on storage mode
	st, err := newStore(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return &service{
		cfg:       cfg,
		logger:    logger.With("component", "indexer"),
		manager:   manager.New(st),
		store:     st,
		pullerSvc: pullerSvc,
	}, nil
}

// newStore creates a store based on the storage mode configuration.
func newStore(cfg config.Config, logger *slog.Logger) (store.Store, error) {
	switch cfg.StorageMode {
	case config.StorageModeMemory, "":
		return mem_store.New(), nil
	case config.StorageModePebble:
		return persist_store.NewPebbleStore(cfg.Store, logger)
	default:
		return nil, fmt.Errorf("unknown storage mode: %s", cfg.StorageMode)
	}
}

// Start starts the indexer service.
func (s *service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("service is closed")
	}
	if s.running {
		return errors.New("service already running")
	}

	// Load templates from directory
	if s.cfg.TemplatePath != "" {
		if err := s.manager.LoadTemplatesFromDir(s.cfg.TemplatePath); err != nil {
			return fmt.Errorf("failed to load templates: %w", err)
		}
		s.logger.Info("loaded index templates",
			"path", s.cfg.TemplatePath,
			"count", len(s.manager.Templates()))
	}

	// Load progress from store
	if savedProgress, err := s.store.LoadProgress(); err != nil {
		return fmt.Errorf("failed to load progress from store: %w", err)
	} else if savedProgress != "" {
		s.progress = savedProgress
		s.logger.Info("loaded progress from store", "progress", savedProgress)
	}

	// Start Puller subscription if configured
	if s.pullerSvc != nil {
		subCtx, cancel := context.WithCancel(ctx)
		subscription, err := s.pullerSvc.Subscribe(subCtx, puller.SubscribeOptions{ConsumerID: s.cfg.ConsumerID, After: s.progress})
		if err != nil {
			cancel()
			return fmt.Errorf("failed to subscribe to puller: %w", err)
		}
		s.cancel = cancel
		s.running = true

		s.wg.Add(1)
		go s.subscriptionLoop(subCtx, subscription)
	} else {
		s.running = true
	}

	s.logger.Info("indexer service started")
	return nil
}

// Stop gracefully stops the indexer service.
func (s *service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return nil
		}
		s.closed = true
		return s.store.Close()
	}
}

func (s *service) fail(err error) {
	s.mu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	s.mu.Unlock()
	s.logger.Error("indexer consumption stopped", "error", err)
}

func (s *service) subscriptionLoop(ctx context.Context, subscription puller.Subscription) {
	defer s.wg.Done()
	defer subscription.Close()
	readCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	monitorDone := make(chan struct{})
	defer close(monitorDone)
	go func() {
		select {
		case <-s.store.Failed():
			cancel(s.store.Err())
		case <-monitorDone:
		case <-readCtx.Done():
		}
	}()
	for {
		evt, err := subscription.Next(readCtx)
		if err != nil {
			if ctx.Err() == nil {
				if cause := context.Cause(readCtx); cause != nil {
					err = cause
				}
				s.fail(fmt.Errorf("puller subscription failed: %w", err))
			}
			return
		}
		if err := s.ApplyEvent(ctx, evt.Change, evt.Progress); err != nil {
			s.fail(fmt.Errorf("failed to apply puller event: %w", err))
			return
		}
		if evt.Progress != "" {
			s.mu.Lock()
			s.progress = evt.Progress
			s.mu.Unlock()
		}
	}
}

// ApplyEvent applies a single change event to the indexes.
func (s *service) ApplyEvent(ctx context.Context, evt *ChangeEvent, progress string) (err error) {
	// Checkpoints follow every accepted template update, including windows that
	// produce no index mutations. The store orders them with pending writes.
	defer func() {
		if err == nil && progress != "" {
			err = s.store.SaveProgress(progress)
		}
	}()
	if evt == nil || evt.FullDocument == nil || evt.FullDocument.Collection == "" {
		return nil
	}
	matches := s.manager.MatchTemplatesForCollection(evt.FullDocument.Collection)
	if len(matches) == 0 {
		return nil
	}
	for _, match := range matches {
		if err := s.applyEventToTemplate(ctx, evt, match.Template, ""); err != nil {
			return fmt.Errorf("failed to apply event %s to template %s: %w", evt.EventID, match.Template.Name, err)
		}
	}
	s.eventsApplied.Add(1)
	s.lastEventTime.Store(time.Now().Unix())
	return nil
}

// applyEventToTemplate applies an event to a specific template's index.
func (s *service) applyEventToTemplate(ctx context.Context, evt *ChangeEvent, tmpl *template.Template, progress string) error {
	doc := evt.FullDocument
	if doc == nil {
		return nil
	}

	// Get the user-facing document ID from Data["id"]
	// This is different from doc.Id which is the internal storage ID (database:hash)
	docID, ok := doc.Data["id"].(string)
	if !ok || docID == "" {
		// Fallback: extract from Fullpath (collection/id)
		if doc.Fullpath != "" {
			parts := strings.Split(doc.Fullpath, "/")
			if len(parts) > 0 {
				docID = parts[len(parts)-1]
			}
		}
	}
	if docID == "" {
		return nil // Cannot index without a document ID
	}

	st := s.manager.Store()
	pattern := tmpl.NormalizedPattern()
	tmplID := tmpl.Identity()

	// Skip deleted documents unless template includes them
	if doc.Deleted && !tmpl.IncludeDeleted {
		// Delete from index
		return st.Delete(evt.Database, pattern, tmplID, docID, progress)
	}

	// Build OrderKey from document fields
	orderKey, err := s.buildOrderKey(doc.Data, tmpl)
	if err != nil {
		return fmt.Errorf("failed to build order key: %w", err)
	}

	// Upsert document using the user-facing document ID
	return st.Upsert(evt.Database, pattern, tmplID, docID, orderKey, progress)
}

// buildOrderKey builds an OrderKey from document data and template fields.
func (s *service) buildOrderKey(data map[string]any, tmpl *template.Template) ([]byte, error) {
	fields := make([]encoding.Field, len(tmpl.Fields))

	for i, tf := range tmpl.Fields {
		value := extractFieldValue(data, tf.Field)

		// Convert template direction to encoding direction
		var dir encoding.Direction
		if tf.Order == template.Desc {
			dir = encoding.Desc
		} else {
			dir = encoding.Asc
		}

		fields[i] = encoding.Field{
			Value:     value,
			Direction: dir,
		}
	}

	// Note: docID is already appended by encoding.Encode as tie-breaker
	// We use empty string here since the index stores ID separately
	return encoding.Encode(fields, "")
}

// extractFieldValue extracts a field value from document data.
// Supports nested fields with dot notation (e.g., "user.name").
func extractFieldValue(data map[string]any, field string) any {
	// TODO: Support dot notation for nested fields
	return data[field]
}

// Search returns document references matching the query plan.
func (s *service) Search(ctx context.Context, database string, plan Plan) ([]DocRef, error) {
	return s.manager.Search(ctx, database, plan)
}

// Health returns current health status of the indexer.
func (s *service) Health(ctx context.Context) (Health, error) {
	s.mu.RLock()
	running := s.running
	terminalErr := s.terminalErr
	s.mu.RUnlock()
	if terminalErr != nil {
		return Health{Status: HealthUnhealthy}, terminalErr
	}

	status := HealthOK
	if !running {
		status = HealthUnhealthy
	}

	st := s.manager.Store()
	if err := st.Err(); err != nil {
		return Health{Status: HealthUnhealthy}, err
	}
	indexes := make(map[string]manager.IndexHealth)
	databases, err := st.ListDatabases()
	if err != nil {
		return Health{Status: HealthUnhealthy}, err
	}
	for _, dbName := range databases {
		dbIndexes, err := st.ListIndexes(dbName)
		if err != nil {
			continue
		}
		for _, idx := range dbIndexes {
			key := dbName + "|" + idx.Pattern + "|" + idx.TemplateID
			indexes[key] = manager.IndexHealth{
				State:    string(idx.State),
				DocCount: int64(idx.DocCount),
			}
		}
	}

	return Health{
		Status:  status,
		Indexes: indexes,
	}, nil
}

// Stats returns index statistics.
func (s *service) Stats(ctx context.Context) (Stats, error) {
	mgrStats := s.manager.Stats()

	// Augment with service-level stats
	mgrStats.EventsApplied = s.eventsApplied.Load()
	mgrStats.LastEventTime = s.lastEventTime.Load()

	return mgrStats, nil
}

// Manager returns the underlying index manager.
func (s *service) Manager() *manager.Manager {
	return s.manager
}

// InvalidateDatabase removes all index entries for a database.
func (s *service) InvalidateDatabase(ctx context.Context, database string) error {
	st := s.manager.Store()
	if err := st.DeleteDatabase(database); err != nil {
		s.logger.Error("failed to invalidate database indexes",
			"database", database,
			"error", err)
		return fmt.Errorf("failed to delete database indexes: %w", err)
	}
	s.logger.Info("invalidated database indexes", "database", database)
	return nil
}
