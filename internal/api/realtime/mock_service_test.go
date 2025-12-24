package realtime

import (
	"context"

	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

type MockQueryService struct{}

var _ query.Service = &MockQueryService{}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (model.Document, error) {
	return nil, nil
}
func (m *MockQueryService) CreateDocument(ctx context.Context, doc model.Document) error {
	return nil
}
func (m *MockQueryService) ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *MockQueryService) PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *MockQueryService) DeleteDocument(ctx context.Context, path string, pred model.Filters) error {
	return nil
}
func (m *MockQueryService) ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error) {
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
