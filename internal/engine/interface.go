package engine

import (
	"context"
	"net/http"

	"github.com/codetrek/syntrix/internal/csp"
	"github.com/codetrek/syntrix/internal/engine/internal/client"
	"github.com/codetrek/syntrix/internal/engine/internal/core"
	"github.com/codetrek/syntrix/internal/engine/internal/httphandler"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Service defines the interface for the Query Engine.
// Both the local Engine and the remote Client implement this interface.
type Service interface {
	GetDocument(ctx context.Context, tenant string, path string) (model.Document, error)
	CreateDocument(ctx context.Context, tenant string, doc model.Document) error
	ReplaceDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error)
	PatchDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error)
	DeleteDocument(ctx context.Context, tenant string, path string, pred model.Filters) error
	ExecuteQuery(ctx context.Context, tenant string, q model.Query) ([]model.Document, error)
	WatchCollection(ctx context.Context, tenant string, collection string) (<-chan storage.Event, error)
	Pull(ctx context.Context, tenant string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error)
	Push(ctx context.Context, tenant string, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error)
}

// NewService creates a new local Query Service with a remote CSP client.
// Use this for distributed mode where CSP runs as a separate service.
func NewService(store storage.DocumentStore, cspURL string) Service {
	return core.New(store, csp.NewRemoteService(cspURL))
}

// NewServiceWithCSP creates a new local Query Service with a custom CSP implementation.
// Use this for standalone mode with csp.NewEmbeddedService() or for testing with mocks.
func NewServiceWithCSP(store storage.DocumentStore, cspService csp.Service) Service {
	return core.New(store, cspService)
}

// NewClient creates a new remote Query Service client (HTTP client).
// Use this when the query service is running remotely.
func NewClient(baseURL string) Service {
	return client.New(baseURL)
}

// NewHTTPHandler creates an HTTP handler for the Query Service.
// The handler exposes the Service interface over HTTP.
func NewHTTPHandler(s Service) http.Handler {
	return httphandler.New(s)
}

// ============================================================================
// Deprecated type aliases and functions - for backward compatibility.
// These will be removed in a future release.
// ============================================================================

// Engine is a deprecated alias. Use NewService instead.
// Deprecated: Use NewService to get a Service interface.
type Engine = core.Engine

// Handler is a deprecated alias. Use NewHTTPHandler instead.
// Deprecated: Use NewHTTPHandler to get an http.Handler.
type Handler = httphandler.Handler

// Client is a deprecated alias. Use NewClient instead.
// Deprecated: Use NewClient to get a Service interface.
type Client = client.Client

// NewEngine is deprecated. Use NewService instead.
// Deprecated: Use NewService to get a Service interface.
func NewEngine(store storage.DocumentStore, cspURL string) *Engine {
	return core.New(store, csp.NewRemoteService(cspURL))
}

// NewHandler is deprecated. Use NewHTTPHandler instead.
// Deprecated: Use NewHTTPHandler to get an http.Handler.
func NewHandler(e *Engine) *Handler {
	return httphandler.NewWithEngine(e)
}
