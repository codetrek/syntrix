package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"syntrix/internal/common"
	"syntrix/internal/storage"
)

func (h *Handler) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	if err := validateDocumentPath(path); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	doc, err := h.engine.GetDocument(r.Context(), path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
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
	collection := r.PathValue("path")

	if err := validateCollection(collection); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var data common.Document
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := data.ValidateDocument(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data.GenerateIDIfEmpty()
	data.SetCollection(collection)

	path := collection + "/" + data.GetID()

	if err := h.engine.CreateDocument(r.Context(), data); err != nil {
		if errors.Is(err, storage.ErrExists) {
			http.Error(w, "Document already exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	doc, err := h.engine.GetDocument(r.Context(), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handleReplaceDocument(w http.ResponseWriter, r *http.Request) {
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

	var data UpdateDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := data.Doc.ValidateDocument(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if id := data.Doc.GetID(); id != "" && id != docID {
		http.Error(w, "Document ID cannot be changed", http.StatusBadRequest)
		return
	}

	data.Doc.SetID(docID)
	data.Doc.SetCollection(collection)

	doc, err := h.engine.ReplaceDocument(r.Context(), data.Doc, data.IfMatch)
	if err != nil {
		if errors.Is(err, storage.ErrPreconditionFailed) {
			http.Error(w, "Version conflict", http.StatusPreconditionFailed)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handlePatchDocument(w http.ResponseWriter, r *http.Request) {
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

	var data UpdateDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := data.Doc.ValidateDocument(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if id := data.Doc.GetID(); id != "" && id != docID {
		http.Error(w, "Document ID cannot be changed", http.StatusBadRequest)
		return
	}

	if data.Doc.IsEmpty() {
		http.Error(w, "No data to update", http.StatusBadRequest)
		return
	}
	data.Doc.SetID(docID)
	data.Doc.SetCollection(collection)
	doc, err := h.engine.PatchDocument(r.Context(), data.Doc, data.IfMatch)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, storage.ErrPreconditionFailed) {
			http.Error(w, "Version conflict", http.StatusPreconditionFailed)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (h *Handler) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	if err := validateDocumentPath(path); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.engine.DeleteDocument(r.Context(), path); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
