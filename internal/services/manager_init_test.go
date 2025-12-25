package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/auth"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestManager_TokenServiceGetter(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	assert.Nil(t, mgr.TokenService())
}

func TestNewManager_DefaultListenHost(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	assert.Equal(t, "localhost", mgr.opts.ListenHost)
}

func TestManager_Init_StorageError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Storage.Document.Mongo.URI = "mongodb://invalid-host:1"
	opt := Options{RunQuery: true}
	mgr := NewManager(cfg, opt)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_Init_TokenServiceError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Auth.PrivateKeyFile = "/nonexistent/dir/key.pem"
	opt := Options{RunAPI: true}
	mgr := NewManager(cfg, opt)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_Init_AuthzRulesLoadError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Auth.RulesFile = "__missing_rules_file__"
	opts := Options{RunAPI: true}
	mgr := NewManager(cfg, opts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_InitTokenService_GenerateKey(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Auth.PrivateKeyFile = filepath.Join(t.TempDir(), "private.pem")
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	err := mgr.initTokenService()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.tokenService)

	_, statErr := os.Stat(cfg.Auth.PrivateKeyFile)
	assert.NoError(t, statErr)
}

func TestManager_InitAPIServer_WithRules(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	rulesContent := "match:\n  /databases/{db}/documents/{doc}:\n    allow:\n      get: \"true\"\n"
	assert.NoError(t, os.WriteFile(rulesPath, []byte(rulesContent), 0644))
	cfg.Auth.RulesFile = rulesPath

	mgr := NewManager(cfg, Options{})
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_InitAPIServer_NoRules(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Auth.RulesFile = ""

	mgr := NewManager(cfg, Options{})
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_InitAPIServer_WithRealtime(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Auth.RulesFile = ""
	mgr := NewManager(cfg, Options{})

	err := mgr.initAPIServer(&stubQueryService{})
	assert.NoError(t, err)
	assert.NotNil(t, mgr.rtServer)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestListenAddr_WithHost(t *testing.T) {
	addr := listenAddr("localhost", 8080)
	assert.Equal(t, "localhost:8080", addr)
}

func TestListenAddr_EmptyHost(t *testing.T) {
	addr := listenAddr("", 8080)
	assert.Equal(t, ":8080", addr)
}

func TestManager_InitTriggerServices_NATSFailure(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Trigger.NatsURL = "nats://127.0.0.1:1"
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	err := mgr.initTriggerServices()
	assert.Error(t, err)
}

func TestManager_InitStorage_SkipsWhenNoServices(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mgr.initStorage(ctx)
	assert.NoError(t, err)
	assert.Nil(t, mgr.docProvider)
	assert.Nil(t, mgr.authProvider)
}

func TestManager_Init_RunAuthPath(t *testing.T) {
	origDocFactory := documentProviderFactory
	origAuthFactory := authProviderFactory
	defer func() {
		documentProviderFactory = origDocFactory
		authProviderFactory = origAuthFactory
	}()

	fakeDocStore := &fakeDocumentStore{}
	documentProviderFactory = func(ctx context.Context, cfg config.StorageConfig) (storage.DocumentProvider, error) {
		return &fakeDocumentProvider{store: fakeDocStore}, nil
	}

	fakeAuth := &fakeAuthStore{}
	authProviderFactory = func(ctx context.Context, cfg config.StorageConfig) (storage.AuthProvider, error) {
		return &fakeAuthProvider{users: fakeAuth, revocations: fakeAuth}, nil
	}

	cfg := config.LoadConfig()
	cfg.Auth.PrivateKeyFile = filepath.Join(t.TempDir(), "auth.pem")

	// Create a dummy rules file
	rulesFile := filepath.Join(t.TempDir(), "security.yaml")
	os.WriteFile(rulesFile, []byte("rules: []"), 0644)
	cfg.Auth.RulesFile = rulesFile

	mgr := NewManager(cfg, Options{RunAPI: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.authService)
	assert.NotNil(t, mgr.tokenService)
}

func TestManager_Init_RunQueryPath(t *testing.T) {
	origDocFactory := documentProviderFactory
	defer func() { documentProviderFactory = origDocFactory }()

	fakeDocStore := &fakeDocumentStore{}
	documentProviderFactory = func(ctx context.Context, cfg config.StorageConfig) (storage.DocumentProvider, error) {
		return &fakeDocumentProvider{store: fakeDocStore}, nil
	}

	cfg := config.LoadConfig()
	cfg.Query.Port = 0
	mgr := NewManager(cfg, Options{RunQuery: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.docProvider)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Query Service", mgr.serverNames[0])
}

func TestManager_Init_RunCSPPath(t *testing.T) {
	origDocFactory := documentProviderFactory
	defer func() { documentProviderFactory = origDocFactory }()

	fakeDocStore := &fakeDocumentStore{}
	documentProviderFactory = func(ctx context.Context, cfg config.StorageConfig) (storage.DocumentProvider, error) {
		return &fakeDocumentProvider{store: fakeDocStore}, nil
	}

	cfg := config.LoadConfig()
	cfg.CSP.Port = 0
	mgr := NewManager(cfg, Options{RunCSP: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.docProvider)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "CSP Service", mgr.serverNames[0])
}

func TestManager_Init_RunRealtimePath(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0

	cfg.Auth.RulesFile = ""
	mgr := NewManager(cfg, Options{RunAPI: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.rtServer)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

type fakeDocumentStore struct {
	db        *mongo.Database
	retention time.Duration
}

func (f *fakeDocumentStore) Get(ctx context.Context, path string) (*storage.Document, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Create(ctx context.Context, doc *storage.Document) error { return nil }
func (f *fakeDocumentStore) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Delete(ctx context.Context, path string, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Query(ctx context.Context, q model.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Watch(ctx context.Context, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Close(ctx context.Context) error { return nil }

type fakeAuthStore struct {
	db           *mongo.Database
	ensureCalled bool
}

func (f *fakeAuthStore) CreateUser(ctx context.Context, user *auth.User) error { return nil }
func (f *fakeAuthStore) GetUserByUsername(ctx context.Context, username string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (f *fakeAuthStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (f *fakeAuthStore) ListUsers(ctx context.Context, limit int, offset int) ([]*auth.User, error) {
	return nil, nil
}
func (f *fakeAuthStore) UpdateUser(ctx context.Context, user *auth.User) error { return nil }
func (f *fakeAuthStore) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return nil
}
func (f *fakeAuthStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStore) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStore) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	return false, nil
}
func (f *fakeAuthStore) EnsureIndexes(ctx context.Context) error {
	f.ensureCalled = true
	return nil
}
func (f *fakeAuthStore) Close(ctx context.Context) error { return nil }

type fakeDocumentProvider struct {
	store storage.DocumentStore
}

func (f *fakeDocumentProvider) Document() storage.DocumentStore { return f.store }
func (f *fakeDocumentProvider) Close(ctx context.Context) error { return nil }

type fakeAuthProvider struct {
	users       auth.UserStore
	revocations auth.TokenRevocationStore
}

func (f *fakeAuthProvider) Users() auth.UserStore                  { return f.users }
func (f *fakeAuthProvider) Revocations() auth.TokenRevocationStore { return f.revocations }
func (f *fakeAuthProvider) Close(ctx context.Context) error        { return nil }

type stubQueryService struct{}

func (s *stubQueryService) GetDocument(context.Context, string) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) CreateDocument(context.Context, model.Document) error {
	return nil
}

func (s *stubQueryService) ReplaceDocument(context.Context, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) PatchDocument(context.Context, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) DeleteDocument(context.Context, string, model.Filters) error {
	return nil
}

func (s *stubQueryService) ExecuteQuery(context.Context, model.Query) ([]model.Document, error) {
	return nil, nil
}

func (s *stubQueryService) WatchCollection(context.Context, string) (<-chan storage.Event, error) {
	return nil, nil
}

func (s *stubQueryService) Pull(context.Context, storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}

func (s *stubQueryService) Push(context.Context, storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}
