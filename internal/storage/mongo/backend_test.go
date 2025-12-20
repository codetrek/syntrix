package mongo

import (
	"context"
	"testing"
	"time"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMongoURI = "mongodb://localhost:27017"
	testDBName   = "syntrix_test"
)

func setupTestBackend(t *testing.T) *MongoBackend {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	backend, err := NewMongoBackend(ctx, testMongoURI, testDBName, "documents", "sys")
	require.NoError(t, err)

	// Clean up test database before starting
	err = backend.db.Drop(ctx)
	require.NoError(t, err)

	return backend
}

func TestMongoBackend_CRUD(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/testuser"

	// 1. Create
	doc := storage.NewDocument(docPath, "users", map[string]interface{}{
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
	assert.Equal(t, "Test User", fetchedDoc.Data["name"])
	assert.Equal(t, int64(1), fetchedDoc.Version)

	// 3. Update (Success)
	newData := map[string]interface{}{
		"name": "Updated User",
		"age":  31,
	}

	filters := storage.Filters{
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
	filters = storage.Filters{
		{Field: "version", Op: "==", Value: oldVersion},
	}
	err = backend.Update(ctx, docPath, newData, filters)
	assert.ErrorIs(t, err, storage.ErrVersionConflict)

	// 5. Delete
	err = backend.Delete(ctx, docPath)
	require.NoError(t, err)

	// Verify Delete
	_, err = backend.Get(ctx, docPath)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestMongoBackend_Update_IfMatch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/ifmatch"

	// 1. Create
	doc := storage.NewDocument(docPath, "users", map[string]interface{}{
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
	filters := storage.Filters{
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
	filters2 := storage.Filters{
		{Field: "score", Op: ">", Value: 200},
	}
	err = backend.Update(ctx, docPath, newData2, filters2)
	assert.ErrorIs(t, err, storage.ErrVersionConflict)

	// Verify No Update
	fetchedDoc, err = backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "inactive", fetchedDoc.Data["status"])
}

func TestMongoBackend_CreateDuplicate(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	doc := storage.NewDocument("users/dup", "users", nil)

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
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestMongoBackend_Patch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx := context.Background()
	docPath := "users/patchuser"

	// Create initial document
	doc := storage.NewDocument(docPath, "users", map[string]interface{}{
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
	err = backend.Patch(ctx, docPath, patchData, storage.Filters{})
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
	err = backend.Patch(ctx, docPath, patchData2, storage.Filters{})
	require.NoError(t, err)

	fetched2, err := backend.Get(ctx, docPath)
	require.NoError(t, err)
	info2, ok := fetched2.Data["info"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 31, info2["age"])
	assert.Equal(t, "New York", info2["city"])
}
