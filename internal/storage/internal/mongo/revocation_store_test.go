package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRevocationStore(t *testing.T) (types.TokenRevocationStore, func()) {
	env := setupTestEnv(t)

	store := NewRevocationStore(env.DB, "")
	err := store.EnsureIndexes(context.Background())
	require.NoError(t, err)

	return store, func() {
		// Cleanup handled by setupTestEnv
	}
}

func TestRevocationStore_Revocation(t *testing.T) {
	s, teardown := setupTestRevocationStore(t)
	defer teardown()

	ctx := context.Background()
	tenant := "default"
	jti := "token-123"
	expiresAt := time.Now().Add(1 * time.Hour)

	// 1. Check not revoked initially
	revoked, err := s.IsRevoked(ctx, tenant, jti, 0)
	require.NoError(t, err)
	assert.False(t, revoked)

	// 2. Revoke Token
	err = s.RevokeToken(ctx, tenant, jti, expiresAt)
	require.NoError(t, err)

	// 3. Check immediate revocation (grace period 0) -> Should be revoked
	revoked, err = s.IsRevoked(ctx, tenant, jti, 0)
	require.NoError(t, err)
	assert.True(t, revoked)

	// 4. Check with grace period -> Should NOT be revoked yet (within grace period)
	revoked, err = s.IsRevoked(ctx, tenant, jti, 1*time.Minute)
	require.NoError(t, err)
	assert.False(t, revoked)

	// 5. Revoke Duplicate (should not error)
	err = s.RevokeToken(ctx, tenant, jti, expiresAt)
	require.NoError(t, err)

	// 6. Revoke Immediate (Force Logout)
	jti2 := "token-456"
	err = s.RevokeTokenImmediate(ctx, tenant, jti2, expiresAt)
	require.NoError(t, err)

	// 7. Check Immediate with grace period -> Should be revoked (bypassed grace period)
	revoked, err = s.IsRevoked(ctx, tenant, jti2, 1*time.Minute)
	require.NoError(t, err)
	assert.True(t, revoked)
}

func TestRevocationStore_RevokeTokenImmediate_Duplicate(t *testing.T) {
	s, teardown := setupTestRevocationStore(t)
	defer teardown()

	ctx := context.Background()
	tenant := "default"
	jti := "token-dup"
	expiresAt := time.Now().Add(1 * time.Hour)

	// First revocation
	err := s.RevokeTokenImmediate(ctx, tenant, jti, expiresAt)
	require.NoError(t, err)

	// Second revocation (should be idempotent)
	err = s.RevokeTokenImmediate(ctx, tenant, jti, expiresAt)
	require.NoError(t, err)
}
