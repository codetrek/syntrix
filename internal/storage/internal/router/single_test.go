package router

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

type fakeDocumentStore struct{}

func (f *fakeDocumentStore) Get(ctx context.Context, path string) (*types.Document, error) { return nil, nil }
func (f *fakeDocumentStore) Create(ctx context.Context, doc *types.Document) error { return nil }
func (f *fakeDocumentStore) Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error {
	return nil
}
func (f *fakeDocumentStore) Delete(ctx context.Context, path string, pred model.Filters) error { return nil }
func (f *fakeDocumentStore) Query(ctx context.Context, q model.Query) ([]*types.Document, error) { return nil, nil }
func (f *fakeDocumentStore) Watch(ctx context.Context, collection string, resumeToken interface{}, opts types.WatchOptions) (<-chan types.Event, error) {
	return nil, nil
}
func (f *fakeDocumentStore) Close(ctx context.Context) error { return nil }

// Ensure indexes isn't part of DocumentStore; we call it on concrete implementations during provider init.

type fakeUserStore struct{}

func (f *fakeUserStore) CreateUser(ctx context.Context, user *types.User) error { return nil }
func (f *fakeUserStore) GetUserByUsername(ctx context.Context, username string) (*types.User, error) { return nil, nil }
func (f *fakeUserStore) GetUserByID(ctx context.Context, id string) (*types.User, error) { return nil, nil }
func (f *fakeUserStore) UpdateUser(ctx context.Context, user *types.User) error { return nil }
func (f *fakeUserStore) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	return nil
}
func (f *fakeUserStore) ListUsers(ctx context.Context, limit, offset int) ([]*types.User, error) { return nil, nil }
func (f *fakeUserStore) EnsureIndexes(ctx context.Context) error { return nil }
func (f *fakeUserStore) Close(ctx context.Context) error { return nil }

// Fake revocation store

type fakeRevocationStore struct{}

func (f *fakeRevocationStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error { return nil }
func (f *fakeRevocationStore) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	return nil
}
func (f *fakeRevocationStore) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	return false, nil
}
func (f *fakeRevocationStore) EnsureIndexes(ctx context.Context) error { return nil }
func (f *fakeRevocationStore) Close(ctx context.Context) error { return nil }

func TestSingleRouter_Selectors(t *testing.T) {
	doc := &fakeDocumentStore{}
	usr := &fakeUserStore{}
	rev := &fakeRevocationStore{}

	r := NewSingleRouter(doc, usr, rev)
	assert.Equal(t, doc, r.SelectDocument(types.OpRead))
	assert.Equal(t, doc, r.SelectDocument(types.OpWrite))
	assert.Equal(t, doc, r.SelectDocument(types.OpMigrate))
	assert.Equal(t, usr, r.SelectUser(types.OpRead))
	assert.Equal(t, usr, r.SelectUser(types.OpWrite))
	assert.Equal(t, usr, r.SelectUser(types.OpMigrate))
	assert.Equal(t, rev, r.SelectRevocation(types.OpRead))
	assert.Equal(t, rev, r.SelectRevocation(types.OpWrite))
	assert.Equal(t, rev, r.SelectRevocation(types.OpMigrate))
}

func TestSingleSplitRouters(t *testing.T) {
	doc := &fakeDocumentStore{}
	usr := &fakeUserStore{}
	rev := &fakeRevocationStore{}

	rd := NewSingleDocumentRouter(doc)
	assert.Equal(t, doc, rd.Select(types.OpRead))

	ru := NewSingleUserRouter(usr)
	assert.Equal(t, usr, ru.Select(types.OpRead))

	rr := NewSingleRevocationRouter(rev)
	assert.Equal(t, rev, rr.Select(types.OpRead))
}
