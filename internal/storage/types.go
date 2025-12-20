package storage

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/zeebo/blake3"
)

var (
	// ErrNotFound is returned when a document is not found
	ErrNotFound = errors.New("document not found")
	// ErrExists is returned when trying to create a document that already exists
	ErrExists = errors.New("document already exists")
	// ErrVersionConflict is returned when a CAS operation fails due to version mismatch
	ErrVersionConflict = errors.New("version conflict")
)

// Document represents a stored document in the database
type Document struct {
	// Id is the unique identifier for the document, 128-bit BLAKE3 of fullpath, binary
	Id string `json:"id" bson:"_id"`

	// Fullpath is the Full Pathname of document
	Fullpath string `json:"fullpath" bson:"fullpath"`

	// Collection is the parent collection name
	Collection string `json:"collection" bson:"collection"`

	// Parent is the parent of collection
	Parent string `json:"parent" bson:"parent"`

	// Data is the actual content of the document
	Data map[string]interface{} `json:"data" bson:"data"`

	// UpdatedAt is the timestamp of the last update (Unix millionseconds)
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`

	// CreatedAt is the timestamp of the creation (Unix millionseconds)
	CreatedAt int64 `json:"created_at" bson:"created_at"`

	// Version is the optimistic concurrency control version
	Version int64 `json:"version" bson:"version"`
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
	Update(ctx context.Context, path string, data map[string]interface{}, pred Filters) error

	// Patch updates specific fields of an existing document.
	// If pred is provided, it performs a CAS (Compare-And-Swap) operation.
	Patch(ctx context.Context, path string, data map[string]interface{}, pred Filters) error

	// Delete removes a document by its path
	Delete(ctx context.Context, path string) error

	// Query executes a complex query
	Query(ctx context.Context, q Query) ([]*Document, error)

	// Watch returns a channel of events for a given collection (or all if empty).
	// resumeToken can be nil to start from now.
	Watch(ctx context.Context, collection string, resumeToken interface{}, opts WatchOptions) (<-chan Event, error)

	// Close closes the connection to the backend
	Close(ctx context.Context) error
}

// EventType represents the type of change
type EventType string

type Filters []Filter

const (
	EventCreate EventType = "create"
	EventUpdate EventType = "update"
	EventDelete EventType = "delete"
)

// Event represents a database change event
type Event struct {
	Type        EventType   `json:"type"`
	Document    *Document   `json:"document,omitempty"` // Nil for delete
	Before      *Document   `json:"before,omitempty"`   // Previous state, if available
	Path        string      `json:"path"`
	Timestamp   int64       `json:"timestamp"`
	ResumeToken interface{} `json:"-"` // Opaque token for resuming watch
}

// Query represents a database query
type Query struct {
	Collection string  `json:"collection"`
	Filters    Filters `json:"filters"`
	OrderBy    []Order `json:"orderBy"`
	Limit      int     `json:"limit"`
	StartAfter string  `json:"startAfter"` // Cursor (usually the last document ID or sort key)
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

// CalculateID calculates the document ID (hash) from the full path
func CalculateID(fullpath string) string {
	hash := blake3.Sum256([]byte(fullpath))
	return hex.EncodeToString(hash[:16])
}

// NewDocument creates a new document instance with initialized metadata
func NewDocument(fullpath string, collection string, data map[string]interface{}) *Document {
	// Calculate Parent from collection path
	parent := ""
	if idx := strings.LastIndex(collection, "/"); idx != -1 {
		parent = collection[:idx]
	}

	id := CalculateID(fullpath)

	now := time.Now().UnixMilli()

	return &Document{
		Id:         id,
		Fullpath:   fullpath,
		Collection: collection,
		Parent:     parent,
		Data:       data,
		UpdatedAt:  now,
		CreatedAt:  now,
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
