package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"syntrix/internal/auth"
	"syntrix/internal/authz"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAdminAuthService for API tests
type MockAdminAuthService struct {
	mock.Mock
}

func (m *MockAdminAuthService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate auth middleware by checking a header
		role := r.Header.Get("X-Role")
		if role == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), "roles", []string{role})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *MockAdminAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	return m.Middleware(next)
}

func (m *MockAdminAuthService) SignIn(ctx context.Context, req auth.LoginRequest) (*auth.TokenPair, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *MockAdminAuthService) Refresh(ctx context.Context, req auth.RefreshRequest) (*auth.TokenPair, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *MockAdminAuthService) ListUsers(ctx context.Context, limit int, offset int) ([]*auth.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*auth.User), args.Error(1)
}

func (m *MockAdminAuthService) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	args := m.Called(ctx, id, roles, disabled)
	return args.Error(0)
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read(p []byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error               { return nil }

func (m *MockAdminAuthService) Logout(ctx context.Context, refreshToken string) error {
	args := m.Called(ctx, refreshToken)
	return args.Error(0)
}

// MockAdminAuthzService for API tests
type MockAdminAuthzService struct {
	mock.Mock
}

func (m *MockAdminAuthzService) GetRules() *authz.RuleSet {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*authz.RuleSet)
}

func (m *MockAdminAuthzService) UpdateRules(content []byte) error {
	args := m.Called(content)
	return args.Error(0)
}

func (m *MockAdminAuthzService) Evaluate(ctx context.Context, path string, action string, req authz.Request, existingRes *authz.Resource) (bool, error) {
	args := m.Called(ctx, path, action, req, existingRes)
	return args.Bool(0), args.Error(1)
}

func (m *MockAdminAuthzService) LoadRules(path string) error {
	return nil
}

func (m *MockAdminAuthService) ValidateToken(tokenString string) (*auth.Claims, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.Claims), args.Error(1)
}

func TestAdmin_ListUsers(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	// Mock ListUsers
	users := []*auth.User{
		{ID: "1", Username: "user1", Roles: []string{"user"}},
		{ID: "2", Username: "user2", Roles: []string{"admin"}},
	}
	mockAuth.On("ListUsers", mock.Anything, 50, 0).Return(users, nil)

	// Create Request
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req.Header.Set("X-Role", "admin") // Mock middleware check
	w := httptest.NewRecorder()

	// Serve
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	var respUsers []*auth.User
	err := json.NewDecoder(w.Body).Decode(&respUsers)
	assert.NoError(t, err)
	assert.Len(t, respUsers, 2)
	assert.Equal(t, "user1", respUsers[0].Username)
}

func TestAdmin_ListUsers_WithPaging(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	users := []*auth.User{}
	mockAuth.On("ListUsers", mock.Anything, 10, 5).Return(users, nil)

	req := httptest.NewRequest("GET", "/admin/users?limit=10&offset=5", nil)
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAdmin_UpdateUser(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	// Mock UpdateUser
	mockAuth.On("UpdateUser", mock.Anything, "123", []string{"admin"}, true).Return(nil)

	// Create Request
	body := map[string]interface{}{
		"roles":    []string{"admin"},
		"disabled": true,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", "/admin/users/123", bytes.NewReader(bodyBytes))
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	// Serve
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAdmin_UpdateUser_InvalidBody(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	req := httptest.NewRequest("PATCH", "/admin/users/123", bytes.NewBufferString("{invalid"))
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAuth.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdmin_UpdateUser_Error(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	body := map[string]interface{}{
		"roles":    []string{"user"},
		"disabled": false,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", "/admin/users/123", bytes.NewReader(bodyBytes))
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	mockAuth.On("UpdateUser", mock.Anything, "123", []string{"user"}, false).Return(errors.New("fail"))

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAdmin_PushRules(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	// Mock UpdateRules
	rulesContent := []byte("rules_version: '1'")
	mockAuthz.On("UpdateRules", rulesContent).Return(nil)

	// Create Request
	req := httptest.NewRequest("POST", "/admin/rules/push", bytes.NewReader(rulesContent))
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	// Serve
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockAuthz.AssertExpectations(t)
}

func TestAdmin_PushRules_Invalid(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	rulesContent := []byte("bad")
	mockAuthz.On("UpdateRules", rulesContent).Return(errors.New("bad rules"))

	req := httptest.NewRequest("POST", "/admin/rules/push", bytes.NewReader(rulesContent))
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAuthz.AssertExpectations(t)
}

func TestAdmin_PushRules_ReadError(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	req := httptest.NewRequest("POST", "/admin/rules/push", nil)
	req.Body = errReadCloser{err: errors.New("read error")}
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAuthz.AssertNotCalled(t, "UpdateRules", mock.Anything)
}

func TestAdmin_ListUsers_Error(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	mockAuth.On("ListUsers", mock.Anything, 50, 0).Return(nil, errors.New("boom"))

	req := httptest.NewRequest("GET", "/admin/users", nil)
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAdmin_GetRules(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	ruleSet := &authz.RuleSet{Version: "1"}
	mockAuthz.On("GetRules").Return(ruleSet)

	req := httptest.NewRequest("GET", "/admin/rules", nil)
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp authz.RuleSet
	assert.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "1", resp.Version)
	mockAuthz.AssertExpectations(t)
}

func TestAdmin_Health(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	req := httptest.NewRequest("GET", "/admin/health", nil)
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestAdmin_AccessDenied(t *testing.T) {
	mockAuth := new(MockAdminAuthService)
	mockAuthz := new(MockAdminAuthzService)
	server := NewServer(nil, mockAuth, mockAuthz)

	// Create Request with non-admin role
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req.Header.Set("X-Role", "user")
	w := httptest.NewRecorder()

	// Serve
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusForbidden, w.Code)
}
