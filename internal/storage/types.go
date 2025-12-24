package storage

import (
	"context"

	"github.com/codetrek/syntrix/pkg/model"
)

// Document represents a stored document in the database
type Document struct {
	// Id is the unique identifier for the document, 128-bit BLAKE3 of fullpath, binary
	Id string `json:"id" bson:"_id"`

	// Fullpath is the Full Pathname of document
	Fullpath string `json:"-" bson:"fullpath"`

	// Collection is the parent collection name
	Collection string `json:"collection" bson:"collection"`

	// Parent is the parent of collection
	Parent string `json:"-" bson:"parent"`

	// UpdatedAt is the timestamp of the last update (Unix millionseconds)
	UpdatedAt int64 `json:"updatedAt" bson:"updated_at"`

	// CreatedAt is the timestamp of the creation (Unix millionseconds)
	CreatedAt int64 `json:"createdAt" bson:"created_at"`

	// Version is the optimistic concurrency control version
	Version int64 `json:"version" bson:"version"`

	// Data is the actual content of the document
	Data map[string]interface{} `json:"data" bson:"data"`

	// Deleted indicates if the document is soft-deleted
	Deleted bool `json:"deleted,omitempty" bson:"deleted,omitempty"`
}

// WatchOptions defines options for watching changes
type WatchOptions struct {
	IncludeBefore bool
}

// StorageBackend defines the interface for storage operations
type StorageBackend interface {
	// Get retrieves a document by its path
	Get(ctx context.Context, path string) (*Document, error)

	// Create inserts a new document. Fails if it already exists.
	Create(ctx context.Context, doc *Document) error

	// Update updates an existing document.
	// If pred is provided, it performs a CAS (Compare-And-Swap) operation.
	Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error

	// Patch updates specific fields of an existing document.
	// If pred is provided, it performs a CAS (Compare-And-Swap) operation.
	Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error

	// Delete removes a document by its path
	Delete(ctx context.Context, path string, pred model.Filters) error

	// Query executes a complex query
	Query(ctx context.Context, q model.Query) ([]*Document, error)

	// Watch returns a channel of events for a given collection (or all if empty).
	// resumeToken can be nil to start from now.
	Watch(ctx context.Context, collection string, resumeToken interface{}, opts WatchOptions) (<-chan Event, error)

	// Close closes the connection to the backend
	Close(ctx context.Context) error
}

// EventType represents the type of change
type EventType string

const (
	EventCreate EventType = "create"
	EventUpdate EventType = "update"
	EventDelete EventType = "delete"
)

// Event represents a database change event
type Event struct {
	Id          string      `json:"id"`
	Type        EventType   `json:"type"`
	Document    *Document   `json:"document,omitempty"` // Nil for delete
	Before      *Document   `json:"before,omitempty"`   // Previous state, if available
	Timestamp   int64       `json:"timestamp"`
	ResumeToken interface{} `json:"-"` // Opaque token for resuming watch
}

// ReplicationPullRequest represents a request to pull changes
type ReplicationPullRequest struct {
	Collection string `json:"collection"`
	Checkpoint int64  `json:"checkpoint"`
	Limit      int    `json:"limit"`
}

// ReplicationPullResponse represents the response for a pull request
type ReplicationPullResponse struct {
	Documents  []*Document `json:"documents"`
	Checkpoint int64       `json:"checkpoint"`
}

// ReplicationPushChange represents a single change in a push request
type ReplicationPushChange struct {
	Doc         *Document `json:"doc"`
	BaseVersion *int64    `json:"base_version"` // Version known to the client
}

// ReplicationPushRequest represents a request to push changes
type ReplicationPushRequest struct {
	Collection string                  `json:"collection"`
	Changes    []ReplicationPushChange `json:"changes"`
}

// ReplicationPushResponse represents the response for a push request
type ReplicationPushResponse struct {
	Conflicts []*Document `json:"conflicts"`
}
