package mongo

import (
	"testing"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestMakeFilterBSON_FieldAndOpMapping(t *testing.T) {
	filters := model.Filters{
		{Field: "path", Op: "==", Value: "users/1"},
		{Field: "collection", Op: "!=", Value: "users"},
		{Field: "updatedAt", Op: ">", Value: int64(10)},
		{Field: "createdAt", Op: "<=", Value: int64(5)},
		{Field: "version", Op: "in", Value: []int{1, 2}},
		{Field: "score", Op: ">=", Value: 90},
	}

	bsonFilter := makeFilterBSON(filters)

	if m, ok := bsonFilter["_id"].(map[string]interface{}); ok {
		assert.Equal(t, "users/1", m["$eq"])
	}
	if m, ok := bsonFilter["collection"].(map[string]interface{}); ok {
		assert.Equal(t, "users", m["$ne"])
	}
	if m, ok := bsonFilter["updated_at"].(map[string]interface{}); ok {
		assert.Equal(t, int64(10), m["$gt"])
	}
	if m, ok := bsonFilter["created_at"].(map[string]interface{}); ok {
		assert.Equal(t, int64(5), m["$lte"])
	}
	if m, ok := bsonFilter["version"].(map[string]interface{}); ok {
		assert.ElementsMatch(t, []interface{}{1, 2}, m["$in"].([]interface{}))
	}
	if m, ok := bsonFilter["data.score"].(map[string]interface{}); ok {
		assert.Equal(t, 90, m["$gte"])
	}
}

func TestMakeFilterBSON_Defaults(t *testing.T) {
	bsonFilter := makeFilterBSON(nil)
	assert.Empty(t, bsonFilter)

	filters := model.Filters{{Field: "custom", Op: "", Value: 1}}
	bsonFilter = makeFilterBSON(filters)
	// default op becomes $eq even if empty
	if m, ok := bsonFilter["data.custom"].(map[string]interface{}); ok {
		assert.Equal(t, 1, m["$eq"])
	}
}
