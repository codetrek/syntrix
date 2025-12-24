package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/codetrek/syntrix/pkg/model"
)

type TriggerGetRequest struct {
	Paths []string `json:"paths"`
}

type TriggerGetResponse struct {
	Documents []map[string]interface{} `json:"documents"`
}

type TriggerWriteOp struct {
	Type string                 `json:"type"` // create, update, delete
	Path string                 `json:"path"`
	Data map[string]interface{} `json:"data,omitempty"`
}

type TriggerWriteRequest struct {
	Writes []TriggerWriteOp `json:"writes"`
}

func (h *Handler) handleTriggerGet(w http.ResponseWriter, r *http.Request) {
	var req TriggerGetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Paths) == 0 {
		http.Error(w, "paths cannot be empty", http.StatusBadRequest)
		return
	}

	docs := make([]map[string]interface{}, 0, len(req.Paths))
	for _, path := range req.Paths {
		doc, err := h.engine.GetDocument(r.Context(), path)
		if err != nil {
			if err == model.ErrNotFound {
				continue // Skip not found documents? Or return null? Docs say "documents" list, implying found ones.
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		docs = append(docs, doc)
	}

	resp := TriggerGetResponse{Documents: docs}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleTriggerWrite(w http.ResponseWriter, r *http.Request) {
	var req TriggerWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Writes) == 0 {
		http.Error(w, "writes cannot be empty", http.StatusBadRequest)
		return
	}

	for _, op := range req.Writes {
		collection, id, err := splitPath(op.Path)
		if err != nil {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}

		switch op.Type {
		case "create":
			doc := model.Document(op.Data)
			if doc == nil {
				doc = make(model.Document)
			}
			doc.SetCollection(collection)
			doc.SetID(id)
			err = h.engine.CreateDocument(r.Context(), doc)
		case "update":
			patchDoc := model.Document(op.Data)
			if patchDoc == nil {
				patchDoc = make(model.Document)
			}
			patchDoc.SetCollection(collection)
			patchDoc.SetID(id)
			_, err = h.engine.PatchDocument(r.Context(), patchDoc, model.Filters{})
		case "replace":
			replaceDoc := model.Document(op.Data)
			if replaceDoc == nil {
				replaceDoc = make(model.Document)
			}
			replaceDoc.SetCollection(collection)
			replaceDoc.SetID(id)
			_, err = h.engine.ReplaceDocument(r.Context(), replaceDoc, model.Filters{})
		case "delete":
			err = h.engine.DeleteDocument(r.Context(), op.Path, model.Filters{})
		default:
			http.Error(w, "invalid write type", http.StatusBadRequest)
			return
		}

		if err != nil {
			http.Error(w, err.Error(), mapStorageError(err))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func splitPath(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", errors.New("invalid path")
	}
	collection := strings.Join(parts[:len(parts)-1], "/")
	id := parts[len(parts)-1]
	if collection == "" || id == "" {
		return "", "", errors.New("invalid path")
	}
	return collection, id, nil
}

func mapStorageError(err error) int {
	switch {
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, model.ErrExists):
		return http.StatusConflict
	case errors.Is(err, model.ErrPreconditionFailed):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
