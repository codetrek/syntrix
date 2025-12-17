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
	err = backend.Update(ctx, docPath, newData, fetchedDoc.Version)
	require.NoError(t, err)

	// Verify Update
	fetchedDoc, err = backend.Get(ctx, docPath)
	require.NoError(t, err)
	assert.Equal(t, "Updated User", fetchedDoc.Data["name"])
	assert.Equal(t, int64(2), fetchedDoc.Version)

	// 4. Update (CAS Failure)
	oldVersion := int64(1)
	err = backend.Update(ctx, docPath, newData, oldVersion)
	assert.ErrorIs(t, err, storage.ErrVersionConflict)

	// 5. Delete
	err = backend.Delete(ctx, docPath)
	require.NoError(t, err)

	// Verify Delete
	_, err = backend.Get(ctx, docPath)
	assert.ErrorIs(t, err, storage.ErrNotFound)
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
