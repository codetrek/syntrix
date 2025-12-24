package query

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Service defines the interface for the Query Engine.
// Both the local Engine and the remote Client implement this interface.
type Service interface {
	GetDocument(ctx context.Context, path string) (model.Document, error)
	CreateDocument(ctx context.Context, doc model.Document) error
	ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error)
	PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error)
	DeleteDocument(ctx context.Context, path string, pred model.Filters) error
	ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error)
	WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error)
	Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error)
	Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error)
}
