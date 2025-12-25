package types

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/zeebo/blake3"
)

// CalculateID calculates the document ID (hash) from the full path
func CalculateID(fullpath string) string {
	hash := blake3.Sum256([]byte(fullpath))
	return hex.EncodeToString(hash[:16])
}

// NewDocument creates a new document instance with initialized metadata
func NewDocument(fullpath string, collection string, data map[string]interface{}) *Document {
	// Calculate Parent from collection path
	parent := ""
	if idx := strings.LastIndex(collection, "/"); idx != -1 {
		parent = collection[:idx]
	}

	id := CalculateID(fullpath)

	now := time.Now().UnixMilli()

	return &Document{
		Id:         id,
		Fullpath:   fullpath,
		Collection: collection,
		Parent:     parent,
		Data:       data,
		UpdatedAt:  now,
		CreatedAt:  now,
		Version:    1,
	}
}
