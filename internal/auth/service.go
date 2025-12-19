package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountDisabled    = errors.New("account disabled")
	ErrAccountLocked      = errors.New("account locked")
	ErrInvalidToken       = errors.New("invalid token")
)

type StorageInterface interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error
	RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error
	RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error
	IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error)
}

type AuthService struct {
	storage      StorageInterface
	tokenService *TokenService
}

func NewAuthService(storage StorageInterface, tokenService *TokenService) *AuthService {
	return &AuthService{
		storage:      storage,
		tokenService: tokenService,
	}
}

func (s *AuthService) SignIn(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	user, err := s.storage.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Auto-register
			return s.register(ctx, req)
		}
		return nil, err
	}

	// Check lockout
	if user.LockoutUntil.After(time.Now()) {
		return nil, ErrAccountLocked
	}

	// Verify password
	valid, err := VerifyPassword(req.Password, user.PasswordHash, user.PasswordAlgo)
	if err != nil {
		return nil, err
	}

	if !valid {
		// Handle failed attempt
		attempts := user.LoginAttempts + 1
		lockoutUntil := user.LockoutUntil
		if attempts >= 10 { // Lockout threshold
			lockoutUntil = time.Now().Add(5 * time.Minute)
		}
		_ = s.storage.UpdateUserLoginStats(ctx, user.ID, user.LastLoginAt, attempts, lockoutUntil)
		return nil, ErrInvalidCredentials
	}

	// Check disabled
	if user.Disabled {
		return nil, ErrAccountDisabled
	}

	// Success - reset stats
	_ = s.storage.UpdateUserLoginStats(ctx, user.ID, time.Now(), 0, time.Time{})

	return s.tokenService.GenerateTokenPair(user)
}

func (s *AuthService) register(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	// Validate password strength (simple check for now)
	if len(req.Password) < 8 {
		return nil, errors.New("password too short")
	}

	hash, algo, err := HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	user := &User{
		ID:           uuid.New().String(),
		Username:     req.Username,
		PasswordHash: hash,
		PasswordAlgo: algo,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Disabled:     false,
		Roles:        []string{}, // Default roles
	}

	if err := s.storage.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	return s.tokenService.GenerateTokenPair(user)
}

func (s *AuthService) Refresh(ctx context.Context, req RefreshRequest) (*TokenPair, error) {
	claims, err := s.tokenService.ValidateToken(req.RefreshToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// Check revocation with overlap
	revoked, err := s.storage.IsRevoked(ctx, claims.ID, s.tokenService.refreshOverlap)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, ErrInvalidToken // Revoked
	}

	// Get user to ensure still exists/active
	user, err := s.storage.GetUserByID(ctx, claims.Subject)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if user.Disabled {
		return nil, ErrAccountDisabled
	}

	// Revoke old token (soft revoke for overlap)
	// We use the Expiration time from claims to clean up later
	if err := s.storage.RevokeToken(ctx, claims.ID, claims.ExpiresAt.Time); err != nil {
		return nil, err
	}

	// Issue new pair
	return s.tokenService.GenerateTokenPair(user)
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	claims, err := s.tokenService.ValidateToken(refreshToken)
	if err != nil {
		return ErrInvalidToken
	}

	return s.storage.RevokeTokenImmediate(ctx, claims.ID, claims.ExpiresAt.Time)
}

func (s *AuthService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization header format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		claims, err := s.tokenService.ValidateToken(tokenString)
		if err != nil {
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Add user info to context
		ctx := context.WithValue(r.Context(), "userID", claims.Subject)
		ctx = context.WithValue(ctx, "username", claims.Username)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
