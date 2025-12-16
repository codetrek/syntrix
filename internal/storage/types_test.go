package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewDocument(t *testing.T) {
	path := "users/bob"
	collection := "users"
	data := map[string]interface{}{"foo": "bar"}

	before := time.Now().UnixNano()
	doc := NewDocument(path, collection, data)
	after := time.Now().UnixNano()

	assert.Equal(t, path, doc.Id)
	assert.Equal(t, collection, doc.Collection)
	assert.Equal(t, data, doc.Data)
	assert.Equal(t, int64(1), doc.Version)

	// Check timestamp is within reasonable range
	assert.GreaterOrEqual(t, doc.UpdatedAt, before)
	assert.LessOrEqual(t, doc.UpdatedAt, after)
}
