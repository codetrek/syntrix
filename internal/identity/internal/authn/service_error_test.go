package authn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestValidateToken_ErrorPaths(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
		AccessTokenTTL: time.Minute,
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)
	authService := svc.(*AuthService)

	t.Run("Invalid Token Format", func(t *testing.T) {
		_, err := authService.ValidateToken("invalid-token-string")
		assert.Error(t, err)
	})

	t.Run("Token Signed with Wrong Method", func(t *testing.T) {
		// Create a token signed with HMAC instead of RSA
		token := jwt.New(jwt.SigningMethodHS256)
		tokenString, err := token.SignedString([]byte("secret"))
		require.NoError(t, err)

		_, err = authService.ValidateToken(tokenString)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected signing method")
	})
}

func TestSignIn_ErrorPaths(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)

	t.Run("VerifyPassword Error", func(t *testing.T) {
		// Setup user with invalid hash format
		user := &User{
			ID:           "u1",
			Username:     "user",
			PasswordHash: "invalid-hash",
			PasswordAlgo: "bcrypt",
			TenantID:     "default",
		}
		mockStorage.On("GetUserByUsername", mock.Anything, "default", "user").Return(user, nil).Once()

		_, err := svc.SignIn(context.Background(), LoginRequest{
			TenantID: "default",
			Username: "user",
			Password: "password",
		})
		assert.Error(t, err)
	})
}

func TestRefresh_ErrorPaths_Extended(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)

	// Helper to get a valid refresh token
	getRefreshToken := func() string {
		mockStorage.On("GetUserByUsername", mock.Anything, "default", "user").Return(nil, ErrUserNotFound).Once()
		mockStorage.On("CreateUser", mock.Anything, "default", mock.Anything).Return(nil).Once()
		pair, err := svc.SignUp(context.Background(), SignupRequest{
			TenantID: "default", Username: "user", Password: "password123456",
		})
		require.NoError(t, err)
		return pair.RefreshToken
	}

	t.Run("RevokeToken Error", func(t *testing.T) {
		token := getRefreshToken()

		// Mock successful checks but failed revocation
		mockStorage.On("IsRevoked", mock.Anything, "default", mock.Anything, mock.Anything).Return(false, nil).Once()
		mockStorage.On("GetUserByID", mock.Anything, "default", mock.Anything).Return(&User{ID: "u1", TenantID: "default"}, nil).Once()
		mockStorage.On("RevokeToken", mock.Anything, "default", mock.Anything, mock.Anything).Return(errors.New("revoke failed")).Once()

		_, err := svc.Refresh(context.Background(), RefreshRequest{RefreshToken: token})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "revoke failed")
	})
}
