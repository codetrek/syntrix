package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"syntrix/internal/storage"
)

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

	var data Document
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := data.ValidateDocument(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data.GenerateIDIfEmpty()

	path := collection + "/" + data.GetID()
	doc := storage.NewDocument(path, collection, stripSystemFields(data))

	if err := s.engine.CreateDocument(r.Context(), doc); err != nil {
		if errors.Is(err, storage.ErrExists) {
			http.Error(w, "Document already exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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

	var data Document
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	data.SetID(docID)
	if err := data.ValidateDocument(); err != nil {
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

	var data Document
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := data.ValidateDocument(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if data.IsEmpty() {
		http.Error(w, "No data to update", http.StatusBadRequest)
		return
	}
	data.SetID(docID)

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
