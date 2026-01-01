package csp

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
)

// Service defines the interface for Change Stream processing.
// Both CSPService and CSPClient implement this interface.
type Service interface {
	// Watch returns a channel of events for a collection.
	// Pass empty string for collection to watch all collections.
	// Pass empty string for tenant to watch all tenants.
	// resumeToken can be nil to start from now.
	Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error)
}
