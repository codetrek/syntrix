package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/core/storage/types"
	"github.com/syntrixbase/syntrix/internal/helper"
	"github.com/syntrixbase/syntrix/internal/indexer"
	"github.com/syntrixbase/syntrix/pkg/model"
)

var (
	ErrIndexerRequired = errors.New("indexer is required")
)

// Engine handles all business logic and coordinates with the storage backend.
type Engine struct {
	storage storage.DocumentStore
	indexer indexer.Service
}

// New creates a new Query Engine instance.
func New(storage storage.DocumentStore, indexer indexer.Service) *Engine {
	return &Engine{
		storage: storage,
		indexer: indexer,
	}
}

// GetDocument retrieves a document by path.
func (e *Engine) GetDocument(ctx context.Context, database string, path string) (model.Document, error) {
	stored, err := e.storage.Get(ctx, database, path)
	if err != nil {
		return nil, err
	}
	return helper.FlattenStorageDocument(stored), nil
}

// CreateDocument creates a new document.
func (e *Engine) CreateDocument(ctx context.Context, database string, doc model.Document) error {
	if doc == nil {
		return errors.New("document cannot be nil")
	}

	doc.GenerateIDIfEmpty()
	collection := doc.GetCollection()
	if collection == "" {
		return errors.New("collection is required")
	}
	doc.StripProtectedFields()

	return e.storage.Create(ctx, database, types.NewStoredDoc(database, collection, doc.GetID(), doc))
}

// ReplaceDocument replaces a document or creates it if it doesn't exist (Upsert).
func (e *Engine) ReplaceDocument(ctx context.Context, database string, doc model.Document, pred model.Filters) (model.Document, error) {
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
	_, err := e.storage.Get(ctx, database, fullpath)
	if err != nil {
		if err == model.ErrNotFound {
			// Create
			storedDoc := storage.NewStoredDoc(database, collection, id, doc)
			if err := e.storage.Create(ctx, database, storedDoc); err != nil {
				return nil, err
			}
			return helper.FlattenStorageDocument(&storedDoc), nil
		}
		return nil, err
	}

	// Update (Replace data)
	if err := e.storage.Update(ctx, database, fullpath, map[string]interface{}(doc), pred); err != nil {
		return nil, err
	}

	// Return updated doc
	updatedDoc, err := e.storage.Get(ctx, database, fullpath)
	if err != nil {
		return nil, err
	}

	return helper.FlattenStorageDocument(updatedDoc), nil
}

// PatchDocument updates specific fields of a document (Merge + CAS).
func (e *Engine) PatchDocument(ctx context.Context, database string, doc model.Document, pred model.Filters) (model.Document, error) {
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

	if err := e.storage.Patch(ctx, database, fullpath, map[string]interface{}(doc), pred); err != nil {
		return nil, err
	}

	updatedDoc, err := e.storage.Get(ctx, database, fullpath)
	if err != nil {
		return nil, err
	}

	return helper.FlattenStorageDocument(updatedDoc), nil
}

// DeleteDocument deletes a document.
func (e *Engine) DeleteDocument(ctx context.Context, database string, path string, pred model.Filters) error {
	return e.storage.Delete(ctx, database, path, pred)
}

// ExecuteQuery executes a structured query.
// Falls back to storage scan when no filters/orderBy, otherwise uses indexer.
func (e *Engine) ExecuteQuery(ctx context.Context, database string, q model.Query) ([]model.Document, error) {
	// If no filters and no orderBy, go directly to storage
	// This handles simple "list all" queries and ShowDeleted properly
	if len(q.Filters) == 0 && len(q.OrderBy) == 0 {
		return e.executeWithStorage(ctx, database, q)
	}

	// If filters only contain id == or id in, go directly to storage
	// These can be resolved by direct document lookups without indexer
	if e.isIDOnlyQuery(q) {
		return e.executeWithStorage(ctx, database, q)
	}

	// Indexer is required for filtered/ordered queries
	if e.indexer == nil {
		return nil, ErrIndexerRequired
	}

	docs, err := e.executeWithIndexer(ctx, database, q)
	if err != nil {
		return nil, err
	}

	return docs, nil
}

// isIDOnlyQuery returns true if the query only filters by id (== or in).
func (e *Engine) isIDOnlyQuery(q model.Query) bool {
	// Must have no orderBy to be an ID-only query
	if len(q.OrderBy) > 0 {
		return false
	}

	// Must have at least one filter
	if len(q.Filters) == 0 {
		return false
	}

	// All filters must be on "id" field with == or in operator
	for _, f := range q.Filters {
		if f.Field != "id" {
			return false
		}
		if f.Op != "==" && f.Op != "in" {
			return false
		}
	}

	return true
}

// executeWithStorage uses storage directly for simple queries without filters/orderBy.
func (e *Engine) executeWithStorage(ctx context.Context, database string, q model.Query) ([]model.Document, error) {
	storedDocs, err := e.storage.Query(ctx, database, q)
	if err != nil {
		return nil, err
	}

	// Convert to model.Document
	flatDocs := make([]model.Document, 0, len(storedDocs))
	for _, d := range storedDocs {
		if d != nil {
			flatDocs = append(flatDocs, helper.FlattenStorageDocument(d))
		}
	}

	return flatDocs, nil
}

// executeWithIndexer uses the indexer to find document IDs and fetches them.
func (e *Engine) executeWithIndexer(ctx context.Context, database string, q model.Query) ([]model.Document, error) {
	plan, err := e.queryToPlan(q)
	if err != nil {
		return nil, err
	}

	refs, err := e.indexer.Search(ctx, database, plan)
	if err != nil {
		return nil, err
	}

	if len(refs) == 0 {
		return []model.Document{}, nil
	}

	// Build full paths from IDs
	paths := make([]string, len(refs))
	for i, ref := range refs {
		paths[i] = q.Collection + "/" + ref.ID
	}

	// Fetch documents by path
	storedDocs, err := e.storage.GetMany(ctx, database, paths)
	if err != nil {
		return nil, err
	}

	// Filter out nils and convert to model.Document
	flatDocs := make([]model.Document, 0, len(storedDocs))
	for _, d := range storedDocs {
		if d != nil {
			flatDocs = append(flatDocs, helper.FlattenStorageDocument(d))
		}
	}

	return flatDocs, nil
}

// queryToPlan converts a model.Query to an indexer.Plan.
func (e *Engine) queryToPlan(q model.Query) (indexer.Plan, error) {
	plan := indexer.Plan{
		Collection:  q.Collection,
		Limit:       q.Limit,
		StartAfter:  q.StartAfter,
		ShowDeleted: q.ShowDeleted,
	}

	// Convert filters
	for _, f := range q.Filters {
		var op indexer.FilterOp
		switch f.Op {
		case "==":
			op = indexer.FilterEq
		case ">":
			op = indexer.FilterGt
		case "<":
			op = indexer.FilterLt
		case ">=":
			op = indexer.FilterGte
		case "<=":
			op = indexer.FilterLte
		default:
			return indexer.Plan{}, fmt.Errorf("%w: operator %q is not supported by indexed queries", model.ErrInvalidQuery, f.Op)
		}
		plan.Filters = append(plan.Filters, indexer.Filter{
			Field: f.Field,
			Op:    op,
			Value: f.Value,
		})
	}

	// Convert order by
	for _, o := range q.OrderBy {
		dir := indexer.Asc
		if o.Direction == "desc" {
			dir = indexer.Desc
		}
		plan.OrderBy = append(plan.OrderBy, indexer.OrderField{
			Field:     o.Field,
			Direction: dir,
		})
	}

	return plan, nil
}

// Pull handles replication pull requests.
func (e *Engine) Pull(ctx context.Context, database string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
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

	docs, err := e.storage.Query(ctx, database, q)
	if err != nil {
		return nil, err
	}

	if docs == nil {
		docs = make([]*storage.StoredDoc, 0)
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
func (e *Engine) Push(ctx context.Context, database string, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	var conflicts []*storage.StoredDoc

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

		existing, err := e.storage.Get(ctx, database, doc.Fullpath, storage.ReadOptions{Consistency: storage.ReadAuthoritative})
		if err != nil {
			if err == model.ErrNotFound {
				if err := e.storage.Create(ctx, database, *doc); err != nil {
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
			if err := e.storage.Delete(ctx, database, doc.Fullpath, filters); err != nil {
				if err == model.ErrPreconditionFailed {
					latest, getErr := e.storage.Get(ctx, database, doc.Fullpath, storage.ReadOptions{Consistency: storage.ReadAuthoritative})
					if getErr != nil && !errors.Is(getErr, model.ErrNotFound) {
						return nil, getErr
					}
					if latest != nil {
						conflicts = append(conflicts, latest)
					}
				} else if err == model.ErrNotFound {
					latest, getErr := e.storage.Get(ctx, database, doc.Fullpath, storage.ReadOptions{Consistency: storage.ReadAuthoritative})
					if getErr != nil && !errors.Is(getErr, model.ErrNotFound) {
						return nil, getErr
					}
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
		if err := e.storage.Update(ctx, database, doc.Fullpath, doc.Data, filters); err != nil {
			if err == model.ErrPreconditionFailed {
				latest, getErr := e.storage.Get(ctx, database, doc.Fullpath, storage.ReadOptions{Consistency: storage.ReadAuthoritative})
				if getErr != nil && !errors.Is(getErr, model.ErrNotFound) {
					return nil, getErr
				}
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
