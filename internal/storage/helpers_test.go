package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateID(t *testing.T) {
	path := "users/bob"
	id := CalculateID(path)
	assert.Len(t, id, 32) // 16 bytes hex = 32 chars
}

func TestNewDocument(t *testing.T) {
	path := "users/bob"
	collection := "users"
	data := map[string]interface{}{"foo": "bar"}

	before := time.Now().UnixMilli()
	doc := NewDocument(path, collection, data)
	after := time.Now().UnixMilli()

	assert.Equal(t, CalculateID(path), doc.Id)
	assert.Equal(t, collection, doc.Collection)
	assert.Equal(t, data, doc.Data)
	assert.Equal(t, int64(1), doc.Version)

	// Check timestamp is within reasonable range
	assert.GreaterOrEqual(t, doc.UpdatedAt, before)
	assert.LessOrEqual(t, doc.UpdatedAt, after)
}

func TestNewDocumentTimestamps(t *testing.T) {
	path := "users/alice"
	collection := "users"
	data := map[string]interface{}{"hello": "world"}

	before := time.Now().UnixMilli()
	doc := NewDocument(path, collection, data)
	after := time.Now().UnixMilli()

	assert.Equal(t, doc.CreatedAt, doc.UpdatedAt)
	assert.GreaterOrEqual(t, doc.CreatedAt, before)
	assert.LessOrEqual(t, doc.CreatedAt, after)
}
