// Package events defines the canonical event schema for the Puller service.
// All consumers MUST use these types for event processing.
package events

import (
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// StoreOperationType represents the type of change operation.
// All values are lowercase to match MongoDB change stream semantics.
type StoreOperationType string

const (
	StoreOperationInsert  StoreOperationType = "insert"
	StoreOperationUpdate  StoreOperationType = "update"
	StoreOperationReplace StoreOperationType = "replace"
	StoreOperationDelete  StoreOperationType = "delete"
)

// IsValid checks if the operation type is a known valid type.
func (o StoreOperationType) IsValid() bool {
	switch o {
	case StoreOperationInsert, StoreOperationUpdate, StoreOperationReplace, StoreOperationDelete:
		return true
	default:
		return false
	}
}

// ClusterTime represents a MongoDB cluster timestamp.
// It preserves source provenance; durable positions define local replay order.
type ClusterTime struct {
	T uint32 `json:"T"` // Seconds since epoch
	I uint32 `json:"I"` // Increment within second
}

// Compare compares two ClusterTime values.
// Returns -1 if c < other, 0 if equal, 1 if c > other.
func (c ClusterTime) Compare(other ClusterTime) int {
	if c.T < other.T {
		return -1
	}
	if c.T > other.T {
		return 1
	}
	if c.I < other.I {
		return -1
	}
	if c.I > other.I {
		return 1
	}
	return 0
}

// IsZero returns true if the ClusterTime is unset.
func (c ClusterTime) IsZero() bool {
	return c.T == 0 && c.I == 0
}

// FromPrimitive converts a MongoDB primitive.Timestamp to ClusterTime.
func ClusterTimeFromPrimitive(ts primitive.Timestamp) ClusterTime {
	return ClusterTime{
		T: ts.T,
		I: ts.I,
	}
}

// ToPrimitive converts ClusterTime to MongoDB primitive.Timestamp.
func (c ClusterTime) ToPrimitive() primitive.Timestamp {
	return primitive.Timestamp{
		T: c.T,
		I: c.I,
	}
}

// UpdateDescription contains the delta for update operations.
type UpdateDescription struct {
	UpdatedFields   map[string]any   `json:"updatedFields,omitempty"`
	RemovedFields   []string         `json:"removedFields,omitempty"`
	TruncatedArrays []TruncatedArray `json:"truncatedArrays,omitempty"`
}

// TruncatedArray describes an array that was truncated during an update.
type TruncatedArray struct {
	Field   string `json:"field"`
	NewSize int    `json:"newSize"`
}

// PullerEvent is the top-level wrapper for events emitted by the Puller service.
// It contains the actual change event and the progress marker.
type PullerEvent struct {
	Change   *StoreChangeEvent `json:"change_event"`
	Progress string            `json:"progress"`
}

// StoreChangeEvent is the canonical event schema published by Puller.
// Field names use short JSON keys with Mongo provenance as defined in the design docs.
type StoreChangeEvent struct {
	// Identity
	EventID  string `json:"eventId"`
	Database string `json:"database"` // Derived from FullDocument.Database

	// Mongo Provenance
	MgoColl  string `json:"mgoColl"`
	MgoDocID string `json:"mgoDocId"`

	// Operation
	OpType       StoreOperationType `json:"opType"`
	FullDocument *storage.StoredDoc `json:"fullDoc,omitempty"`
	UpdateDesc   *UpdateDescription `json:"updateDesc,omitempty"`

	// Metadata
	ClusterTime ClusterTime `json:"clusterTime"`
	TxnNumber   *int64      `json:"txnNumber,omitempty"`
	Timestamp   int64       `json:"timestamp"` // Unix milliseconds
	Backend     string      `json:"backend,omitempty"`
}
