package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/helper"
	"github.com/syntrixbase/syntrix/pkg/model"

	"github.com/gorilla/schema"
)

var validateReplicationPushFn = validateReplicationPush

func (h *Handler) handlePull(w http.ResponseWriter, r *http.Request) {
	var reqBody ReplicaPullRequest
	decoder := schema.NewDecoder()
	decoder.IgnoreUnknownKeys(true)
	if err := decoder.Decode(&reqBody, r.URL.Query()); err != nil {
		slog.Warn("Pull: invalid query parameters", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid query parameters")
		return
	}

	if reqBody.Collection == "" {
		slog.Warn("Pull: missing collection")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Collection is required")
		return
	}

	checkpointInt, err := strconv.ParseInt(reqBody.Checkpoint, 10, 64)
	if err != nil {
		slog.Warn("Pull: invalid checkpoint", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid checkpoint")
		return
	}

	req := storage.ReplicationPullRequest{
		Collection: reqBody.Collection,
		Checkpoint: checkpointInt,
		Limit:      reqBody.Limit,
	}

	if err := validateReplicationPull(req); err != nil {
		slog.Warn("Pull: validation error", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid replication parameters")
		return
	}

	database, ok := h.databaseOrError(w, r)
	if !ok {
		return
	}

	slog.Info("Pull: started", "collection", req.Collection, "checkpoint", req.Checkpoint, "limit", req.Limit)

	resp, err := h.engine.Pull(r.Context(), database, req)
	if err != nil {
		writeInternalError(w, err, "Failed to pull changes")
		return
	}

	flatDocs := make([]model.Document, len(resp.Documents))
	for i, doc := range resp.Documents {
		flatDocs[i] = flattenDocument(doc)
	}

	slog.Info("Pull: completed", "collection", req.Collection, "returned_docs", len(flatDocs), "new_checkpoint", resp.Checkpoint)

	writeJSON(w, http.StatusOK, ReplicaPullResponse{
		Documents:  flatDocs,
		Checkpoint: strconv.FormatInt(resp.Checkpoint, 10),
	})
}

func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request) {
	// Parse flattened push request
	var reqBody ReplicaPushRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		slog.Warn("Push: invalid request body", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	collection := reqBody.Collection
	if collection == "" {
		slog.Warn("Push: change missing collection")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Collection is required")
		return
	}

	if err := helper.CheckCollectionPath(collection); err != nil {
		slog.Warn("Push: invalid collection", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid collection")
		return
	}

	database, ok := h.databaseOrError(w, r)
	if !ok {
		return
	}

	// Convert flattened changes to storage.ReplicationPushRequest
	var changes []storage.ReplicationPushChange
	for _, change := range reqBody.Changes {
		docData := change.Doc
		if err := docData.ValidateDocument(); err != nil {
			slog.Warn("Push: change document validation failed", "error", err)
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid document in changes")
			return
		}

		docData.StripProtectedFields()

		if docData.GetID() == "" {
			slog.Warn("Push: change document missing ID, skipping")
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Document ID is required in changes")
			return
		}

		// Extract ID
		var docID = docData.GetID()
		if docID == "" {
			continue // Skip invalid
		}

		doc := storage.NewStoredDoc(database, collection, docID, docData)

		if change.Action == "delete" {
			doc.Deleted = true
		}

		changes = append(changes, storage.ReplicationPushChange{
			Doc:         &doc,
			BaseVersion: change.BaseVersion,
		})
	}

	if len(changes) == 0 {
		slog.Warn("Push: no valid changes in request")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "No valid changes to push")
		return
	}

	pushReq := storage.ReplicationPushRequest{
		Collection: collection,
		Changes:    changes,
	}

	if err := validateReplicationPushFn(pushReq); err != nil {
		slog.Warn("Push: validation error", "error", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid replication parameters")
		return
	}

	slog.Info("Push: started", "collection", collection, "changes", len(changes))
	resp, err := h.engine.Push(r.Context(), database, pushReq)
	if err != nil {
		writeInternalError(w, err, "Failed to push changes")
		return
	}

	// Flatten conflicts
	flatConflicts := make([]model.Document, len(resp.Conflicts))
	for i, doc := range resp.Conflicts {
		flatConflicts[i] = flattenDocument(doc)
	}

	slog.Info("Push: completed", "collection", collection, "conflicts", len(flatConflicts))

	writeJSON(w, http.StatusOK, ReplicaPushResponse{
		Conflicts: flatConflicts,
	})
}
