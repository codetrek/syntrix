package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Engine handles all business logic and coordinates with the storage backend.
type Engine struct {
	storage storage.DocumentStore
	cspURL  string
	client  *http.Client
}

// NewEngine creates a new Query Engine instance.
func NewEngine(storage storage.DocumentStore, cspURL string) *Engine {
	return &Engine{
		storage: storage,
		cspURL:  cspURL,
		client:  &http.Client{},
	}
}

// SetHTTPClient sets the HTTP client for the engine.
func (e *Engine) SetHTTPClient(client *http.Client) {
	e.client = client
}

// GetDocument retrieves a document by path.
func (e *Engine) GetDocument(ctx context.Context, tenant string, path string) (model.Document, error) {
	// Future: Add authorization check here
	stored, err := e.storage.Get(ctx, tenant, path)
	if err != nil {
		return nil, err
	}
	return flattenStorageDocument(stored), nil
}

// CreateDocument creates a new document.
func (e *Engine) CreateDocument(ctx context.Context, tenant string, doc model.Document) error {
	if doc == nil {
		return errors.New("document cannot be nil")
	}

	doc.GenerateIDIfEmpty()
	collection := doc.GetCollection()
	if collection == "" {
		return errors.New("collection is required")
	}
	fullpath := collection + "/" + doc.GetID()
	doc.StripProtectedFields()

	// Future: Add validation and authorization check here
	return e.storage.Create(ctx, tenant, storage.NewDocument(tenant, fullpath, collection, doc))
}

func flattenStorageDocument(doc *storage.Document) model.Document {
	if doc == nil {
		return nil
	}
	out := make(model.Document)
	for k, v := range doc.Data {
		out[k] = v
	}
	out.SetID(extractIDFromFullpath(doc.Fullpath))
	out.SetCollection(doc.Collection)
	out["version"] = doc.Version
	out["updatedAt"] = doc.UpdatedAt
	out["createdAt"] = doc.CreatedAt
	if doc.Deleted {
		out["deleted"] = true
	}
	return out
}

func extractIDFromFullpath(fullpath string) string {
	idx := strings.LastIndex(fullpath, "/")
	if idx == -1 {
		return fullpath
	}
	return fullpath[idx+1:]
}

// ReplaceDocument replaces a document or creates it if it doesn't exist (Upsert).
func (e *Engine) ReplaceDocument(ctx context.Context, tenant string, doc model.Document, pred model.Filters) (model.Document, error) {
	if doc == nil {
		return nil, errors.New("document cannot be nil")
	}

	collection := doc.GetCollection()
	if collection == "" {
		return nil, errors.New("collection is required")
	}

	id := doc.GetID()
	if id == "" {
		return nil, errors.New("document ID is required")
	}
	doc.StripProtectedFields()

	fullpath := collection + "/" + id

	// Try Get first
	_, err := e.storage.Get(ctx, tenant, fullpath)
	if err != nil {
		if err == model.ErrNotFound {
			// Create
			storedDoc := storage.NewDocument(tenant, fullpath, collection, doc)
			if err := e.storage.Create(ctx, tenant, storedDoc); err != nil {
				return nil, err
			}
			return flattenStorageDocument(storedDoc), nil
		}
		return nil, err
	}

	// Update (Replace data)
	if err := e.storage.Update(ctx, tenant, fullpath, map[string]interface{}(doc), pred); err != nil {
		return nil, err
	}

	// Return updated doc
	updatedDoc, err := e.storage.Get(ctx, tenant, fullpath)
	if err != nil {
		return nil, err
	}

	return flattenStorageDocument(updatedDoc), nil
}

// PatchDocument updates specific fields of a document (Merge + CAS).
func (e *Engine) PatchDocument(ctx context.Context, tenant string, doc model.Document, pred model.Filters) (model.Document, error) {
	if doc == nil {
		return nil, errors.New("document cannot be nil")
	}

	collection := doc.GetCollection()
	if collection == "" {
		return nil, errors.New("collection is required")
	}

	id := doc.GetID()
	if id == "" {
		return nil, errors.New("document ID is required")
	}

	fullpath := collection + "/" + id
	doc.StripProtectedFields()
	delete(doc, "id")

	if err := e.storage.Patch(ctx, tenant, fullpath, map[string]interface{}(doc), pred); err != nil {
		return nil, err
	}

	updatedDoc, err := e.storage.Get(ctx, tenant, fullpath)
	if err != nil {
		return nil, err
	}

	return flattenStorageDocument(updatedDoc), nil
}

// DeleteDocument deletes a document.
func (e *Engine) DeleteDocument(ctx context.Context, tenant string, path string, pred model.Filters) error {
	// Future: Add authorization check here
	return e.storage.Delete(ctx, tenant, path, pred)
}

// ExecuteQuery executes a structured query.
func (e *Engine) ExecuteQuery(ctx context.Context, tenant string, q model.Query) ([]model.Document, error) {
	// Future: Add query validation and optimization here
	storedDocs, err := e.storage.Query(ctx, tenant, q)
	if err != nil {
		return nil, err
	}

	flatDocs := make([]model.Document, len(storedDocs))
	for i, d := range storedDocs {
		flatDocs[i] = flattenStorageDocument(d)
	}

	return flatDocs, nil
}

// WatchCollection returns a channel of events for a collection.
func (e *Engine) WatchCollection(ctx context.Context, tenant string, collection string) (<-chan storage.Event, error) {
	// Call CSP to watch
	reqBody, err := json.Marshal(map[string]string{
		"collection": collection,
		"tenant":     tenant,
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
func (e *Engine) Pull(ctx context.Context, tenant string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	q := model.Query{
		Collection: req.Collection,
		Filters: []model.Filter{
			{
				Field: "updatedAt",
				Op:    ">",
				Value: req.Checkpoint,
			},
		},
		OrderBy: []model.Order{
			{
				Field:     "updatedAt",
				Direction: "asc",
			},
		},
		Limit:       req.Limit,
		ShowDeleted: true,
	}

	docs, err := e.storage.Query(ctx, tenant, q)
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
func (e *Engine) Push(ctx context.Context, tenant string, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
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

		existing, err := e.storage.Get(ctx, tenant, doc.Fullpath)
		if err != nil {
			if err == model.ErrNotFound {
				if err := e.storage.Create(ctx, tenant, doc); err != nil {
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
		if change.BaseVersion != nil && existing.Version != *change.BaseVersion {
			conflicts = append(conflicts, existing) // Return server state
			continue
		}

		filters := model.Filters{}
		if change.BaseVersion != nil {
			filters = append(filters, model.Filter{
				Field: "version",
				Op:    "==",
				Value: *change.BaseVersion,
			})
		}

		// Handle Delete
		if doc.Deleted {
			if err := e.storage.Delete(ctx, tenant, doc.Fullpath, filters); err != nil {
				if err == model.ErrPreconditionFailed {
					// Fetch latest to return as conflict
					latest, _ := e.storage.Get(ctx, tenant, doc.Fullpath)
					if latest != nil {
						conflicts = append(conflicts, latest)
					}
				} else if err == model.ErrNotFound {
					// Already deleted or not found, which is fine for delete
					// But if we had a base version, it might be a conflict?
					// If client wants to delete v1, but it's already deleted (v2), is it a conflict?
					// Usually yes, but for now let's ignore if not found.
					// Actually, if ErrNotFound is returned by Delete with filters, it means conflict or not found.
					// If we want to be strict, we should return conflict.
					// But if it's already deleted, maybe we don't care.
					// Let's assume if it's not found, it's fine.
				} else {
					return nil, err
				}
			}
			continue
		}

		// Update
		if err := e.storage.Update(ctx, tenant, doc.Fullpath, doc.Data, filters); err != nil {
			if err == model.ErrPreconditionFailed {
				// Fetch latest to return as conflict
				latest, _ := e.storage.Get(ctx, tenant, doc.Fullpath)
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
