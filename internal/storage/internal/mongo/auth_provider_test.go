package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestAuthProvider(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider, err := NewAuthProvider(ctx, testMongoURI, env.DBName)
	if err != nil {
		t.Fatalf("MongoDB not available: %v", err)
	}
	require.NoError(t, err)
	defer provider.Close(ctx)

	assert.NotNil(t, provider.Users())
	assert.NotNil(t, provider.Revocations())
}

func TestAuthProvider_Error(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Invalid URI
	_, err := NewAuthProvider(ctx, "mongodb://invalid-host:27017", "db")
	assert.Error(t, err)
}

func TestAuthProvider_EnsureIndexesError(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect manually to create conflicting index
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(testMongoURI))
	if err != nil {
		t.Fatalf("MongoDB not available: %v", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database(env.DBName)
	coll := db.Collection("auth_users")

	// Create a conflicting index (same keys, but not unique)
	// MongoDB throws IndexOptionsConflict if we try to create an index with same keys but different options
	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "username", Value: 1}},
		Options: options.Index().SetUnique(false),
	})
	require.NoError(t, err)

	// Now NewAuthProvider should fail when trying to create unique index on same keys
	_, err = NewAuthProvider(ctx, testMongoURI, env.DBName)
	assert.Error(t, err)
}
