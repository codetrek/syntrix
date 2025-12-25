package mongo

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoBackend_Query(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()

	// Seed data
	users := []map[string]interface{}{
		{"name": "Alice", "age": 30, "active": true},
		{"name": "Bob", "age": 25, "active": true},
		{"name": "Charlie", "age": 35, "active": false},
	}

	for _, u := range users {
		doc := types.NewDocument("users/"+u["name"].(string), "users", u)
		err := backend.Create(ctx, doc)
		require.NoError(t, err)
	}

	// Test 1: Simple Filter
	q1 := model.Query{
		Collection: "users",
		Filters: []model.Filter{
			{Field: "age", Op: ">", Value: 28},
		},
	}
	docs, err := backend.Query(ctx, q1)
	require.NoError(t, err)
	assert.Len(t, docs, 2) // Alice (30), Charlie (35)

	// Test 2: Multiple Filters
	q2 := model.Query{
		Collection: "users",
		Filters: []model.Filter{
			{Field: "age", Op: ">", Value: 20},
			{Field: "active", Op: "==", Value: true},
		},
	}
	docs, err = backend.Query(ctx, q2)
	require.NoError(t, err)
	assert.Len(t, docs, 2) // Alice, Bob

	// Test 3: Sorting
	q3 := model.Query{
		Collection: "users",
		OrderBy: []model.Order{
			{Field: "age", Direction: "desc"},
		},
	}
	docs, err = backend.Query(ctx, q3)
	require.NoError(t, err)
	assert.Len(t, docs, 3)
	assert.Equal(t, "Charlie", docs[0].Data["name"])
	assert.Equal(t, "Alice", docs[1].Data["name"])
	assert.Equal(t, "Bob", docs[2].Data["name"])

	// Test 4: Limit
	q4 := model.Query{
		Collection: "users",
		OrderBy: []model.Order{
			{Field: "age", Direction: "asc"},
		},
		Limit: 2,
	}
	docs, err = backend.Query(ctx, q4)
	require.NoError(t, err)
	assert.Len(t, docs, 2)
	assert.Equal(t, "Bob", docs[0].Data["name"])
	assert.Equal(t, "Alice", docs[1].Data["name"])

	// Test 5: Field mapping (version and custom) with multi-filter and IN
	docs, err = backend.Query(ctx, model.Query{
		Collection: "users",
		Filters: []model.Filter{
			{Field: "version", Op: ">=", Value: int64(1)},
			{Field: "active", Op: "==", Value: true},
			{Field: "name", Op: "in", Value: []string{"Alice", "Charlie"}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.Equal(t, "Alice", docs[0].Data["name"])
}
