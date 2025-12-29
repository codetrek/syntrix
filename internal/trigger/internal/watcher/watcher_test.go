package watcher

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockDocumentStore is a mock implementation of storage.DocumentStore
type MockDocumentStore struct {
	mock.Mock
}

func (m *MockDocumentStore) Create(ctx context.Context, tenant string, doc *storage.Document) error {
	args := m.Called(ctx, tenant, doc)
	return args.Error(0)
}

func (m *MockDocumentStore) Get(ctx context.Context, tenant, id string) (*storage.Document, error) {
	args := m.Called(ctx, tenant, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockDocumentStore) Update(ctx context.Context, tenant, id string, data map[string]interface{}, filters model.Filters) error {
	args := m.Called(ctx, tenant, id, data, filters)
	return args.Error(0)
}

func (m *MockDocumentStore) Patch(ctx context.Context, tenant, id string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, tenant, id, data, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Delete(ctx context.Context, tenant, id string, pred model.Filters) error {
	args := m.Called(ctx, tenant, id, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Query(ctx context.Context, tenant string, q model.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, tenant, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockDocumentStore) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	args := m.Called(ctx, tenant, collection, resumeToken, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockDocumentStore) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestNewWatcher(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})
	assert.NotNil(t, w)
}

func TestWatch_WithCheckpoint(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	checkpointDoc := &storage.Document{
		Data: map[string]interface{}{"token": "resume-token"},
	}
	mockStore.On("Get", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1").Return(checkpointDoc, nil)

	ch := make(chan storage.Event)
	mockStore.On("Watch", mock.Anything, "tenant1", "", "resume-token", mock.Anything).Return((<-chan storage.Event)(ch), nil)

	eventCh, err := w.Watch(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, (<-chan storage.Event)(ch), eventCh)
	mockStore.AssertExpectations(t)
}

func TestWatch_NoCheckpoint_StartFromNow(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{StartFromNow: true})

	mockStore.On("Get", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1").Return(nil, model.ErrNotFound)

	ch := make(chan storage.Event)
	mockStore.On("Watch", mock.Anything, "tenant1", "", nil, mock.Anything).Return((<-chan storage.Event)(ch), nil)

	eventCh, err := w.Watch(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, (<-chan storage.Event)(ch), eventCh)
	mockStore.AssertExpectations(t)
}

func TestWatch_NoCheckpoint_Fail(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{StartFromNow: false})

	mockStore.On("Get", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1").Return(nil, model.ErrNotFound)

	_, err := w.Watch(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint not found")
	mockStore.AssertExpectations(t)
}

func TestSaveCheckpoint_Update(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	mockStore.On("Update", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1", mock.Anything, mock.Anything).Return(nil)

	err := w.SaveCheckpoint(context.Background(), "new-token")
	assert.NoError(t, err)
	mockStore.AssertExpectations(t)
}

func TestSaveCheckpoint_Create(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	mockStore.On("Update", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1", mock.Anything, mock.Anything).Return(model.ErrNotFound)
	mockStore.On("Create", mock.Anything, "default", mock.Anything).Return(nil)

	err := w.SaveCheckpoint(context.Background(), "new-token")
	assert.NoError(t, err)
	mockStore.AssertExpectations(t)
}

func TestClose(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	// Close should be a no-op and return nil
	err := w.Close()
	assert.NoError(t, err)
}

func TestWatch_GetError(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	// Get returns unexpected error
	mockStore.On("Get", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1").Return(nil, assert.AnError)

	_, err := w.Watch(context.Background())
	assert.Error(t, err)
	mockStore.AssertExpectations(t)
}

func TestSaveCheckpoint_UpdateError(t *testing.T) {
	mockStore := new(MockDocumentStore)
	w := NewWatcher(mockStore, "tenant1", WatcherOptions{})

	mockStore.On("Update", mock.Anything, "default", "sys/checkpoints/trigger_evaluator/tenant1", mock.Anything, mock.Anything).Return(assert.AnError)

	err := w.SaveCheckpoint(context.Background(), "new-token")
	assert.Error(t, err)
	mockStore.AssertExpectations(t)
}
