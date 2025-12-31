package core

import (
	"context"
	"errors"
	"strings"

	"github.com/codetrek/syntrix/internal/csp"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Engine handles all business logic and coordinates with the storage backend.
type Engine struct {
	storage    storage.DocumentStore
	cspService csp.Service
}

// New creates a new Query Engine instance with a CSP service.
func New(storage storage.DocumentStore, cspService csp.Service) *Engine {
	return &Engine{
		storage:    storage,
		cspService: cspService,
	}
}

// GetDocument retrieves a document by path.
func (e *Engine) GetDocument(ctx context.Context, tenant string, path string) (model.Document, error) {
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
	parts := strings.Split(fullpath, "/")
	if len(parts)%2 != 0 {
		return ""
	}
	return parts[len(parts)-1]
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
	return e.storage.Delete(ctx, tenant, path, pred)
}

// ExecuteQuery executes a structured query.
func (e *Engine) ExecuteQuery(ctx context.Context, tenant string, q model.Query) ([]model.Document, error) {
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
// Delegates to the CSP service for watching change streams.
func (e *Engine) WatchCollection(ctx context.Context, tenant string, collection string) (<-chan storage.Event, error) {
	return e.cspService.Watch(ctx, tenant, collection, nil, storage.WatchOptions{})
}

// Pull handles replication pull requests.
func (e *Engine) Pull(ctx context.Context, tenant string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	q := model.Query{
		Collection: req.Collection,
		Filters: []model.Filter{
			{
				Field: "updatedAt",
				Op:    ">=",
				Value: req.Checkpoint,
			},
		},
		OrderBy: []model.Order{
			{
				Field:     "updatedAt",
				Direction: "asc",
			},
			{
				Field:     "id",
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
		doc.Collection = req.Collection

		if doc.Fullpath == "" {
			var id string
			if v, ok := doc.Data["id"].(string); ok {
				id = v
			}

			if id != "" {
				doc.Fullpath = doc.Collection + "/" + id
			}
		}

		existing, err := e.storage.Get(ctx, tenant, doc.Fullpath)
		if err != nil {
			if err == model.ErrNotFound {
				if err := e.storage.Create(ctx, tenant, doc); err != nil {
					conflicts = append(conflicts, doc)
				}
				continue
			}
			return nil, err
		}

		if change.BaseVersion != nil && existing.Version != *change.BaseVersion {
			conflicts = append(conflicts, existing)
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
					latest, _ := e.storage.Get(ctx, tenant, doc.Fullpath)
					if latest != nil {
						conflicts = append(conflicts, latest)
					}
				} else if err == model.ErrNotFound {
					latest, getErr := e.storage.Get(ctx, tenant, doc.Fullpath)
					if getErr == nil && latest != nil {
						conflicts = append(conflicts, latest)
					}
				} else {
					return nil, err
				}
			}
			continue
		}

		// Update
		if err := e.storage.Update(ctx, tenant, doc.Fullpath, doc.Data, filters); err != nil {
			if err == model.ErrPreconditionFailed {
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
