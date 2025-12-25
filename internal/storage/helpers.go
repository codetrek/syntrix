package storage

import "github.com/codetrek/syntrix/internal/storage/types"

// CalculateID calculates the document ID (hash) from the full path
func CalculateID(fullpath string) string {
	return types.CalculateID(fullpath)
}

// NewDocument creates a new document instance with initialized metadata
func NewDocument(fullpath string, collection string, data map[string]interface{}) *Document {
	return types.NewDocument(fullpath, collection, data)
}
