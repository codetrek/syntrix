package rest

import (
	"github.com/codetrek/syntrix/pkg/model"
)

type ReplicaChange struct {
	Action string `json:"action"` // "create", "update", "delete"

	// User facing document type, represents a JSON object.
	//
	//	"id" field is reserved for document ID.
	//	"version" field is reserved for document version.
	Doc model.Document `json:"document"`
}

type ReplicaPushRequest struct {
	Collection string          `json:"collection"`
	Changes    []ReplicaChange `json:"changes"`
}

type ReplicaPushResponse struct {
	Conflicts []model.Document `json:"conflicts"`
}

type ReplicaPullRequest struct {
	Collection string `json:"collection"`
	Checkpoint string `json:"checkpoint"`
	Limit      int    `json:"limit"`
}

type ReplicaPullResponse struct {
	Documents  []model.Document `json:"documents"`
	Checkpoint string           `json:"checkpoint"`
}

type UpdateDocumentRequest struct {
	Doc     model.Document  `json:"doc"`
	IfMatch model.Filters `json:"ifMatch,omitempty"`
}

type DeleteDocumentRequest struct {
	IfMatch model.Filters `json:"ifMatch,omitempty"`
}
