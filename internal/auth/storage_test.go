package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	testMongoURI = "mongodb://localhost:27017"
	testDBName   = "syntrix_auth_test"
)

func setupTestStorage(t *testing.T) (*Storage, func()) {
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

	storage := NewStorage(db)
	err = storage.EnsureIndexes(ctx)
	require.NoError(t, err)

	return storage, func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}
}

func TestStorage_UserLifecycle(t *testing.T) {
	s, teardown := setupTestStorage(t)
	defer teardown()

	ctx := context.Background()

	user := &User{
		ID:           "user1",
		Username:     "TestUser",
		PasswordHash: "hash",
		CreatedAt:    time.Now().Truncate(time.Millisecond), // Truncate for mongo precision
		UpdatedAt:    time.Now().Truncate(time.Millisecond),
	}

	// 1. Create User
	err := s.CreateUser(ctx, user)
	require.NoError(t, err)

	// 2. Create Duplicate User (should fail)
	err = s.CreateUser(ctx, user)
	assert.ErrorIs(t, err, ErrUserExists)

	// 3. Get User By Username (case insensitive)
	fetched, err := s.GetUserByUsername(ctx, "testuser")
	require.NoError(t, err)
	assert.Equal(t, user.ID, fetched.ID)
	assert.Equal(t, "testuser", fetched.Username) // Should be stored lowercase

	// 4. Get User By ID
	fetchedID, err := s.GetUserByID(ctx, "user1")
	require.NoError(t, err)
	assert.Equal(t, "testuser", fetchedID.Username)

	// 5. Get Non-existent User
	_, err = s.GetUserByUsername(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = s.GetUserByID(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrUserNotFound)

	// 6. Update Login Stats
	now := time.Now().Truncate(time.Millisecond)
	lockout := now.Add(1 * time.Hour)
	err = s.UpdateUserLoginStats(ctx, user.ID, now, 5, lockout)
	require.NoError(t, err)

	fetchedUpdated, err := s.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, fetchedUpdated.LoginAttempts)
	assert.Equal(t, now.UnixMilli(), fetchedUpdated.LastLoginAt.UnixMilli())
	assert.Equal(t, lockout.UnixMilli(), fetchedUpdated.LockoutUntil.UnixMilli())
}

func TestStorage_Revocation(t *testing.T) {
	s, teardown := setupTestStorage(t)
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

func TestStorage_ListUsersAndUpdate(t *testing.T) {
	s, teardown := setupTestStorage(t)
	defer teardown()

	ctx := context.Background()

	baseTime := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	users := []*User{
		{ID: "u1", Username: "Alice", Roles: []string{"reader"}, CreatedAt: baseTime, UpdatedAt: baseTime},
		{ID: "u2", Username: "Bob", Roles: []string{"writer"}, CreatedAt: baseTime, UpdatedAt: baseTime},
		{ID: "u3", Username: "Carol", Roles: []string{"admin"}, CreatedAt: baseTime, UpdatedAt: baseTime},
	}

	for _, u := range users {
		require.NoError(t, s.CreateUser(ctx, u))
	}

	firstPage, err := s.ListUsers(ctx, 2, 0)
	require.NoError(t, err)
	assert.Len(t, firstPage, 2)

	secondPage, err := s.ListUsers(ctx, 2, 2)
	require.NoError(t, err)
	assert.Len(t, secondPage, 1)

	allUsers := append(firstPage, secondPage...)
	idSet := map[string]struct{}{}
	for _, u := range allUsers {
		idSet[u.ID] = struct{}{}
		assert.Equal(t, strings.ToLower(u.Username), u.Username)
	}
	assert.Len(t, idSet, 3)

	original, err := s.GetUserByID(ctx, "u2")
	require.NoError(t, err)

	update := &User{ID: "u2", Roles: []string{"admin", "editor"}, Disabled: true}
	require.NoError(t, s.UpdateUser(ctx, update))

	updated, err := s.GetUserByID(ctx, "u2")
	require.NoError(t, err)
	assert.Equal(t, []string{"admin", "editor"}, updated.Roles)
	assert.True(t, updated.Disabled)
	assert.True(t, updated.UpdatedAt.After(original.UpdatedAt))
}
