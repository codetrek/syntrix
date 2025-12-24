package trigger

import (
	"context"
	"errors"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestProcessEvent_EvaluateError(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	service := NewTriggerService(evaluator, publisher)

	trigger := &Trigger{ID: "trig-1"}
	service.LoadTriggers([]*Trigger{trigger})

	event := &storage.Event{Type: storage.EventCreate}

	// Expectation: Evaluate returns error
	evaluator.On("Evaluate", mock.Anything, trigger, event).Return(false, errors.New("eval error"))

	// Expectation: Publish is NOT called
	// ProcessEvent should log error and continue (return nil)

	err := service.ProcessEvent(context.Background(), event)
	assert.NoError(t, err)

	evaluator.AssertExpectations(t)
	publisher.AssertNotCalled(t, "Publish")
}

func TestProcessEvent_PublishError(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	service := NewTriggerService(evaluator, publisher)

	trigger := &Trigger{ID: "trig-1"}
	service.LoadTriggers([]*Trigger{trigger})

	event := &storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "doc-1",
			Collection: "users",
		},
	}

	// Expectation: Evaluate returns true
	evaluator.On("Evaluate", mock.Anything, trigger, event).Return(true, nil)

	// Expectation: Publish returns error
	publisher.On("Publish", mock.Anything, mock.Anything).Return(errors.New("pub error"))

	err := service.ProcessEvent(context.Background(), event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pub error")

	evaluator.AssertExpectations(t)
	publisher.AssertExpectations(t)
}

func TestWatch_CheckpointLoadError(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	backend := new(MockStorageBackend)
	service := NewTriggerService(evaluator, publisher)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Mock Checkpoint Get Error (not ErrNotFound)
	backend.On("Get", ctx, "sys/checkpoints/trigger_evaluator").Return(nil, errors.New("db error"))

	// 2. Mock Watch (should still proceed)
	eventChan := make(chan storage.Event)
	close(eventChan)
	backend.On("Watch", ctx, "", nil, storage.WatchOptions{IncludeBefore: true}).Return((<-chan storage.Event)(eventChan), nil)

	err := service.Watch(ctx, backend)
	assert.NoError(t, err)

	backend.AssertExpectations(t)
}

func TestWatch_CheckpointSave_Create(t *testing.T) {
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

	// 3. Mock Update Checkpoint -> ErrNotFound
	backend.On("Update", ctx, "sys/checkpoints/trigger_evaluator", mock.Anything, model.Filters{}).Return(model.ErrNotFound)

	// 4. Mock Create Checkpoint
	backend.On("Create", ctx, mock.MatchedBy(func(doc *storage.Document) bool {
		return doc.Id == "sys/checkpoints/trigger_evaluator"
	})).Return(nil)

	// 5. Send Event
	event := storage.Event{
		Type:        storage.EventCreate,
		ResumeToken: "token-1",
	}
	eventChan <- event

	evaluator.On("Evaluate", ctx, mock.Anything, &event).Return(false, nil)

	errChan := make(chan error)
	go func() {
		errChan <- service.Watch(ctx, backend)
	}()

	close(eventChan)
	err := <-errChan
	assert.NoError(t, err)

	backend.AssertExpectations(t)
}

func TestWatch_CheckpointSave_Error(t *testing.T) {
	evaluator := new(MockEvaluator)
	publisher := new(MockPublisher)
	backend := new(MockStorageBackend)
	service := NewTriggerService(evaluator, publisher)
	service.LoadTriggers([]*Trigger{{ID: "t1"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend.On("Get", ctx, "sys/checkpoints/trigger_evaluator").Return(nil, model.ErrNotFound)

	eventChan := make(chan storage.Event, 1)
	backend.On("Watch", ctx, "", nil, storage.WatchOptions{IncludeBefore: true}).Return((<-chan storage.Event)(eventChan), nil)

	// Mock Update Checkpoint -> Error
	backend.On("Update", ctx, "sys/checkpoints/trigger_evaluator", mock.Anything, model.Filters{}).Return(errors.New("update error"))

	event := storage.Event{
		Type:        storage.EventCreate,
		ResumeToken: "token-1",
	}
	eventChan <- event

	evaluator.On("Evaluate", ctx, mock.Anything, &event).Return(false, nil)

	errChan := make(chan error)
	go func() {
		errChan <- service.Watch(ctx, backend)
	}()

	close(eventChan)
	err := <-errChan
	assert.NoError(t, err)

	backend.AssertExpectations(t)
}
