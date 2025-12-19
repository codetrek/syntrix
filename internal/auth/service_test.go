package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockStorage struct {
	mock.Mock
}

func (m *MockStorage) CreateUser(ctx context.Context, user *User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockStorage) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStorage) GetUserByID(ctx context.Context, id string) (*User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStorage) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	args := m.Called(ctx, id, lastLogin, attempts, lockoutUntil)
	return args.Error(0)
}

func (m *MockStorage) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	args := m.Called(ctx, jti, expiresAt)
	return args.Error(0)
}

func (m *MockStorage) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	args := m.Called(ctx, jti, expiresAt)
	return args.Error(0)
}

func (m *MockStorage) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	args := m.Called(ctx, jti, gracePeriod)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorage) EnsureIndexes(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestSignIn_AutoRegister(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	ctx := context.Background()
	req := LoginRequest{
		Username: "newuser",
		Password: "password123",
	}

	// Expect GetUserByUsername to return ErrUserNotFound
	mockStorage.On("GetUserByUsername", ctx, "newuser").Return(nil, ErrUserNotFound)

	// Expect CreateUser to be called
	mockStorage.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	tokenPair, err := authService.SignIn(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, tokenPair)
	assert.NotEmpty(t, tokenPair.AccessToken)
	assert.NotEmpty(t, tokenPair.RefreshToken)

	mockStorage.AssertExpectations(t)
}

func TestSignIn_Success(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	ctx := context.Background()
	req := LoginRequest{
		Username: "existinguser",
		Password: "password123",
	}

	hash, algo, _ := HashPassword("password123")
	user := &User{
		ID:           "user-id",
		Username:     "existinguser",
		PasswordHash: hash,
		PasswordAlgo: algo,
	}

	mockStorage.On("GetUserByUsername", ctx, "existinguser").Return(user, nil)
	mockStorage.On("UpdateUserLoginStats", ctx, "user-id", mock.Anything, 0, mock.Anything).Return(nil)

	tokenPair, err := authService.SignIn(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, tokenPair)

	mockStorage.AssertExpectations(t)
}

func TestSignIn_WrongPassword(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	ctx := context.Background()
	req := LoginRequest{
		Username: "existinguser",
		Password: "wrongpassword",
	}

	hash, algo, _ := HashPassword("password123")
	user := &User{
		ID:           "user-id",
		Username:     "existinguser",
		PasswordHash: hash,
		PasswordAlgo: algo,
	}

	mockStorage.On("GetUserByUsername", ctx, "existinguser").Return(user, nil)
	mockStorage.On("UpdateUserLoginStats", ctx, "user-id", mock.Anything, 1, mock.Anything).Return(nil)

	tokenPair, err := authService.SignIn(ctx, req)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidCredentials, err)
	assert.Nil(t, tokenPair)

	mockStorage.AssertExpectations(t)
}

func TestRefresh_Success(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	ctx := context.Background()

	// Create a valid refresh token
	user := &User{ID: "user-id", Username: "user"}
	pair, _ := tokenService.GenerateTokenPair(user)

	mockStorage.On("IsRevoked", ctx, mock.Anything, 2*time.Minute).Return(false, nil)
	mockStorage.On("GetUserByID", ctx, "user-id").Return(user, nil)
	mockStorage.On("RevokeToken", ctx, mock.Anything, mock.Anything).Return(nil)

	newPair, err := authService.Refresh(ctx, RefreshRequest{RefreshToken: pair.RefreshToken})
	assert.NoError(t, err)
	assert.NotNil(t, newPair)
	assert.NotEqual(t, pair.AccessToken, newPair.AccessToken)

	mockStorage.AssertExpectations(t)
}

func TestRefresh_Revoked(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	ctx := context.Background()

	// Create a valid refresh token
	user := &User{ID: "user-id", Username: "user"}
	pair, _ := tokenService.GenerateTokenPair(user)

	mockStorage.On("IsRevoked", ctx, mock.Anything, 2*time.Minute).Return(true, nil)

	newPair, err := authService.Refresh(ctx, RefreshRequest{RefreshToken: pair.RefreshToken})
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidToken, err)
	assert.Nil(t, newPair)

	mockStorage.AssertExpectations(t)
}

func TestMiddleware(t *testing.T) {
	mockStorage := new(MockStorage)
	tokenService, _ := NewTokenService(15*time.Minute, 7*24*time.Hour, 2*time.Minute)
	authService := NewAuthService(mockStorage, tokenService)

	// Create a valid token
	user := &User{ID: "user-id", Username: "user"}
	pair, _ := tokenService.GenerateTokenPair(user)

	// Create a handler that checks context
	handler := authService.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Context().Value("userID")
		username := r.Context().Value("username")
		assert.Equal(t, "user-id", userID)
		assert.Equal(t, "user", username)
		w.WriteHeader(http.StatusOK)
	}))

	// Test valid token
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Test missing header
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test invalid token
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
