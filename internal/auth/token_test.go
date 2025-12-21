package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenService_GenerateAndValidate(t *testing.T) {
	key, _ := GeneratePrivateKey()
	ts, err := NewTokenService(key, 15*time.Minute, 1*time.Hour, 2*time.Minute)
	require.NoError(t, err)

	user := &User{
		ID:       "user-123",
		Username: "testuser",
		Roles:    []string{"admin"},
		Disabled: false,
	}

	// Generate
	pair, err := ts.GenerateTokenPair(user)
	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, 900, pair.ExpiresIn) // 15 minutes in seconds

	// Validate Access Token
	claims, err := ts.ValidateToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, user.ID, claims.Subject)
	assert.Equal(t, user.Username, claims.Username)
	assert.Equal(t, user.Roles, claims.Roles)

	// Validate Refresh Token
	refreshClaims, err := ts.ValidateToken(pair.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, user.ID, refreshClaims.Subject)
	assert.Equal(t, user.Username, refreshClaims.Username)
}

func TestTokenService_ExpiredToken(t *testing.T) {
	// Create service with very short TTL
	key, _ := GeneratePrivateKey()
	ts, err := NewTokenService(key, 1*time.Millisecond, 1*time.Millisecond, 0)
	require.NoError(t, err)

	user := &User{ID: "user-1", Username: "user"}
	pair, err := ts.GenerateTokenPair(user)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(2 * time.Millisecond)

	// Validate
	_, err = ts.ValidateToken(pair.AccessToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token is expired")
}

func TestTokenService_InvalidSignature(t *testing.T) {
	key1, _ := GeneratePrivateKey()
	ts1, _ := NewTokenService(key1, 1*time.Hour, 1*time.Hour, 0)
	key2, _ := GeneratePrivateKey()
	ts2, _ := NewTokenService(key2, 1*time.Hour, 1*time.Hour, 0) // Different keys

	user := &User{ID: "user-1", Username: "user"}
	pair, _ := ts1.GenerateTokenPair(user)

	// Try to validate with ts2 (different public key)
	_, err := ts2.ValidateToken(pair.AccessToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "verification error")
}
