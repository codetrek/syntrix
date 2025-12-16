package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"syntrix/internal/query"
	"syntrix/internal/storage"

	"github.com/google/uuid"
)

type Server struct {
	engine query.Service
	mux    *http.ServeMux
}

func NewServer(engine query.Service) *Server {
	s := &Server{
		engine: engine,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func flattenDocument(doc *storage.Document) Document {
	if doc == nil {
		return nil
	}
	flat := make(Document)

	// Copy data
	for k, v := range doc.Data {
		flat[k] = v
	}
	// Add system fields
	flat["_version"] = doc.Version
	flat["_updated_at"] = doc.UpdatedAt
	return flat
}

func (s *Server) routes() {
	// Document Operations
	s.mux.HandleFunc("GET /v1/{path...}", s.handleGetDocument)
	s.mux.HandleFunc("POST /v1/{path...}", s.handleCreateDocument)
	s.mux.HandleFunc("PUT /v1/{path...}", s.handleReplaceDocument)
	s.mux.HandleFunc("PATCH /v1/{path...}", s.handleUpdateDocument)
	s.mux.HandleFunc("DELETE /v1/{path...}", s.handleDeleteDocument)

	// Query Operations
	s.mux.HandleFunc("POST /v1/query", s.handleQuery)

	// Replication Operations
	s.mux.HandleFunc("GET /v1/replication/pull", s.handlePull)
	s.mux.HandleFunc("POST /v1/replication/push", s.handlePush)

	// Health Check
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	if err := validateDocumentPath(path); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	doc, err := s.engine.GetDocument(r.Context(), path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(flattenDocument(doc))
}

func (s *Server) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	collection := r.PathValue("path")

	if err := validateCollection(collection); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateData(data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Extract ID from data or generate it
	var docID string
	if idVal, ok := data["id"]; ok {
		if idStr, ok := idVal.(string); ok && idStr != "" {
			docID = idStr
		}
	}

	if docID == "" {
		docID = uuid.New().String()
		data["id"] = docID
	}

	path := collection + "/" + docID

	doc := storage.NewDocument(path, collection, stripSystemFields(data))

	if err := s.engine.CreateDocument(r.Context(), doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(flattenDocument(doc))
}

func (s *Server) handleReplaceDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	collection, docID, err := validateAndExplodeFullpath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if docID == "" {
		http.Error(w, "Invalid document path: missing document ID", http.StatusBadRequest)
		return
	}

	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	data["id"] = docID
	if err := validateData(data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	doc, err := s.engine.ReplaceDocument(r.Context(), path, collection, stripSystemFields(data))
	if err != nil {
		if errors.Is(err, storage.ErrVersionConflict) {
			http.Error(w, "Version conflict", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(flattenDocument(doc))
}

func (s *Server) handleUpdateDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	_, docID, err := validateAndExplodeFullpath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if docID == "" {
		http.Error(w, "Invalid document path: missing document ID", http.StatusBadRequest)
		return
	}

	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateData(data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	delete(data, "id") // ID comes from path
	data = stripSystemFields(data)
	if len(data) == 0 {
		http.Error(w, "No data to update", http.StatusBadRequest)
		return
	}

	data["id"] = docID
	doc, err := s.engine.PatchDocument(r.Context(), path, data)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, storage.ErrVersionConflict) {
			http.Error(w, "Version conflict", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(flattenDocument(doc))
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	if err := validateDocumentPath(path); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.engine.DeleteDocument(r.Context(), path); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q storage.Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateQuery(q); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	docs, err := s.engine.ExecuteQuery(r.Context(), q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	flatDocs := make([]map[string]interface{}, len(docs))
	for i, doc := range docs {
		flatDocs[i] = flattenDocument(doc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(flatDocs)
}
