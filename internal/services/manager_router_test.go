package services

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mocks
type mockDocumentProvider struct {
	mock.Mock
}

func (m *mockDocumentProvider) Document() storage.DocumentStore {
	args := m.Called()
	return args.Get(0).(storage.DocumentStore)
}

func (m *mockDocumentProvider) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type mockAuthProvider struct {
	mock.Mock
}

func (m *mockAuthProvider) Users() storage.UserStore {
	args := m.Called()
	return args.Get(0).(storage.UserStore)
}

func (m *mockAuthProvider) Revocations() storage.TokenRevocationStore {
	args := m.Called()
	return args.Get(0).(storage.TokenRevocationStore)
}

func (m *mockAuthProvider) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type mockDocumentStore struct {
	mock.Mock
}

func (m *mockDocumentStore) Get(ctx context.Context, path string) (*types.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Document), args.Error(1)
}
func (m *mockDocumentStore) Create(ctx context.Context, doc *types.Document) error {
	return m.Called(ctx, doc).Error(0)
}
func (m *mockDocumentStore) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return m.Called(ctx, path, data, pred).Error(0)
}
func (m *mockDocumentStore) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return m.Called(ctx, path, data, pred).Error(0)
}
func (m *mockDocumentStore) Delete(ctx context.Context, path string, pred model.Filters) error {
	return m.Called(ctx, path, pred).Error(0)
}
func (m *mockDocumentStore) Query(ctx context.Context, q model.Query) ([]*types.Document, error) {
	args := m.Called(ctx, q)
	return args.Get(0).([]*types.Document), args.Error(1)
}
func (m *mockDocumentStore) Watch(ctx context.Context, collection string, resumeToken interface{}, opts types.WatchOptions) (<-chan types.Event, error) {
	args := m.Called(ctx, collection, resumeToken, opts)
	return args.Get(0).(<-chan types.Event), args.Error(1)
}
func (m *mockDocumentStore) Close(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

type mockUserStore struct {
	mock.Mock
}

func (m *mockUserStore) CreateUser(ctx context.Context, user *types.User) error {
	return nil
}
func (m *mockUserStore) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	return nil, nil
}
func (m *mockUserStore) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	return nil, nil
}
func (m *mockUserStore) ListUsers(ctx context.Context, limit int, offset int) ([]*types.User, error) {
	return nil, nil
}
func (m *mockUserStore) UpdateUser(ctx context.Context, user *types.User) error {
	return nil
}
func (m *mockUserStore) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return nil
}
func (m *mockUserStore) EnsureIndexes(ctx context.Context) error {
	return nil
}
func (m *mockUserStore) Close(ctx context.Context) error {
	return nil
}

type mockRevocationStore struct {
	mock.Mock
}

func (m *mockRevocationStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (m *mockRevocationStore) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (m *mockRevocationStore) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	return false, nil
}
func (m *mockRevocationStore) EnsureIndexes(ctx context.Context) error {
	return nil
}
func (m *mockRevocationStore) Close(ctx context.Context) error {
	return nil
}

func TestManager_Init_RouterWiring(t *testing.T) {
	// Save original factories and restore after test
	origFactory := storageFactoryFactory
	defer func() {
		storageFactoryFactory = origFactory
	}()

	// Setup mocks
	mockDocStore := new(mockDocumentStore)
	mockUsrStore := new(mockUserStore)
	mockRevStore := new(mockRevocationStore)

	// Override factories
	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return &fakeStorageFactory{
			docStore: mockDocStore,
			usrStore: mockUsrStore,
			revStore: mockRevStore,
		}, nil
	}

	// Initialize Manager
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunQuery: true}) // RunQuery triggers initStorage

	err := mgr.Init(context.Background())
	assert.NoError(t, err)

	// Verify Stores are initialized and wired correctly
	assert.NotNil(t, mgr.docStore)
	assert.NotNil(t, mgr.userStore)
	assert.NotNil(t, mgr.revocationStore)

	// Verify Stores route to the mocked stores
	// Since we use RoutedStore, we can't directly compare equality of the store object itself easily
	// without exposing the inner router. But we can verify behavior or check if it's not nil.
	// For now, just checking not nil is a basic check.
	// To be more rigorous, we could call a method and see if it hits the mock.

	mockDocStore.On("Get", mock.Anything, "test").Return(&types.Document{}, nil)
	_, _ = mgr.docStore.Get(context.Background(), "test")
	mockDocStore.AssertCalled(t, "Get", mock.Anything, "test")
}
