package authn

import (
	identtypes "github.com/codetrek/syntrix/internal/identity/types"
	"github.com/codetrek/syntrix/internal/storage"
)

// Directly reuse public identity types to avoid duplicate definitions and adapters.
type (
	ContextKey     = identtypes.ContextKey
	Claims         = identtypes.Claims
	TokenPair      = identtypes.TokenPair
	LoginRequest   = identtypes.LoginRequest
	RefreshRequest = identtypes.RefreshRequest
)

const (
	ContextKeyUser     = identtypes.ContextKeyUser
	ContextKeyUserID   = identtypes.ContextKeyUserID
	ContextKeyUsername = identtypes.ContextKeyUsername
	ContextKeyRoles    = identtypes.ContextKeyRoles
	ContextKeyClaims   = identtypes.ContextKeyClaims
)

// Keep storage alias for store interfaces.
type User = storage.User
