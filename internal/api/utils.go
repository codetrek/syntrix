package api

import "syntrix/internal/storage"

func flattenDocument(doc *storage.Document) Document {
	if doc == nil {
		return nil
	}
	flat := make(Document)

	// Copy data
	for k, v := range doc.Data {
		flat[k] = v
	}
	// Add system fields
	flat["_version"] = doc.Version
	flat["_updated_at"] = doc.UpdatedAt
	return flat
}
