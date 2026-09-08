package mongo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage/types"
	"github.com/syntrixbase/syntrix/pkg/model"
)

func TestDocumentStore_Delete_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()
	database := "default"

	// Ensure indexes are created if the method exists
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		err := ds.EnsureIndexes(ctx)
		require.NoError(t, err)
	}

	t.Run("Delete Non-Existent Document", func(t *testing.T) {
		err := store.Delete(ctx, database, "non/existent", nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Delete Already Soft-Deleted Document", func(t *testing.T) {
		collection := "users"
		docID := "deleted_user"
		path := collection + "/" + docID
		doc := types.NewStoredDoc(database, collection, docID, map[string]interface{}{
			"name": "To Be Deleted",
		})

		// Create
		err := store.Create(ctx, database, doc)
		require.NoError(t, err)

		// First Delete (Soft Delete)
		err = store.Delete(ctx, database, path, nil)
		require.NoError(t, err)

		// Verify it is soft deleted (Get should fail with ErrNotFound)
		_, err = store.Get(ctx, database, path)
		assert.ErrorIs(t, err, model.ErrNotFound)

		// Second Delete (Should fail with ErrNotFound because it's already deleted)
		err = store.Delete(ctx, database, path, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestDocumentStore_Coverage_Extended(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()
	database := "default"

	// Ensure indexes
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		require.NoError(t, ds.EnsureIndexes(ctx))
	}

	t.Run("Get Non-Existent Document", func(t *testing.T) {
		_, err := store.Get(ctx, database, "non/existent/get")
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Create with Empty CollectionHash", func(t *testing.T) {
		doc := types.NewStoredDoc(database, "users", "empty_hash", map[string]interface{}{"a": 1})
		doc.CollectionHash = "" // Explicitly empty
		err := store.Create(ctx, database, doc)
		require.NoError(t, err)
		// Fetch the doc from the database to verify CollectionHash was populated
		fetched, err := store.Get(ctx, database, "users/empty_hash")
		require.NoError(t, err)
		assert.NotEmpty(t, fetched.CollectionHash)
		assert.Equal(t, types.CalculateCollectionHash("users"), fetched.CollectionHash)
	})

	t.Run("Update Non-Existent Document", func(t *testing.T) {
		err := store.Update(ctx, database, "non/existent/update", map[string]interface{}{"a": 2}, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Patch Non-Existent Document", func(t *testing.T) {
		err := store.Patch(ctx, database, "non/existent/patch", map[string]interface{}{"a": 2}, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestDocumentStore_GetMany_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()
	database := "default"

	// Ensure indexes
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		require.NoError(t, ds.EnsureIndexes(ctx))
	}

	t.Run("GetMany Empty Paths", func(t *testing.T) {
		docs, err := store.GetMany(ctx, database, []string{})
		require.NoError(t, err)
		assert.Empty(t, docs)
	})

	t.Run("GetMany Non-Existent Documents", func(t *testing.T) {
		docs, err := store.GetMany(ctx, database, []string{"nonexistent/doc1", "nonexistent/doc2"})
		require.NoError(t, err)
		assert.Len(t, docs, 2)
		assert.Nil(t, docs[0])
		assert.Nil(t, docs[1])
	})

	t.Run("GetMany Mixed Existing and Non-Existing", func(t *testing.T) {
		// Create a doc
		doc := types.NewStoredDoc(database, "getmany", "existing1", map[string]interface{}{"name": "test"})
		require.NoError(t, store.Create(ctx, database, doc))

		paths := []string{"getmany/existing1", "getmany/nonexistent"}
		docs, err := store.GetMany(ctx, database, paths)
		require.NoError(t, err)
		assert.Len(t, docs, 2)
		assert.NotNil(t, docs[0])
		assert.Equal(t, "getmany/existing1", docs[0].Fullpath)
		assert.Nil(t, docs[1])
	})

	t.Run("GetMany All Existing", func(t *testing.T) {
		// Create docs
		doc1 := types.NewStoredDoc(database, "getmany", "all1", map[string]interface{}{"name": "one"})
		doc2 := types.NewStoredDoc(database, "getmany", "all2", map[string]interface{}{"name": "two"})
		require.NoError(t, store.Create(ctx, database, doc1))
		require.NoError(t, store.Create(ctx, database, doc2))

		paths := []string{"getmany/all1", "getmany/all2"}
		docs, err := store.GetMany(ctx, database, paths)
		require.NoError(t, err)
		assert.Len(t, docs, 2)
		assert.NotNil(t, docs[0])
		assert.NotNil(t, docs[1])
		assert.Equal(t, "getmany/all1", docs[0].Fullpath)
		assert.Equal(t, "getmany/all2", docs[1].Fullpath)
	})

	t.Run("GetMany Excludes Deleted Docs", func(t *testing.T) {
		// Create and delete a doc
		doc := types.NewStoredDoc(database, "getmany", "deleted1", map[string]interface{}{"name": "deleted"})
		require.NoError(t, store.Create(ctx, database, doc))
		require.NoError(t, store.Delete(ctx, database, "getmany/deleted1", nil))

		paths := []string{"getmany/deleted1"}
		docs, err := store.GetMany(ctx, database, paths)
		require.NoError(t, err)
		assert.Len(t, docs, 1)
		assert.Nil(t, docs[0]) // Deleted doc should not be returned
	})
}

func TestDocumentStore_DeleteByDatabase(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()

	// Create documents in two different databases
	for i := 0; i < 5; i++ {
		doc := types.NewStoredDoc("db1", "users", "user"+string(rune('a'+i)), map[string]interface{}{
			"name": "User " + string(rune('A'+i)),
		})
		err := store.Create(ctx, "db1", doc)
		require.NoError(t, err)
	}

	for i := 0; i < 3; i++ {
		doc := types.NewStoredDoc("db2", "posts", "post"+string(rune('a'+i)), map[string]interface{}{
			"title": "Post " + string(rune('A'+i)),
		})
		err := store.Create(ctx, "db2", doc)
		require.NoError(t, err)
	}

	t.Run("DeleteByDatabase with no limit", func(t *testing.T) {
		deleted, err := store.DeleteByDatabase(ctx, "db1", 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, deleted, 5) // At least 5 docs deleted
	})

	t.Run("DeleteByDatabase with limit", func(t *testing.T) {
		// First add more documents
		for i := 0; i < 5; i++ {
			doc := types.NewStoredDoc("db3", "items", "item"+string(rune('a'+i)), map[string]interface{}{
				"name": "Item " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db3", doc)
			require.NoError(t, err)
		}

		// Delete with limit
		deleted, err := store.DeleteByDatabase(ctx, "db3", 2)
		require.NoError(t, err)
		assert.Equal(t, 2, deleted)

		// Delete remaining
		deleted, err = store.DeleteByDatabase(ctx, "db3", 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, deleted, 3)
	})

	t.Run("DeleteByDatabase non-existent database", func(t *testing.T) {
		deleted, err := store.DeleteByDatabase(ctx, "nonexistent", 0)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})

	t.Run("DeleteByDatabase with canceled context", func(t *testing.T) {
		// Add documents to test error path
		for i := 0; i < 3; i++ {
			doc := types.NewStoredDoc("db_cancel", "items", "item"+string(rune('a'+i)), map[string]interface{}{
				"name": "Item " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db_cancel", doc)
			require.NoError(t, err)
		}

		// Cancel context before delete
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// Delete with canceled context should return error
		_, err := store.DeleteByDatabase(cancelCtx, "db_cancel", 0)
		assert.Error(t, err)
	})

	t.Run("DeleteByDatabase with limit and canceled context", func(t *testing.T) {
		// Add documents
		for i := 0; i < 3; i++ {
			doc := types.NewStoredDoc("db_cancel2", "items", "item"+string(rune('a'+i)), map[string]interface{}{
				"name": "Item " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db_cancel2", doc)
			require.NoError(t, err)
		}

		// Cancel context before delete with limit
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// Delete with canceled context and limit should return error
		_, err := store.DeleteByDatabase(cancelCtx, "db_cancel2", 2)
		assert.Error(t, err)
	})

	t.Run("DeleteByDatabase deletes from both data and sys collections", func(t *testing.T) {
		// Add documents to data collection
		for i := 0; i < 2; i++ {
			doc := types.NewStoredDoc("db_both", "users", "user"+string(rune('a'+i)), map[string]interface{}{
				"name": "User " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db_both", doc)
			require.NoError(t, err)
		}

		// Add documents to sys collection
		for i := 0; i < 2; i++ {
			doc := types.NewStoredDoc("db_both", "sys", "config"+string(rune('a'+i)), map[string]interface{}{
				"value": i,
			})
			err := store.Create(ctx, "db_both", doc)
			require.NoError(t, err)
		}

		// Delete all - should delete from both collections
		deleted, err := store.DeleteByDatabase(ctx, "db_both", 0)
		require.NoError(t, err)
		assert.Equal(t, 4, deleted)
	})

	t.Run("DeleteByDatabase with limit hits data collection only", func(t *testing.T) {
		// Add documents to data collection
		for i := 0; i < 5; i++ {
			doc := types.NewStoredDoc("db_limit_test", "users", "user"+string(rune('a'+i)), map[string]interface{}{
				"name": "User " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db_limit_test", doc)
			require.NoError(t, err)
		}

		// Add documents to sys collection
		for i := 0; i < 3; i++ {
			doc := types.NewStoredDoc("db_limit_test", "sys", "config"+string(rune('a'+i)), map[string]interface{}{
				"value": i,
			})
			err := store.Create(ctx, "db_limit_test", doc)
			require.NoError(t, err)
		}

		// Delete with limit 5 - should only delete from data collection
		deleted, err := store.DeleteByDatabase(ctx, "db_limit_test", 5)
		require.NoError(t, err)
		assert.Equal(t, 5, deleted)

		// Delete remaining - should be 3 from sys collection
		deleted, err = store.DeleteByDatabase(ctx, "db_limit_test", 0)
		require.NoError(t, err)
		assert.Equal(t, 3, deleted)
	})

	t.Run("DeleteByDatabase with limit spans both collections", func(t *testing.T) {
		// Add documents to data collection
		for i := 0; i < 2; i++ {
			doc := types.NewStoredDoc("db_span", "users", "user"+string(rune('a'+i)), map[string]interface{}{
				"name": "User " + string(rune('A'+i)),
			})
			err := store.Create(ctx, "db_span", doc)
			require.NoError(t, err)
		}

		// Add documents to sys collection
		for i := 0; i < 3; i++ {
			doc := types.NewStoredDoc("db_span", "sys", "config"+string(rune('a'+i)), map[string]interface{}{
				"value": i,
			})
			err := store.Create(ctx, "db_span", doc)
			require.NoError(t, err)
		}

		// Delete with limit 4 - should delete 2 from data and 2 from sys
		deleted, err := store.DeleteByDatabase(ctx, "db_span", 4)
		require.NoError(t, err)
		assert.Equal(t, 4, deleted)

		// Delete remaining - should be 1 from sys collection
		deleted, err = store.DeleteByDatabase(ctx, "db_span", 0)
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
	})
}
