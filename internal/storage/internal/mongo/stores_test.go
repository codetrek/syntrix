package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestNewDocumentStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(testMongoURI)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		t.Skipf("Skipping test: MongoDB not available: %v", err)
	}
	require.NoError(t, err)
	defer client.Disconnect(ctx)

	db := client.Database(testDBName)

	store := NewDocumentStore(client, db, "docs", "sys", 0)
	assert.NotNil(t, store)
}

func TestUserStore_Close(t *testing.T) {
	ctx := context.Background()
	// We can't easily create a userStore without a db, but we can use NewUserStore
	// We need a dummy db or real one.

	// Using nil db might panic if NewUserStore uses it immediately.
	// NewUserStore: return &userStore{ coll: db.Collection(...) }
	// So we need a real db object, but it doesn't need to be connected for this test if we just call Close.
	// But NewUserStore takes *mongo.Database.

	// Let's use the real connection from above logic.

	client, _ := mongo.Connect(ctx, options.Client().ApplyURI(testMongoURI))
	if client != nil {
		defer client.Disconnect(ctx)
		db := client.Database(testDBName)
		store := NewUserStore(db, "")
		err := store.Close(ctx)
		assert.NoError(t, err)
	}
}

func TestRevocationStore_Close(t *testing.T) {
	ctx := context.Background()
	client, _ := mongo.Connect(ctx, options.Client().ApplyURI(testMongoURI))
	if client != nil {
		defer client.Disconnect(ctx)
		db := client.Database(testDBName)
		store := NewRevocationStore(db, "")
		err := store.Close(ctx)
		assert.NoError(t, err)
	}
}
