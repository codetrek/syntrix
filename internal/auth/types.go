package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// User represents a user in the system
type User struct {
	ID            string                 `json:"id" bson:"_id"`
	Username      string                 `json:"username" bson:"username"`
	PasswordHash  string                 `json:"password_hash" bson:"password_hash"`
	PasswordAlgo  string                 `json:"password_algo" bson:"password_algo"` // "argon2id" or "bcrypt"
	CreatedAt     time.Time              `json:"createdAt" bson:"createdAt"`
	UpdatedAt     time.Time              `json:"updatedAt" bson:"updatedAt"`
	Disabled      bool                   `json:"disabled" bson:"disabled"`
	Roles         []string               `json:"roles" bson:"roles"`
	Profile       map[string]interface{} `json:"profile" bson:"profile"`
	LastLoginAt   time.Time              `json:"last_login_at" bson:"last_login_at"`
	LoginAttempts int                    `json:"login_attempts" bson:"login_attempts"`
	LockoutUntil  time.Time              `json:"lockout_until" bson:"lockout_until"`
}

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
