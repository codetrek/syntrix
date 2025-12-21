package api

import (
	"context"
	"net/http"
	"syntrix/internal/auth"
	"syntrix/internal/authz"
)

type AuthService interface {
	Middleware(next http.Handler) http.Handler
	MiddlewareOptional(next http.Handler) http.Handler
	SignIn(ctx context.Context, req auth.LoginRequest) (*auth.TokenPair, error)
	Refresh(ctx context.Context, req auth.RefreshRequest) (*auth.TokenPair, error)
	ListUsers(ctx context.Context, limit int, offset int) ([]*auth.User, error)
	UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error
	Logout(ctx context.Context, refreshToken string) error
}

type AuthzService interface {
	Evaluate(ctx context.Context, path string, action string, req authz.Request, existingRes *authz.Resource) (bool, error)
	GetRules() *authz.RuleSet
	UpdateRules(content []byte) error
}
