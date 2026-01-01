package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestDocumentStore_Delete_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()
	tenant := "default"

	// Ensure indexes are created if the method exists
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		err := ds.EnsureIndexes(ctx)
		require.NoError(t, err)
	}

	t.Run("Delete Non-Existent Document", func(t *testing.T) {
		err := store.Delete(ctx, tenant, "non/existent", nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Delete Already Soft-Deleted Document", func(t *testing.T) {
		path := "users/deleted_user"
		doc := types.NewDocument(tenant, path, "users", map[string]interface{}{
			"name": "To Be Deleted",
		})

		// Create
		err := store.Create(ctx, tenant, doc)
		require.NoError(t, err)

		// First Delete (Soft Delete)
		err = store.Delete(ctx, tenant, path, nil)
		require.NoError(t, err)

		// Verify it is soft deleted (Get should fail with ErrNotFound)
		_, err = store.Get(ctx, tenant, path)
		assert.ErrorIs(t, err, model.ErrNotFound)

		// Second Delete (Should fail with ErrNotFound because it's already deleted)
		err = store.Delete(ctx, tenant, path, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestDocumentStore_Coverage_Extended(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx := context.Background()
	tenant := "default"

	// Ensure indexes
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		require.NoError(t, ds.EnsureIndexes(ctx))
	}

	t.Run("Get Non-Existent Document", func(t *testing.T) {
		_, err := store.Get(ctx, tenant, "non/existent/get")
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Create with Empty CollectionHash", func(t *testing.T) {
		doc := types.NewDocument(tenant, "users/empty_hash", "users", map[string]interface{}{"a": 1})
		doc.CollectionHash = "" // Explicitly empty
		err := store.Create(ctx, tenant, doc)
		require.NoError(t, err)
		assert.NotEmpty(t, doc.CollectionHash)
		assert.Equal(t, types.CalculateCollectionHash("users"), doc.CollectionHash)
	})

	t.Run("Update Non-Existent Document", func(t *testing.T) {
		err := store.Update(ctx, tenant, "non/existent/update", map[string]interface{}{"a": 2}, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("Patch Non-Existent Document", func(t *testing.T) {
		err := store.Patch(ctx, tenant, "non/existent/patch", map[string]interface{}{"a": 2}, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestDocumentStore_Watch_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tenant := "default"

	// Ensure indexes
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		require.NoError(t, ds.EnsureIndexes(ctx))
	}

	// 1. Watch with ResumeToken
	t.Run("Watch with ResumeToken", func(t *testing.T) {
		// Create a doc to generate an event
		doc := types.NewDocument(tenant, "watch/resume", "watch", map[string]interface{}{"v": 1})
		require.NoError(t, store.Create(ctx, tenant, doc))

		// Start watching to get a token
		ch, err := store.Watch(ctx, tenant, "watch", nil, types.WatchOptions{})
		require.NoError(t, err)

		// Update to get an event
		require.NoError(t, store.Update(ctx, tenant, "watch/resume", map[string]interface{}{"v": 2}, nil))

		var resumeToken interface{}
		select {
		case evt := <-ch:
			resumeToken = evt.ResumeToken
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for event")
		}

		// Now watch with resume token
		chResume, err := store.Watch(ctx, tenant, "watch", resumeToken, types.WatchOptions{})
		require.NoError(t, err)

		// Trigger another update
		require.NoError(t, store.Update(ctx, tenant, "watch/resume", map[string]interface{}{"v": 3}, nil))

		select {
		case evt := <-chResume:
			assert.Equal(t, types.EventUpdate, evt.Type)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for resumed event")
		}
	})

	// 2. Watch without Tenant (Global Watch)
	t.Run("Watch without Tenant", func(t *testing.T) {
		ch, err := store.Watch(ctx, "", "watch_global", nil, types.WatchOptions{})
		require.NoError(t, err)

		doc := types.NewDocument("tenant1", "watch_global/doc1", "watch_global", map[string]interface{}{"v": 1})
		require.NoError(t, store.Create(ctx, "tenant1", doc))

		select {
		case evt := <-ch:
			assert.Equal(t, "tenant1", evt.TenantID)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for global event")
		}
	})

	// 3. Watch Undelete (Update on soft-deleted)
	t.Run("Watch Undelete", func(t *testing.T) {
		path := "watch/undelete"
		doc := types.NewDocument(tenant, path, "watch_undelete", map[string]interface{}{"v": 1})
		require.NoError(t, store.Create(ctx, tenant, doc))
		require.NoError(t, store.Delete(ctx, tenant, path, nil))

		// Watch with IncludeBefore to catch the state transition
		ch, err := store.Watch(ctx, tenant, "watch_undelete", nil, types.WatchOptions{IncludeBefore: true})
		require.NoError(t, err)

		// Re-create (which is an update/replace on soft-deleted doc)
		doc2 := types.NewDocument(tenant, path, "watch_undelete", map[string]interface{}{"v": 2})
		require.NoError(t, store.Create(ctx, tenant, doc2))

		select {
		case evt := <-ch:
			assert.Equal(t, types.EventCreate, evt.Type)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for undelete event")
		}
	})

	// 4. Watch Real Delete
	t.Run("Watch Real Delete", func(t *testing.T) {
		path := "watch/real_delete"
		doc := types.NewDocument(tenant, path, "watch_delete", nil)
		require.NoError(t, store.Create(ctx, tenant, doc))

		ch, err := store.Watch(ctx, tenant, "watch_delete", nil, types.WatchOptions{})
		require.NoError(t, err)

		// Perform hard delete using raw mongo client
		// We need to access the underlying collection, but store is an interface.
		// We can use reflection or just assume it's *documentStore if we created it that way.
		// But here we can just use env.DB directly.
		id := types.CalculateTenantID(tenant, path)
		_, err = env.DB.Collection("docs").DeleteOne(ctx, map[string]interface{}{"_id": id})
		require.NoError(t, err)

		select {
		case evt := <-ch:
			assert.Equal(t, types.EventDelete, evt.Type)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for delete event")
		}
	})

	// 5. Watch Exit
	t.Run("Watch Exit", func(t *testing.T) {
		ctxCancel, cancel := context.WithCancel(context.Background())
		ch, err := store.Watch(ctxCancel, tenant, "watch_exit", nil, types.WatchOptions{})
		require.NoError(t, err)

		cancel() // Cancel context immediately

		select {
		case _, ok := <-ch:
			if !ok {
				// Channel closed, success
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for channel close")
		}
	})

	t.Run("ConvertChangeEvent_DefaultAndReplace", func(t *testing.T) {
		ds := store.(*documentStore)

		// Unknown operation should be skipped
		noOp := changeStreamEvent{OperationType: "noop", DocumentKey: struct {
			ID string `bson:"_id"`
		}{ID: "tenant:path"}}
		_, ok := ds.convertChangeEvent(noOp, "tenant", "")
		assert.False(t, ok)

		// Replace with soft-deleted before-state should yield create
		change := changeStreamEvent{
			OperationType:            "replace",
			FullDocument:             &types.Document{TenantID: "t1", Collection: "c1"},
			FullDocumentBeforeChange: &types.Document{Deleted: true},
			DocumentKey: struct {
				ID string `bson:"_id"`
			}{ID: "t1:path"},
		}
		evt, ok := ds.convertChangeEvent(change, "", "")
		assert.True(t, ok)
		assert.Equal(t, types.EventCreate, evt.Type)
		assert.Equal(t, "t1", evt.TenantID)
	})

	t.Run("ConvertChangeEvent_UpdateFromDeleted", func(t *testing.T) {
		ds := store.(*documentStore)
		change := changeStreamEvent{
			OperationType:            "update",
			FullDocumentBeforeChange: &types.Document{Deleted: true},
			FullDocument:             &types.Document{TenantID: "t1", Collection: "c1"},
			DocumentKey: struct {
				ID string `bson:"_id"`
			}{ID: "t1:path"},
		}
		evt, ok := ds.convertChangeEvent(change, "", "")
		assert.True(t, ok)
		assert.Equal(t, types.EventCreate, evt.Type)
	})

	t.Run("Watch_CtxDoneDuringSend", func(t *testing.T) {
		ds := &documentStore{
			client:              env.Client,
			db:                  env.DB,
			dataCollection:      "docs",
			sysCollection:       "sys",
			softDeleteRetention: 0,
			openStream: func(ctx context.Context, coll *mongo.Collection, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
				return &stubChangeStream{}, nil
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		ch, err := ds.Watch(ctx, "tenant", "docs", nil, types.WatchOptions{})
		require.NoError(t, err)

		// Cancel immediately so the select favors ctx.Done over the blocked send.
		cancel()

		require.Eventually(t, func() bool {
			select {
			case _, ok := <-ch:
				return !ok
			default:
				return false
			}
		}, time.Second, 10*time.Millisecond)
	})
}

type stubChangeStream struct {
	emitted bool
}

func (s *stubChangeStream) Next(ctx context.Context) bool {
	if s.emitted {
		<-ctx.Done()
		return false
	}
	s.emitted = true
	return true
}

func (s *stubChangeStream) Decode(v any) error {
	ce := v.(*changeStreamEvent)
	ce.OperationType = "insert"
	ce.FullDocument = &types.Document{TenantID: "tenant", Collection: "docs"}
	ce.DocumentKey = struct {
		ID string `bson:"_id"`
	}{ID: "tenant:docs/id"}
	return nil
}

func (s *stubChangeStream) Err() error { return nil }

func (s *stubChangeStream) Close(context.Context) error { return nil }
