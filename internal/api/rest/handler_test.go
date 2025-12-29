package rest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestAuthorized_GetDocumentError covers the case where GetDocument returns an error other than ErrNotFound
func TestAuthorized_GetDocumentError(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := new(MockAuthService)
	mockAuthz := new(MockAuthzService)

	handler := NewHandler(mockService, mockAuth, mockAuthz)

	// We need to wrap a dummy handler with authorized
	target := handler.authorized(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "update")

	// Mock GetDocument to return an error
	mockService.On("GetDocument", mock.Anything, "default", "col/doc").Return(nil, errors.New("db error"))

	req, _ := http.NewRequest("PUT", "/api/v1/col/doc", nil)
	req.SetPathValue("path", "col/doc")

	// Add tenant to context so tenantOrError passes (though authorized doesn't call it directly,
	// it uses r.Context() for GetDocument which usually needs tenant, but here we mock it with "default")
	// Wait, authorized calls h.engine.GetDocument(r.Context(), "default", path) directly.
	// So we don't need tenant in context for this specific call in authorized.

	rr := httptest.NewRecorder()
	target(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Contains(t, rr.Body.String(), "Failed to check resource")
}

// MockAuthService_NoContext is a mock that calls next but doesn't set any context
type MockAuthService_NoContext struct {
	mock.Mock
}

func (m *MockAuthService_NoContext) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
func (m *MockAuthService_NoContext) MiddlewareOptional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
func (m *MockAuthService_NoContext) SignIn(ctx context.Context, req identity.LoginRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService_NoContext) SignUp(ctx context.Context, req identity.SignupRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService_NoContext) Refresh(ctx context.Context, req identity.RefreshRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService_NoContext) ListUsers(ctx context.Context, limit int, offset int) ([]*identity.User, error) {
	return nil, nil
}
func (m *MockAuthService_NoContext) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	return nil
}
func (m *MockAuthService_NoContext) Logout(ctx context.Context, refreshToken string) error {
	return nil
}
func (m *MockAuthService_NoContext) GenerateSystemToken(serviceName string) (string, error) {
	return "", nil
}
func (m *MockAuthService_NoContext) ValidateToken(tokenString string) (*identity.Claims, error) {
	return nil, nil
}

// TestTriggerProtected_NoRoles covers the case where ContextKeyRoles is missing
func TestTriggerProtected_NoRoles(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := new(MockAuthService_NoContext) // Use the no-context mock

	handler := NewHandler(mockService, mockAuth, nil)

	// Wrap a dummy handler
	target := handler.triggerProtected(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req, _ := http.NewRequest("POST", "/trigger/v1/write", nil)
	rr := httptest.NewRecorder()

	target(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "Access denied")
}

// TestAdminOnly_NoRoles covers the case where ContextKeyRoles is missing
func TestAdminOnly_NoRoles(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := new(MockAuthService_NoContext) // Use the no-context mock

	handler := NewHandler(mockService, mockAuth, nil)

	// Wrap a dummy handler
	target := handler.adminOnly(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/admin/users", nil)
	rr := httptest.NewRecorder()

	target(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "Access denied")
}

func TestClaimsToMap_NilDates(t *testing.T) {
	claims := &identity.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "sub",
		},
		// Dates are nil
	}
	m := claimsToMap(claims)
	assert.Nil(t, m["nbf"])
	assert.Nil(t, m["exp"])
	assert.Nil(t, m["iat"])
	assert.Equal(t, "sub", m["sub"])
}

func TestAuthorized_InvalidJSONBody(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := new(MockAuthService)
	mockAuthz := new(MockAuthzService)

	handler := NewHandler(mockService, mockAuth, mockAuthz)

	// We need to verify that Evaluate is called with nil Resource (or Resource with nil Data)
	// when body is invalid JSON.

	mockAuthz.On("Evaluate", mock.Anything, "col/doc", "create", mock.MatchedBy(func(req identity.AuthzRequest) bool {
		// Resource should be nil because Unmarshal failed
		return req.Resource == nil
	}), mock.Anything).Return(true, nil)

	// Wrap a dummy handler
	target := handler.authorized(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "create")

	req, _ := http.NewRequest("POST", "/api/v1/col/doc", bytes.NewBufferString("{invalid-json"))
	req.SetPathValue("path", "col/doc")

	rr := httptest.NewRecorder()
	target(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockAuthz.AssertExpectations(t)
}

func TestClaimsToMap_NilClaims(t *testing.T) {
	var claims *identity.Claims
	m := claimsToMap(claims)
	assert.Nil(t, m)
}
