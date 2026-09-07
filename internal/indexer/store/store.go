// Package store defines the shared Store interface and types for index storage backends.
// This package exists to break circular dependencies between manager and storage implementations.
package store

// Store defines the interface for index storage backends.
// Implementations: mem_store.Store (memory), persist_store.PebbleStore (PebbleDB)
type Store interface {
	// Index operations
	// progress is the event progress marker to save atomically with the operation.
	// Pass empty string if no progress update is needed.
	Upsert(db, pattern, tmplID, docID string, orderKey []byte, progress string) error
	Delete(db, pattern, tmplID, docID string, progress string) error
	Get(db, pattern, tmplID, docID string) (orderKey []byte, found bool)
	Search(db, pattern, tmplID string, opts SearchOptions) ([]DocRef, error)

	// Index management
	DeleteIndex(db, pattern, tmplID string) error
	DeleteDatabase(db string) error // Delete all indexes for a database
	SetState(db, pattern, tmplID string, state IndexState) error
	GetState(db, pattern, tmplID string) (IndexState, error)

	// Index enumeration (for reconciliation)
	ListDatabases() ([]string, error)
	ListIndexes(db string) ([]IndexInfo, error)

	// Checkpoint for event progress
	// LoadProgress loads the last saved progress marker.
	LoadProgress() (string, error)
	// SaveProgress joins the ordered write batch after all changes for an event.
	SaveProgress(progress string) error
	// Failed closes when an asynchronous write fails; memory stores return nil.
	Failed() <-chan struct{}
	Err() error

	// Lifecycle
	Flush() error
	Close() error
}

// IndexInfo provides metadata about an index.
type IndexInfo struct {
	Pattern    string
	TemplateID string
	RawPattern string
	State      IndexState
	DocCount   int
}

// SearchOptions configures a search operation.
type SearchOptions struct {
	Lower      []byte // Lower bound (inclusive)
	Upper      []byte // Upper bound (exclusive)
	StartAfter []byte // Cursor for pagination (exclusive)
	Limit      int    // Max results to return
}

// DocRef represents a document reference returned by Store.Search.
type DocRef struct {
	ID       string
	OrderKey []byte
}

// IndexState represents the current state of an index.
type IndexState string

const (
	IndexStateHealthy    IndexState = "healthy"
	IndexStateRebuilding IndexState = "rebuilding"
	IndexStateFailed     IndexState = "failed"
)
