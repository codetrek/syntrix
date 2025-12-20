package query

import (
	"context"
	"syntrix/internal/common"
	"syntrix/internal/storage"
)

// Service defines the interface for the Query Engine.
// Both the local Engine and the remote Client implement this interface.
type Service interface {
	GetDocument(ctx context.Context, path string) (*storage.Document, error)
	CreateDocument(ctx context.Context, doc *storage.Document) error
	ReplaceDocument(ctx context.Context, path string, collection string, data common.Document, pred storage.Filters) (*storage.Document, error)
	PatchDocument(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) (*storage.Document, error)
	DeleteDocument(ctx context.Context, path string) error
	ExecuteQuery(ctx context.Context, q storage.Query) ([]*storage.Document, error)
	WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error)
	Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error)
	Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error)
	RunTransaction(ctx context.Context, fn func(ctx context.Context, tx Service) error) error
}
