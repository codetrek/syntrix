package router

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock Router
type mockDocRouter struct {
	mock.Mock
}

func (m *mockDocRouter) Select(op types.OpKind) types.DocumentStore {
	args := m.Called(op)
	return args.Get(0).(types.DocumentStore)
}

// Mock Store
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
	args := m.Called(ctx, doc)
	return args.Error(0)
}

func (m *mockDocumentStore) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *mockDocumentStore) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, path, data, pred)
	return args.Error(0)
}

func (m *mockDocumentStore) Delete(ctx context.Context, path string, pred model.Filters) error {
	args := m.Called(ctx, path, pred)
	return args.Error(0)
}

func (m *mockDocumentStore) Query(ctx context.Context, q model.Query) ([]*types.Document, error) {
	args := m.Called(ctx, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.Document), args.Error(1)
}

func (m *mockDocumentStore) Watch(ctx context.Context, collection string, resumeToken interface{}, opts types.WatchOptions) (<-chan types.Event, error) {
	args := m.Called(ctx, collection, resumeToken, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan types.Event), args.Error(1)
}

func (m *mockDocumentStore) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestRoutedDocumentStore(t *testing.T) {
	ctx := context.Background()

	t.Run("Get uses Read op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpRead).Return(store)
		store.On("Get", ctx, "path").Return(&types.Document{}, nil)

		rs := NewRoutedDocumentStore(router)
		_, err := rs.Get(ctx, "path")

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Create uses Write op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpWrite).Return(store)
		store.On("Create", ctx, mock.Anything).Return(nil)

		rs := NewRoutedDocumentStore(router)
		err := rs.Create(ctx, &types.Document{})

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Update uses Write op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpWrite).Return(store)
		store.On("Update", ctx, "path", mock.Anything, mock.Anything).Return(nil)

		rs := NewRoutedDocumentStore(router)
		err := rs.Update(ctx, "path", nil, nil)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Close does nothing", func(t *testing.T) {
		router := new(mockDocRouter)
		rs := NewRoutedDocumentStore(router)
		err := rs.Close(ctx)
		assert.NoError(t, err)
	})

	t.Run("Patch uses Write op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpWrite).Return(store)
		store.On("Patch", ctx, "path", mock.Anything, mock.Anything).Return(nil)

		rs := NewRoutedDocumentStore(router)
		err := rs.Patch(ctx, "path", nil, nil)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Delete uses Write op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpWrite).Return(store)
		store.On("Delete", ctx, "path", mock.Anything).Return(nil)

		rs := NewRoutedDocumentStore(router)
		err := rs.Delete(ctx, "path", nil)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Query uses Read op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpRead).Return(store)
		store.On("Query", ctx, mock.Anything).Return([]*types.Document{}, nil)

		rs := NewRoutedDocumentStore(router)
		_, err := rs.Query(ctx, model.Query{})

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Watch uses Read op", func(t *testing.T) {
		router := new(mockDocRouter)
		store := new(mockDocumentStore)

		router.On("Select", types.OpRead).Return(store)
		store.On("Watch", ctx, "col", nil, mock.Anything).Return(make(<-chan types.Event), nil)

		rs := NewRoutedDocumentStore(router)
		_, err := rs.Watch(ctx, "col", nil, types.WatchOptions{})

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})
}

// Mock User Router & Store
type mockUserRouter struct {
	mock.Mock
}

func (m *mockUserRouter) Select(op types.OpKind) types.UserStore {
	args := m.Called(op)
	return args.Get(0).(types.UserStore)
}

type mockUserStoreImpl struct {
	mock.Mock
}

func (m *mockUserStoreImpl) CreateUser(ctx context.Context, user *types.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *mockUserStoreImpl) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil { return nil, args.Error(1) }
	return args.Get(0).(*types.User), args.Error(1)
}
func (m *mockUserStoreImpl) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil { return nil, args.Error(1) }
	return args.Get(0).(*types.User), args.Error(1)
}
func (m *mockUserStoreImpl) ListUsers(ctx context.Context, limit int, offset int) ([]*types.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil { return nil, args.Error(1) }
	return args.Get(0).([]*types.User), args.Error(1)
}
func (m *mockUserStoreImpl) UpdateUser(ctx context.Context, user *types.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *mockUserStoreImpl) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return m.Called(ctx, id, lastLogin, attempts, lockoutUntil).Error(0)
}
func (m *mockUserStoreImpl) EnsureIndexes(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockUserStoreImpl) Close(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func TestRoutedUserStore(t *testing.T) {
	ctx := context.Background()

	t.Run("GetUserByID uses Read op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpRead).Return(store)
		store.On("GetUserByID", ctx, "id").Return(&types.User{}, nil)

		rs := NewRoutedUserStore(router)
		_, err := rs.GetUserByID(ctx, "id")

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("CreateUser uses Write op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("CreateUser", ctx, mock.Anything).Return(nil)

		rs := NewRoutedUserStore(router)
		err := rs.CreateUser(ctx, &types.User{})

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("GetUserByUsername uses Read op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpRead).Return(store)
		store.On("GetUserByUsername", ctx, "user").Return(&types.User{}, nil)

		rs := NewRoutedUserStore(router)
		_, err := rs.GetUserByUsername(ctx, "user")

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("ListUsers uses Read op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpRead).Return(store)
		store.On("ListUsers", ctx, 10, 0).Return([]*types.User{}, nil)

		rs := NewRoutedUserStore(router)
		_, err := rs.ListUsers(ctx, 10, 0)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("UpdateUser uses Write op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("UpdateUser", ctx, mock.Anything).Return(nil)

		rs := NewRoutedUserStore(router)
		err := rs.UpdateUser(ctx, &types.User{})

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("UpdateUserLoginStats uses Write op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("UpdateUserLoginStats", ctx, "id", mock.Anything, 1, mock.Anything).Return(nil)

		rs := NewRoutedUserStore(router)
		err := rs.UpdateUserLoginStats(ctx, "id", time.Now(), 1, time.Now())

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("EnsureIndexes uses Write op", func(t *testing.T) {
		router := new(mockUserRouter)
		store := new(mockUserStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("EnsureIndexes", ctx).Return(nil)

		rs := NewRoutedUserStore(router)
		err := rs.EnsureIndexes(ctx)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Close does nothing", func(t *testing.T) {
		router := new(mockUserRouter)
		rs := NewRoutedUserStore(router)
		err := rs.Close(ctx)
		assert.NoError(t, err)
	})
}

// Mock Revocation Router & Store
type mockRevRouter struct {
	mock.Mock
}

func (m *mockRevRouter) Select(op types.OpKind) types.TokenRevocationStore {
	args := m.Called(op)
	return args.Get(0).(types.TokenRevocationStore)
}

type mockRevStoreImpl struct {
	mock.Mock
}

func (m *mockRevStoreImpl) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	return m.Called(ctx, jti, expiresAt).Error(0)
}
func (m *mockRevStoreImpl) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return m.Called(ctx, jti, expiresAt).Error(0)
}
func (m *mockRevStoreImpl) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	args := m.Called(ctx, jti, gracePeriod)
	return args.Bool(0), args.Error(1)
}
func (m *mockRevStoreImpl) EnsureIndexes(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockRevStoreImpl) Close(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func TestRoutedRevocationStore(t *testing.T) {
	ctx := context.Background()

	t.Run("RevokeToken uses Write op", func(t *testing.T) {
		router := new(mockRevRouter)
		store := new(mockRevStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("RevokeToken", ctx, "jti", mock.Anything).Return(nil)

		rs := NewRoutedRevocationStore(router)
		err := rs.RevokeToken(ctx, "jti", time.Now())

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("RevokeTokenImmediate uses Write op", func(t *testing.T) {
		router := new(mockRevRouter)
		store := new(mockRevStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("RevokeTokenImmediate", ctx, "jti", mock.Anything).Return(nil)

		rs := NewRoutedRevocationStore(router)
		err := rs.RevokeTokenImmediate(ctx, "jti", time.Now())

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("IsRevoked uses Read op", func(t *testing.T) {
		router := new(mockRevRouter)
		store := new(mockRevStoreImpl)

		router.On("Select", types.OpRead).Return(store)
		store.On("IsRevoked", ctx, "jti", mock.Anything).Return(false, nil)

		rs := NewRoutedRevocationStore(router)
		_, err := rs.IsRevoked(ctx, "jti", time.Minute)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("EnsureIndexes uses Write op", func(t *testing.T) {
		router := new(mockRevRouter)
		store := new(mockRevStoreImpl)

		router.On("Select", types.OpWrite).Return(store)
		store.On("EnsureIndexes", ctx).Return(nil)

		rs := NewRoutedRevocationStore(router)
		err := rs.EnsureIndexes(ctx)

		assert.NoError(t, err)
		router.AssertExpectations(t)
		store.AssertExpectations(t)
	})

	t.Run("Close does nothing", func(t *testing.T) {
		router := new(mockRevRouter)
		rs := NewRoutedRevocationStore(router)
		err := rs.Close(ctx)
		assert.NoError(t, err)
	})
}
