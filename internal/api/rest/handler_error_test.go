package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestAuthHandlerErrors covers error paths in handler_auth.go
func TestAuthHandlerErrors(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := createTestServer(nil, mockAuth, nil)

	t.Run("SignUp_TenantRequired", func(t *testing.T) {
		reqBody := identity.SignupRequest{Username: "user", Password: "password"} // Missing TenantID
		mockAuth.On("SignUp", mock.Anything, reqBody).Return(nil, identity.ErrTenantRequired).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/signup", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleSignUp(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), identity.ErrTenantRequired.Error())
	})

	t.Run("Login_AccountLocked", func(t *testing.T) {
		reqBody := identity.LoginRequest{TenantID: "default", Username: "locked", Password: "password"}
		mockAuth.On("SignIn", mock.Anything, reqBody).Return(nil, identity.ErrAccountLocked).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), identity.ErrAccountLocked.Error())
	})

	t.Run("Login_TenantRequired", func(t *testing.T) {
		reqBody := identity.LoginRequest{Username: "user", Password: "password"}
		mockAuth.On("SignIn", mock.Anything, reqBody).Return(nil, identity.ErrTenantRequired).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Login_InternalError", func(t *testing.T) {
		reqBody := identity.LoginRequest{TenantID: "default", Username: "user", Password: "password"}
		mockAuth.On("SignIn", mock.Anything, reqBody).Return(nil, errors.New("db error")).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Refresh_InvalidBody", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/auth/v1/refresh", bytes.NewReader([]byte("invalid")))
		w := httptest.NewRecorder()

		server.handleRefresh(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Refresh_Error", func(t *testing.T) {
		reqBody := identity.RefreshRequest{RefreshToken: "bad_token"}
		mockAuth.On("Refresh", mock.Anything, reqBody).Return(nil, errors.New("invalid token")).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/refresh", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleRefresh(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Logout_MissingToken", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/auth/v1/logout", bytes.NewReader([]byte("{}")))
		w := httptest.NewRecorder()

		server.handleLogout(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Missing refresh token")
	})

	t.Run("Logout_Error", func(t *testing.T) {
		reqBody := identity.RefreshRequest{RefreshToken: "token"}
		mockAuth.On("Logout", mock.Anything, "token").Return(errors.New("db error")).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/auth/v1/logout", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogout(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// TestAdminHandlerErrors covers error paths in handler_admin.go
func TestAdminHandlerErrors(t *testing.T) {
	mockAuth := &AdminTestAuthService{MockAuthService: new(MockAuthService)}
	mockAuthz := new(MockAuthzService)
	server := createTestServer(nil, mockAuth, mockAuthz)

	t.Run("ListUsers_Error", func(t *testing.T) {
		mockAuth.On("ListUsers", mock.Anything, 50, 0).Return(nil, errors.New("db error")).Once()

		req := httptest.NewRequest("GET", "/admin/users", nil)
		req.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("UpdateUser_InvalidBody", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/admin/users/123", bytes.NewReader([]byte("invalid")))
		req.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("UpdateUser_Error", func(t *testing.T) {
		reqBody := UpdateUserRequest{Roles: []string{"admin"}, Disabled: true}
		mockAuth.On("UpdateUser", mock.Anything, "123", reqBody.Roles, reqBody.Disabled).Return(errors.New("db error")).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PATCH", "/admin/users/123", bytes.NewReader(body))
		req.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PushRules_Error", func(t *testing.T) {
		rules := []byte("invalid rules")
		mockAuthz.On("UpdateRules", rules).Return(errors.New("parse error")).Once()

		req := httptest.NewRequest("POST", "/admin/rules/push", bytes.NewReader(rules))
		req.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// TestReplicationHandlerErrors covers error paths in handler_replication.go
func TestReplicationHandlerErrors(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	t.Run("Pull_InvalidCheckpoint", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/replication/v1/pull?collection=c&checkpoint=invalid", nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "Invalid checkpoint")
	})

	t.Run("Pull_ValidationError", func(t *testing.T) {
		// Limit -1 should trigger validation error
		req, _ := http.NewRequest("GET", "/replication/v1/pull?collection=c&checkpoint=0&limit=-1", nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Pull_EngineError", func(t *testing.T) {
		mockService.On("Pull", mock.Anything, "default", mock.Anything).Return(nil, errors.New("db error")).Once()

		req, _ := http.NewRequest("GET", "/replication/v1/pull?collection=c&checkpoint=0", nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("Push_InvalidBody", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/replication/v1/push", bytes.NewReader([]byte("invalid")))
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Push_MissingCollection", func(t *testing.T) {
		reqBody := ReplicaPushRequest{Collection: ""}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/replication/v1/push", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "Collection is required")
	})

	t.Run("Push_InvalidCollection", func(t *testing.T) {
		reqBody := ReplicaPushRequest{Collection: "invalid!"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/replication/v1/push", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Push_EngineError", func(t *testing.T) {
		reqBody := ReplicaPushRequest{
			Collection: "valid_collection",
			Changes: []ReplicaChange{
				{
					Doc: model.Document{"id": "doc1", "name": "test"},
				},
			},
		}
		mockService.On("Push", mock.Anything, "default", mock.Anything).Return(nil, errors.New("db error")).Once()

		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/replication/v1/push", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

// TestDocumentHandlerErrors covers error paths in handler_document.go
func TestDocumentHandlerErrors(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := new(MockAuthService)
	server := createTestServer(mockService, mockAuth, nil)

	t.Run("GetDocument_InvalidPath", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/v1/rooms", nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("CreateDocument_InternalError", func(t *testing.T) {
		body := []byte(`{"id":"msg-1", "name": "Bob"}`)
		req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		mockService.On("CreateDocument", mock.Anything, "default", mock.Anything).Return(errors.New("db error")).Once()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Contains(t, rr.Body.String(), "db error")
	})

	t.Run("ReplaceDocument_InternalError", func(t *testing.T) {
		body := []byte(`{"doc":{"name": "Bob"}}`)
		req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		mockService.On("ReplaceDocument", mock.Anything, "default", mock.MatchedBy(func(doc model.Document) bool {
			return doc.GetID() == "msg-1"
		}), mock.Anything).Return(nil, errors.New("db error")).Once()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Contains(t, rr.Body.String(), "db error")
	})

	t.Run("DeleteDocument_InvalidBody", func(t *testing.T) {
		body := []byte(`{invalid-json}`)
		req, _ := http.NewRequest("DELETE", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "Invalid request body")
	})

	t.Run("DeleteDocument_PreconditionFailed", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/api/v1/rooms/room-1/messages/msg-1", nil)
		rr := httptest.NewRecorder()

		mockService.On("DeleteDocument", mock.Anything, "default", "rooms/room-1/messages/msg-1", mock.Anything).Return(model.ErrPreconditionFailed).Once()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
		assert.Contains(t, rr.Body.String(), "Version conflict")
	})

	t.Run("DeleteDocument_InvalidPath", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/api/v1/rooms", nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("PatchDocument_MissingID", func(t *testing.T) {
		req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages", bytes.NewBuffer([]byte("{}")))
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "Invalid document path: missing document ID")
	})
}

// FailAuthService embeds MockAuthService but overrides Middleware
type FailAuthService struct {
	*MockAuthService
}

func (m *FailAuthService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not set tenant in context
		next.ServeHTTP(w, r)
	})
}

func (m *FailAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not set tenant in context
		next.ServeHTTP(w, r)
	})
}

func TestDocumentHandler_TenantError(t *testing.T) {
	mockService := new(MockQueryService)
	mockAuth := &FailAuthService{MockAuthService: new(MockAuthService)}

	server := createTestServer(mockService, mockAuth, nil)

	// GET request
	req, _ := http.NewRequest("GET", "/api/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	// Should fail with 401 Unauthorized because tenant is missing
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "tenant is required")
}
