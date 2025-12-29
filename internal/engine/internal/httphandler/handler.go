package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Service defines the interface required by the HTTP handler.
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

// Handler is the HTTP handler for the Query Service.
type Handler struct {
	service Service
	mux     *http.ServeMux
}

func tenantOrDefault(t string) string {
	if t == "" {
		return model.DefaultTenantID
	}
	return t
}

// New creates a new HTTP handler for the Query Service.
func New(service Service) http.Handler {
	h := &Handler{
		service: service,
		mux:     http.NewServeMux(),
	}
	h.routes()
	return h
}

// NewWithEngine creates a new HTTP handler with a concrete engine (deprecated).
// Deprecated: Use New with a Service interface instead.
func NewWithEngine(service Service) *Handler {
	h := &Handler{
		service: service,
		mux:     http.NewServeMux(),
	}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("POST /internal/v1/document/get", h.handleGetDocument)
	h.mux.HandleFunc("POST /internal/v1/document/create", h.handleCreateDocument)
	h.mux.HandleFunc("POST /internal/v1/document/replace", h.handleReplaceDocument)
	h.mux.HandleFunc("POST /internal/v1/document/patch", h.handlePatchDocument)
	h.mux.HandleFunc("POST /internal/v1/document/delete", h.handleDeleteDocument)
	h.mux.HandleFunc("POST /internal/v1/query/execute", h.handleExecuteQuery)
	h.mux.HandleFunc("POST /internal/v1/watch", h.handleWatchCollection)
	h.mux.HandleFunc("POST /internal/replication/v1/pull", h.handlePull)
	h.mux.HandleFunc("POST /internal/replication/v1/push", h.handlePush)
	h.mux.HandleFunc("GET /health", h.handleHealth)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (h *Handler) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Tenant string `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	doc, err := h.service.GetDocument(r.Context(), tenant, req.Path)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data   model.Document `json:"data"`
		Tenant string         `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	if err := h.service.CreateDocument(r.Context(), tenant, req.Data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) handleReplaceDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data   model.Document `json:"data"`
		Pred   model.Filters  `json:"pred"`
		Tenant string         `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	doc, err := h.service.ReplaceDocument(r.Context(), tenant, req.Data, req.Pred)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handlePatchDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data   model.Document `json:"data"`
		Pred   model.Filters  `json:"pred"`
		Tenant string         `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	doc, err := h.service.PatchDocument(r.Context(), tenant, req.Data, req.Pred)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string        `json:"path"`
		Pred   model.Filters `json:"pred"`
		Tenant string        `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	if err := h.service.DeleteDocument(r.Context(), tenant, req.Path, req.Pred); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, model.ErrPreconditionFailed) {
			http.Error(w, "Version conflict", http.StatusPreconditionFailed)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleExecuteQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query  model.Query `json:"query"`
		Tenant string      `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	docs, err := h.service.ExecuteQuery(r.Context(), tenant, req.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}

func (h *Handler) handleWatchCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
		Tenant     string `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()
	tenant := tenantOrDefault(req.Tenant)
	stream, err := h.service.WatchCollection(r.Context(), tenant, req.Collection)
	if err != nil {
		return
	}
	encoder := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-stream:
			if !ok {
				return
			}
			if err := encoder.Encode(evt); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handler) handlePull(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Request storage.ReplicationPullRequest `json:"request"`
		Tenant  string                         `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	resp, err := h.service.Pull(r.Context(), tenant, req.Request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Request storage.ReplicationPushRequest `json:"request"`
		Tenant  string                         `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	tenant := tenantOrDefault(req.Tenant)
	resp, err := h.service.Push(r.Context(), tenant, req.Request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
