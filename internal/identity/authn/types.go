package authn

import (
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/golang-jwt/jwt/v5"
)

// User is an alias to storage.User to avoid circular dependency
type User = storage.User

// Claims represents the JWT claims
type Claims struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles,omitempty"`
	Disabled bool     `json:"disabled"`
	jwt.RegisteredClaims
}

// TokenPair contains access and refresh tokens
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // Seconds
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RefreshRequest represents the refresh payload
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}
