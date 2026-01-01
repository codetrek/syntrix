package csp

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
)

// CSPService provides direct access to storage for change stream watching.
// Use this in standalone mode where no inter-service HTTP is needed.
type CSPService struct {
	storage storage.DocumentStore
}

// NewService creates a new CSPService with the given storage backend.
func NewService(store storage.DocumentStore) *CSPService {
	return &CSPService{storage: store}
}

// Watch returns a channel of events for a collection by calling storage directly.
// This bypasses HTTP and is used in standalone mode.
func (s *CSPService) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	return s.storage.Watch(ctx, tenant, collection, resumeToken, opts)
}

// Ensure CSPService implements Service interface at compile time.
var _ Service = (*CSPService)(nil)
