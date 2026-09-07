package indexer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/indexer/config"
	"github.com/syntrixbase/syntrix/internal/indexer/manager"
	"github.com/syntrixbase/syntrix/internal/indexer/mem_store"
	"github.com/syntrixbase/syntrix/internal/indexer/store"
	"github.com/syntrixbase/syntrix/internal/puller"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// mockPullerService is a mock implementation of puller.Service for testing.
type mockPullerService struct {
	mu          sync.Mutex
	events      chan *puller.Event
	subscribers []mockSubscription
}

type mockSubscription struct {
	consumerID string
	after      string
	ch         chan *puller.Event
}

func newMockPullerService() *mockPullerService {
	return &mockPullerService{
		events: make(chan *puller.Event, 100),
	}
}

func (m *mockPullerService) Subscribe(ctx context.Context, opts puller.SubscribeOptions) (puller.Subscription, error) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan *puller.Event, 100)
	m.subscribers = append(m.subscribers, mockSubscription{
		consumerID: opts.ConsumerID,
		after:      opts.After,
		ch:         ch,
	})

	// Forward events from main channel to subscriber
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(ch)
				return
			case evt, ok := <-m.events:
				if !ok {
					close(ch)
					return
				}
				select {
				case ch <- evt:
				case <-ctx.Done():
					close(ch)
					return
				}
			}
		}
	}()

	return &channelSubscription{ctx: ctx, cancel: cancel, events: ch}, nil
}

func (m *mockPullerService) SendEvent(evt *puller.Event) {
	m.events <- evt
}

func (m *mockPullerService) Close() {
	close(m.events)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestService creates a new service for testing, panics on error.
func newTestService(cfg config.Config, pullerSvc puller.Service, logger *slog.Logger) LocalService {
	svc, err := NewService(cfg, pullerSvc, logger)
	if err != nil {
		panic(fmt.Sprintf("failed to create test service: %v", err))
	}
	return svc
}

func TestNewService(t *testing.T) {
	t.Run("with defaults", func(t *testing.T) {
		cfg := config.Config{}
		svc := newTestService(cfg, nil, testLogger())

		require.NotNil(t, svc)
		s := svc.(*service)
		assert.Equal(t, "indexer", s.cfg.ConsumerID)
		assert.Equal(t, "data/indexer/progress", s.cfg.ProgressPath)
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := config.Config{
			ConsumerID:   "my-indexer",
			ProgressPath: "/custom/path",
		}
		svc := newTestService(cfg, nil, testLogger())

		s := svc.(*service)
		assert.Equal(t, "my-indexer", s.cfg.ConsumerID)
		assert.Equal(t, "/custom/path", s.cfg.ProgressPath)
	})
}

func TestService_StartStop(t *testing.T) {
	t.Run("start without puller", func(t *testing.T) {
		cfg := config.Config{}
		svc := newTestService(cfg, nil, testLogger())

		ctx := context.Background()
		err := svc.Start(ctx)
		require.NoError(t, err)

		// Verify running
		health, err := svc.Health(ctx)
		require.NoError(t, err)
		assert.Equal(t, HealthOK, health.Status)

		// Stop
		err = svc.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("start already running", func(t *testing.T) {
		cfg := config.Config{}
		svc := newTestService(cfg, nil, testLogger())

		ctx := context.Background()
		err := svc.Start(ctx)
		require.NoError(t, err)
		defer svc.Stop(ctx)

		// Try to start again - returns error
		err = svc.Start(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already running")
	})

	t.Run("stop not running", func(t *testing.T) {
		cfg := config.Config{}
		svc := newTestService(cfg, nil, testLogger())

		ctx := context.Background()
		err := svc.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("start with puller", func(t *testing.T) {
		mockPuller := newMockPullerService()
		cfg := config.Config{}
		svc := newTestService(cfg, mockPuller, testLogger())

		ctx := context.Background()
		err := svc.Start(ctx)
		require.NoError(t, err)

		// Give subscription loop time to start
		time.Sleep(10 * time.Millisecond)

		err = svc.Stop(ctx)
		require.NoError(t, err)
	})
}

func TestService_ApplyEvent(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		err := svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		err = svc.ApplyEvent(context.Background(), nil, "")
		require.NoError(t, err)
	})

	t.Run("event without FullDocument", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		err := svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		evt := &ChangeEvent{
			EventID:      "evt1",
			Database:     "testdb",
			FullDocument: nil,
		}
		err = svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)
	})

	t.Run("event with empty collection", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		err := svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "",
			},
		}
		err = svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)
	})

	t.Run("event with no matching template", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		err := svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "unknown/collection",
			},
		}
		err = svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)
	})

	t.Run("event with matching template", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		s := svc.(*service)

		// Load templates
		templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/doc1",
				Data: map[string]any{
					"id":        "doc1",
					"timestamp": float64(1000),
				},
			},
		}
		err = svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)

		// Verify document was indexed
		stats, err := svc.Stats(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(1), stats.EventsApplied)
	})

	t.Run("deleted document without includeDeleted", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		s := svc.(*service)

		templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
    includeDeleted: false
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// First, insert a document
		evt1 := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/doc1",
				Data: map[string]any{
					"id":        "doc1",
					"timestamp": float64(1000),
				},
				Deleted: false,
			},
		}
		err = svc.ApplyEvent(context.Background(), evt1, "")
		require.NoError(t, err)

		// Verify indexed
		st := s.manager.Store().(*mem_store.Store)
		orderKey, found := st.Get("testdb", "users/*/chats", "chats_by_timestamp", "doc1")
		require.True(t, found, "document should be indexed")
		require.NotNil(t, orderKey)

		// Now mark as deleted
		evt2 := &ChangeEvent{
			EventID:  "evt2",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/doc1",
				Data: map[string]any{
					"id":        "doc1",
					"timestamp": float64(1000),
				},
				Deleted: true,
			},
		}
		err = svc.ApplyEvent(context.Background(), evt2, "")
		require.NoError(t, err)

		// Verify deleted from index
		_, found = st.Get("testdb", "users/*/chats", "chats_by_timestamp", "doc1")
		assert.False(t, found, "document should be deleted from index")
	})

	t.Run("deleted document with includeDeleted", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		s := svc.(*service)

		templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
    includeDeleted: true
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// Insert a deleted document
		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/doc1",
				Data: map[string]any{
					"id":        "doc1",
					"timestamp": float64(1000),
				},
				Deleted: true,
			},
		}
		err = svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)

		// Verify still in index (includeDeleted = true)
		st := s.manager.Store().(*mem_store.Store)
		orderKey, found := st.Get("testdb", "users/*/chats", "chats_by_timestamp", "doc1")
		require.True(t, found, "document should still be in index with includeDeleted=true")
		assert.NotNil(t, orderKey)
	})
}

func TestService_Search(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`
	err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
	require.NoError(t, err)

	err = svc.Start(context.Background())
	require.NoError(t, err)
	defer svc.Stop(context.Background())

	// Add some documents
	for i := 1; i <= 5; i++ {
		docID := "doc" + string(rune('0'+i))
		evt := &ChangeEvent{
			EventID:  "evt" + string(rune('0'+i)),
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         docID,
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/" + docID,
				Data: map[string]any{
					"id":        docID,
					"timestamp": float64(i * 1000),
				},
			},
		}
		err := svc.ApplyEvent(context.Background(), evt, "")
		require.NoError(t, err)
	}

	t.Run("basic search", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "testdb", Plan{
			Collection: "users/alice/chats",
			OrderBy: []OrderField{
				{Field: "timestamp", Direction: 1}, // Desc
			},
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 5)
	})

	t.Run("search with limit", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "testdb", Plan{
			Collection: "users/alice/chats",
			OrderBy: []OrderField{
				{Field: "timestamp", Direction: 1}, // Desc
			},
			Limit: 2,
		})
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("search non-existent database", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "nonexistent", Plan{
			Collection: "users/alice/chats",
			OrderBy: []OrderField{
				{Field: "timestamp", Direction: 1},
			},
		})
		// Returns empty results since no documents have been indexed yet
		assert.NoError(t, err)
		assert.Empty(t, results)
	})
}

func TestService_Health(t *testing.T) {
	t.Run("healthy when running", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		err := svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		health, err := svc.Health(context.Background())
		require.NoError(t, err)
		assert.Equal(t, HealthOK, health.Status)
	})

	t.Run("unhealthy when not running", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())

		health, err := svc.Health(context.Background())
		require.NoError(t, err)
		assert.Equal(t, HealthUnhealthy, health.Status)
	})
}

func TestService_Stats(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`
	err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
	require.NoError(t, err)

	err = svc.Start(context.Background())
	require.NoError(t, err)
	defer svc.Stop(context.Background())

	stats, err := svc.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TemplateCount)
	assert.Equal(t, int64(0), stats.EventsApplied)

	// Add an event
	evt := &ChangeEvent{
		EventID:  "evt1",
		Database: "testdb",
		FullDocument: &storage.StoredDoc{
			Id:         "doc1",
			Collection: "users/alice/chats",
			Fullpath:   "users/alice/chats/doc1",
			Data: map[string]any{
				"id":        "doc1",
				"timestamp": float64(1000),
			},
		},
	}
	err = svc.ApplyEvent(context.Background(), evt, "")
	require.NoError(t, err)

	stats, err = svc.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.EventsApplied)
	assert.Greater(t, stats.LastEventTime, int64(0))
}

func TestService_Manager(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	mgr := svc.Manager()
	assert.Same(t, s.manager, mgr)
}

func TestService_PullerSubscription(t *testing.T) {
	t.Run("receives events from puller", func(t *testing.T) {
		mockPuller := newMockPullerService()
		svc := newTestService(config.Config{}, mockPuller, testLogger())
		s := svc.(*service)

		templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// Give subscription loop time to start
		time.Sleep(50 * time.Millisecond)

		// Send an event through puller
		mockPuller.SendEvent(&puller.Event{
			Change: &events.StoreChangeEvent{
				EventID:  "evt1",
				Database: "testdb",
				FullDocument: &storage.StoredDoc{
					Id:         "doc1",
					Collection: "users/alice/chats",
					Fullpath:   "users/alice/chats/doc1",
					Data: map[string]any{
						"id":        "doc1",
						"timestamp": float64(1000),
					},
				},
			},
			Progress: "progress-1",
		})

		// Wait for event to be processed
		time.Sleep(100 * time.Millisecond)

		// Verify event was applied
		stats, err := svc.Stats(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(1), stats.EventsApplied)

		// Verify progress was updated
		s.mu.RLock()
		progress := s.progress
		s.mu.RUnlock()
		assert.Equal(t, "progress-1", progress)
	})
}

func TestExtractFieldValue(t *testing.T) {
	t.Run("simple field", func(t *testing.T) {
		data := map[string]any{
			"name":  "Alice",
			"count": 42,
		}
		assert.Equal(t, "Alice", extractFieldValue(data, "name"))
		assert.Equal(t, 42, extractFieldValue(data, "count"))
	})

	t.Run("missing field", func(t *testing.T) {
		data := map[string]any{
			"name": "Alice",
		}
		assert.Nil(t, extractFieldValue(data, "unknown"))
	})
}

func TestService_StartWithTemplatePath(t *testing.T) {
	t.Run("valid template directory", func(t *testing.T) {
		// Create temp template directory
		tmpDir, err := os.MkdirTemp("", "templates-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		templateYAML := `
database: default
templates:
  - name: test_template
    collectionPattern: users/{uid}/docs
    fields:
      - { field: timestamp, order: desc }
`
		err = os.WriteFile(tmpDir+"/default.yml", []byte(templateYAML), 0644)
		require.NoError(t, err)

		cfg := config.Config{
			TemplatePath: tmpDir,
		}
		svc := newTestService(cfg, nil, testLogger())

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// Verify templates were loaded
		stats, err := svc.Stats(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 1, stats.TemplateCount)
	})

	t.Run("invalid template directory", func(t *testing.T) {
		cfg := config.Config{
			TemplatePath: "/nonexistent/path/templates",
		}
		svc := newTestService(cfg, nil, testLogger())

		err := svc.Start(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load templates")
	})
}

func TestService_StopWithTimeout(t *testing.T) {
	t.Run("stop with context timeout", func(t *testing.T) {
		// Create a mock puller that keeps subscription alive
		mockPuller := &slowPullerService{}
		svc := newTestService(config.Config{}, mockPuller, testLogger())

		err := svc.Start(context.Background())
		require.NoError(t, err)

		// Give subscription loop time to start
		time.Sleep(10 * time.Millisecond)

		// Stop with an already canceled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Stop returns context.Canceled because context is already cancelled
		// However, since subscriptionLoop might return immediately (before context check in Stop),
		// we might get nil (successful stop). We accept both.
		err = svc.Stop(ctx)
		if err != nil {
			assert.Equal(t, context.Canceled, err)
		}

		// Clean up with a proper context
		svc.Stop(context.Background())
	})
}

type slowPullerService struct{}

func (s *slowPullerService) Subscribe(ctx context.Context, opts puller.SubscribeOptions) (puller.Subscription, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &channelSubscription{ctx: ctx, cancel: cancel, events: make(chan *puller.Event)}, nil
}

func TestService_SubscriptionTermination(t *testing.T) {
	mock := newMockPullerService()
	svc := newTestService(config.Config{}, mock, testLogger())
	require.NoError(t, svc.Start(context.Background()))
	defer svc.Stop(context.Background())
	mock.Close()
	require.Eventually(t, func() bool {
		health, err := svc.Health(context.Background())
		return health.Status == HealthUnhealthy && errors.Is(err, io.EOF)
	}, time.Second, time.Millisecond)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.subscribers, 1, "terminal closure must not silently open a fresh subscription")
}

type channelSubscription struct {
	ctx    context.Context
	cancel context.CancelFunc
	events <-chan *puller.Event
}

func (s *channelSubscription) Next(ctx context.Context) (*puller.Event, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return nil, io.EOF
		}
		return event, nil
	}
}
func (s *channelSubscription) Close() error { s.cancel(); return nil }

func TestService_ApplyEventWithUnsupportedType(t *testing.T) {
	t.Run("unsupported field type returns error", func(t *testing.T) {
		svc := newTestService(config.Config{}, nil, testLogger())
		s := svc.(*service)

		templateYAML := `
templates:
  - name: test_template
    collectionPattern: users/{uid}/docs
    fields:
      - { field: data, order: asc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// Event with unsupported type (slice)
		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/docs",
				Fullpath:   "users/alice/docs/doc1",
				Data: map[string]any{
					"id":   "doc1",
					"data": []string{"a", "b", "c"}, // Unsupported type
				},
			},
		}

		err = svc.ApplyEvent(context.Background(), evt, "")
		require.ErrorContains(t, err, "failed to build order key")

		// But the document should not be indexed
		st := s.manager.Store().(*mem_store.Store)
		_, found := st.Get("testdb", "users/*/docs", "test_template", "doc1")
		assert.False(t, found, "document with unsupported field type should not be indexed")
	})
}

func TestService_BuildOrderKey(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	t.Run("asc direction", func(t *testing.T) {
		templateYAML := `
templates:
  - name: test_asc
    collectionPattern: test/docs
    fields:
      - { field: value, order: asc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		tmpl := s.manager.Templates()[0]
		data := map[string]any{"value": float64(100)}

		key, err := s.buildOrderKey(data, &tmpl)
		require.NoError(t, err)
		assert.NotEmpty(t, key)
	})

	t.Run("desc direction", func(t *testing.T) {
		templateYAML := `
templates:
  - name: test_desc
    collectionPattern: test/docs
    fields:
      - { field: value, order: desc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		templates := s.manager.Templates()
		tmpl := templates[len(templates)-1] // Get the last loaded template
		data := map[string]any{"value": float64(100)}

		key, err := s.buildOrderKey(data, &tmpl)
		require.NoError(t, err)
		assert.NotEmpty(t, key)
	})

	t.Run("unsupported type returns error", func(t *testing.T) {
		templateYAML := `
templates:
  - name: test_unsupported
    collectionPattern: test/unsupported
    fields:
      - { field: value, order: asc }
`
		err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		templates := s.manager.Templates()
		tmpl := templates[len(templates)-1]
		data := map[string]any{"value": struct{}{}} // Unsupported type

		_, err = s.buildOrderKey(data, &tmpl)
		require.Error(t, err)
	})
}

func TestService_ApplyEventToTemplate_NilDoc(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	templateYAML := `
templates:
  - name: test_template
    collectionPattern: users/{uid}/docs
    fields:
      - { field: timestamp, order: desc }
`
	err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
	require.NoError(t, err)

	err = svc.Start(context.Background())
	require.NoError(t, err)
	defer svc.Stop(context.Background())

	tmpl := s.manager.Templates()[0]

	// Event with nil FullDocument
	evt := &ChangeEvent{
		EventID:      "evt1",
		Database:     "testdb",
		FullDocument: nil,
	}

	err = s.applyEventToTemplate(context.Background(), evt, &tmpl, "")
	require.NoError(t, err)
}

func TestService_ApplyEventToTemplate_DocIDFromFullpath(t *testing.T) {
	svc := newTestService(config.Config{}, nil, testLogger())
	s := svc.(*service)

	templateYAML := `
templates:
  - name: test_template
    collectionPattern: users
    fields:
      - { field: name, order: asc }
`
	err := s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
	require.NoError(t, err)

	err = svc.Start(context.Background())
	require.NoError(t, err)
	defer svc.Stop(context.Background())

	tmpl := s.manager.Templates()[0]

	// Event with FullDocument that has no "id" in Data but has Fullpath
	evt := &ChangeEvent{
		EventID:  "evt1",
		Database: "testdb",
		FullDocument: &storage.StoredDoc{
			Fullpath:   "users/user123", // ID should be extracted from here
			Collection: "users",
			Data: map[string]any{
				"name": "John",
				// No "id" field - should fallback to Fullpath
			},
		},
	}

	err = s.applyEventToTemplate(context.Background(), evt, &tmpl, "")
	require.NoError(t, err)

	// Verify the document was indexed
	st := s.manager.Store().(*mem_store.Store)
	orderKey, found := st.Get("testdb", "users", tmpl.Identity(), "user123")
	assert.True(t, found, "document should be indexed with ID extracted from Fullpath")
	assert.NotNil(t, orderKey)
}

// ============================================================================
// Pebble Storage Mode Tests
// ============================================================================

func TestNewService_PebbleMode(t *testing.T) {
	t.Run("valid pebble config", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "indexer-pebble-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		cfg := config.Config{
			StorageMode: "pebble",
			Store: config.StoreConfig{
				Path:           tmpDir + "/test.db",
				BatchSize:      100,
				BatchInterval:  50 * time.Millisecond,
				BlockCacheSize: 8 * 1024 * 1024,
			},
		}

		svc, err := NewService(cfg, nil, testLogger())
		require.NoError(t, err)
		require.NotNil(t, svc)

		// Start and stop to verify it works
		ctx := context.Background()
		err = svc.Start(ctx)
		require.NoError(t, err)

		err = svc.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("pebble mode without logger uses provided logger", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "indexer-pebble-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		cfg := config.Config{
			StorageMode: "pebble",
			Store: config.StoreConfig{
				Path:           tmpDir + "/test.db",
				BatchSize:      100,
				BatchInterval:  50 * time.Millisecond,
				BlockCacheSize: 8 * 1024 * 1024,
			},
		}

		logger := testLogger()
		svc, err := NewService(cfg, nil, logger)
		require.NoError(t, err)
		require.NotNil(t, svc)
		defer svc.Stop(context.Background())
	})

	t.Run("invalid storage mode", func(t *testing.T) {
		cfg := config.Config{
			StorageMode: "invalid_mode",
		}

		_, err := NewService(cfg, nil, testLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown storage mode")
	})

	t.Run("pebble mode with empty path fails", func(t *testing.T) {
		cfg := config.Config{
			StorageMode: "pebble",
			Store: config.StoreConfig{
				Path: "", // Empty path should fail
			},
		}

		_, err := NewService(cfg, nil, testLogger())
		require.Error(t, err)
	})
}

func TestService_PebbleMode_ApplyEvent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "indexer-pebble-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.Config{
		StorageMode: "pebble",
		Store: config.StoreConfig{
			Path:           tmpDir + "/test.db",
			BatchSize:      100,
			BatchInterval:  50 * time.Millisecond,
			BlockCacheSize: 8 * 1024 * 1024,
		},
	}

	svc, err := NewService(cfg, nil, testLogger())
	require.NoError(t, err)
	require.NotNil(t, svc)

	s := svc.(*service)

	// Load templates
	templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`
	err = s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
	require.NoError(t, err)

	err = svc.Start(context.Background())
	require.NoError(t, err)
	defer svc.Stop(context.Background())

	// Apply an event with progress
	evt := &ChangeEvent{
		EventID:  "evt1",
		Database: "testdb",
		FullDocument: &storage.StoredDoc{
			Id:         "doc1",
			Collection: "users/alice/chats",
			Fullpath:   "users/alice/chats/doc1",
			Data: map[string]any{
				"id":        "doc1",
				"timestamp": float64(1000),
			},
		},
	}
	err = svc.ApplyEvent(context.Background(), evt, "event-progress-1")
	require.NoError(t, err)

	// Verify event was applied
	stats, err := svc.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.EventsApplied)

	// Verify progress was saved via store
	progress, err := s.store.LoadProgress()
	require.NoError(t, err)
	assert.Equal(t, "event-progress-1", progress)
}

func TestService_PebbleMode_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "indexer-pebble-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := tmpDir + "/test.db"
	templateYAML := `
templates:
  - name: chats_by_timestamp
    collectionPattern: users/{uid}/chats
    fields:
      - { field: timestamp, order: desc }
`

	// First service instance - insert data
	{
		cfg := config.Config{
			StorageMode: "pebble",
			Store: config.StoreConfig{
				Path:           dbPath,
				BatchSize:      100,
				BatchInterval:  50 * time.Millisecond,
				BlockCacheSize: 8 * 1024 * 1024,
			},
		}

		svc, err := NewService(cfg, nil, testLogger())
		require.NoError(t, err)

		s := svc.(*service)
		err = s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)

		// Apply event with progress
		evt := &ChangeEvent{
			EventID:  "evt1",
			Database: "testdb",
			FullDocument: &storage.StoredDoc{
				Id:         "doc1",
				Collection: "users/alice/chats",
				Fullpath:   "users/alice/chats/doc1",
				Data: map[string]any{
					"id":        "doc1",
					"timestamp": float64(1000),
				},
			},
		}
		err = svc.ApplyEvent(context.Background(), evt, "persist-test-progress")
		require.NoError(t, err)

		// Close the service
		err = svc.Stop(context.Background())
		require.NoError(t, err)
	}

	// Second service instance - verify data persisted
	{
		cfg := config.Config{
			StorageMode: "pebble",
			Store: config.StoreConfig{
				Path:           dbPath,
				BatchSize:      100,
				BatchInterval:  50 * time.Millisecond,
				BlockCacheSize: 8 * 1024 * 1024,
			},
		}

		svc, err := NewService(cfg, nil, testLogger())
		require.NoError(t, err)

		s := svc.(*service)
		err = s.manager.LoadTemplatesFromBytes([]byte(templateYAML))
		require.NoError(t, err)

		err = svc.Start(context.Background())
		require.NoError(t, err)
		defer svc.Stop(context.Background())

		// Verify progress was loaded from persistent store
		s.mu.RLock()
		progress := s.progress
		s.mu.RUnlock()
		assert.Equal(t, "persist-test-progress", progress)
	}
}

func TestService_InvalidateDatabase(t *testing.T) {
	cfg := config.Config{
		StorageMode: config.StorageModeMemory,
	}

	svc, err := NewService(cfg, nil, testLogger())
	require.NoError(t, err)

	// Insert some data
	s := svc.(*service)
	store := s.manager.Store().(*mem_store.Store)

	// Add entries for two databases
	err = store.Upsert("db1", "users/*", "tmpl1", "user1", []byte{0x01}, "")
	require.NoError(t, err)
	err = store.Upsert("db2", "posts/*", "tmpl2", "post1", []byte{0x02}, "")
	require.NoError(t, err)

	// Verify both databases have indexes
	indexes, err := store.ListIndexes("db1")
	require.NoError(t, err)
	assert.Len(t, indexes, 1)

	indexes, err = store.ListIndexes("db2")
	require.NoError(t, err)
	assert.Len(t, indexes, 1)

	// Invalidate db1
	err = svc.InvalidateDatabase(context.Background(), "db1")
	require.NoError(t, err)

	// Verify db1 indexes are gone (returns error for non-existent db)
	_, err = store.ListIndexes("db1")
	assert.Error(t, err) // db1 no longer exists

	// Verify db2 indexes remain
	indexes, err = store.ListIndexes("db2")
	require.NoError(t, err)
	assert.Len(t, indexes, 1)
}

func TestService_InvalidateDatabase_NonExistent(t *testing.T) {
	cfg := config.Config{
		StorageMode: config.StorageModeMemory,
	}

	svc, err := NewService(cfg, nil, testLogger())
	require.NoError(t, err)

	// Invalidating a non-existent database should not error
	err = svc.InvalidateDatabase(context.Background(), "nonexistent")
	require.NoError(t, err)
}

// mockStoreWithDeleteDatabaseError is a mock store that returns an error on DeleteDatabase.
type mockStoreWithDeleteDatabaseError struct {
	store.Store
	deleteDatabaseErr error
}

func (m *mockStoreWithDeleteDatabaseError) DeleteDatabase(db string) error {
	if m.deleteDatabaseErr != nil {
		return m.deleteDatabaseErr
	}
	return m.Store.DeleteDatabase(db)
}

func TestService_InvalidateDatabase_StoreError(t *testing.T) {
	// Create a regular service first
	cfg := config.Config{
		StorageMode: config.StorageModeMemory,
	}

	svc, err := NewService(cfg, nil, testLogger())
	require.NoError(t, err)

	s := svc.(*service)

	// Wrap the manager's store with our error-returning mock
	originalStore := s.manager.Store()
	mockStore := &mockStoreWithDeleteDatabaseError{
		Store:             originalStore,
		deleteDatabaseErr: errors.New("mock delete database error"),
	}

	// Create a new manager with the mock store
	s.manager = manager.New(mockStore)

	// InvalidateDatabase should return error
	err = svc.InvalidateDatabase(context.Background(), "testdb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete database indexes")
	assert.Contains(t, err.Error(), "mock delete database error")
}

func TestSubscriptionDoesNotAdvancePastFailedApply(t *testing.T) {
	mock := newMockPullerService()
	svc := newTestService(config.Config{}, mock, testLogger())
	s := svc.(*service)
	require.NoError(t, s.manager.LoadTemplatesFromBytes([]byte(`
templates:
  - name: docs_by_value
    collectionPattern: docs
    fields:
      - { field: value, order: asc }
`)))
	require.NoError(t, svc.Start(context.Background()))
	defer svc.Stop(context.Background())
	mock.SendEvent(&puller.Event{Progress: "p1"})
	mock.SendEvent(&puller.Event{Change: &events.StoreChangeEvent{
		EventID: "bad", Database: "db", FullDocument: &storage.StoredDoc{
			Collection: "docs", Fullpath: "docs/bad", Data: map[string]any{"id": "bad", "value": []string{"unsupported"}},
		},
	}, Progress: "p2"})
	mock.SendEvent(&puller.Event{Progress: "p3"})
	require.Eventually(t, func() bool {
		health, err := svc.Health(context.Background())
		return health.Status == HealthUnhealthy && err != nil
	}, time.Second, time.Millisecond)
	s.mu.RLock()
	progress := s.progress
	s.mu.RUnlock()
	require.Equal(t, "p1", progress)
	persisted, err := s.store.LoadProgress()
	require.NoError(t, err)
	require.Equal(t, "p1", persisted)
}

type failingStore struct {
	store.Store
	failure error
	failed  chan struct{}
}

func (s *failingStore) Failed() <-chan struct{}                                     { return s.failed }
func (s *failingStore) Err() error                                                  { return s.failure }
func (s *failingStore) Upsert(string, string, string, string, []byte, string) error { return s.failure }
func (s *failingStore) Delete(string, string, string, string, string) error         { return s.failure }
func (s *failingStore) LoadProgress() (string, error)                               { return "", s.failure }

func TestStartRejectsCheckpointReadFailure(t *testing.T) {
	s := newTestService(config.Config{}, nil, testLogger()).(*service)
	cause := errors.New("checkpoint read failed")
	s.store = &failingStore{Store: s.store, failure: cause}
	require.ErrorIs(t, s.Start(context.Background()), cause)
	require.NoError(t, s.Stop(context.Background()))
}

func TestApplyPropagatesStoreMutationErrors(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%v", deleted), func(t *testing.T) {
			s := newTestService(config.Config{}, nil, testLogger()).(*service)
			cause := errors.New("write failed")
			st := &failingStore{Store: s.store, failure: cause}
			s.manager = manager.New(st)
			require.NoError(t, s.manager.LoadTemplatesFromBytes([]byte(`
templates:
  - name: docs_by_value
    collectionPattern: docs
    fields:
      - { field: value, order: asc }
`)))
			event := &events.StoreChangeEvent{Database: "db", FullDocument: &storage.StoredDoc{
				Collection: "docs", Fullpath: "docs/one", Deleted: deleted, Data: map[string]any{"id": "one", "value": "a"},
			}}
			require.ErrorIs(t, s.ApplyEvent(context.Background(), event, "later"), cause)
			progress, err := s.store.LoadProgress()
			require.NoError(t, err)
			require.Empty(t, progress)
			require.NoError(t, s.Stop(context.Background()))
		})
	}
}

type asyncFailingStore struct {
	store.Store
	cause  error
	failed chan struct{}
}

func (s *asyncFailingStore) Failed() <-chan struct{} { return s.failed }
func (s *asyncFailingStore) Err() error {
	select {
	case <-s.failed:
		return s.cause
	default:
		return nil
	}
}

func TestStorageFailureCancelsBlockedSubscription(t *testing.T) {
	mock := newMockPullerService()
	s := newTestService(config.Config{}, mock, testLogger()).(*service)
	cause := errors.New("asynchronous commit failed")
	st := &asyncFailingStore{Store: s.store, cause: cause, failed: make(chan struct{})}
	s.store = st
	s.manager = manager.New(st)
	require.NoError(t, s.Start(context.Background()))
	close(st.failed)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("storage failure did not interrupt Next")
	}
	health, err := s.Health(context.Background())
	require.Equal(t, HealthUnhealthy, health.Status)
	require.ErrorIs(t, err, cause)
	require.NoError(t, s.Stop(context.Background()))
}
