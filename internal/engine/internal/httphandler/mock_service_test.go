package httphandler

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/mock"
)

// MockService is a mock implementation of the Service interface for testing.
type MockService struct {
	mock.Mock
}

func (m *MockService) GetDocument(ctx context.Context, tenant string, path string) (model.Document, error) {
	args := m.Called(ctx, tenant, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockService) CreateDocument(ctx context.Context, tenant string, doc model.Document) error {
	args := m.Called(ctx, tenant, doc)
	return args.Error(0)
}

func (m *MockService) ReplaceDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error) {
	args := m.Called(ctx, tenant, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockService) PatchDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error) {
	args := m.Called(ctx, tenant, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockService) DeleteDocument(ctx context.Context, tenant string, path string, pred model.Filters) error {
	args := m.Called(ctx, tenant, path, pred)
	return args.Error(0)
}

func (m *MockService) ExecuteQuery(ctx context.Context, tenant string, q model.Query) ([]model.Document, error) {
	args := m.Called(ctx, tenant, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.Document), args.Error(1)
}

func (m *MockService) WatchCollection(ctx context.Context, tenant string, collection string) (<-chan storage.Event, error) {
	args := m.Called(ctx, tenant, collection)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockService) Pull(ctx context.Context, tenant string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	args := m.Called(ctx, tenant, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPullResponse), args.Error(1)
}

func (m *MockService) Push(ctx context.Context, tenant string, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	args := m.Called(ctx, tenant, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPushResponse), args.Error(1)
}
