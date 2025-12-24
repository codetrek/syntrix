package trigger

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockEvaluator struct {
	mock.Mock
}

func (m *MockEvaluator) Evaluate(ctx context.Context, trigger *Trigger, event *storage.Event) (bool, error) {
	args := m.Called(ctx, trigger, event)
	return args.Bool(0), args.Error(1)
}

type MockPublisher struct {
	mock.Mock
}

func (m *MockPublisher) Publish(ctx context.Context, task *DeliveryTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

func TestProcessEvent(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	service := NewTriggerService(evaluator, publisher)

	trigger := &Trigger{
		ID:         "trig-1",
		Tenant:     "acme",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}
	service.LoadTriggers([]*Trigger{trigger})

	event := &storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "user-1",
			Collection: "users",
			Data:       map[string]interface{}{"name": "Alice"},
		},
	}

	// Expectation: Evaluate is called
	evaluator.On("Evaluate", mock.Anything, trigger, event).Return(true, nil)

	// Expectation: Publish is called
	publisher.On("Publish", mock.Anything, mock.MatchedBy(func(task *DeliveryTask) bool {
		return task.TriggerID == "trig-1" && task.DocKey == "user-1"
	})).Return(nil)

	err := service.ProcessEvent(context.Background(), event)
	assert.NoError(t, err)

	evaluator.AssertExpectations(t)
	publisher.AssertExpectations(t)
}

func TestProcessEvent_NoMatch(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	service := NewTriggerService(evaluator, publisher)

	trigger := &Trigger{ID: "trig-1"}
	service.LoadTriggers([]*Trigger{trigger})

	event := &storage.Event{Type: storage.EventCreate}

	// Expectation: Evaluate returns false
	evaluator.On("Evaluate", mock.Anything, trigger, event).Return(false, nil)

	// Expectation: Publish is NOT called

	err := service.ProcessEvent(context.Background(), event)
	assert.NoError(t, err)

	evaluator.AssertExpectations(t)
	publisher.AssertNotCalled(t, "Publish")
}

type MockStorageBackend struct {
	mock.Mock
}

func (m *MockStorageBackend) Get(ctx context.Context, path string) (*storage.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockStorageBackend) Create(ctx context.Context, doc *storage.Document) error {
	args := m.Called(ctx, doc)
	return args.Error(0)
}

func (m *MockStorageBackend) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Delete(ctx context.Context, path string, pred model.Filters) error {
	args := m.Called(ctx, path, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Query(ctx context.Context, q model.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockStorageBackend) Watch(ctx context.Context, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	args := m.Called(ctx, collection, resumeToken, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockStorageBackend) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestWatch(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	backend := new(MockStorageBackend)
	service := NewTriggerService(evaluator, publisher)
	service.LoadTriggers([]*Trigger{{ID: "t1"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Mock Checkpoint Get (Not Found)
	backend.On("Get", ctx, "sys/checkpoints/trigger_evaluator").Return(nil, model.ErrNotFound)

	// 2. Mock Watch
	eventChan := make(chan storage.Event, 1)
	backend.On("Watch", ctx, "", nil, storage.WatchOptions{IncludeBefore: true}).Return((<-chan storage.Event)(eventChan), nil)

	// 3. Mock Update Checkpoint (after processing event)
	backend.On("Update", ctx, "sys/checkpoints/trigger_evaluator", mock.Anything, model.Filters{}).Return(nil)

	// 4. Start Watch in Goroutine
	errChan := make(chan error)
	go func() {
		errChan <- service.Watch(ctx, backend)
	}()

	// 5. Send Event
	event := storage.Event{
		Type:        storage.EventCreate,
		ResumeToken: "token-1",
		Document: &storage.Document{
			Collection: "users",
			Data:       map[string]interface{}{"name": "Bob"},
		},
	}
	eventChan <- event

	// Wait a bit for processing
	// In a real test we might want to synchronize better, but for now sleep is okay or we can rely on mock assertions
	// However, ProcessEvent calls Evaluate. We need to mock that too.
	evaluator.On("Evaluate", ctx, mock.Anything, &event).Return(false, nil)

	// Close channel to stop Watch
	close(eventChan)

	// Wait for Watch to return
	err := <-errChan
	assert.NoError(t, err)

	backend.AssertExpectations(t)
	evaluator.AssertExpectations(t)
}
