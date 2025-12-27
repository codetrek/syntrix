package authn

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

type MockStorage struct {
	mock.Mock
}

func (m *MockStorage) CreateUser(ctx context.Context, tenant string, user *User) error {
	args := m.Called(ctx, tenant, user)
	return args.Error(0)
}

func (m *MockStorage) GetUserByUsername(ctx context.Context, tenant string, username string) (*User, error) {
	args := m.Called(ctx, tenant, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStorage) GetUserByID(ctx context.Context, tenant string, id string) (*User, error) {
	args := m.Called(ctx, tenant, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStorage) UpdateUserLoginStats(ctx context.Context, tenant string, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	args := m.Called(ctx, tenant, id, lastLogin, attempts, lockoutUntil)
	return args.Error(0)
}

func (m *MockStorage) RevokeToken(ctx context.Context, tenant string, jti string, expiresAt time.Time) error {
	args := m.Called(ctx, tenant, jti, expiresAt)
	return args.Error(0)
}

func (m *MockStorage) RevokeTokenImmediate(ctx context.Context, tenant string, jti string, expiresAt time.Time) error {
	args := m.Called(ctx, tenant, jti, expiresAt)
	return args.Error(0)
}

func (m *MockStorage) IsRevoked(ctx context.Context, tenant string, jti string, gracePeriod time.Duration) (bool, error) {
	args := m.Called(ctx, tenant, jti, gracePeriod)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorage) ListUsers(ctx context.Context, tenant string, limit int, offset int) ([]*User, error) {
	args := m.Called(ctx, tenant, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*User), args.Error(1)
}

func (m *MockStorage) UpdateUser(ctx context.Context, tenant string, user *User) error {
	args := m.Called(ctx, tenant, user)
	return args.Error(0)
}

func (m *MockStorage) EnsureIndexes(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockStorage) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
