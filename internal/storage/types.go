package storage

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a document is not found
	ErrNotFound = errors.New("document not found")
	// ErrVersionConflict is returned when a CAS operation fails due to version mismatch
	ErrVersionConflict = errors.New("version conflict")
)

// Document represents a stored document in the database
type Document struct {
	// Id is the unique identifier for the document (e.g., "chats/chatroom-1/members/alice")
	Id string `json:"id" bson:"_id"`

	// Collection is the parent collection name
	Collection string `json:"collection" bson:"collection"`

	// Data is the actual content of the document
	Data map[string]interface{} `json:"data" bson:"data"`

	// UpdatedAt is the timestamp of the last update (Unix nanoseconds)
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`

	// Version is the optimistic concurrency control version
	Version int64 `json:"version" bson:"version"`
}

// StorageBackend defines the interface for storage operations
type StorageBackend interface {
	// Get retrieves a document by its path
	Get(ctx context.Context, path string) (*Document, error)

	// Create inserts a new document. Fails if it already exists.
	Create(ctx context.Context, doc *Document) error

	// Update updates an existing document.
	// If version > 0, it performs a CAS (Compare-And-Swap) operation.
	Update(ctx context.Context, path string, data map[string]interface{}, version int64) error

	// Delete removes a document by its path
	Delete(ctx context.Context, path string) error

	// Query executes a complex query
	Query(ctx context.Context, q Query) ([]*Document, error)

	// Watch returns a channel of events for a given collection (or all if empty)
	Watch(ctx context.Context, collection string) (<-chan Event, error)

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
	Type      EventType `json:"type"`
	Document  *Document `json:"document,omitempty"` // Nil for delete
	Path      string    `json:"path"`
	Timestamp int64     `json:"timestamp"`
}

// Query represents a database query
type Query struct {
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters"`
	OrderBy    []Order  `json:"orderBy"`
	Limit      int      `json:"limit"`
	StartAfter string   `json:"startAfter"` // Cursor (usually the last document ID or sort key)
}

// Filter represents a query filter
type Filter struct {
	Field string      `json:"field"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}

// Order represents a sort order
type Order struct {
	Field     string `json:"field"`
	Direction string `json:"direction"` // "asc" or "desc"
}

// NewDocument creates a new document instance with initialized metadata
func NewDocument(id string, collection string, data map[string]interface{}) *Document {
	return &Document{
		Id:         id,
		Collection: collection,
		Data:       data,
		UpdatedAt:  time.Now().UnixNano(),
		Version:    1,
	}
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
	Doc *Document `json:"doc"`
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
