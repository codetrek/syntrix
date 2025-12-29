package authn

import (
	"context"
	"errors"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestListUsers_Coverage(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)

	t.Run("Missing Tenant in Context", func(t *testing.T) {
		ctx := context.Background()
		users, err := svc.ListUsers(ctx, 10, 0)
		assert.ErrorIs(t, err, ErrTenantRequired)
		assert.Nil(t, users)
	})

	t.Run("Success", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, "tenant1")
		expectedUsers := []*User{{ID: "u1", Username: "user1"}}

		mockStorage.On("ListUsers", ctx, "tenant1", 10, 0).Return(expectedUsers, nil).Once()

		users, err := svc.ListUsers(ctx, 10, 0)
		assert.NoError(t, err)
		assert.Equal(t, expectedUsers, users)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Storage Error", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, "tenant1")

		mockStorage.On("ListUsers", ctx, "tenant1", 10, 0).Return(nil, errors.New("db error")).Once()

		users, err := svc.ListUsers(ctx, 10, 0)
		assert.Error(t, err)
		assert.Nil(t, users)
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateUser_Coverage(t *testing.T) {
	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)

	t.Run("Missing Tenant in Context", func(t *testing.T) {
		ctx := context.Background()
		err := svc.UpdateUser(ctx, "u1", []string{"admin"}, false)
		assert.ErrorIs(t, err, ErrTenantRequired)
	})

	t.Run("User Not Found", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, "tenant1")

		mockStorage.On("GetUserByID", ctx, "tenant1", "u1").Return(nil, errors.New("not found")).Once()

		err := svc.UpdateUser(ctx, "u1", []string{"admin"}, false)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Success", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, "tenant1")
		user := &User{ID: "u1", TenantID: "tenant1", Roles: []string{"user"}}

		mockStorage.On("GetUserByID", ctx, "tenant1", "u1").Return(user, nil).Once()
		mockStorage.On("UpdateUser", ctx, "tenant1", mock.MatchedBy(func(u *User) bool {
			return u.ID == "u1" && u.Disabled == true && len(u.Roles) == 1 && u.Roles[0] == "admin"
		})).Return(nil).Once()

		err := svc.UpdateUser(ctx, "u1", []string{"admin"}, true)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestTenantFromContext_Coverage(t *testing.T) {
	// This function is private, but we can test it via public methods like ListUsers
	// We already covered "Missing Tenant" (nil value) in TestListUsers_Coverage.
	// Let's cover "Invalid Type" or "Empty String" if possible.

	mockStorage := new(MockStorage)
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}
	svc, err := NewAuthService(cfg, mockStorage, mockStorage)
	require.NoError(t, err)

	t.Run("Empty Tenant String", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, "")
		_, err := svc.ListUsers(ctx, 10, 0)
		assert.ErrorIs(t, err, ErrTenantRequired)
	})

	t.Run("Invalid Tenant Type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyTenant, 123) // Not a string
		_, err := svc.ListUsers(ctx, 10, 0)
		assert.ErrorIs(t, err, ErrTenantRequired)
	})
}
