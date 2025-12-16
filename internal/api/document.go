package api

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// User facing document type, represents a JSON object.
//
//	"id" field is reserved for document ID.
//	"_version" field is reserved for document version.
//	"_updated_at" field is reserved for last updated timestamp.
type Document map[string]interface{}

func (doc Document) GetID() string {
	if id, ok := doc["id"].(string); ok {
		return id
	}
	return ""
}

func (doc Document) SetID(newID string) {
	doc["id"] = newID
}

func (doc Document) GenerateIDIfEmpty() {
	if _, ok := doc["id"]; !ok {
		doc["id"] = uuid.New().String()
	}
}

func (doc Document) HasVersion() bool {
	_, exists := doc["_version"]
	return exists
}

func (doc Document) GetVersion() int64 {
	if v, ok := doc["_version"].(float64); ok {
		return int64(v)
	}

	return -1
}

func (doc Document) HasKey(key string) bool {
	_, exists := doc[key]
	return exists
}

func (doc Document) StripProtectedFields() Document {
	stripped := make(Document)
	for k, v := range doc {
		if k != "_version" && k != "_updated_at" {
			stripped[k] = v
		}
	}
	return stripped
}

func (doc Document) IsEmpty() bool {
	stripped := doc.StripProtectedFields()
	delete(stripped, "id")
	return len(stripped) == 0
}

func (doc Document) ValidateDocument() error {
	if doc == nil {
		return errors.New("data cannot be nil")
	}

	if idVal, ok := doc["id"]; ok {
		switch idValue := idVal.(type) {
		case string:
			if idValue == "" {
				return errors.New("data field 'id' cannot be empty")
			}

			if !idRegex.MatchString(idVal.(string)) {
				return errors.New("invalid 'id' field: must be 1-64 characters of a-z, A-Z, 0-9, _, ., -")
			}
		case int, int32, int64:
			doc["id"] = fmt.Sprintf("%d", idValue)
		default:
			return errors.New("data field 'id' must be a string or integer")
		}
	}

	return nil
}
