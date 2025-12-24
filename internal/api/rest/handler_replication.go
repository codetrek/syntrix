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
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	if reqBody.Collection == "" {
		log.Println("[Warning][Pull] missing collection")
		http.Error(w, "collection is required", http.StatusBadRequest)
		return
	}

	checkpointInt, err := strconv.ParseInt(reqBody.Checkpoint, 10, 64)
	if err != nil {
		log.Println("[Warning][Pull] invalid checkpoint:", err)
		http.Error(w, "Invalid checkpoint", http.StatusBadRequest)
		return
	}

	req := storage.ReplicationPullRequest{
		Collection: reqBody.Collection,
		Checkpoint: checkpointInt,
		Limit:      reqBody.Limit,
	}

	if err := validateReplicationPull(req); err != nil {
		log.Println("[Warning][Pull] validation error:", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[Info][Pull] collection: %s, checkpoint: %d, limit: %d", req.Collection, req.Checkpoint, req.Limit)

	resp, err := h.engine.Pull(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	flatDocs := make([]model.Document, len(resp.Documents))
	for i, doc := range resp.Documents {
		flatDocs[i] = flattenDocument(doc)
	}

	log.Printf("[Info][Pull] completed collection: %s, returned: %d docs, new checkpoint: %d",
		req.Collection, len(flatDocs), resp.Checkpoint)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ReplicaPullResponse{
		Documents:  flatDocs,
		Checkpoint: strconv.FormatInt(resp.Checkpoint, 10),
	})
}

func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request) {
	// Parse flattened push request
	var reqBody ReplicaPushRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		log.Println("[Warning][Push] invalid request body")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	collection := reqBody.Collection
	if collection == "" {
		log.Println("[Warning][Push] change missing collection")
		http.Error(w, "Collection is required", http.StatusBadRequest)
		return
	}

	if err := validateCollection(collection); err != nil {
		log.Println("[Warning][Push] invalid collection:", err)
		http.Error(w, "Invalid collection", http.StatusBadRequest)
		return
	}

	// Convert flattened changes to storage.ReplicationPushRequest
	var changes []storage.ReplicationPushChange
	for _, change := range reqBody.Changes {
		docData := change.Doc
		if err := docData.ValidateDocument(); err != nil {
			log.Println("[Warning][Push] change document validation failed:", err)
			http.Error(w, "Invalid document in changes: "+err.Error(), http.StatusBadRequest)
			return
		}

		docData.StripProtectedFields()

		if docData.GetID() == "" {
			log.Println("[Warning][Push] change document missing ID, skipping")
			http.Error(w, "Document ID is required in changes", http.StatusBadRequest)
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
		doc := storage.NewDocument(id, collection, docData)
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
		http.Error(w, "No valid changes to push", http.StatusBadRequest)
		return
	}

	pushReq := storage.ReplicationPushRequest{
		Collection: collection,
		Changes:    changes,
	}

	if err := validateReplicationPush(pushReq); err != nil {
		log.Println("[Warning][Push] validation error:", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[Info][Push] collection: %s, changes: %d", collection, len(changes))
	resp, err := h.engine.Push(r.Context(), pushReq)
	if err != nil {
		log.Println("[Error][Push] error during push:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Flatten conflicts
	flatConflicts := make([]model.Document, len(resp.Conflicts))
	for i, doc := range resp.Conflicts {
		flatConflicts[i] = flattenDocument(doc)
	}

	log.Printf("[Info][Push] completed collection: %s, conflicts: %d", collection, len(flatConflicts))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ReplicaPushResponse{
		Conflicts: flatConflicts,
	})
}
