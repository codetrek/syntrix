package evaluator

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/internal/trigger/evaluator/watcher"
	"github.com/syntrixbase/syntrix/internal/trigger/types"
)

// MockDocumentWatcher mocks watcher.DocumentWatcher
type MockDocumentWatcher struct {
	mock.Mock
}

func (m *MockDocumentWatcher) Watch(ctx context.Context) (watcher.WatcherStream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watcher.WatcherStream), args.Error(1)
}

func (m *MockDocumentWatcher) SaveCheckpoint(ctx context.Context, token interface{}) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockDocumentWatcher) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockTaskPublisher mocks TaskPublisher
type MockTaskPublisher struct {
	mock.Mock
}

func (m *MockTaskPublisher) Publish(ctx context.Context, task *types.DeliveryTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

func (m *MockTaskPublisher) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockPuller mocks puller.Service
type MockPuller struct {
	mock.Mock
}

func (m *MockPuller) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(events.Subscription), args.Error(1)
}

type testWatcherStream struct {
	next     func(context.Context) (events.SyntrixChangeEvent, error)
	closed   bool
	closeErr error
}

func (s *testWatcherStream) Next(ctx context.Context) (events.SyntrixChangeEvent, error) {
	return s.next(ctx)
}

func (s *testWatcherStream) Close() error {
	s.closed = true
	return s.closeErr
}

func triggerEvent(progress string) events.SyntrixChangeEvent {
	return events.SyntrixChangeEvent{
		Type:     events.EventCreate,
		Document: &storage.StoredDoc{Id: "doc1", Database: "db1", Collection: "users", Data: map[string]interface{}{"name": "test"}},
		Progress: progress,
	}
}

func testService(t *testing.T, w watcher.DocumentWatcher, publisher TaskPublisher) *service {
	t.Helper()
	eval, err := NewEvaluator()
	require.NoError(t, err)
	return &service{
		evaluator: eval, watcher: w, publisher: publisher,
		triggers: []*types.Trigger{{ID: "trigger1", Database: "db1", Collection: "users", Events: []string{"create", "delete"}, URL: "https://example.com/webhook"}},
	}
}

func TestService_LoadTriggers(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	triggers := []*types.Trigger{
		{
			ID:         "trigger1",
			Database:   "db1",
			Collection: "users",
			Events:     []string{"create"},
			URL:        "https://example.com/webhook",
		},
	}

	err = svc.LoadTriggers(triggers)
	assert.NoError(t, err)
	assert.Len(t, svc.triggers, 1)
}

func TestService_LoadTriggers_ValidationError(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	// Invalid trigger - missing ID
	triggers := []*types.Trigger{
		{
			Database:   "db1",
			Collection: "users",
			Events:     []string{"create"},
			URL:        "https://example.com/webhook",
		},
	}

	err = svc.LoadTriggers(triggers)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "trigger id is required")
}

func TestService_Start_PublishesBeforeCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		event   events.SyntrixChangeEvent
		publish bool
	}{
		{name: "document", event: triggerEvent("p1"), publish: true},
		{name: "before image", event: events.SyntrixChangeEvent{Type: events.EventDelete, Before: triggerEvent("").Document, Progress: "p1"}, publish: true},
		{name: "progress only", event: events.SyntrixChangeEvent{Progress: "p1"}},
		{name: "unmatched", event: events.SyntrixChangeEvent{Type: events.EventUpdate, Document: triggerEvent("").Document, Progress: "p1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			w := new(MockDocumentWatcher)
			pub := new(MockTaskPublisher)
			svc := testService(t, w, pub)
			first := true
			stream := &testWatcherStream{next: func(ctx context.Context) (events.SyntrixChangeEvent, error) {
				if first {
					first = false
					return tc.event, nil
				}
				<-ctx.Done()
				return events.SyntrixChangeEvent{}, ctx.Err()
			}}
			w.On("Watch", mock.Anything).Return(stream, nil).Once()
			published := make(chan struct{})
			if tc.publish {
				pub.On("Publish", mock.Anything, mock.MatchedBy(func(task *types.DeliveryTask) bool {
					return task.DocumentID == "doc1" && task.Database == "db1" && task.Collection == "users"
				})).Run(func(mock.Arguments) { close(published) }).Return(nil).Once()
			}
			w.On("SaveCheckpoint", mock.Anything, "p1").Run(func(mock.Arguments) {
				if tc.publish {
					select {
					case <-published:
					default:
						t.Error("checkpoint preceded publication")
					}
				}
				cancel()
			}).Return(nil).Once()
			require.NoError(t, svc.Start(ctx))
			assert.True(t, stream.closed)
			w.AssertExpectations(t)
			pub.AssertExpectations(t)
		})
	}
}

func TestService_Start_StopsBeforeLaterEventOnProcessingFailure(t *testing.T) {
	for _, failure := range []string{"evaluation", "publication", "missing publisher", "missing document"} {
		t.Run(failure, func(t *testing.T) {
			w := new(MockDocumentWatcher)
			pub := new(MockTaskPublisher)
			svc := testService(t, w, pub)
			if failure == "evaluation" {
				svc.triggers[0].Condition = "invalid.cel.expression["
			}
			if failure == "missing publisher" {
				svc.publisher = nil
			}
			if failure == "publication" {
				pub.On("Publish", mock.Anything, mock.Anything).Return(assert.AnError).Once()
			}
			nextCalls := 0
			stream := &testWatcherStream{next: func(ctx context.Context) (events.SyntrixChangeEvent, error) {
				nextCalls++
				if failure == "missing document" {
					return events.SyntrixChangeEvent{Type: events.EventCreate, Progress: "p1"}, nil
				}
				return triggerEvent("p1"), nil
			}}
			w.On("Watch", mock.Anything).Return(stream, nil).Once()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := svc.Start(ctx)
			require.Error(t, err)
			if failure == "publication" {
				require.ErrorIs(t, err, assert.AnError)
			}
			assert.Equal(t, 1, nextCalls)
			assert.True(t, stream.closed)
			w.AssertNotCalled(t, "SaveCheckpoint", mock.Anything, mock.Anything)
			pub.AssertExpectations(t)
		})
	}
}

func TestService_Start_PublishFailurePreservesSavedPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := new(MockDocumentWatcher)
	pub := new(MockTaskPublisher)
	svc := testService(t, w, pub)
	saved := make(chan struct{})
	nextCalls := 0
	stream := &testWatcherStream{next: func(ctx context.Context) (events.SyntrixChangeEvent, error) {
		nextCalls++
		if nextCalls == 1 {
			return triggerEvent("p1"), nil
		}
		select {
		case <-saved:
			evt := triggerEvent("p2")
			evt.Document.Id = "doc2"
			return evt, nil
		case <-ctx.Done():
			return events.SyntrixChangeEvent{}, ctx.Err()
		}
	}}
	w.On("Watch", mock.Anything).Return(stream, nil).Once()
	w.On("SaveCheckpoint", mock.Anything, "p1").Run(func(mock.Arguments) { close(saved) }).Return(nil).Once()
	pub.On("Publish", mock.Anything, mock.MatchedBy(func(task *types.DeliveryTask) bool { return task.DocumentID == "doc1" })).Return(nil).Once()
	pub.On("Publish", mock.Anything, mock.MatchedBy(func(task *types.DeliveryTask) bool { return task.DocumentID == "doc2" })).Return(assert.AnError).Once()
	require.ErrorIs(t, svc.Start(ctx), assert.AnError)
	assert.Equal(t, 2, nextCalls)
	w.AssertExpectations(t)
	pub.AssertExpectations(t)
}

func TestService_Start_WatchError(t *testing.T) {
	w := new(MockDocumentWatcher)
	svc := testService(t, w, nil)
	w.On("Watch", mock.Anything).Return(nil, assert.AnError).Once()
	require.ErrorIs(t, svc.Start(context.Background()), assert.AnError)
}

func TestService_Start_UpstreamFailureCancelsBlockedCheckpointSaver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := new(MockDocumentWatcher)
	svc := testService(t, w, nil)
	started := make(chan struct{})
	saverCancelled := make(chan struct{})
	upstreamErr := errors.New("source continuity lost")
	first := true
	stream := &testWatcherStream{next: func(ctx context.Context) (events.SyntrixChangeEvent, error) {
		if first {
			first = false
			return events.SyntrixChangeEvent{Progress: "p1"}, nil
		}
		select {
		case <-started:
			return events.SyntrixChangeEvent{}, upstreamErr
		case <-ctx.Done():
			return events.SyntrixChangeEvent{}, ctx.Err()
		}
	}}
	w.On("Watch", mock.Anything).Return(stream, nil).Once()
	w.On("SaveCheckpoint", mock.Anything, "p1").Run(func(args mock.Arguments) {
		close(started)
		<-args.Get(0).(context.Context).Done()
		close(saverCancelled)
	}).Return(context.Canceled).Once()
	require.ErrorIs(t, svc.Start(ctx), upstreamErr)
	require.NoError(t, ctx.Err(), "upstream termination must not wait for caller cancellation")
	select {
	case <-saverCancelled:
	default:
		t.Fatal("checkpoint saver was not canceled")
	}
	assert.True(t, stream.closed)
	w.AssertExpectations(t)
}

func TestService_Start_CheckpointFailureCancelsNextAndDoesNotAdvance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := new(MockDocumentWatcher)
	svc := testService(t, w, nil)
	first := true
	stream := &testWatcherStream{next: func(ctx context.Context) (events.SyntrixChangeEvent, error) {
		if first {
			first = false
			return events.SyntrixChangeEvent{Progress: "p1"}, nil
		}
		<-ctx.Done()
		return events.SyntrixChangeEvent{}, ctx.Err()
	}}
	w.On("Watch", mock.Anything).Return(stream, nil).Once()
	w.On("SaveCheckpoint", mock.Anything, "p1").Return(assert.AnError).Once()
	require.ErrorIs(t, svc.Start(ctx), assert.AnError)
	require.NoError(t, ctx.Err())
	assert.True(t, stream.closed)
	w.AssertExpectations(t)
}

func TestService_Start_EmptyStreamTerminatesWithoutCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := new(MockDocumentWatcher)
	svc := testService(t, w, nil)
	stream := &testWatcherStream{next: func(context.Context) (events.SyntrixChangeEvent, error) { return events.SyntrixChangeEvent{}, io.EOF }, closeErr: assert.AnError}
	w.On("Watch", mock.Anything).Return(stream, nil).Once()
	err := svc.Start(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.ErrorIs(t, err, assert.AnError)
	require.NoError(t, ctx.Err())
	assert.True(t, stream.closed)
}

func TestService_Close(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	mockWatcher.On("Close").Return(nil)
	mockPublisher.On("Close").Return(nil)

	err = svc.Close()
	assert.NoError(t, err)
	mockWatcher.AssertExpectations(t)
	mockPublisher.AssertExpectations(t)
}

func TestService_Close_WithWatcherError(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	mockWatcher.On("Close").Return(errors.New("watcher close error"))
	mockPublisher.On("Close").Return(nil)

	err = svc.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "watcher close error")
}

func TestService_Close_WithPublisherError(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	mockWatcher.On("Close").Return(nil)
	mockPublisher.On("Close").Return(errors.New("publisher close error"))

	err = svc.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "publisher close error")
}

func TestService_Close_WithBothErrors(t *testing.T) {
	mockWatcher := new(MockDocumentWatcher)
	mockPublisher := new(MockTaskPublisher)
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	mockWatcher.On("Close").Return(errors.New("watcher close error"))
	mockPublisher.On("Close").Return(errors.New("publisher close error"))

	err = svc.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "watcher close error")
	assert.Contains(t, err.Error(), "publisher close error")
}

func TestService_Close_Success(t *testing.T) {
	eval, err := NewEvaluator()
	require.NoError(t, err)

	svc := &service{
		evaluator: eval,
		watcher:   nil,
		publisher: nil,
	}

	err = svc.Close()
	assert.NoError(t, err)
}

func TestNewService_NoPuller(t *testing.T) {
	deps := Dependencies{
		Puller: nil,
	}
	cfg := Config{}

	_, err := NewService(deps, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "puller service is required")
}

func TestNewService_Success(t *testing.T) {
	mockPuller := new(MockPuller)

	deps := Dependencies{
		Puller: mockPuller,
	}
	cfg := Config{}

	svc, err := NewService(deps, cfg)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewService_WithRulesPath(t *testing.T) {
	mockPuller := new(MockPuller)

	// Create directory with trigger file in new format
	tmpDir := t.TempDir()
	content := `database: db1
triggers:
  trigger1:
    collection: users
    events:
      - create
    url: https://example.com/webhook
`

	err := os.WriteFile(filepath.Join(tmpDir, "db1.yml"), []byte(content), 0644)
	require.NoError(t, err)

	deps := Dependencies{
		Puller: mockPuller,
	}
	cfg := Config{
		RulesPath: tmpDir,
	}

	svc, err := NewService(deps, cfg)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewService_WithInvalidRulesPath(t *testing.T) {
	mockPuller := new(MockPuller)

	deps := Dependencies{
		Puller: mockPuller,
	}
	cfg := Config{
		RulesPath: "/nonexistent/directory",
	}

	_, err := NewService(deps, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load trigger rules")
}

func TestNewService_WithInvalidTrigger(t *testing.T) {
	mockPuller := new(MockPuller)

	// Invalid trigger - missing collection
	tmpDir := t.TempDir()
	content := `database: db1
triggers:
  trigger1:
    events:
      - create
    url: https://example.com/webhook
`

	err := os.WriteFile(filepath.Join(tmpDir, "db1.yml"), []byte(content), 0644)
	require.NoError(t, err)

	deps := Dependencies{
		Puller: mockPuller,
	}
	cfg := Config{
		RulesPath: tmpDir,
	}

	_, err = NewService(deps, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load triggers")
}

func TestValidateTrigger(t *testing.T) {
	tests := []struct {
		name      string
		trigger   *types.Trigger
		wantError string
	}{
		{
			name: "valid trigger",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "",
		},
		{
			name: "missing id",
			trigger: &types.Trigger{
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "trigger id is required",
		},
		{
			name: "invalid id",
			trigger: &types.Trigger{
				ID:         "trigger.1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "invalid trigger id",
		},
		{
			name: "missing database",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "database is required",
		},
		{
			name: "invalid database",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db.1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "invalid database",
		},
		{
			name: "database too long",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "database name too long",
		},
		{
			name: "missing collection",
			trigger: &types.Trigger{
				ID:       "trigger1",
				Database: "db1",
				Events:   []string{"create"},
				URL:      "https://example.com/webhook",
			},
			wantError: "collection is required",
		},
		{
			name: "collection too long",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz",
				Events:     []string{"create"},
				URL:        "https://example.com/webhook",
			},
			wantError: "collection name too long",
		},
		{
			name: "missing events",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				URL:        "https://example.com/webhook",
			},
			wantError: "at least one event is required",
		},
		{
			name: "invalid event",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"invalid"},
				URL:        "https://example.com/webhook",
			},
			wantError: "invalid event type",
		},
		{
			name: "missing url",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
			},
			wantError: "url is required",
		},
		{
			name: "invalid url scheme",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "ftp://example.com/webhook",
			},
			wantError: "url must use http or https scheme",
		},
		{
			name: "url missing host",
			trigger: &types.Trigger{
				ID:         "trigger1",
				Database:   "db1",
				Collection: "users",
				Events:     []string{"create"},
				URL:        "https:///webhook",
			},
			wantError: "url must have a host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTrigger(tt.trigger)
			if tt.wantError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			}
		})
	}
}
