package api

import (
	"context"
	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/mock"
)

// MockQueryService is a mock implementation of query.Service
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (*storage.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) CreateDocument(ctx context.Context, doc *storage.Document) error {
	args := m.Called(ctx, doc)
	return args.Error(0)
}

func (m *MockQueryService) ReplaceDocument(ctx context.Context, path string, collection string, data common.Document, pred storage.Filters) (*storage.Document, error) {
	args := m.Called(ctx, path, collection, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) PatchDocument(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) (*storage.Document, error) {
	args := m.Called(ctx, path, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) DeleteDocument(ctx context.Context, path string) error {
	args := m.Called(ctx, path)
	return args.Error(0)
}

func (m *MockQueryService) ExecuteQuery(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	args := m.Called(ctx, collection)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPullResponse), args.Error(1)
}

func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPushResponse), args.Error(1)
}

func (m *MockQueryService) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	// For mocks, we can just execute the function immediately with the mock itself (m)
	// or a new mock if we want to verify transaction isolation.
	// For simplicity, let's just run it with 'm'.
	return fn(ctx, m)
}
