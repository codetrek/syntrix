package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserStore_CreateUser_EmptyID(t *testing.T) {
	s, teardown := setupTestUserStore(t)
	defer teardown()

	ctx := context.Background()
	tenant := "default"

	user := &types.User{
		ID:           "", // Empty ID to trigger generation logic
		Username:     "AutoIDUser",
		PasswordHash: "hash",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err := s.CreateUser(ctx, tenant, user)
	require.NoError(t, err)

	// Verify ID was generated
	assert.NotEmpty(t, user.ID)
	assert.Contains(t, user.ID, tenant+":")

	// Verify we can fetch it
	fetched, err := s.GetUserByUsername(ctx, tenant, "autoiduser")
	require.NoError(t, err)
	assert.Equal(t, user.ID, fetched.ID)
}
