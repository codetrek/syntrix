package csp

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
)

// EmbeddedService provides direct access to storage for change stream watching.
// Use this in standalone mode where no inter-service HTTP is needed.
type EmbeddedService struct {
	storage storage.DocumentStore
}

// NewEmbeddedService creates a new EmbeddedService with the given storage backend.
func NewEmbeddedService(store storage.DocumentStore) *EmbeddedService {
	return &EmbeddedService{storage: store}
}

// Watch returns a channel of events for a collection by calling storage directly.
// This bypasses HTTP and is used in standalone mode.
func (s *EmbeddedService) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	return s.storage.Watch(ctx, tenant, collection, resumeToken, opts)
}

// Ensure EmbeddedService implements Service interface at compile time.
var _ Service = (*EmbeddedService)(nil)
