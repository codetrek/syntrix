// Package normalizer converts MongoDB change stream events to normalized events.
package normalizer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/codetrek/syntrix/internal/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RawEvent represents a MongoDB change stream event before normalization.
type RawEvent struct {
	OperationType     string              `bson:"operationType"`
	ClusterTime       primitive.Timestamp `bson:"clusterTime"`
	TxnNumber         *int64              `bson:"txnNumber,omitempty"`
	FullDocument      bson.M              `bson:"fullDocument,omitempty"`
	UpdateDescription bson.M              `bson:"updateDescription,omitempty"`
	DocumentKey       bson.M              `bson:"documentKey"`
	Namespace         struct {
		DB   string `bson:"db"`
		Coll string `bson:"coll"`
	} `bson:"ns"`
	ResumeToken bson.Raw `bson:"_id"`
}

// TenantExtractor defines how to extract tenant ID from a document.
type TenantExtractor interface {
	// Extract extracts the tenant ID from a full document or document key.
	// Returns empty string if tenant cannot be determined.
	Extract(fullDoc bson.M, docKey bson.M, collection string) string
}

// DefaultTenantExtractor extracts tenant from the "tenantId" or "_tenant" field.
type DefaultTenantExtractor struct{}

// Extract implements TenantExtractor.
func (e *DefaultTenantExtractor) Extract(fullDoc bson.M, docKey bson.M, collection string) string {
	// Try fullDocument first
	if fullDoc != nil {
		if tid, ok := fullDoc["tenantId"].(string); ok && tid != "" {
			return tid
		}
		if tid, ok := fullDoc["_tenant"].(string); ok && tid != "" {
			return tid
		}
	}

	// Try documentKey
	if docKey != nil {
		if tid, ok := docKey["tenantId"].(string); ok && tid != "" {
			return tid
		}
	}

	// Default tenant
	return "default"
}

// Normalizer converts MongoDB change stream events to NormalizedEvents.
type Normalizer struct {
	// tenantExtractor extracts tenant ID from the document or collection
	tenantExtractor TenantExtractor
}

// New creates a new Normalizer with the given tenant extractor.
// If extractor is nil, DefaultTenantExtractor is used.
func New(extractor TenantExtractor) *Normalizer {
	if extractor == nil {
		extractor = &DefaultTenantExtractor{}
	}
	return &Normalizer{
		tenantExtractor: extractor,
	}
}

// Normalize converts a RawEvent to a NormalizedEvent.
func (n *Normalizer) Normalize(raw *RawEvent) (*events.NormalizedEvent, error) {
	// Extract operation type
	opType := events.OperationType(raw.OperationType)
	if !opType.IsValid() {
		return nil, fmt.Errorf("unknown operation type: %s", raw.OperationType)
	}

	// Extract document ID
	docID, err := extractDocumentID(raw.DocumentKey)
	if err != nil {
		return nil, fmt.Errorf("failed to extract document ID: %w", err)
	}

	// Extract tenant ID
	tenantID := n.tenantExtractor.Extract(raw.FullDocument, raw.DocumentKey, raw.Namespace.Coll)

	// Generate event ID
	eventID := generateEventID(raw.ClusterTime, raw.Namespace.Coll, docID)

	// Create normalized event
	evt := &events.NormalizedEvent{
		EventID:     eventID,
		TenantID:    tenantID,
		Collection:  raw.Namespace.Coll,
		DocumentID:  docID,
		Type:        opType,
		ClusterTime: events.ClusterTimeFromPrimitive(raw.ClusterTime),
		Timestamp:   time.Now().UnixMilli(),
	}

	// Convert full document
	if raw.FullDocument != nil {
		evt.FullDocument = convertBsonM(raw.FullDocument)
	}

	// Convert update description
	if raw.UpdateDescription != nil {
		evt.UpdateDesc = convertUpdateDescription(raw.UpdateDescription)
	}

	// Set transaction number
	if raw.TxnNumber != nil {
		evt.TxnNumber = raw.TxnNumber
	}

	return evt, nil
}

// extractDocumentID extracts the document ID from the documentKey.
func extractDocumentID(docKey bson.M) (string, error) {
	if docKey == nil {
		return "", fmt.Errorf("documentKey is nil")
	}

	// Try _id field first (most common)
	if id, ok := docKey["_id"]; ok {
		return formatID(id), nil
	}

	// If no _id, use the entire documentKey as a compound key
	// This handles sharded collections with compound shard keys
	return formatCompoundKey(docKey), nil
}

// formatID formats a MongoDB _id value as a string.
func formatID(id any) string {
	switch v := id.(type) {
	case primitive.ObjectID:
		return v.Hex()
	case string:
		return v
	case int, int32, int64:
		return fmt.Sprintf("%d", v)
	default:
		// For other types, use JSON-like representation
		return fmt.Sprintf("%v", v)
	}
}

// formatCompoundKey formats a compound document key as a string.
func formatCompoundKey(docKey bson.M) string {
	// Create a deterministic hash of the document key
	data, _ := bson.Marshal(docKey)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes
}

// generateEventID generates a unique event ID.
func generateEventID(ct primitive.Timestamp, collection, docID string) string {
	// Format: {clusterTime.T}-{clusterTime.I}-{hash(collection+docID)}
	data := fmt.Sprintf("%s/%s", collection, docID)
	hash := sha256.Sum256([]byte(data))
	hashStr := hex.EncodeToString(hash[:8]) // Use first 8 bytes
	return fmt.Sprintf("%d-%d-%s", ct.T, ct.I, hashStr)
}

// convertBsonM converts a bson.M to map[string]any.
func convertBsonM(m bson.M) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = convertBsonValue(v)
	}
	return result
}

// convertBsonValue converts a BSON value to a Go value.
func convertBsonValue(v any) any {
	switch val := v.(type) {
	case bson.M:
		return convertBsonM(val)
	case bson.A:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = convertBsonValue(item)
		}
		return result
	case primitive.ObjectID:
		return val.Hex()
	case primitive.DateTime:
		return val.Time()
	case primitive.Timestamp:
		return map[string]uint32{"T": val.T, "I": val.I}
	default:
		return v
	}
}

// convertUpdateDescription converts a BSON update description to UpdateDescription.
func convertUpdateDescription(m bson.M) *events.UpdateDescription {
	desc := &events.UpdateDescription{}

	if updatedFields, ok := m["updatedFields"].(bson.M); ok {
		desc.UpdatedFields = convertBsonM(updatedFields)
	}

	if removedFields, ok := m["removedFields"].(bson.A); ok {
		desc.RemovedFields = make([]string, 0, len(removedFields))
		for _, f := range removedFields {
			if s, ok := f.(string); ok {
				desc.RemovedFields = append(desc.RemovedFields, s)
			}
		}
	}

	if truncatedArrays, ok := m["truncatedArrays"].(bson.A); ok {
		desc.TruncatedArrays = make([]events.TruncatedArray, 0, len(truncatedArrays))
		for _, ta := range truncatedArrays {
			if taMap, ok := ta.(bson.M); ok {
				field, _ := taMap["field"].(string)
				newSize, _ := taMap["newSize"].(int32)
				desc.TruncatedArrays = append(desc.TruncatedArrays, events.TruncatedArray{
					Field:   field,
					NewSize: int(newSize),
				})
			}
		}
	}

	return desc
}
