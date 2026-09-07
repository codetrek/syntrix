package indexer_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/indexer"
	"github.com/syntrixbase/syntrix/internal/indexer/config"
	"github.com/syntrixbase/syntrix/internal/puller"
)

// mockPuller implements puller.Service for testing.
type mockPuller struct {
	events chan *puller.Event
}

func newMockPuller(bufferSize int) *mockPuller {
	return &mockPuller{
		events: make(chan *puller.Event, bufferSize),
	}
}

func (m *mockPuller) Subscribe(ctx context.Context, opts puller.SubscribeOptions) (puller.Subscription, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &integrationSubscription{ctx: ctx, cancel: cancel, events: m.events}, nil
}

type integrationSubscription struct {
	ctx    context.Context
	cancel context.CancelFunc
	events <-chan *puller.Event
}

func (s *integrationSubscription) Next(ctx context.Context) (*puller.Event, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case evt, ok := <-s.events:
		if !ok {
			return nil, io.EOF
		}
		return evt, nil
	}
}
func (s *integrationSubscription) Close() error { s.cancel(); return nil }

func (m *mockPuller) pushEvent(evt *puller.ChangeEvent, progress string) {
	m.events <- &puller.Event{
		Change:   evt,
		Progress: progress,
	}
}

func (m *mockPuller) close() {
	close(m.events)
}

// createTestEvent creates a change event for testing.
func createTestEvent(database, collection, docID string, data map[string]any) *puller.ChangeEvent {
	// Ensure Data contains the "id" field for proper indexing
	if data == nil {
		data = make(map[string]any)
	}
	if _, ok := data["id"]; !ok {
		data["id"] = docID
	}
	return &puller.ChangeEvent{
		EventID:  "evt-" + docID,
		Database: database,
		OpType:   puller.OperationInsert,
		FullDocument: &storage.StoredDoc{
			Id:         docID,
			Database:   database,
			Collection: collection,
			Fullpath:   collection + "/" + docID,
			Data:       data,
		},
		ClusterTime: puller.ClusterTime{T: uint32(time.Now().Unix()), I: 1},
		Timestamp:   time.Now().UnixMilli(),
	}
}

// templateYAML defines test templates.
const templateYAML = `
database: default
templates:
  - name: users_by_timestamp
    collectionPattern: "users"
    fields:
      - field: timestamp
        order: desc

  - name: chats_by_priority
    collectionPattern: "users/{userId}/chats"
    fields:
      - field: priority
        order: desc
      - field: timestamp
        order: desc

  - name: orders_by_amount
    collectionPattern: "orders"
    fields:
      - field: amount
        order: desc

  - name: products_by_price
    collectionPattern: "products"
    fields:
      - field: price
        order: asc
`

// setupIndexerService creates and starts an indexer service for testing.
func setupIndexerService(t *testing.T, mockPullerSvc *mockPuller) (indexer.LocalService, context.Context, context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create temp directory for templates
	tmpDir, err := os.MkdirTemp("", "templates-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	if err := os.WriteFile(tmpDir+"/default.yml", []byte(templateYAML), 0644); err != nil {
		t.Fatalf("failed to write templates: %v", err)
	}

	cfg := config.Config{
		TemplatePath: tmpDir,
		ConsumerID:   "test-indexer",
	}
	svc, err := indexer.NewService(cfg, mockPullerSvc, logger)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := svc.Start(ctx); err != nil {
		cancel()
		t.Fatalf("failed to start service: %v", err)
	}

	return svc, ctx, cancel
}

// stopService stops the indexer service gracefully.
func stopService(t *testing.T, svc indexer.LocalService, mockPullerSvc *mockPuller) {
	t.Helper()
	mockPullerSvc.close()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Errorf("failed to stop service: %v", err)
	}
}

// ============================================================================
// Pebble Mode Integration Tests
// ============================================================================

// setupIndexerServiceWithPebble creates and starts an indexer service using pebble storage.
func setupIndexerServiceWithPebble(t *testing.T, mockPullerSvc *mockPuller, dataDir string) (indexer.LocalService, context.Context, context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create temp directory for templates
	tmpDir, err := os.MkdirTemp("", "templates-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	if err := os.WriteFile(tmpDir+"/default.yml", []byte(templateYAML), 0644); err != nil {
		t.Fatalf("failed to write templates: %v", err)
	}

	cfg := config.Config{
		TemplatePath: tmpDir,
		ConsumerID:   "test-indexer-pebble",
		StorageMode:  "pebble",
		Store: config.StoreConfig{
			Path:          dataDir,
			BatchSize:     10,
			BatchInterval: 10 * time.Millisecond,
			QueueSize:     10000,
		},
	}
	svc, err := indexer.NewService(cfg, mockPullerSvc, logger)
	if err != nil {
		t.Fatalf("failed to create pebble service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := svc.Start(ctx); err != nil {
		cancel()
		t.Fatalf("failed to start pebble service: %v", err)
	}

	return svc, ctx, cancel
}
