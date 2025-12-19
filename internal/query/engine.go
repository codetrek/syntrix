package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"syntrix/internal/storage"
)

// Engine handles all business logic and coordinates with the storage backend.
type Engine struct {
	storage storage.StorageBackend
	cspURL  string
	client  *http.Client
}

// NewEngine creates a new Query Engine instance.
func NewEngine(storage storage.StorageBackend, cspURL string) *Engine {
	return &Engine{
		storage: storage,
		cspURL:  cspURL,
		client:  &http.Client{},
	}
}

// GetDocument retrieves a document by path.
func (e *Engine) GetDocument(ctx context.Context, path string) (*storage.Document, error) {
	// Future: Add authorization check here
	return e.storage.Get(ctx, path)
}

// CreateDocument creates a new document.
func (e *Engine) CreateDocument(ctx context.Context, doc *storage.Document) error {
	// Future: Add validation and authorization check here
	return e.storage.Create(ctx, doc)
}

// UpdateDocument updates an existing document.
func (e *Engine) UpdateDocument(ctx context.Context, path string, data map[string]interface{}, version int64) error {
	// Future: Add validation and authorization check here
	return e.storage.Update(ctx, path, data, version)
}

// ReplaceDocument replaces a document or creates it if it doesn't exist (Upsert).
func (e *Engine) ReplaceDocument(ctx context.Context, path string, collection string, data map[string]interface{}) (*storage.Document, error) {
	// Try Get first
	existing, err := e.storage.Get(ctx, path)
	if err != nil {
		if err == storage.ErrNotFound {
			// Create
			doc := storage.NewDocument(path, collection, data)
			if err := e.storage.Create(ctx, doc); err != nil {
				return nil, err
			}
			return doc, nil
		}
		return nil, err
	}

	// Update (Replace data)
	if err := e.storage.Update(ctx, path, data, existing.Version); err != nil {
		return nil, err
	}

	// Return updated doc
	return e.storage.Get(ctx, path)
}

// PatchDocument updates specific fields of a document (Merge + CAS).
func (e *Engine) PatchDocument(ctx context.Context, path string, data map[string]interface{}) (*storage.Document, error) {
	for {
		existing, err := e.storage.Get(ctx, path)
		if err != nil {
			return nil, err
		}

		// Merge data
		mergedData := existing.Data
		if mergedData == nil {
			mergedData = make(map[string]interface{})
		}
		for k, v := range data {
			mergedData[k] = v
		}

		// Try Update with CAS
		err = e.storage.Update(ctx, path, mergedData, existing.Version)
		if err == nil {
			// Success, fetch latest to return
			return e.storage.Get(ctx, path)
		}

		if err != storage.ErrVersionConflict {
			return nil, err
		}
		// If VersionConflict, retry loop
	}
}

// DeleteDocument deletes a document.
func (e *Engine) DeleteDocument(ctx context.Context, path string) error {
	// Future: Add authorization check here
	return e.storage.Delete(ctx, path)
}

// ExecuteQuery executes a structured query.
func (e *Engine) ExecuteQuery(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	// Future: Add query validation and optimization here
	return e.storage.Query(ctx, q)
}

// WatchCollection returns a channel of events for a collection.
func (e *Engine) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	// Call CSP to watch
	reqBody, err := json.Marshal(map[string]string{
		"collection": collection,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/internal/v1/watch", e.cspURL), bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("csp watch failed with status: %d", resp.StatusCode)
	}

	out := make(chan storage.Event)

	go func() {
		defer close(out)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		for {
			var evt storage.Event
			if err := decoder.Decode(&evt); err != nil {
				log.Printf("[Error][Engine] Watch decode event failed: %v\n", err)
				return
			}
			select {
			case out <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// Pull handles replication pull requests.
func (e *Engine) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	q := storage.Query{
		Collection: req.Collection,
		Filters: []storage.Filter{
			{
				Field: "updated_at",
				Op:    ">",
				Value: req.Checkpoint,
			},
		},
		OrderBy: []storage.Order{
			{
				Field:     "updated_at",
				Direction: "asc",
			},
		},
		Limit: req.Limit,
	}

	docs, err := e.storage.Query(ctx, q)
	if err != nil {
		return nil, err
	}

	if docs == nil {
		docs = make([]*storage.Document, 0)
	}

	newCheckpoint := req.Checkpoint
	if len(docs) > 0 {
		newCheckpoint = docs[len(docs)-1].UpdatedAt
	}

	// If we have more documents than the limit, we should return the checkpoint of the last document
	// If we have fewer documents than the limit, it means we've reached the end, so we can return the latest checkpoint
	// However, RxDB expects the checkpoint to be the value of the last document in the batch.
	// If the batch is empty, we should return the requested checkpoint (or the latest known checkpoint if we knew it, but we don't).
	// The current implementation is correct for non-empty batches.
	// For empty batches, returning req.Checkpoint is also correct as it indicates no progress.

	return &storage.ReplicationPullResponse{
		Documents:  docs,
		Checkpoint: newCheckpoint,
	}, nil
}

// Push handles replication push requests.
func (e *Engine) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	var conflicts []*storage.Document

	for _, change := range req.Changes {
		doc := change.Doc
		// Ensure collection matches
		doc.Collection = req.Collection

		// Try to replace (Upsert)
		// If version is provided, it acts as CAS.
		// If not, we might want to force overwrite or check existence.
		// RxDB usually sends the document state it assumes.

		// Strategy:
		// 1. Try to Get existing doc.
		// 2. If not found, Create.
		// 3. If found, check version/conflict.

		existing, err := e.storage.Get(ctx, doc.Id)
		if err != nil {
			if err == storage.ErrNotFound {
				if err := e.storage.Create(ctx, doc); err != nil {
					// If create fails (race condition), treat as conflict
					conflicts = append(conflicts, doc)
				}
				continue
			}
			return nil, err
		}

		// Conflict detection
		// If incoming doc has a version, check if it matches existing.
		// Or simply overwrite if "last write wins" is desired.
		// For now, let's use strict version checking if version > 0.
		if doc.Version > 0 && existing.Version != doc.Version {
			conflicts = append(conflicts, existing) // Return server state
			continue
		}

		// Update
		if err := e.storage.Update(ctx, doc.Id, doc.Data, existing.Version); err != nil {
			if err == storage.ErrVersionConflict {
				// Fetch latest to return as conflict
				latest, _ := e.storage.Get(ctx, doc.Id)
				if latest != nil {
					conflicts = append(conflicts, latest)
				}
			} else {
				return nil, err
			}
		}
	}

	return &storage.ReplicationPushResponse{
		Conflicts: conflicts,
	}, nil
}
