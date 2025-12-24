package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"syntrix/internal/auth"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandleLogin(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := createTestServer(nil, mockAuth, nil)

	t.Run("Success", func(t *testing.T) {
		reqBody := auth.LoginRequest{Username: "user", Password: "password"}
		tokenPair := &auth.TokenPair{AccessToken: "access", RefreshToken: "refresh"}
		mockAuth.On("SignIn", mock.Anything, reqBody).Return(tokenPair, nil).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp auth.TokenPair
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Equal(t, "access", resp.AccessToken)
	})

	t.Run("InvalidCredentials", func(t *testing.T) {
		reqBody := auth.LoginRequest{Username: "user", Password: "wrong"}
		mockAuth.On("SignIn", mock.Anything, reqBody).Return(nil, auth.ErrInvalidCredentials).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("InvalidBody", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader([]byte("invalid")))
		w := httptest.NewRecorder()

		server.handleLogin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleRefresh(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := createTestServer(nil, mockAuth, nil)

	t.Run("Success", func(t *testing.T) {
		reqBody := auth.RefreshRequest{RefreshToken: "valid_refresh"}
		tokenPair := &auth.TokenPair{AccessToken: "new_access", RefreshToken: "new_refresh"}
		mockAuth.On("Refresh", mock.Anything, reqBody).Return(tokenPair, nil).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleRefresh(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp auth.TokenPair
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Equal(t, "new_access", resp.AccessToken)
	})

	t.Run("InvalidToken", func(t *testing.T) {
		reqBody := auth.RefreshRequest{RefreshToken: "invalid_refresh"}
		mockAuth.On("Refresh", mock.Anything, reqBody).Return(nil, errors.New("invalid token")).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleRefresh(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleLogout(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := createTestServer(nil, mockAuth, nil)

	t.Run("Success_Body", func(t *testing.T) {
		reqBody := auth.RefreshRequest{RefreshToken: "refresh_token"}
		mockAuth.On("Logout", mock.Anything, "refresh_token").Return(nil).Once()

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/auth/logout", bytes.NewReader(body))
		w := httptest.NewRecorder()

		server.handleLogout(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Success_Header", func(t *testing.T) {
		mockAuth.On("Logout", mock.Anything, "refresh_token").Return(nil).Once()

		req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
		req.Header.Set("Authorization", "Bearer refresh_token")
		w := httptest.NewRecorder()

		server.handleLogout(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("MissingToken", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
		w := httptest.NewRecorder()

		server.handleLogout(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
