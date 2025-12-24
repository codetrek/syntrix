package rest

import (
	"context"
	"net/http"

	"github.com/codetrek/syntrix/internal/auth"
	"github.com/codetrek/syntrix/internal/authz"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/mock"
)

// MockQueryService is a mock implementation of query.Service
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (model.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockQueryService) CreateDocument(ctx context.Context, doc model.Document) error {
	args := m.Called(ctx, doc)
	return args.Error(0)
}

func (m *MockQueryService) ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	args := m.Called(ctx, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockQueryService) PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	args := m.Called(ctx, data, pred)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockQueryService) DeleteDocument(ctx context.Context, path string, pred model.Filters) error {
	args := m.Called(ctx, path, pred)
	return args.Error(0)
}

func (m *MockQueryService) ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error) {
	args := m.Called(ctx, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.Document), args.Error(1)
}

func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	args := m.Called(ctx, collection)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPullResponse), args.Error(1)
}

func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPushResponse), args.Error(1)
}

// MockAuthService is a mock implementation of AuthService
type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) Middleware(next http.Handler) http.Handler {
	// For testing, we can just pass through or mock behavior if needed.
	// But since it returns http.Handler, it's tricky to mock with testify directly in a way that executes 'next'.
	// Usually we mock the side effects (context setting).
	// For now, let's assume the test sets up the context manually or we implement a simple pass-through.
	// Or we can use the mock to return a handler.
	args := m.Called(next)
	if args.Get(0) != nil {
		return args.Get(0).(http.Handler)
	}
	return next
}

func (m *MockAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	args := m.Called(next)
	if args.Get(0) != nil {
		return args.Get(0).(http.Handler)
	}
	return next
}

func (m *MockAuthService) SignIn(ctx context.Context, req auth.LoginRequest) (*auth.TokenPair, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *MockAuthService) Refresh(ctx context.Context, req auth.RefreshRequest) (*auth.TokenPair, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *MockAuthService) ListUsers(ctx context.Context, limit int, offset int) ([]*auth.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*auth.User), args.Error(1)
}

func (m *MockAuthService) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	args := m.Called(ctx, id, roles, disabled)
	return args.Error(0)
}

func (m *MockAuthService) Logout(ctx context.Context, refreshToken string) error {
	args := m.Called(ctx, refreshToken)
	return args.Error(0)
}

// MockAuthzService is a mock implementation of AuthzService
type MockAuthzService struct {
	mock.Mock
}

func (m *MockAuthzService) Evaluate(ctx context.Context, path string, action string, req authz.Request, existingRes *authz.Resource) (bool, error) {
	args := m.Called(ctx, path, action, req, existingRes)
	return args.Bool(0), args.Error(1)
}

func (m *MockAuthzService) GetRules() *authz.RuleSet {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*authz.RuleSet)
}

func (m *MockAuthzService) UpdateRules(content []byte) error {
	args := m.Called(content)
	return args.Error(0)
}

func (m *MockAuthzService) LoadRules(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

type TestServer struct {
	*Handler
	mux *http.ServeMux
}

func (s *TestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func createTestServer(engine query.Service, auth auth.Service, authz authz.Engine) *TestServer {
	h := NewHandler(engine, auth, authz)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return &TestServer{
		Handler: h,
		mux:     mux,
	}
}
