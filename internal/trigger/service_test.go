package trigger

import (
	"context"
	"syntrix/internal/storage"
	"testing"

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
