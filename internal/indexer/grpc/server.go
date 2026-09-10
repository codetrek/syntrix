// Package grpc provides the gRPC server adapter for the Indexer service.
package grpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	indexerv1 "github.com/syntrixbase/syntrix/api/gen/indexer/v1"
	"github.com/syntrixbase/syntrix/internal/indexer/encoding"
	"github.com/syntrixbase/syntrix/internal/indexer/manager"
	"github.com/syntrixbase/syntrix/internal/indexer/store"
	"github.com/syntrixbase/syntrix/internal/indexer/template"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LocalService is the interface that the gRPC server delegates to.
type LocalService interface {
	Search(ctx context.Context, database string, plan manager.Plan) ([]manager.DocRef, error)
	Health(ctx context.Context) (manager.Health, error)
	Stats(ctx context.Context) (manager.Stats, error)
	Manager() *manager.Manager
}

// Server implements the gRPC IndexerServiceServer interface.
type Server struct {
	indexerv1.UnimplementedIndexerServiceServer
	svc LocalService
}

// NewServer creates a new gRPC server adapter.
func NewServer(svc LocalService) *Server {
	return &Server{svc: svc}
}

// Search executes an index query and returns ordered document references.
func (s *Server) Search(ctx context.Context, req *indexerv1.SearchRequest) (*indexerv1.SearchResponse, error) {
	// Convert request to Plan
	plan, err := s.requestToPlan(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	// Execute search
	docs, err := s.svc.Search(ctx, req.Database, plan)
	if err != nil {
		return nil, s.convertError(err)
	}

	// Convert results
	protoRefs := make([]*indexerv1.DocRef, len(docs))
	for i, doc := range docs {
		protoRefs[i] = &indexerv1.DocRef{
			Id:       doc.ID,
			OrderKey: base64.StdEncoding.EncodeToString(doc.OrderKey),
		}
	}

	return &indexerv1.SearchResponse{
		Docs: protoRefs,
	}, nil
}

// Health returns the current health status of the indexer.
func (s *Server) Health(ctx context.Context, req *indexerv1.HealthRequest) (*indexerv1.HealthResponse, error) {
	health, err := s.svc.Health(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "health check failed: %v", err)
	}

	indexes := make(map[string]*indexerv1.IndexHealth)
	for k, v := range health.Indexes {
		indexes[k] = &indexerv1.IndexHealth{
			State:    v.State,
			DocCount: v.DocCount,
		}
	}

	return &indexerv1.HealthResponse{
		Status:  string(health.Status),
		Indexes: indexes,
	}, nil
}

// Stats returns the service's aggregate statistics without scanning index data.
func (s *Server) Stats(ctx context.Context, req *indexerv1.StatsRequest) (*indexerv1.StatsResponse, error) {
	stats, err := s.svc.Stats(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		if st, ok := status.FromError(err); ok {
			return nil, st.Err()
		}
		return nil, status.Errorf(codes.Internal, "stats collection failed: %v", err)
	}

	return &indexerv1.StatsResponse{
		TemplateCount: int64(stats.TemplateCount),
		EventsApplied: stats.EventsApplied,
		LastEventTime: stats.LastEventTime,
	}, nil
}

// GetState returns the complete index state including desired, actual, and pending operations.
func (s *Server) GetState(ctx context.Context, req *indexerv1.GetStateRequest) (*indexerv1.IndexerState, error) {
	mgr := s.svc.Manager()
	if mgr == nil {
		return nil, status.Error(codes.Internal, "manager not available")
	}

	// Build desired state from templates
	desired := s.buildDesiredState(mgr, req.Database, req.Pattern)

	// Build actual state from indexes
	actual := s.buildActualState(mgr, req.Database, req.Pattern)

	// TODO: Get pending operations from reconciler when implemented
	pendingOps := []*indexerv1.PendingOperation{}

	return &indexerv1.IndexerState{
		Desired:    desired,
		Actual:     actual,
		PendingOps: pendingOps,
	}, nil
}

// Reload reloads index templates from the configuration file.
func (s *Server) Reload(ctx context.Context, req *indexerv1.ReloadRequest) (*indexerv1.ReloadResponse, error) {
	mgr := s.svc.Manager()
	if mgr == nil {
		return nil, status.Error(codes.Internal, "manager not available")
	}

	// TODO: Get template path from configuration
	// For now, return current template count without reloading
	templates := mgr.Templates()

	return &indexerv1.ReloadResponse{
		TemplatesLoaded: int32(len(templates)),
		Errors:          nil,
	}, nil
}

// InvalidateIndex marks index(es) for rebuild.
func (s *Server) InvalidateIndex(ctx context.Context, req *indexerv1.InvalidateIndexRequest) (*indexerv1.InvalidateIndexResponse, error) {
	mgr := s.svc.Manager()
	if mgr == nil {
		return nil, status.Error(codes.Internal, "manager not available")
	}

	st := mgr.Store()
	count := 0

	indexes, err := st.ListIndexes(req.Database)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list indexes: %v", err)
	}

	for _, idx := range indexes {
		// Match by pattern
		if req.Pattern != "" && idx.Pattern != req.Pattern {
			continue
		}
		// Match by template ID if specified
		if req.TemplateId != "" && idx.TemplateID != req.TemplateId {
			continue
		}

		// Mark as failed to trigger rebuild
		st.SetState(req.Database, idx.Pattern, idx.TemplateID, store.IndexStateFailed)
		count++
	}

	return &indexerv1.InvalidateIndexResponse{
		IndexesInvalidated: int32(count),
	}, nil
}

// requestToPlan converts a gRPC SearchRequest to a manager.Plan.
func (s *Server) requestToPlan(req *indexerv1.SearchRequest) (manager.Plan, error) {
	plan := manager.Plan{
		Collection: req.Collection,
		Limit:      int(req.Limit),
		StartAfter: req.StartAfter,
	}

	// Convert filters
	for _, f := range req.Filters {
		op, err := parseFilterOp(f.Op)
		if err != nil {
			return plan, err
		}

		// Decode JSON value
		var value any
		if len(f.Value) > 0 {
			if err := json.Unmarshal(f.Value, &value); err != nil {
				return plan, err
			}
		}

		plan.Filters = append(plan.Filters, manager.Filter{
			Field: f.Field,
			Op:    op,
			Value: value,
		})
	}

	// Convert order by
	for _, ob := range req.OrderBy {
		dir := encoding.Asc
		if ob.Direction == "desc" {
			dir = encoding.Desc
		}
		plan.OrderBy = append(plan.OrderBy, manager.OrderField{
			Field:     ob.Field,
			Direction: dir,
		})
	}

	return plan, nil
}

// parseFilterOp parses a filter operator string to FilterOp.
func parseFilterOp(op string) (manager.FilterOp, error) {
	switch op {
	case "eq":
		return manager.FilterEq, nil
	case "gt":
		return manager.FilterGt, nil
	case "lt":
		return manager.FilterLt, nil
	case "gte":
		return manager.FilterGte, nil
	case "lte":
		return manager.FilterLte, nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "invalid filter operator: %s", op)
	}
}

// convertError converts internal errors to gRPC status errors.
func (s *Server) convertError(err error) error {
	switch err {
	case manager.ErrNoMatchingIndex:
		return status.Error(codes.NotFound, "no matching index for query")
	case manager.ErrIndexNotReady:
		return status.Error(codes.Unavailable, "index not ready")
	case manager.ErrIndexRebuilding:
		return status.Error(codes.Unavailable, "index is rebuilding")
	case manager.ErrInvalidPlan:
		return status.Error(codes.InvalidArgument, "invalid query plan")
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// buildDesiredState builds the desired index specs from templates.
func (s *Server) buildDesiredState(mgr *manager.Manager, filterDB, filterPattern string) []*indexerv1.IndexSpec {
	templates := mgr.Templates()
	specs := make([]*indexerv1.IndexSpec, 0, len(templates))

	for _, tmpl := range templates {
		// Apply pattern filter
		if filterPattern != "" && tmpl.NormalizedPattern() != filterPattern {
			continue
		}

		fields := make([]*indexerv1.IndexField, len(tmpl.Fields))
		for i, f := range tmpl.Fields {
			dir := "asc"
			if f.Order == template.Desc {
				dir = "desc"
			}
			fields[i] = &indexerv1.IndexField{
				Field:     f.Field,
				Direction: dir,
			}
		}

		specs = append(specs, &indexerv1.IndexSpec{
			Pattern:    tmpl.NormalizedPattern(),
			TemplateId: tmpl.Identity(),
			Fields:     fields,
		})
	}

	return specs
}

// buildActualState builds the actual index info from in-memory indexes.
func (s *Server) buildActualState(mgr *manager.Manager, filterDB, filterPattern string) []*indexerv1.IndexInfo {
	var infos []*indexerv1.IndexInfo
	st := mgr.Store()

	databases, err := st.ListDatabases()
	if err != nil {
		return nil
	}

	for _, dbName := range databases {
		// Apply database filter
		if filterDB != "" && dbName != filterDB {
			continue
		}

		indexes, err := st.ListIndexes(dbName)
		if err != nil {
			continue // Skip database on error
		}

		for _, idx := range indexes {
			// Apply pattern filter
			if filterPattern != "" && idx.Pattern != filterPattern {
				continue
			}

			infos = append(infos, &indexerv1.IndexInfo{
				Database:   dbName,
				Pattern:    idx.Pattern,
				TemplateId: idx.TemplateID,
				State:      string(idx.State),
				DocCount:   int64(idx.DocCount),
			})
		}
	}

	return infos
}
