package core

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/mock"
)

// MockStorageBackend is a mock implementation of storage.StorageBackend
type MockStorageBackend struct {
	mock.Mock
}

func (m *MockStorageBackend) Get(ctx context.Context, tenant, path string) (*storage.Document, error) {
	args := m.Called(ctx, tenant, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockStorageBackend) Create(ctx context.Context, tenant string, doc *storage.Document) error {
	args := m.Called(ctx, tenant, doc)
	return args.Error(0)
}

func (m *MockStorageBackend) Update(ctx context.Context, tenant, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Patch(ctx context.Context, tenant, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Delete(ctx context.Context, tenant, path string, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Query(ctx context.Context, tenant string, q model.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, tenant, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockStorageBackend) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	args := m.Called(ctx, tenant, collection, resumeToken, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockStorageBackend) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
