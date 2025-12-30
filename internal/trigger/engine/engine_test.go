package engine

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestLoadTriggers(t *testing.T) {
	t.Parallel()
	e := &defaultTriggerEngine{}

	validTrigger := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}

	err := e.LoadTriggers([]*trigger.Trigger{validTrigger})
	assert.NoError(t, err)
	assert.Len(t, e.triggers, 1)

	invalidTrigger := &trigger.Trigger{}
	err = e.LoadTriggers([]*trigger.Trigger{invalidTrigger})
	assert.Error(t, err)
}

func TestStart(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	// Setup triggers
	trig := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}
	e.LoadTriggers([]*trigger.Trigger{trig})

	// Setup watcher channel
	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	// Setup event
	evt := storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "doc1",
			Collection: "users",
			Data:       map[string]interface{}{"foo": "bar"},
		},
		ResumeToken: "token1",
	}

	// Expect evaluation
	mockEvaluator.On("Evaluate", mock.Anything, trig, &evt).Return(true, nil)

	// Expect publish
	mockPublisher.On("Publish", mock.Anything, mock.MatchedBy(func(task *types.DeliveryTask) bool {
		return task.TriggerID == "t1" && task.DocumentID == "doc1"
	})).Return(nil)

	// Expect checkpoint save
	mockWatcher.On("SaveCheckpoint", mock.Anything, "token1").Return(nil)

	// Run Start in background
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	// Send event
	eventCh <- evt

	// Give some time for processing
	time.Sleep(100 * time.Millisecond)

	// Stop
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err)

	mockEvaluator.AssertExpectations(t)
	mockWatcher.AssertExpectations(t)
	mockPublisher.AssertExpectations(t)
}

func TestClose(t *testing.T) {
	t.Parallel()
	e := &defaultTriggerEngine{}
	assert.NoError(t, e.Close())
}

func TestStart_WatchError(t *testing.T) {
	t.Parallel()
	mockWatcher := new(MockWatcher)
	e := &defaultTriggerEngine{
		watcher: mockWatcher,
	}

	mockWatcher.On("Watch", mock.Anything).Return(nil, assert.AnError)

	err := e.Start(context.Background())
	assert.Error(t, err)
	assert.Equal(t, assert.AnError, err)
	mockWatcher.AssertExpectations(t)
}

func TestStart_EvaluateError(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	// Setup triggers
	trig := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}
	e.LoadTriggers([]*trigger.Trigger{trig})

	// Setup watcher channel
	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	// Setup event
	evt := storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "doc1",
			Collection: "users",
			Data:       map[string]interface{}{"foo": "bar"},
		},
	}

	// Expect evaluation error
	mockEvaluator.On("Evaluate", mock.Anything, trig, &evt).Return(false, assert.AnError)

	// Run Start in background
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	// Send event
	eventCh <- evt

	// Give some time for processing
	time.Sleep(100 * time.Millisecond)

	// Stop
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err)

	mockEvaluator.AssertExpectations(t)
	mockWatcher.AssertExpectations(t)
}

func TestStart_NilDocumentAndBefore(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
	}

	// Setup watcher channel
	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	// Setup event with nil Document and Before
	evt := storage.Event{
		Type:     storage.EventDelete,
		Document: nil,
		Before:   nil,
	}

	// Run Start in background
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	// Send event
	eventCh <- evt

	// Give some time for processing
	time.Sleep(100 * time.Millisecond)

	// Stop
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err)
	// Should have logged warning and skipped
	mockWatcher.AssertExpectations(t)
}

func TestStart_PublishError(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	trig := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}
	e.LoadTriggers([]*trigger.Trigger{trig})

	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	evt := storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "doc1",
			Collection: "users",
			Data:       map[string]interface{}{"foo": "bar"},
		},
	}

	mockEvaluator.On("Evaluate", mock.Anything, trig, &evt).Return(true, nil)
	mockPublisher.On("Publish", mock.Anything, mock.Anything).Return(assert.AnError)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	eventCh <- evt
	time.Sleep(100 * time.Millisecond)
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err) // Continues despite publish error
	mockPublisher.AssertExpectations(t)
}

func TestStart_SaveCheckpointError(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	trig := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"create"},
		URL:        "http://example.com",
	}
	e.LoadTriggers([]*trigger.Trigger{trig})

	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	evt := storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "doc1",
			Collection: "users",
			Data:       map[string]interface{}{"foo": "bar"},
		},
		ResumeToken: "token1",
	}

	mockEvaluator.On("Evaluate", mock.Anything, trig, &evt).Return(false, nil)
	mockWatcher.On("SaveCheckpoint", mock.Anything, "token1").Return(assert.AnError)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	eventCh <- evt
	time.Sleep(100 * time.Millisecond)
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err)
	mockWatcher.AssertExpectations(t)
}

func TestStart_BeforeOnlyEvent(t *testing.T) {
	t.Parallel()
	mockEvaluator := new(MockEvaluator)
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)

	e := &defaultTriggerEngine{
		evaluator: mockEvaluator,
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	trig := &trigger.Trigger{
		ID:         "t1",
		Tenant:     "tenant1",
		Collection: "users",
		Events:     []string{"delete"},
		URL:        "http://example.com",
	}
	e.LoadTriggers([]*trigger.Trigger{trig})

	eventCh := make(chan storage.Event)
	mockWatcher.On("Watch", mock.Anything).Return((<-chan storage.Event)(eventCh), nil)

	// Delete event with only Before (Document is nil)
	evt := storage.Event{
		Type:     storage.EventDelete,
		Document: nil,
		Before: &storage.Document{
			Id:         "doc1",
			Collection: "users",
			Data:       map[string]interface{}{"foo": "bar"},
		},
	}

	mockEvaluator.On("Evaluate", mock.Anything, trig, &evt).Return(true, nil)
	mockPublisher.On("Publish", mock.Anything, mock.MatchedBy(func(task *types.DeliveryTask) bool {
		return task.DocumentID == "doc1" && task.Collection == "users"
	})).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() {
		errCh <- e.Start(ctx)
	}()

	eventCh <- evt
	time.Sleep(100 * time.Millisecond)
	cancel()
	close(eventCh)

	err := <-errCh
	assert.NoError(t, err)
	mockPublisher.AssertExpectations(t)
}

func TestClose_WithWatcherError(t *testing.T) {
	t.Parallel()
	mockWatcher := new(MockWatcher)
	mockWatcher.On("Close").Return(assert.AnError)

	e := &defaultTriggerEngine{
		watcher: mockWatcher,
	}

	err := e.Close()
	assert.Error(t, err)
	mockWatcher.AssertExpectations(t)
}

func TestClose_WithPublisherError(t *testing.T) {
	t.Parallel()
	mockPublisher := new(MockPublisher)
	mockPublisher.On("Close").Return(assert.AnError)

	e := &defaultTriggerEngine{
		publisher: mockPublisher,
	}

	err := e.Close()
	assert.Error(t, err)
	mockPublisher.AssertExpectations(t)
}

func TestClose_WithBothErrors(t *testing.T) {
	t.Parallel()
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)
	mockWatcher.On("Close").Return(assert.AnError)
	mockPublisher.On("Close").Return(assert.AnError)

	e := &defaultTriggerEngine{
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	err := e.Close()
	assert.Error(t, err)
	mockWatcher.AssertExpectations(t)
	mockPublisher.AssertExpectations(t)
}

func TestClose_Success(t *testing.T) {
	t.Parallel()
	mockWatcher := new(MockWatcher)
	mockPublisher := new(MockPublisher)
	mockWatcher.On("Close").Return(nil)
	mockPublisher.On("Close").Return(nil)

	e := &defaultTriggerEngine{
		watcher:   mockWatcher,
		publisher: mockPublisher,
	}

	err := e.Close()
	assert.NoError(t, err)
	mockWatcher.AssertExpectations(t)
	mockPublisher.AssertExpectations(t)
}
