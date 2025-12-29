package rest

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/gorilla/schema"
)

func (h *Handler) handlePull(w http.ResponseWriter, r *http.Request) {
	var reqBody ReplicaPullRequest
	decoder := schema.NewDecoder()
	decoder.IgnoreUnknownKeys(true)
	if err := decoder.Decode(&reqBody, r.URL.Query()); err != nil {
		log.Println("[Warning][Pull] invalid query parameters:", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid query parameters")
		return
	}

	if reqBody.Collection == "" {
		log.Println("[Warning][Pull] missing collection")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Collection is required")
		return
	}

	checkpointInt, err := strconv.ParseInt(reqBody.Checkpoint, 10, 64)
	if err != nil {
		log.Println("[Warning][Pull] invalid checkpoint:", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid checkpoint")
		return
	}

	req := storage.ReplicationPullRequest{
		Collection: reqBody.Collection,
		Checkpoint: checkpointInt,
		Limit:      reqBody.Limit,
	}

	if err := validateReplicationPull(req); err != nil {
		log.Println("[Warning][Pull] validation error:", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid replication parameters")
		return
	}

	tenant, ok := h.tenantOrError(w, r)
	if !ok {
		return
	}

	log.Printf("[Info][Pull] collection: %s, checkpoint: %d, limit: %d", req.Collection, req.Checkpoint, req.Limit)

	resp, err := h.engine.Pull(r.Context(), tenant, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to pull changes")
		return
	}

	flatDocs := make([]model.Document, len(resp.Documents))
	for i, doc := range resp.Documents {
		flatDocs[i] = flattenDocument(doc)
	}

	log.Printf("[Info][Pull] completed collection: %s, returned: %d docs, new checkpoint: %d",
		req.Collection, len(flatDocs), resp.Checkpoint)

	writeJSON(w, http.StatusOK, ReplicaPullResponse{
		Documents:  flatDocs,
		Checkpoint: strconv.FormatInt(resp.Checkpoint, 10),
	})
}

func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request) {
	// Parse flattened push request
	var reqBody ReplicaPushRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		log.Println("[Warning][Push] invalid request body")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	collection := reqBody.Collection
	if collection == "" {
		log.Println("[Warning][Push] change missing collection")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Collection is required")
		return
	}

	if err := validateCollection(collection); err != nil {
		log.Println("[Warning][Push] invalid collection:", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid collection")
		return
	}

	tenant, ok := h.tenantOrError(w, r)
	if !ok {
		return
	}

	// Convert flattened changes to storage.ReplicationPushRequest
	var changes []storage.ReplicationPushChange
	for _, change := range reqBody.Changes {
		docData := change.Doc
		if err := docData.ValidateDocument(); err != nil {
			log.Println("[Warning][Push] change document validation failed:", err)
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid document in changes")
			return
		}

		docData.StripProtectedFields()

		if docData.GetID() == "" {
			log.Println("[Warning][Push] change document missing ID, skipping")
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Document ID is required in changes")
			return
		}

		// Extract metadata
		var version int64
		if docData.HasVersion() {
			version = docData.GetVersion()
		}

		// Extract ID
		var docID = docData.GetID()
		if docID == "" {
			continue // Skip invalid
		}

		id := collection + "/" + docID
		doc := storage.NewDocument(tenant, id, collection, docData)
		doc.Version = version

		if change.Action == "delete" {
			doc.Deleted = true
		}

		changes = append(changes, storage.ReplicationPushChange{
			Doc: doc,
		})
	}

	if len(changes) == 0 {
		log.Println("[Warning][Push] no valid changes in request")
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "No valid changes to push")
		return
	}

	pushReq := storage.ReplicationPushRequest{
		Collection: collection,
		Changes:    changes,
	}

	if err := validateReplicationPush(pushReq); err != nil {
		log.Println("[Warning][Push] validation error:", err)
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid replication parameters")
		return
	}

	log.Printf("[Info][Push] collection: %s, changes: %d", collection, len(changes))
	resp, err := h.engine.Push(r.Context(), tenant, pushReq)
	if err != nil {
		log.Println("[Error][Push] error during push:", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to push changes")
		return
	}

	// Flatten conflicts
	flatConflicts := make([]model.Document, len(resp.Conflicts))
	for i, doc := range resp.Conflicts {
		flatConflicts[i] = flattenDocument(doc)
	}

	log.Printf("[Info][Push] completed collection: %s, conflicts: %d", collection, len(flatConflicts))

	writeJSON(w, http.StatusOK, ReplicaPushResponse{
		Conflicts: flatConflicts,
	})
}
