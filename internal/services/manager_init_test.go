package services

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestManager_AuthServiceGetter(t *testing.T) {
	t.Parallel()
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	assert.Nil(t, mgr.AuthService())
}

func TestNewManager_DefaultListenHost(t *testing.T) {
	t.Parallel()
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	assert.Equal(t, "localhost", mgr.opts.ListenHost)
}

func TestManager_Init_StorageError(t *testing.T) {
	t.Parallel()
	cfg := config.LoadConfig()
	if backend, ok := cfg.Storage.Backends["default_mongo"]; ok {
		backend.Mongo.URI = "mongodb://invalid-host:1"
		cfg.Storage.Backends["default_mongo"] = backend
	}
	opt := Options{RunQuery: true}
	mgr := NewManager(cfg, opt)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_Init_TokenServiceError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Identity.AuthN.PrivateKeyFile = "/nonexistent/dir/key.pem"
	opt := Options{RunAPI: true}
	mgr := NewManager(cfg, opt)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_Init_AuthzRulesLoadError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Identity.AuthZ.RulesFile = "__missing_rules_file__"
	opts := Options{RunAPI: true}
	mgr := NewManager(cfg, opts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mgr.Init(ctx)
	assert.Error(t, err)
}

func TestManager_InitAuthService_GenerateKey(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Identity.AuthN.PrivateKeyFile = filepath.Join(t.TempDir(), "private.pem")
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	// Mock storage factory
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}
	defer func() {
		storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
			return storage.NewFactory(ctx, cfg)
		}
	}()

	// We need to init storage first because initAuthService depends on it
	err := mgr.initStorage(context.Background())
	assert.NoError(t, err)

	err = mgr.initAuthService(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.authService)

	_, statErr := os.Stat(cfg.Identity.AuthN.PrivateKeyFile)
	assert.NoError(t, statErr)
}

func TestManager_InitAPIServer_WithRules(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	rulesContent := "match:\n  /databases/{db}/documents/{doc}:\n    allow:\n      get: \"true\"\n"
	assert.NoError(t, os.WriteFile(rulesPath, []byte(rulesContent), 0644))
	cfg.Identity.AuthZ.RulesFile = rulesPath

	mgr := NewManager(cfg, Options{})
	mgr.authService = &stubAuthN{}
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_InitAPIServer_NoRules(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Identity.AuthZ.RulesFile = ""

	mgr := NewManager(cfg, Options{})
	mgr.authService = &stubAuthN{}
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_InitAPIServer_WithRealtime(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Identity.AuthZ.RulesFile = ""
	mgr := NewManager(cfg, Options{})
	mgr.authService = &stubAuthN{}

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
	assert.Nil(t, mgr.storageFactory)
	assert.Nil(t, mgr.docStore)
	assert.Nil(t, mgr.userStore)
}

func TestManager_Init_RunAuthPath(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Identity.AuthN.PrivateKeyFile = filepath.Join(t.TempDir(), "auth.pem")

	// Create a dummy rules file
	rulesFile := filepath.Join(t.TempDir(), "security.yaml")
	os.WriteFile(rulesFile, []byte("rules: []"), 0644)
	cfg.Identity.AuthZ.RulesFile = rulesFile

	mgr := NewManager(cfg, Options{RunAPI: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.authService)
}

func TestManager_Init_RunQueryPath(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Query.Port = 0
	mgr := NewManager(cfg, Options{RunQuery: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.docStore)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Query Service", mgr.serverNames[0])
}

func TestManager_Init_RunCSPPath(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.CSP.Port = 0
	mgr := NewManager(cfg, Options{RunCSP: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.docStore)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "CSP Service", mgr.serverNames[0])
}

func TestManager_Init_RunRealtimePath(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0

	cfg.Identity.AuthZ.RulesFile = ""
	mgr := NewManager(cfg, Options{RunAPI: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.rtServer)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_initPullerService_Success(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Puller.Buffer.Path = t.TempDir()
	cfg.Puller.Backends = []config.PullerBackendConfig{{Name: "primary"}}

	mgr := NewManager(cfg, Options{Mode: ModeDistributed})
	mgr.storageFactory = &stubStorageFactory{
		dbByName: map[string]string{"primary": "db_primary"},
		client:   &mongo.Client{},
	}

	err := mgr.initPullerService(context.Background())
	assert.NoError(t, err)
	names := mgr.pullerService.BackendNames()
	assert.Len(t, names, 1)
	assert.Equal(t, "primary", names[0])
	assert.NotNil(t, mgr.pullerGRPC)
}

func TestManager_initPullerService_GetMongoError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Puller.Buffer.Path = t.TempDir()
	cfg.Puller.Backends = []config.PullerBackendConfig{{Name: "missing"}}

	mgr := NewManager(cfg, Options{})
	mgr.storageFactory = &stubStorageFactory{
		errByName: map[string]error{"missing": errors.New("no backend")},
		client:    &mongo.Client{},
	}
	mgr.pullerService = &stubPullerService{}

	err := mgr.initPullerService(context.Background())
	assert.Error(t, err)
}

type fakeDocumentStore struct {
	db        *mongo.Database
	retention time.Duration
}

func (f *fakeDocumentStore) Get(ctx context.Context, tenant, path string) (*storage.Document, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Create(ctx context.Context, tenant string, doc *storage.Document) error {
	return nil
}
func (f *fakeDocumentStore) Update(ctx context.Context, tenant, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Patch(ctx context.Context, tenant, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Delete(ctx context.Context, tenant, path string, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Query(ctx context.Context, tenant string, q model.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Close(ctx context.Context) error { return nil }

type fakeAuthStore struct {
	db           *mongo.Database
	ensureCalled bool
}

func (f *fakeAuthStore) CreateUser(ctx context.Context, tenant string, user *storage.User) error {
	return nil
}
func (f *fakeAuthStore) GetUserByUsername(ctx context.Context, tenant, username string) (*storage.User, error) {
	return nil, identity.ErrUserNotFound
}
func (f *fakeAuthStore) GetUserByID(ctx context.Context, tenant, id string) (*storage.User, error) {
	return nil, identity.ErrUserNotFound
}
func (f *fakeAuthStore) ListUsers(ctx context.Context, tenant string, limit int, offset int) ([]*storage.User, error) {
	return nil, nil
}
func (f *fakeAuthStore) UpdateUser(ctx context.Context, tenant string, user *storage.User) error {
	return nil
}
func (f *fakeAuthStore) UpdateUserLoginStats(ctx context.Context, tenant, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return nil
}
func (f *fakeAuthStore) RevokeToken(ctx context.Context, tenant, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStore) RevokeTokenImmediate(ctx context.Context, tenant, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStore) IsRevoked(ctx context.Context, tenant, jti string, gracePeriod time.Duration) (bool, error) {
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
	users       storage.UserStore
	revocations storage.TokenRevocationStore
}

func (f *fakeAuthProvider) Users() storage.UserStore                  { return f.users }
func (f *fakeAuthProvider) Revocations() storage.TokenRevocationStore { return f.revocations }
func (f *fakeAuthProvider) Close(ctx context.Context) error           { return nil }

type stubQueryService struct{}

func (s *stubQueryService) GetDocument(context.Context, string, string) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) CreateDocument(context.Context, string, model.Document) error {
	return nil
}

func (s *stubQueryService) ReplaceDocument(context.Context, string, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) PatchDocument(context.Context, string, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}

func (s *stubQueryService) DeleteDocument(context.Context, string, string, model.Filters) error {
	return nil
}

func (s *stubQueryService) ExecuteQuery(context.Context, string, model.Query) ([]model.Document, error) {
	return nil, nil
}

func (s *stubQueryService) WatchCollection(context.Context, string, string) (<-chan storage.Event, error) {
	return nil, nil
}

func (s *stubQueryService) Pull(context.Context, string, storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}

func (s *stubQueryService) Push(context.Context, string, storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}

type stubStorageFactory struct {
	dbByName  map[string]string
	errByName map[string]error
	client    *mongo.Client
}

func (s *stubStorageFactory) Document() storage.DocumentStore          { return nil }
func (s *stubStorageFactory) User() storage.UserStore                  { return nil }
func (s *stubStorageFactory) Revocation() storage.TokenRevocationStore { return nil }
func (s *stubStorageFactory) GetMongoClient(name string) (*mongo.Client, string, error) {
	if err := s.errByName[name]; err != nil {
		return nil, "", err
	}

	db := s.dbByName[name]
	if db == "" {
		db = name
	}
	return s.client, db, nil
}
func (s *stubStorageFactory) Close() error { return nil }

type fakeStorageFactory struct {
	docStore storage.DocumentStore
	usrStore storage.UserStore
	revStore storage.TokenRevocationStore
}

func (f *fakeStorageFactory) Document() storage.DocumentStore          { return f.docStore }
func (f *fakeStorageFactory) User() storage.UserStore                  { return f.usrStore }
func (f *fakeStorageFactory) Revocation() storage.TokenRevocationStore { return f.revStore }
func (f *fakeStorageFactory) GetMongoClient(name string) (*mongo.Client, string, error) {
	return nil, "", nil
}
func (f *fakeStorageFactory) Close() error { return nil }

type stubAuthN struct{}

func (s *stubAuthN) Middleware(next http.Handler) http.Handler         { return next }
func (s *stubAuthN) MiddlewareOptional(next http.Handler) http.Handler { return next }
func (s *stubAuthN) SignIn(ctx context.Context, req identity.LoginRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (s *stubAuthN) SignUp(ctx context.Context, req identity.SignupRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (s *stubAuthN) Refresh(ctx context.Context, req identity.RefreshRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (s *stubAuthN) ListUsers(ctx context.Context, limit int, offset int) ([]*storage.User, error) {
	return nil, nil
}
func (s *stubAuthN) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	return nil
}
func (s *stubAuthN) Logout(ctx context.Context, refreshToken string) error      { return nil }
func (s *stubAuthN) GenerateSystemToken(serviceName string) (string, error)     { return "", nil }
func (s *stubAuthN) ValidateToken(tokenString string) (*identity.Claims, error) { return nil, nil }

func TestManager_Init_StandaloneMode(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Identity.AuthZ.RulesFile = ""
	mgr := NewManager(cfg, Options{
		Mode:   ModeStandalone,
		RunAPI: true,
	})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	// In standalone mode, only the Gateway server should be created
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
	assert.NotNil(t, mgr.docStore)
}

func TestManager_Init_StandaloneMode_NoHTTPForCSP(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.CSP.Port = 0
	cfg.Query.Port = 0
	cfg.Identity.AuthZ.RulesFile = ""
	mgr := NewManager(cfg, Options{
		Mode:   ModeStandalone,
		RunAPI: true,
		RunCSP: true, // This should be ignored in standalone mode
	})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	// In standalone mode, CSP and Query servers are not created,
	// only the Gateway server should exist
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Unified Gateway", mgr.serverNames[0])
}

func TestManager_createQueryService(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})
	mgr.docStore = &fakeDocumentStore{}

	// Create a mock CSP service
	mockCSP := &mockCSPService{}
	service := mgr.createQueryService(mockCSP)
	assert.NotNil(t, service)
}

func TestManager_createCSPService(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})
	mgr.docStore = &fakeDocumentStore{}

	service := mgr.createCSPService()
	assert.NotNil(t, service)
}

func TestManager_initQueryHTTPServer(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Query.Port = 0
	mgr := NewManager(cfg, Options{})

	mockService := &stubQueryService{}
	mgr.initQueryHTTPServer(mockService)

	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Query Service", mgr.serverNames[0])
}

// mockCSPService implements csp.Service for testing
type mockCSPService struct{}

func (m *mockCSPService) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	ch := make(chan storage.Event)
	close(ch)
	return ch, nil
}

func TestManager_initStandalone_APIServerError(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Identity.AuthZ.RulesFile = "/nonexistent/rules/file.yaml"
	mgr := NewManager(cfg, Options{
		Mode:   ModeStandalone,
		RunAPI: true,
	})

	err := mgr.Init(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create authz engine")
}

func TestManager_initDistributed_APIServerError(t *testing.T) {
	origFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = origFactory }()

	fakeDocStore := &fakeDocumentStore{}
	fakeAuth := &fakeAuthStore{}
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: fakeDocStore,
			usrStore: fakeAuth,
			revStore: fakeAuth,
		}, nil
	}

	cfg := config.LoadConfig()
	cfg.Gateway.Port = 0
	cfg.Identity.AuthZ.RulesFile = "/nonexistent/rules/file.yaml"
	mgr := NewManager(cfg, Options{
		Mode:   ModeDistributed,
		RunAPI: true,
	})

	err := mgr.Init(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create authz engine")
}
