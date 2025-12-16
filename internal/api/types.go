package api

type ReplicaChange struct {
	Action string `json:"action"` // "create", "update", "delete"

	// User facing document type, represents a JSON object.
	//
	//	"id" field is reserved for document ID.
	//	"_version" field is reserved for document version.
	Doc Document `json:"document"`
}

type ReplicaPushRequest struct {
	Collection string          `json:"collection"`
	Changes    []ReplicaChange `json:"changes"`
}

type ReplicaPushResponse struct {
	Conflicts []Document `json:"conflicts"`
}

type ReplicaPullRequest struct {
	Collection string `json:"collection"`
	Checkpoint string `json:"checkpoint"`
	Limit      int    `json:"limit"`
}

type ReplicaPullResponse struct {
	Documents  []Document `json:"documents"`
	Checkpoint string     `json:"checkpoint"`
}
