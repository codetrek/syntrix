package api

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
