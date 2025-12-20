package api

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

func (s *Server) handleTriggerGet(w http.ResponseWriter, r *http.Request) {
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
		doc, err := s.engine.GetDocument(r.Context(), path)
		if err != nil {
			if err == storage.ErrNotFound {
				continue // Skip not found documents? Or return null? Docs say "documents" list, implying found ones.
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		docs = append(docs, flattenDocument(doc))
	}

	resp := TriggerGetResponse{Documents: docs}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleTriggerWrite(w http.ResponseWriter, r *http.Request) {
	var req TriggerWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err := s.engine.RunTransaction(r.Context(), func(ctx context.Context, tx query.Service) error {
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

				doc := storage.NewDocument(op.Path, collection, op.Data)
				err = tx.CreateDocument(ctx, doc)
			case "update":
				// Map "update" to PatchDocument
				_, err = tx.PatchDocument(ctx, op.Path, op.Data, storage.Filters{})
			case "replace":
				parts := strings.Split(op.Path, "/")
				if len(parts) < 2 {
					return storage.ErrNotFound
				}
				collection := strings.Join(parts[:len(parts)-1], "/")
				_, err = tx.ReplaceDocument(ctx, op.Path, collection, common.Document(op.Data), storage.Filters{})
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
