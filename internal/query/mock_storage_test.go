package query

import (
	"context"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/mock"
)

// MockStorageBackend is a mock implementation of storage.StorageBackend
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

func (m *MockStorageBackend) Update(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Patch(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *MockStorageBackend) Delete(ctx context.Context, path string) error {
	args := m.Called(ctx, path)
	return args.Error(0)
}

func (m *MockStorageBackend) Query(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
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

func (m *MockStorageBackend) Transaction(ctx context.Context, fn func(ctx context.Context, tx storage.StorageBackend) error) error {
	// For mocks, we just execute the function with the mock itself
	return fn(ctx, m)
}

func (m *MockStorageBackend) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
