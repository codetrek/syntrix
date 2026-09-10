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
	mu       sync.RWMutex
	running  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	progress string // last progress marker

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
		s.logger.Warn("failed to load progress from store", "error", err)
	} else if savedProgress != "" {
		s.progress = savedProgress
		s.logger.Info("loaded progress from store", "progress", savedProgress)
	}

	// Start Puller subscription if configured
	if s.pullerSvc != nil {
		subCtx, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		s.running = true

		s.wg.Add(1)
		go s.subscriptionLoop(subCtx)
	} else {
		s.running = true
	}

	s.logger.Info("indexer service started")
	return nil
}

// Stop gracefully stops the indexer service.
func (s *service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}

	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	s.mu.Unlock()

	// Wait for subscription loop to finish
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Close the store
		if s.store != nil {
			if err := s.store.Close(); err != nil {
				s.logger.Error("failed to close store", "error", err)
			}
		}
		s.logger.Info("indexer service stopped")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscriptionLoop runs the Puller subscription.
func (s *service) subscriptionLoop(ctx context.Context) {
	defer s.wg.Done()

	s.logger.Info("starting puller subscription",
		"consumerID", s.cfg.ConsumerID,
		"progress", s.progress)

	events := s.pullerSvc.Subscribe(ctx, s.cfg.ConsumerID, s.progress)

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				s.logger.Warn("puller subscription closed, will reconnect")
				// Reconnect after backoff
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
					events = s.pullerSvc.Subscribe(ctx, s.cfg.ConsumerID, s.progress)
					continue
				}
			}

			if evt.Change != nil {
				if err := s.ApplyEvent(ctx, evt.Change, evt.Progress); err != nil {
					s.logger.Error("failed to apply event",
						"eventID", evt.Change.EventID,
						"error", err)
					// Don't update progress if ApplyEvent failed
					continue
				}
			}

			// Update in-memory progress marker
			if evt.Progress != "" {
				s.mu.Lock()
				s.progress = evt.Progress
				s.mu.Unlock()
			}
		}
	}
}

// ApplyEvent applies a single change event to the indexes.
func (s *service) ApplyEvent(ctx context.Context, evt *ChangeEvent, progress string) error {
	if evt == nil {
		return nil
	}

	// Get collection path from the event
	var collection string
	if evt.FullDocument != nil {
		collection = evt.FullDocument.Collection
	} else {
		// For deletes without FullDocument, we cannot update indexes
		// This is handled by includeDeleted templates only
		return nil
	}

	if collection == "" {
		return nil
	}

	// Match templates for this collection
	matches := s.manager.MatchTemplatesForCollection(collection)
	if len(matches) == 0 {
		// No indexes for this collection
		return nil
	}

	// Process each matching template
	// Pass progress only to the last template operation
	for i, match := range matches {
		// Only pass progress on the last operation to avoid duplicate writes
		var opProgress string
		if i == len(matches)-1 {
			opProgress = progress
		}
		if err := s.applyEventToTemplate(ctx, evt, match.Template, opProgress); err != nil {
			s.logger.Error("failed to apply event to template",
				"template", match.Template.Name,
				"eventID", evt.EventID,
				"error", err)
			// Continue with other templates
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
		st.Delete(evt.Database, pattern, tmplID, docID, progress)
		return nil
	}

	// Build OrderKey from document fields
	orderKey, err := s.buildOrderKey(doc.Data, tmpl)
	if err != nil {
		return fmt.Errorf("failed to build order key: %w", err)
	}

	// Upsert document using the user-facing document ID
	st.Upsert(evt.Database, pattern, tmplID, docID, orderKey, progress)

	return nil
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
	s.mu.RUnlock()

	status := HealthOK
	if !running {
		status = HealthUnhealthy
	}

	st := s.manager.Store()
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
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	mgrStats := s.manager.Stats()

	// Augment with service-level stats
	mgrStats.EventsApplied = s.eventsApplied.Load()
	mgrStats.LastEventTime = s.lastEventTime.Load()

	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
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
