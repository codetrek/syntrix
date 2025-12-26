package identity

import "github.com/codetrek/syntrix/internal/identity/types"

type (
	ContextKey     = types.ContextKey
	Claims         = types.Claims
	TokenPair      = types.TokenPair
	LoginRequest   = types.LoginRequest
	RefreshRequest = types.RefreshRequest
	RuleSet        = types.RuleSet
	MatchBlock     = types.MatchBlock
	AuthzRequest   = types.AuthzRequest
	Auth           = types.Authenticated
	Resource       = types.Resource
	User           = types.User
)

const (
	ContextKeyUser     = types.ContextKeyUser
	ContextKeyUserID   = types.ContextKeyUserID
	ContextKeyUsername = types.ContextKeyUsername
	ContextKeyRoles    = types.ContextKeyRoles
	ContextKeyClaims   = types.ContextKeyClaims
)
