package router

import (
	"context"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
)

// RoutedDocumentStore implements DocumentStore by routing operations
type RoutedDocumentStore struct {
	router types.DocumentRouter
}

func NewRoutedDocumentStore(router types.DocumentRouter) types.DocumentStore {
	return &RoutedDocumentStore{router: router}
}

func (s *RoutedDocumentStore) Get(ctx context.Context, path string) (*types.Document, error) {
	return s.router.Select(types.OpRead).Get(ctx, path)
}

func (s *RoutedDocumentStore) Create(ctx context.Context, doc *types.Document) error {
	return s.router.Select(types.OpWrite).Create(ctx, doc)
}

func (s *RoutedDocumentStore) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return s.router.Select(types.OpWrite).Update(ctx, path, data, pred)
}

func (s *RoutedDocumentStore) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return s.router.Select(types.OpWrite).Patch(ctx, path, data, pred)
}

func (s *RoutedDocumentStore) Delete(ctx context.Context, path string, pred model.Filters) error {
	return s.router.Select(types.OpWrite).Delete(ctx, path, pred)
}

func (s *RoutedDocumentStore) Query(ctx context.Context, q model.Query) ([]*types.Document, error) {
	return s.router.Select(types.OpRead).Query(ctx, q)
}

func (s *RoutedDocumentStore) Watch(ctx context.Context, collection string, resumeToken interface{}, opts types.WatchOptions) (<-chan types.Event, error) {
	return s.router.Select(types.OpRead).Watch(ctx, collection, resumeToken, opts)
}

func (s *RoutedDocumentStore) Close(ctx context.Context) error {
	// We don't close the underlying store here as it might be shared.
	// The Provider manages lifecycle.
	return nil
}

// RoutedUserStore implements UserStore by routing operations
type RoutedUserStore struct {
	router types.UserRouter
}

func NewRoutedUserStore(router types.UserRouter) types.UserStore {
	return &RoutedUserStore{router: router}
}

func (s *RoutedUserStore) CreateUser(ctx context.Context, user *types.User) error {
	return s.router.Select(types.OpWrite).CreateUser(ctx, user)
}

func (s *RoutedUserStore) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	return s.router.Select(types.OpRead).GetUserByUsername(ctx, username)
}

func (s *RoutedUserStore) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	return s.router.Select(types.OpRead).GetUserByID(ctx, id)
}

func (s *RoutedUserStore) ListUsers(ctx context.Context, limit int, offset int) ([]*types.User, error) {
	return s.router.Select(types.OpRead).ListUsers(ctx, limit, offset)
}

func (s *RoutedUserStore) UpdateUser(ctx context.Context, user *types.User) error {
	return s.router.Select(types.OpWrite).UpdateUser(ctx, user)
}

func (s *RoutedUserStore) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return s.router.Select(types.OpWrite).UpdateUserLoginStats(ctx, id, lastLogin, attempts, lockoutUntil)
}

func (s *RoutedUserStore) EnsureIndexes(ctx context.Context) error {
	return s.router.Select(types.OpWrite).EnsureIndexes(ctx)
}

func (s *RoutedUserStore) Close(ctx context.Context) error {
	return nil
}

// RoutedRevocationStore implements TokenRevocationStore by routing operations
type RoutedRevocationStore struct {
	router types.RevocationRouter
}

func NewRoutedRevocationStore(router types.RevocationRouter) types.TokenRevocationStore {
	return &RoutedRevocationStore{router: router}
}

func (s *RoutedRevocationStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	return s.router.Select(types.OpWrite).RevokeToken(ctx, jti, expiresAt)
}

func (s *RoutedRevocationStore) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return s.router.Select(types.OpWrite).RevokeTokenImmediate(ctx, jti, expiresAt)
}

func (s *RoutedRevocationStore) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	return s.router.Select(types.OpRead).IsRevoked(ctx, jti, gracePeriod)
}

func (s *RoutedRevocationStore) EnsureIndexes(ctx context.Context) error {
	return s.router.Select(types.OpWrite).EnsureIndexes(ctx)
}

func (s *RoutedRevocationStore) Close(ctx context.Context) error {
	return nil
}
