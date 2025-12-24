package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestServeHTTP_OptionsSetsCORS(t *testing.T) {
	server := createTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "OPTIONS")
}

func TestServeHTTP_CORSHeadersOnGET(t *testing.T) {
	server := createTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestProtected_NoAuth(t *testing.T) {
	server := &Handler{}

	handler := server.protected(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/any", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestProtected_WithAuth(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := &Handler{auth: mockAuth}

	mockAuth.On("Middleware", mock.Anything).Return(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	handler := server.protected(func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/any", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestMaybeProtected_NoAuth(t *testing.T) {
	server := &Handler{}

	handler := server.maybeProtected(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/any", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestMaybeProtected_WithAuth(t *testing.T) {
	mockAuth := new(MockAuthService)
	server := &Handler{auth: mockAuth}

	mockAuth.On("MiddlewareOptional", mock.Anything).Return(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	handler := server.maybeProtected(func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/any", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	mockAuth.AssertExpectations(t)
}
