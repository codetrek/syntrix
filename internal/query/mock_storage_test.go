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

func (m *MockStorageBackend) Update(ctx context.Context, path string, data map[string]interface{}, version int64) error {
	args := m.Called(ctx, path, data, version)
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

func (m *MockStorageBackend) Watch(ctx context.Context, collection string, resumeToken interface{}) (<-chan storage.Event, error) {
	args := m.Called(ctx, collection, resumeToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockStorageBackend) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
