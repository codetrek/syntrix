package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func setupTestRevocationStore(t *testing.T) (types.TokenRevocationStore, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(testMongoURI))
	require.NoError(t, err)

	// Ping to ensure connection
	err = client.Ping(ctx, nil)
	if err != nil {
		t.Skip("MongoDB not available, skipping integration tests")
	}

	db := client.Database(testDBName)

	// Clean up
	err = db.Drop(ctx)
	require.NoError(t, err)

	store := NewRevocationStore(db, "")
	err = store.EnsureIndexes(ctx)
	require.NoError(t, err)

	return store, func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}
}

func TestRevocationStore_Revocation(t *testing.T) {
	s, teardown := setupTestRevocationStore(t)
	defer teardown()

	ctx := context.Background()
	jti := "token-123"
	expiresAt := time.Now().Add(1 * time.Hour)

	// 1. Check not revoked initially
	revoked, err := s.IsRevoked(ctx, jti, 0)
	require.NoError(t, err)
	assert.False(t, revoked)

	// 2. Revoke Token
	err = s.RevokeToken(ctx, jti, expiresAt)
	require.NoError(t, err)

	// 3. Check immediate revocation (grace period 0) -> Should be revoked
	revoked, err = s.IsRevoked(ctx, jti, 0)
	require.NoError(t, err)
	assert.True(t, revoked)

	// 4. Check with grace period -> Should NOT be revoked yet (within grace period)
	revoked, err = s.IsRevoked(ctx, jti, 1*time.Minute)
	require.NoError(t, err)
	assert.False(t, revoked)

	// 5. Revoke Duplicate (should not error)
	err = s.RevokeToken(ctx, jti, expiresAt)
	require.NoError(t, err)

	// 6. Revoke Immediate (Force Logout)
	jti2 := "token-456"
	err = s.RevokeTokenImmediate(ctx, jti2, expiresAt)
	require.NoError(t, err)

	// 7. Check Immediate with grace period -> Should be revoked (bypassed grace period)
	revoked, err = s.IsRevoked(ctx, jti2, 1*time.Minute)
	require.NoError(t, err)
	assert.True(t, revoked)
}
