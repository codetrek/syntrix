package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"
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
			if err == storage.ErrNotFound {
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

	err := h.engine.RunTransaction(r.Context(), func(ctx context.Context, tx query.Service) error {
		for _, op := range req.Writes {
			var err error
			switch op.Type {
			case "create":
				// Extract collection from path
				parts := strings.Split(op.Path, "/")
				if len(parts) < 2 {
					return storage.ErrNotFound // Or invalid path error
				}
				collection := strings.Join(parts[:len(parts)-1], "/")

				doc := common.Document(op.Data)
				if doc == nil {
					doc = make(common.Document)
				}
				doc.SetCollection(collection)
				doc.SetID(parts[len(parts)-1])
				err = tx.CreateDocument(ctx, doc)
			case "update":
				// Map "update" to PatchDocument
				parts := strings.Split(op.Path, "/")
				if len(parts) < 2 {
					return storage.ErrNotFound
				}
				collection := strings.Join(parts[:len(parts)-1], "/")
				patchDoc := common.Document(op.Data)
				if patchDoc == nil {
					patchDoc = make(common.Document)
				}
				patchDoc.SetCollection(collection)
				patchDoc.SetID(parts[len(parts)-1])
				_, err = tx.PatchDocument(ctx, patchDoc, storage.Filters{})
			case "replace":
				parts := strings.Split(op.Path, "/")
				if len(parts) < 2 {
					return storage.ErrNotFound
				}
				collection := strings.Join(parts[:len(parts)-1], "/")
				replaceDoc := common.Document(op.Data)
				if replaceDoc == nil {
					replaceDoc = make(common.Document)
				}
				replaceDoc.SetCollection(collection)
				replaceDoc.SetID(parts[len(parts)-1])
				_, err = tx.ReplaceDocument(ctx, replaceDoc, storage.Filters{})
			case "delete":
				err = tx.DeleteDocument(ctx, op.Path)
			default:
				return storage.ErrNotFound // Invalid type
			}

			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
