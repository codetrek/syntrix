package authn

import (
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateToken(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
		AuthCodeTTL:     2 * time.Minute,
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)
	authService := svc.(*AuthService)

	// Generate a token
	token, err := authService.GenerateSystemToken("test-service")
	require.NoError(t, err)

	// Validate valid token
	claims, err := authService.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, "system:test-service", claims.Subject)

	// Validate invalid token
	_, err = authService.ValidateToken("invalid-token")
	assert.Error(t, err)
}
