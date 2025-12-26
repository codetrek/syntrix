package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMongoURI = "mongodb://localhost:27017"
	testDBName   = "syntrix_test"
)

func setupTestBackend(t *testing.T) types.DocumentStore {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider, err := NewDocumentProvider(ctx, testMongoURI, testDBName, "documents", "sys", 0)
	require.NoError(t, err)

	store := provider.Document()

	// Clean up test database before starting
	dStore, ok := store.(*documentStore)
	require.True(t, ok)

	err = dStore.db.Drop(ctx)
	require.NoError(t, err)

	// Recreate indexes after dropping the database
	require.NoError(t, dStore.EnsureIndexes(ctx))

	return store
}

func TestMongoBackend_CRUD(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/testuser"

	// 1. Create
	doc := types.NewDocument(docPath, "users", map[string]interface{}{
		"name": "Test User",
		"age":  30,
	})
	err := backend.Create(ctx, doc)
	require.NoError(t, err)

	// 2. Get
	fetchedDoc, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, doc.Id, fetchedDoc.Id)
	assert.Equal(t, doc.Collection, fetchedDoc.Collection)
	assert.Equal(t, types.CalculateCollectionHash("users"), fetchedDoc.CollectionHash)
	assert.Equal(t, "Test User", fetchedDoc.Data["name"])
	assert.Equal(t, int64(1), fetchedDoc.Version)

	// 3. Update (Success)
	newData := map[string]interface{}{
		"name": "Updated User",
		"age":  31,
	}

	filters := model.Filters{
		{Field: "version", Op: "==", Value: fetchedDoc.Version},
	}

	err = backend.Update(ctx, docPath, newData, filters)
	require.NoError(t, err)

	// Verify Update
	fetchedDoc, err = backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "Updated User", fetchedDoc.Data["name"])
	assert.Equal(t, int64(2), fetchedDoc.Version)

	// 4. Update (CAS Failure)
	oldVersion := int64(1)
	filters = model.Filters{
		{Field: "version", Op: "==", Value: oldVersion},
	}
	err = backend.Update(ctx, docPath, newData, filters)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)

	// 5. Delete
	err = backend.Delete(ctx, docPath, nil)
	require.NoError(t, err)

	// Verify Delete
	_, err = backend.Get(ctx, docPath)
	assert.ErrorIs(t, err, model.ErrNotFound)
}

func TestMongoBackend_Update_IfMatch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/ifmatch"

	// 1. Create
	doc := types.NewDocument(docPath, "users", map[string]interface{}{
		"status": "active",
		"score":  100,
	})
	err := backend.Create(ctx, doc)
	require.NoError(t, err)

	// 2. Update with matching filter (status == active)
	newData := map[string]interface{}{
		"status": "inactive",
		"score":  100,
	}
	filters := model.Filters{
		{Field: "status", Op: "==", Value: "active"},
	}
	err = backend.Update(ctx, docPath, newData, filters)
	require.NoError(t, err)

	// Verify Update
	fetchedDoc, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "inactive", fetchedDoc.Data["status"])

	// 3. Update with non-matching filter (score > 200)
	newData2 := map[string]interface{}{
		"status": "banned",
		"score":  100,
	}
	filters2 := model.Filters{
		{Field: "score", Op: ">", Value: 200},
	}
	err = backend.Update(ctx, docPath, newData2, filters2)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)

	// Verify No Update
	fetchedDoc, err = backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "inactive", fetchedDoc.Data["status"])
}

func TestMongoBackend_FilterOperators_OnUpdatePatchDelete(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	path := "users/filter-ops"

	seed := types.NewDocument(path, "users", map[string]interface{}{
		"age":  int64(30),
		"tags": []string{"a", "b"},
	})
	require.NoError(t, backend.Create(ctx, seed))

	// Update guarded by numeric GT
	gtFilter := model.Filters{{Field: "age", Op: ">", Value: int64(25)}}
	require.NoError(t, backend.Update(ctx, path, map[string]interface{}{
		"age":  int64(31),
		"tags": []string{"a", "b"},
	}, gtFilter))

	updated, err := backend.Get(ctx, path)
	require.NoError(t, err)
	assert.EqualValues(t, 31, updated.Data["age"])

	// Update blocked by LT (should conflict)
	ltFilter := model.Filters{{Field: "age", Op: "<", Value: int64(20)}}
	err = backend.Update(ctx, path, map[string]interface{}{"age": 40}, ltFilter)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)

	// Patch using IN
	inFilter := model.Filters{{Field: "tags", Op: "in", Value: []string{"a", "c"}}}
	require.NoError(t, backend.Patch(ctx, path, map[string]interface{}{"city": "NY"}, inFilter))

	afterPatch, err := backend.Get(ctx, path)
	require.NoError(t, err)
	require.EqualValues(t, int64(3), afterPatch.Version)
	assert.Equal(t, "NY", afterPatch.Data["city"])

	// Delete guarded by GTE version
	gteFilter := model.Filters{{Field: "version", Op: ">=", Value: afterPatch.Version}}
	require.NoError(t, backend.Delete(ctx, path, gteFilter))

	_, err = backend.Get(ctx, path)
	assert.ErrorIs(t, err, model.ErrNotFound)
}

func TestMongoBackend_CreateDuplicate(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	doc := types.NewDocument("users/dup", "users", nil)

	err := backend.Create(ctx, doc)
	require.NoError(t, err)

	err = backend.Create(ctx, doc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "document already exists") // Checking error message as we didn't define a specific error var for this yet
}

func TestMongoBackend_GetNotFound(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	_, err := backend.Get(ctx, "non/existent")
	assert.ErrorIs(t, err, model.ErrNotFound)
}

func TestMongoBackend_IndexesIncludeCollectionHash(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()

	dStore, ok := backend.(*documentStore)
	require.True(t, ok)

	coll := dStore.getCollection("")
	cur, err := coll.Indexes().List(ctx)
	require.NoError(t, err)
	defer cur.Close(ctx)

	found := false
	for cur.Next(ctx) {
		var idx bson.M
		require.NoError(t, cur.Decode(&idx))
		if key, ok := idx["key"].(bson.M); ok {
			if _, exists := key["collection_hash"]; exists {
				found = true
				break
			}
		}
	}

	assert.True(t, found, "collection_hash index should exist")
}

func TestMongoBackend_Patch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/patchuser"

	// Create initial document
	doc := types.NewDocument(docPath, "users", map[string]interface{}{
		"name": "Original Name",
		"info": map[string]interface{}{
			"age":  30,
			"city": "New York",
		},
	})
	err := backend.Create(ctx, doc)
	require.NoError(t, err)

	// Patch top-level field
	patchData := map[string]interface{}{
		"name": "Patched Name",
	}
	err = backend.Patch(ctx, docPath, patchData, model.Filters{})
	require.NoError(t, err)

	fetched, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "Patched Name", fetched.Data["name"])

	// Ensure other fields are preserved
	info, ok := fetched.Data["info"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 30, info["age"])

	// Patch nested field using dot notation in key
	patchData2 := map[string]interface{}{
		"info.age": 31,
	}
	err = backend.Patch(ctx, docPath, patchData2, model.Filters{})
	require.NoError(t, err)

	fetched2, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	info2, ok := fetched2.Data["info"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 31, info2["age"])
	assert.Equal(t, "New York", info2["city"])
}

func TestMongoBackend_Patch_WithFilter(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	path := "users/patch-precond"

	base := types.NewDocument(path, "users", map[string]interface{}{"name": "One"})
	require.NoError(t, backend.Create(ctx, base))

	wrong := model.Filters{{Field: "version", Op: "==", Value: int64(0)}}
	err := backend.Patch(ctx, path, map[string]interface{}{"name": "Wrong"}, wrong)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)

	current, err := backend.Get(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, int64(1), current.Version)
	assert.Equal(t, "One", current.Data["name"])

	match := model.Filters{{Field: "version", Op: "==", Value: current.Version}}
	require.NoError(t, backend.Patch(ctx, path, map[string]interface{}{"name": "Two"}, match))

	updated, err := backend.Get(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, "Two", updated.Data["name"])
	assert.Equal(t, int64(2), updated.Version)
}

func TestMongoBackend_Delete_WithFilter(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	path := "users/delete-precond"

	doc := types.NewDocument(path, "users", map[string]interface{}{"name": "ToDelete"})
	require.NoError(t, backend.Create(ctx, doc))

	wrong := model.Filters{{Field: "version", Op: "==", Value: int64(0)}}
	err := backend.Delete(ctx, path, wrong)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)

	stillThere, err := backend.Get(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, "ToDelete", stillThere.Data["name"])

	match := model.Filters{{Field: "version", Op: "==", Value: stillThere.Version}}
	require.NoError(t, backend.Delete(ctx, path, match))

	_, err = backend.Get(ctx, path)
	assert.ErrorIs(t, err, model.ErrNotFound)
}
