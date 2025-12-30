// Package events defines the canonical event schema for the Puller service.
// All consumers MUST use these types for event processing.
package events

import (
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// OperationType represents the type of change operation.
// All values are lowercase to match MongoDB change stream semantics.
type OperationType string

const (
	OperationInsert  OperationType = "insert"
	OperationUpdate  OperationType = "update"
	OperationReplace OperationType = "replace"
	OperationDelete  OperationType = "delete"
)

// IsValid checks if the operation type is a known valid type.
func (o OperationType) IsValid() bool {
	switch o {
	case OperationInsert, OperationUpdate, OperationReplace, OperationDelete:
		return true
	default:
		return false
	}
}

// ClusterTime represents a MongoDB cluster timestamp.
// This is used for ordering and idempotency checks.
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

// NormalizedEvent is the canonical event schema published by Puller.
// Field names use shorter JSON keys as defined in the design docs:
// - "tenant" (not "tenantId")
// - "documentId" (not "documentKey")
// - "operationType" is lowercase
type NormalizedEvent struct {
	// Identity
	EventID    string `json:"eventId"`
	TenantID   string `json:"tenant"` // JSON: "tenant" (NOT "tenantId")
	Collection string `json:"collection"`
	DocumentID string `json:"documentId"` // JSON: "documentId" (NOT "documentKey")

	// Operation
	Type         OperationType      `json:"operationType"` // lowercase: insert, update, replace, delete
	FullDocument map[string]any     `json:"fullDocument,omitempty"`
	UpdateDesc   *UpdateDescription `json:"updateDescription,omitempty"`

	// Metadata
	ClusterTime ClusterTime `json:"clusterTime"`
	TxnNumber   *int64      `json:"txnNumber,omitempty"`
	Timestamp   int64       `json:"timestamp"` // Unix milliseconds
}

// NewNormalizedEvent creates a new NormalizedEvent with the current timestamp.
func NewNormalizedEvent(
	eventID, tenantID, collection, documentID string,
	opType OperationType,
	clusterTime ClusterTime,
) *NormalizedEvent {
	return &NormalizedEvent{
		EventID:     eventID,
		TenantID:    tenantID,
		Collection:  collection,
		DocumentID:  documentID,
		Type:        opType,
		ClusterTime: clusterTime,
		Timestamp:   time.Now().UnixMilli(),
	}
}

// WithFullDocument sets the full document and returns the event for chaining.
func (e *NormalizedEvent) WithFullDocument(doc map[string]any) *NormalizedEvent {
	e.FullDocument = doc
	return e
}

// WithUpdateDescription sets the update description and returns the event for chaining.
func (e *NormalizedEvent) WithUpdateDescription(desc *UpdateDescription) *NormalizedEvent {
	e.UpdateDesc = desc
	return e
}

// WithTxnNumber sets the transaction number and returns the event for chaining.
func (e *NormalizedEvent) WithTxnNumber(txn int64) *NormalizedEvent {
	e.TxnNumber = &txn
	return e
}

// MarshalJSON implements json.Marshaler.
func (e *NormalizedEvent) MarshalJSON() ([]byte, error) {
	type Alias NormalizedEvent
	return json.Marshal((*Alias)(e))
}

// UnmarshalJSON implements json.Unmarshaler.
func (e *NormalizedEvent) UnmarshalJSON(data []byte) error {
	type Alias NormalizedEvent
	return json.Unmarshal(data, (*Alias)(e))
}

// BufferKey generates the PebbleDB key for this event.
// Format: {clusterTime.T}-{clusterTime.I}-{eventId}
// This ensures events are stored in cluster time order.
func (e *NormalizedEvent) BufferKey() string {
	return FormatBufferKey(e.ClusterTime, e.EventID)
}

// FormatBufferKey formats a buffer key from components.
func FormatBufferKey(ct ClusterTime, eventID string) string {
	// Use fixed-width formatting for proper lexicographic ordering
	return formatUint32(ct.T) + "-" + formatUint32(ct.I) + "-" + eventID
}

// formatUint32 formats a uint32 as a fixed-width string for lexicographic ordering.
func formatUint32(v uint32) string {
	// 10 digits is enough for uint32 max value (4294967295)
	s := "0000000000"
	n := s + uintToString(v)
	return n[len(n)-10:]
}

func uintToString(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
