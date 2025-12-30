package checkpoint

import (
	"context"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// mockDeleteErrorStore wraps a Store and returns error on Delete
type mockDeleteErrorStore struct {
	Store
}

func (m *mockDeleteErrorStore) Delete(ctx context.Context) error {
	return fmt.Errorf("mock delete error")
}

func TestMongoStore_Delete_Error(t *testing.T) {
	// Since MongoStore.Delete uses the collection, we can't easily mock the collection failure
	// without an interface for the collection or using a real connection that is broken.
	// However, we can use a context that is cancelled to trigger a timeout/cancel error from the driver.

	env := setupTestEnv(t)
	store := NewMongoStore(env.DB, "test-backend")

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := store.Delete(ctx)
	if err == nil {
		t.Error("Expected error from Delete with cancelled context, got nil")
	}
}

func TestMongoStore_Load_Error(t *testing.T) {
	env := setupTestEnv(t)
	store := NewMongoStore(env.DB, "test-backend")

	// 1. Test FindOne error (cancelled context)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Load(ctx)
	if err == nil {
		t.Error("Expected error from Load with cancelled context, got nil")
	}

	// 2. Test DecodeString error (invalid base64 in DB)
	ctx2 := context.Background()
	// Insert invalid document manually
	// Use a string that is definitely invalid base64 (invalid length)
	_, err = env.DB.Collection("_puller_checkpoints").InsertOne(ctx2, bson.M{
		"_id":   "test-backend",
		"token": "invalid-base64-length-!",
	})
	if err != nil {
		t.Fatalf("Failed to insert invalid checkpoint: %v", err)
	}

	_, err = store.Load(ctx2)
	if err == nil {
		t.Error("Expected error from Load with invalid base64 token, got nil")
	} else if err.Error() == "failed to load checkpoint: mongo: no documents in result" {
		t.Error("Expected decode error, got no documents error")
	}
}
