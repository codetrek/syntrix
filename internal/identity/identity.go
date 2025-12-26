package identity

import (
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/identity/internal/authn"
	"github.com/codetrek/syntrix/internal/identity/internal/authz"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/internal/storage"
)

// Errors from authn
var (
	ErrInvalidCredentials = authn.ErrInvalidCredentials
	ErrAccountDisabled    = authn.ErrAccountDisabled
	ErrAccountLocked      = authn.ErrAccountLocked
	ErrInvalidToken       = authn.ErrInvalidToken
	ErrUserNotFound       = authn.ErrUserNotFound
	ErrUserExists         = authn.ErrUserExists
)

// AuthN and AuthZ are re-exports of the internal implementations using public types.
type (
	AuthN = authn.Service
	AuthZ = authz.Engine
)

// NewAuthN creates a new authentication service.
func NewAuthN(cfg config.AuthNConfig, users storage.UserStore, revocations storage.TokenRevocationStore) (AuthN, error) {
	return authn.NewAuthService(cfg, users, revocations)
}

// NewAuthZ creates a new authorization engine.
func NewAuthZ(cfg config.AuthZConfig, qs query.Service) (AuthZ, error) {
	return authz.NewEngine(cfg, qs)
}
