package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syntrix/internal/auth"
	"syntrix/internal/common"
	"syntrix/internal/config"
	"syntrix/internal/query"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestManager_TokenServiceGetter(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	assert.Nil(t, mgr.TokenService())
}

func TestManager_Init_StorageError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Storage.MongoURI = "mongodb://invalid-host:1"
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
	opt := Options{RunAuth: true}
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
	cfg.API.Port = 0
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	rulesContent := "match:\n  /databases/{db}/documents/{doc}:\n    allow:\n      get: \"true\"\n"
	assert.NoError(t, os.WriteFile(rulesPath, []byte(rulesContent), 0644))
	cfg.Auth.RulesFile = rulesPath

	mgr := NewManager(cfg, Options{})
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "API Gateway", mgr.serverNames[0])
}

func TestManager_InitAPIServer_NoRules(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.API.Port = 0
	cfg.Auth.RulesFile = ""

	mgr := NewManager(cfg, Options{})
	querySvc := &stubQueryService{}

	err := mgr.initAPIServer(querySvc)
	assert.NoError(t, err)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "API Gateway", mgr.serverNames[0])
}

func TestManager_InitRealtimeServer(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Realtime.Port = 0
	mgr := NewManager(cfg, Options{})

	mgr.initRealtimeServer(&stubQueryService{})

	assert.NotNil(t, mgr.rtServer)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Realtime Gateway", mgr.serverNames[0])
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
	assert.Nil(t, mgr.storageBackend)
}

func TestManager_Init_RunAuthPath(t *testing.T) {
	origBackendFactory := mongoBackendFactory
	origAuthStorageFactory := authStorageFactory
	defer func() {
		mongoBackendFactory = origBackendFactory
		authStorageFactory = origAuthStorageFactory
	}()

	fakeBackend := &fakeStorageBackend{db: &mongo.Database{}}
	mongoBackendFactory = func(ctx context.Context, uri, dbName, dataColl, sysColl string, retention time.Duration) (storageBackend, error) {
		fakeBackend.retention = retention
		return fakeBackend, nil
	}

	fakeAuth := &fakeAuthStorage{}
	authStorageFactory = func(db *mongo.Database) authStorage {
		fakeAuth.db = db
		return fakeAuth
	}

	cfg := config.LoadConfig()
	cfg.Auth.PrivateKeyFile = filepath.Join(t.TempDir(), "auth.pem")
	mgr := NewManager(cfg, Options{RunAuth: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.authService)
	assert.NotNil(t, mgr.tokenService)
	assert.True(t, fakeAuth.ensureCalled)
}

func TestManager_Init_RunQueryPath(t *testing.T) {
	origBackendFactory := mongoBackendFactory
	defer func() { mongoBackendFactory = origBackendFactory }()

	fakeBackend := &fakeStorageBackend{db: &mongo.Database{}}
	mongoBackendFactory = func(ctx context.Context, uri, dbName, dataColl, sysColl string, retention time.Duration) (storageBackend, error) {
		return fakeBackend, nil
	}

	cfg := config.LoadConfig()
	cfg.Query.Port = 0
	mgr := NewManager(cfg, Options{RunQuery: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.storageBackend)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Query Service", mgr.serverNames[0])
}

func TestManager_Init_RunCSPPath(t *testing.T) {
	origBackendFactory := mongoBackendFactory
	defer func() { mongoBackendFactory = origBackendFactory }()

	fakeBackend := &fakeStorageBackend{db: &mongo.Database{}}
	mongoBackendFactory = func(ctx context.Context, uri, dbName, dataColl, sysColl string, retention time.Duration) (storageBackend, error) {
		return fakeBackend, nil
	}

	cfg := config.LoadConfig()
	cfg.CSP.Port = 0
	mgr := NewManager(cfg, Options{RunCSP: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.storageBackend)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "CSP Service", mgr.serverNames[0])
}

func TestManager_Init_RunRealtimePath(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Realtime.Port = 0
	mgr := NewManager(cfg, Options{RunRealtime: true})

	err := mgr.Init(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mgr.rtServer)
	assert.Len(t, mgr.servers, 1)
	assert.Equal(t, "Realtime Gateway", mgr.serverNames[0])
}

type fakeStorageBackend struct {
	db        *mongo.Database
	retention time.Duration
}

func (f *fakeStorageBackend) DB() *mongo.Database { return f.db }
func (f *fakeStorageBackend) Get(ctx context.Context, path string) (*storage.Document, error) {
	return nil, nil
}
func (f *fakeStorageBackend) Create(ctx context.Context, doc *storage.Document) error { return nil }
func (f *fakeStorageBackend) Update(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	return nil
}
func (f *fakeStorageBackend) Patch(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	return nil
}
func (f *fakeStorageBackend) Delete(ctx context.Context, path string, pred storage.Filters) error {
	return nil
}
func (f *fakeStorageBackend) Query(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (f *fakeStorageBackend) Watch(ctx context.Context, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	return nil, nil
}
func (f *fakeStorageBackend) Transaction(ctx context.Context, fn func(ctx context.Context, tx storage.StorageBackend) error) error {
	return fn(ctx, f)
}
func (f *fakeStorageBackend) Close(ctx context.Context) error { return nil }

type fakeAuthStorage struct {
	db           *mongo.Database
	ensureCalled bool
}

func (f *fakeAuthStorage) CreateUser(ctx context.Context, user *auth.User) error { return nil }
func (f *fakeAuthStorage) GetUserByUsername(ctx context.Context, username string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (f *fakeAuthStorage) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (f *fakeAuthStorage) ListUsers(ctx context.Context, limit int, offset int) ([]*auth.User, error) {
	return nil, nil
}
func (f *fakeAuthStorage) UpdateUser(ctx context.Context, user *auth.User) error { return nil }
func (f *fakeAuthStorage) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return nil
}
func (f *fakeAuthStorage) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStorage) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeAuthStorage) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	return false, nil
}
func (f *fakeAuthStorage) EnsureIndexes(ctx context.Context) error {
	f.ensureCalled = true
	return nil
}

type stubQueryService struct{}

func (s *stubQueryService) GetDocument(context.Context, string) (common.Document, error) {
	return common.Document{}, nil
}

func (s *stubQueryService) CreateDocument(context.Context, common.Document) error {
	return nil
}

func (s *stubQueryService) ReplaceDocument(context.Context, common.Document, storage.Filters) (common.Document, error) {
	return common.Document{}, nil
}

func (s *stubQueryService) PatchDocument(context.Context, common.Document, storage.Filters) (common.Document, error) {
	return common.Document{}, nil
}

func (s *stubQueryService) DeleteDocument(context.Context, string) error {
	return nil
}

func (s *stubQueryService) ExecuteQuery(context.Context, storage.Query) ([]common.Document, error) {
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

func (s *stubQueryService) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	return fn(ctx, s)
}
