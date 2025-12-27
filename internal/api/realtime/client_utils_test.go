package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/stretchr/testify/assert"
)

func TestTenantFromContextMust(t *testing.T) {
	// Case 1: Tenant present
	ctx := context.WithValue(context.Background(), identity.ContextKeyTenant, "t1")
	w := httptest.NewRecorder()
	tenant := tenantFromContextMust(ctx, w)
	assert.Equal(t, "t1", tenant)
	assert.Equal(t, http.StatusOK, w.Code)

	// Case 2: Tenant missing
	ctx2 := context.Background()
	w2 := httptest.NewRecorder()
	tenant2 := tenantFromContextMust(ctx2, w2)
	assert.Equal(t, "", tenant2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)
}

func TestHasSystemRoleFromClaims(t *testing.T) {
	// Case 1: Nil claims
	assert.False(t, hasSystemRoleFromClaims(nil))

	// Case 2: No system role
	claims1 := &identity.Claims{Roles: []string{"user", "admin"}}
	assert.False(t, hasSystemRoleFromClaims(claims1))

	// Case 3: System role present
	claims2 := &identity.Claims{Roles: []string{"user", "system"}}
	assert.True(t, hasSystemRoleFromClaims(claims2))

	// Case 4: System role case insensitive
	claims3 := &identity.Claims{Roles: []string{"SYSTEM"}}
	assert.True(t, hasSystemRoleFromClaims(claims3))
}

func TestTokenFromQuery(t *testing.T) {
	// Case 1: Nil request
	assert.Equal(t, "", tokenFromQuery(nil))

	// Case 2: access_token
	req1 := httptest.NewRequest("GET", "/?access_token=abc", nil)
	assert.Equal(t, "abc", tokenFromQuery(req1))

	// Case 3: token
	req2 := httptest.NewRequest("GET", "/?token=xyz", nil)
	assert.Equal(t, "xyz", tokenFromQuery(req2))

	// Case 4: Both (access_token takes precedence)
	req3 := httptest.NewRequest("GET", "/?access_token=abc&token=xyz", nil)
	assert.Equal(t, "abc", tokenFromQuery(req3))

	// Case 5: None
	req4 := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "", tokenFromQuery(req4))
}

func TestHasCredentials(t *testing.T) {
	// Case 1: Nil request
	assert.False(t, hasCredentials(nil))

	// Case 2: No auth header
	req1 := httptest.NewRequest("GET", "/", nil)
	assert.False(t, hasCredentials(req1))

	// Case 3: Auth header present
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Authorization", "Bearer token")
	assert.True(t, hasCredentials(req2))
}

func TestCheckAllowedOrigin(t *testing.T) {
	// Case 1: Empty origin, no credentials -> Allowed
	assert.NoError(t, checkAllowedOrigin("", "host", Config{}, false))

	// Case 2: Empty origin, credentials, dev allowed -> Allowed
	assert.NoError(t, checkAllowedOrigin("", "host", Config{AllowDevOrigin: true}, true))

	// Case 3: Empty origin, credentials, dev not allowed -> Error
	assert.Error(t, checkAllowedOrigin("", "host", Config{AllowDevOrigin: false}, true))

	// Case 4: Invalid URL -> Error
	assert.Error(t, checkAllowedOrigin(":", "host", Config{}, false))

	// Case 5: Same host -> Allowed
	assert.NoError(t, checkAllowedOrigin("http://host:8080", "host:8080", Config{}, false))

	// Case 6: Dev origin (localhost) allowed
	assert.NoError(t, checkAllowedOrigin("http://localhost:3000", "host", Config{AllowDevOrigin: true}, false))

	// Case 7: Dev origin (127.0.0.1) allowed
	assert.NoError(t, checkAllowedOrigin("http://127.0.0.1:3000", "host", Config{AllowDevOrigin: true}, false))

	// Case 8: Dev origin not allowed
	assert.Error(t, checkAllowedOrigin("http://localhost:3000", "host", Config{AllowDevOrigin: false}, false))

	// Case 9: Allowed origins list match
	assert.NoError(t, checkAllowedOrigin("http://example.com", "host", Config{AllowedOrigins: []string{"http://example.com"}}, false))

	// Case 10: Allowed origins list mismatch
	assert.Error(t, checkAllowedOrigin("http://other.com", "host", Config{AllowedOrigins: []string{"http://example.com"}}, false))
}
