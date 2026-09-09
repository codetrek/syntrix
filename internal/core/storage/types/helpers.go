package types

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/syntrixbase/syntrix/pkg/model"
	"github.com/zeebo/blake3"
)

// ResolveReadOptions preserves default routing only when no other consistency is requested.
func ResolveReadOptions(opts []ReadOptions) (ReadOptions, error) {
	if len(opts) > 1 {
		return ReadOptions{}, fmt.Errorf("Get accepts at most one ReadOptions value")
	}
	var resolved ReadOptions
	if len(opts) == 1 {
		resolved = opts[0]
	}
	switch resolved.Consistency {
	case ReadDefault, ReadAuthoritative:
		return resolved, nil
	default:
		return ReadOptions{}, fmt.Errorf("unsupported read consistency: %d", resolved.Consistency)
	}
}

// CalculateDatabase calculates the database-aware document ID
// Format: database:hash(fullpath)
func CalculateDatabase(database, fullpath string) string {
	hash := blake3.Sum256([]byte(fullpath))
	hashStr := hex.EncodeToString(hash[:16])
	return database + ":" + hashStr
}

// CalculateID calculates the document ID (hash) from the full path
// Deprecated: Use CalculateDatabase instead
func CalculateID(fullpath string) string {
	hash := blake3.Sum256([]byte(fullpath))
	return hex.EncodeToString(hash[:16])
}

// CalculateCollectionHash calculates a stable hash for a collection name.
// A prefix keeps the namespace distinct from document IDs.
func CalculateCollectionHash(collection string) string {
	hash := blake3.Sum256([]byte("collection:" + collection))
	return hex.EncodeToString(hash[:16])
}

func NewStoredDoc(database, collection, docid string, data map[string]interface{}) StoredDoc {
	// Calculate Parent from collection path
	parent := ""
	if idx := strings.LastIndex(collection, "/"); idx != -1 {
		parent = collection[:idx]
	}

	if data == nil {
		data = make(map[string]interface{})
	}

	if id, exists := data["id"]; !exists || id != docid {
		data["id"] = docid
	}

	model.StripProtectedFields(data)

	fullpath := collection + "/" + docid
	id := CalculateDatabase(database, fullpath)
	collectionHash := CalculateCollectionHash(collection)

	now := time.Now().UnixMilli()

	return StoredDoc{
		Id:             id,
		Database:       database,
		Fullpath:       fullpath,
		Collection:     collection,
		CollectionHash: collectionHash,
		Parent:         parent,
		Data:           data,
		UpdatedAt:      now,
		CreatedAt:      now,
		Version:        1,
	}
}
