package rest

import (
	"syntrix/internal/common"
	"syntrix/internal/storage"
)

type ReplicaChange struct {
	Action string `json:"action"` // "create", "update", "delete"

	// User facing document type, represents a JSON object.
	//
	//	"id" field is reserved for document ID.
	//	"version" field is reserved for document version.
	Doc common.Document `json:"document"`
}

type ReplicaPushRequest struct {
	Collection string          `json:"collection"`
	Changes    []ReplicaChange `json:"changes"`
}

type ReplicaPushResponse struct {
	Conflicts []common.Document `json:"conflicts"`
}

type ReplicaPullRequest struct {
	Collection string `json:"collection"`
	Checkpoint string `json:"checkpoint"`
	Limit      int    `json:"limit"`
}

type ReplicaPullResponse struct {
	Documents  []common.Document `json:"documents"`
	Checkpoint string            `json:"checkpoint"`
}

type UpdateDocumentRequest struct {
	Doc     common.Document `json:"doc"`
	IfMatch storage.Filters `json:"ifMatch,omitempty"`
}
