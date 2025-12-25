package mongo

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoBackend_SoftDelete(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/softdelete"

	// 1. Create
	doc := types.NewDocument(docPath, "users", map[string]interface{}{
		"name": "To Be Deleted",
	})
	err := backend.Create(ctx, doc)
	require.NoError(t, err)

	// 2. Soft Delete
	err = backend.Delete(ctx, docPath, nil)
	require.NoError(t, err)

	// 3. Verify Get returns NotFound
	_, err = backend.Get(ctx, docPath)
	assert.ErrorIs(t, err, model.ErrNotFound)

	// 4. Verify Query excludes deleted by default
	q := model.Query{
		Collection: "users",
		Filters:    model.Filters{},
	}
	docs, err := backend.Query(ctx, q)
	require.NoError(t, err)
	assert.Empty(t, docs)

	// 5. Verify Query includes deleted when requested
	q.ShowDeleted = true
	docs, err = backend.Query(ctx, q)
	require.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.True(t, docs[0].Deleted)
	assert.Empty(t, docs[0].Data) // Data should be cleared

	// 6. Re-create (Revive)
	newDoc := types.NewDocument(docPath, "users", map[string]interface{}{
		"name": "Revived",
	})
	err = backend.Create(ctx, newDoc)
	require.NoError(t, err)

	// 7. Verify Get returns new doc
	fetchedDoc, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "Revived", fetchedDoc.Data["name"])
	assert.False(t, fetchedDoc.Deleted)
}
