package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
)

func TestMongoBackend_Watch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tenant := "default"

	// Start Watching
	stream, err := backend.Watch(ctx, tenant, "users", nil, types.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch failed (likely no replica set): %v", err)
		return
	}

	// Perform Operations
	go func() {
		time.Sleep(10 * time.Millisecond) // Wait for watch to establish

		// Create
		doc := types.NewDocument(tenant, "users/watcher", "users", map[string]interface{}{"msg": "hello"})
		backend.Create(context.Background(), tenant, doc)

		filters := model.Filters{
			{Field: "version", Op: "==", Value: doc.Version},
		}
		// Update
		if err := backend.Update(context.Background(), tenant, "users/watcher", map[string]interface{}{"msg": "world"}, filters); err != nil {
			t.Logf("Update failed: %v", err)
		}

		// Delete
		if err := backend.Delete(context.Background(), tenant, "users/watcher", nil); err != nil {
			t.Logf("Delete failed: %v", err)
		}
	}()

	// Verify Events
	expectedEvents := []types.EventType{types.EventCreate, types.EventUpdate, types.EventDelete}
	for i, expectedType := range expectedEvents {
		select {
		case evt := <-stream:
			t.Logf("Received event: Type=%s ID=%s", evt.Type, evt.Id)
			assert.Equal(t, expectedType, evt.Type)
			if i == 0 {
				assert.Equal(t, "hello", evt.Document.Data["msg"])
			} else if i == 1 {
				assert.Equal(t, "world", evt.Document.Data["msg"])
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for event %s", expectedType)
		}
	}
}

func TestMongoBackend_Watch_Recreate(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tenant := "default"

	stream, err := backend.Watch(ctx, tenant, "users", nil, types.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch recreate test failed (likely no replica set): %v", err)
		return
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		doc := types.NewDocument(tenant, "users/recreate", "users", map[string]interface{}{"msg": "v1"})
		_ = backend.Create(context.Background(), tenant, doc)

		_ = backend.Delete(context.Background(), tenant, "users/recreate", nil)

		_ = backend.Create(context.Background(), tenant, types.NewDocument(tenant, "users/recreate", "users", map[string]interface{}{"msg": "v2"}))
	}()

	expected := []types.EventType{types.EventCreate, types.EventDelete, types.EventCreate}
	msgs := []string{"v1", "v2"}
	createIdx := 0
	for _, evtType := range expected {
		select {
		case evt := <-stream:
			assert.Equal(t, evtType, evt.Type)
			if evtType == types.EventCreate {
				if assert.NotNil(t, evt.Document) && createIdx < len(msgs) {
					assert.Equal(t, msgs[createIdx], evt.Document.Data["msg"])
				}
				createIdx++
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for event %s", evtType)
		}
	}
}

func TestMongoBackend_Watch_Recreate_WithBefore(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tenant := "default"

	stream, err := backend.Watch(ctx, tenant, "users", nil, types.WatchOptions{IncludeBefore: true})
	if err != nil {
		t.Fatalf("Watch recreate (before) test failed (likely no replica set): %v", err)
		return
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		doc := types.NewDocument(tenant, "users/recreate-before", "users", map[string]interface{}{"msg": "v1"})
		_ = backend.Create(context.Background(), tenant, doc)

		_ = backend.Delete(context.Background(), tenant, "users/recreate-before", nil)

		_ = backend.Create(context.Background(), tenant, types.NewDocument(tenant, "users/recreate-before", "users", map[string]interface{}{"msg": "v2"}))
	}()

	expected := []types.EventType{types.EventCreate, types.EventDelete, types.EventCreate}
	msgs := []string{"v1", "v2"}
	createIdx := 0
	for _, evtType := range expected {
		select {
		case evt := <-stream:
			assert.Equal(t, evtType, evt.Type)
			if evtType == types.EventCreate {
				if assert.NotNil(t, evt.Document) && createIdx < len(msgs) {
					assert.Equal(t, msgs[createIdx], evt.Document.Data["msg"])
				}
				assert.Nil(t, evt.Before)
				createIdx++
			}
			if evtType == types.EventDelete {
				if evt.Before != nil {
					assert.Equal(t, "v1", evt.Before.Data["msg"])
				}
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for event %s", evtType)
		}
	}
}
