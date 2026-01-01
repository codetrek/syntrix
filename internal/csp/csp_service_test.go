package csp

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

func (m *MockDocumentStore) Get(ctx context.Context, tenant string, path string) (*storage.Document, error) {
	args := m.Called(ctx, tenant, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockDocumentStore) Create(ctx context.Context, tenant string, doc *storage.Document) error {
	args := m.Called(ctx, tenant, doc)
	return args.Error(0)
}

func (m *MockDocumentStore) Update(ctx context.Context, tenant string, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, data, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Patch(ctx context.Context, tenant string, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, data, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Delete(ctx context.Context, tenant string, path string, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Query(ctx context.Context, tenant string, q model.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, tenant, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockDocumentStore) Watch(ctx context.Context, tenant string, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
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

func TestNewService(t *testing.T) {
	mockStore := new(MockDocumentStore)
	svc := NewService(mockStore)

	assert.NotNil(t, svc)
	assert.Equal(t, mockStore, svc.storage)
}

func TestNewService_Watch_Success(t *testing.T) {
	mockStore := new(MockDocumentStore)
	svc := NewService(mockStore)

	ctx := context.Background()
	tenant := "test-tenant"
	collection := "users"
	resumeToken := "token123"
	opts := storage.WatchOptions{IncludeBefore: true}

	expectedCh := make(chan storage.Event, 1)
	expectedCh <- storage.Event{Id: "users/1", Type: storage.EventCreate}
	close(expectedCh)

	mockStore.On("Watch", ctx, tenant, collection, resumeToken, opts).Return((<-chan storage.Event)(expectedCh), nil)

	ch, err := svc.Watch(ctx, tenant, collection, resumeToken, opts)

	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Read event from channel
	evt, ok := <-ch
	assert.True(t, ok)
	assert.Equal(t, "users/1", evt.Id)
	assert.Equal(t, storage.EventCreate, evt.Type)

	// Channel should be closed
	_, ok = <-ch
	assert.False(t, ok)

	mockStore.AssertExpectations(t)
}

func TestNewService_Watch_Error(t *testing.T) {
	mockStore := new(MockDocumentStore)
	svc := NewService(mockStore)

	ctx := context.Background()
	tenant := "test-tenant"
	collection := "users"

	mockStore.On("Watch", ctx, tenant, collection, nil, storage.WatchOptions{}).Return(nil, assert.AnError)

	ch, err := svc.Watch(ctx, tenant, collection, nil, storage.WatchOptions{})

	assert.Error(t, err)
	assert.Nil(t, ch)
	mockStore.AssertExpectations(t)
}

func TestNewService_Watch_EmptyTenantAndCollection(t *testing.T) {
	mockStore := new(MockDocumentStore)
	svc := NewService(mockStore)

	ctx := context.Background()
	// Empty tenant and collection should watch all
	tenant := ""
	collection := ""

	expectedCh := make(chan storage.Event)
	close(expectedCh)

	mockStore.On("Watch", ctx, tenant, collection, nil, storage.WatchOptions{}).Return((<-chan storage.Event)(expectedCh), nil)

	ch, err := svc.Watch(ctx, tenant, collection, nil, storage.WatchOptions{})

	assert.NoError(t, err)
	assert.NotNil(t, ch)
	mockStore.AssertExpectations(t)
}

func TestNewService_ImplementsService(t *testing.T) {
	mockStore := new(MockDocumentStore)
	var svc Service = NewService(mockStore)
	assert.NotNil(t, svc)
}
