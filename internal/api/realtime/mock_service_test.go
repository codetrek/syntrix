package realtime

import (
	"context"

	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"
)

type MockQueryService struct{}

var _ query.Service = &MockQueryService{}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (common.Document, error) {
	return nil, nil
}
func (m *MockQueryService) CreateDocument(ctx context.Context, doc common.Document) error {
	return nil
}
func (m *MockQueryService) ReplaceDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}
func (m *MockQueryService) PatchDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}
func (m *MockQueryService) DeleteDocument(ctx context.Context, path string) error { return nil }
func (m *MockQueryService) ExecuteQuery(ctx context.Context, q storage.Query) ([]common.Document, error) {
	return nil, nil
}
func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return make(chan storage.Event), nil
}
func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}
func (m *MockQueryService) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	return fn(ctx, m)
}
