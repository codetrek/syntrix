package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mocks
type MockQueryService struct {
	mock.Mock
	query.Service
}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (model.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

type MockAuthService struct {
	mock.Mock
	identity.AuthN
}

func (m *MockAuthService) Middleware(next http.Handler) http.Handler {
	return next
}

func (m *MockAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	return next
}

type MockAuthzEngine struct {
	mock.Mock
	identity.AuthZ
}

func (m *MockAuthzEngine) Evaluate(ctx context.Context, path string, action string, req identity.AuthzRequest, existingRes *identity.Resource) (bool, error) {
	args := m.Called(ctx, path, action, req, existingRes)
	return args.Bool(0), args.Error(1)
}

func TestNewServer(t *testing.T) {
	mockQuery := new(MockQueryService)
	mockAuth := new(MockAuthService)
	mockAuthz := new(MockAuthzEngine)

	// Realtime server needs a stub, but we can pass nil for now if allowed, or create a dummy
	// NewServer allows rt to be nil? Let's check implementation.
	// routes() checks if s.realtime != nil. So nil is fine.

	server := NewServer(mockQuery, mockAuth, mockAuthz, nil)
	assert.NotNil(t, server)
	assert.NotNil(t, server.mux)
	assert.NotNil(t, server.rest)
}

func TestNewServer_WithRealtime(t *testing.T) {
	mockQuery := new(MockQueryService)
	mockAuth := new(MockAuthService)
	mockAuthz := new(MockAuthzEngine)

	rt := realtime.NewServer(mockQuery, "docs")

	server := NewServer(mockQuery, mockAuth, mockAuthz, rt)
	assert.NotNil(t, server)
	assert.NotNil(t, server.realtime)
}

func TestServer_ServeHTTP(t *testing.T) {
	mockQuery := new(MockQueryService)
	mockAuth := new(MockAuthService)
	mockAuthz := new(MockAuthzEngine)

	server := NewServer(mockQuery, mockAuth, mockAuthz, nil)

	t.Run("CORS Options", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/v1/health", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("Normal Request", func(t *testing.T) {
		// We need to register a route to test normal request, or rely on rest handler's routes.
		// rest.NewHandler registers /api/v1/health by default? Let's check rest handler.
		// Assuming /api/v1/health exists.

		// Mock GetDocument to return ErrNotFound so authorized middleware continues
		mockQuery.On("GetDocument", mock.Anything, "health").Return(nil, model.ErrNotFound)

		// Mock Evaluate to return true
		mockAuthz.On("Evaluate", mock.Anything, "health", "read", mock.Anything, mock.Anything).Return(true, nil)

		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		// Even if 404, it means ServeHTTP delegated to mux.
		// But we want to verify it hit the mux.
		// Since we can't easily inspect mux, we check if headers are set.
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	})
}
