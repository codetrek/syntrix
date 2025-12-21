package mongo

import (
	"context"
	"testing"
	"time"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestMongoBackend_Watch(t *testing.T) {
	backend := setupTestBackend(t)
	defer backend.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start Watching
	stream, err := backend.Watch(ctx, "users", nil, storage.WatchOptions{})
	if err != nil {
		t.Skipf("Skipping Watch test (likely no replica set): %v", err)
		return
	}

	// Perform Operations
	go func() {
		time.Sleep(100 * time.Millisecond) // Wait for watch to establish

		// Create
		doc := storage.NewDocument("users/watcher", "users", map[string]interface{}{"msg": "hello"})
		backend.Create(context.Background(), doc)

		time.Sleep(50 * time.Millisecond)

		filters := storage.Filters{
			{Field: "version", Op: "==", Value: doc.Version},
		}
		// Update
		if err := backend.Update(context.Background(), "users/watcher", map[string]interface{}{"msg": "world"}, filters); err != nil {
			t.Logf("Update failed: %v", err)
		}

		time.Sleep(50 * time.Millisecond)

		// Delete
		if err := backend.Delete(context.Background(), "users/watcher", nil); err != nil {
			t.Logf("Delete failed: %v", err)
		}
	}()

	// Verify Events
	expectedEvents := []storage.EventType{storage.EventCreate, storage.EventUpdate, storage.EventDelete}
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
