package realtime

import (
	"context"
	"net/http"

	"github.com/codetrek/syntrix/internal/identity"
)

// mockAuthService is a minimal AuthN stub for realtime tests.
type mockAuthService struct{}

func (m *mockAuthService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withTenantAndRole(r.Context(), "default", []string{"user"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *mockAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withTenantAndRole(r.Context(), "default", []string{"user"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *mockAuthService) SignIn(ctx context.Context, req identity.LoginRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *mockAuthService) SignUp(ctx context.Context, req identity.SignupRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *mockAuthService) Refresh(ctx context.Context, req identity.RefreshRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *mockAuthService) ListUsers(ctx context.Context, limit int, offset int) ([]*identity.User, error) {
	return nil, nil
}

func (m *mockAuthService) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	return nil
}

func (m *mockAuthService) Logout(ctx context.Context, refreshToken string) error { return nil }

func (m *mockAuthService) GenerateSystemToken(serviceName string) (string, error) { return "", nil }

func (m *mockAuthService) ValidateToken(tokenString string) (*identity.Claims, error) {
	if tokenString != "good" {
		return nil, identity.ErrInvalidToken
	}
	return &identity.Claims{TenantID: "default", Roles: []string{"user"}}, nil
}

func withTenantAndRole(ctx context.Context, tenant string, roles []string) context.Context {
	ctx = context.WithValue(ctx, identity.ContextKeyTenant, tenant)
	ctx = context.WithValue(ctx, identity.ContextKeyRoles, roles)
	return ctx
}
