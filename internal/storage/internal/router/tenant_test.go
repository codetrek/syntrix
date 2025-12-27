package router

import (
	"testing"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/stretchr/testify/assert"
)

type mockTenantDocStore struct {
	types.DocumentStore
}

type mockTenantUserStore struct {
	types.UserStore
}

type mockTenantRevStore struct {
	types.TokenRevocationStore
}

func TestTenantDocumentRouter(t *testing.T) {
	defaultStore := &mockTenantDocStore{}
	tenantStore := &mockTenantDocStore{}

	defaultRouter := NewSingleDocumentRouter(defaultStore)
	tenantRouter := NewSingleDocumentRouter(tenantStore)

	tenants := map[string]types.DocumentRouter{
		"t1": tenantRouter,
	}

	r := NewTenantDocumentRouter(defaultRouter, tenants)

	// Case 1: Tenant found
	s, err := r.Select("t1", types.OpRead)
	assert.NoError(t, err)
	assert.Equal(t, tenantStore, s)

	// Case 2: Tenant not found, use default
	s2, err2 := r.Select("t2", types.OpRead)
	assert.NoError(t, err2)
	assert.Equal(t, defaultStore, s2)

	// Case 3: Tenant empty -> use default
	s3, err3 := r.Select("", types.OpRead)
	assert.NoError(t, err3)
	assert.Equal(t, defaultStore, s3)

	// Case 4: No default router
	rNoDefault := NewTenantDocumentRouter(nil, tenants)
	_, err4 := rNoDefault.Select("t2", types.OpRead)
	assert.ErrorIs(t, err4, ErrTenantNotFound)

	// Case 5: Tenant required (no default)
	_, err5 := rNoDefault.Select("", types.OpRead)
	assert.ErrorIs(t, err5, ErrTenantRequired)
}

func TestTenantUserRouter(t *testing.T) {
	defaultStore := &mockTenantUserStore{}
	tenantStore := &mockTenantUserStore{}

	defaultRouter := NewSingleUserRouter(defaultStore)
	tenantRouter := NewSingleUserRouter(tenantStore)

	tenants := map[string]types.UserRouter{
		"t1": tenantRouter,
	}

	r := NewTenantUserRouter(defaultRouter, tenants)

	// Case 1: Tenant found
	s, err := r.Select("t1", types.OpRead)
	assert.NoError(t, err)
	assert.Equal(t, tenantStore, s)

	// Case 2: Tenant not found, use default
	s2, err2 := r.Select("t2", types.OpRead)
	assert.NoError(t, err2)
	assert.Equal(t, defaultStore, s2)

	// Case 3: Tenant required
	_, err3 := r.Select("", types.OpRead)
	assert.ErrorIs(t, err3, ErrTenantRequired)

	// Case 4: No default router
	rNoDefault := NewTenantUserRouter(nil, tenants)
	_, err4 := rNoDefault.Select("t2", types.OpRead)
	assert.ErrorIs(t, err4, ErrTenantNotFound)
}

func TestTenantRevocationRouter(t *testing.T) {
	defaultStore := &mockTenantRevStore{}
	tenantStore := &mockTenantRevStore{}

	defaultRouter := NewSingleRevocationRouter(defaultStore)
	tenantRouter := NewSingleRevocationRouter(tenantStore)

	tenants := map[string]types.RevocationRouter{
		"t1": tenantRouter,
	}

	r := NewTenantRevocationRouter(defaultRouter, tenants)

	// Case 1: Tenant found
	s, err := r.Select("t1", types.OpRead)
	assert.NoError(t, err)
	assert.Equal(t, tenantStore, s)

	// Case 2: Tenant not found, use default
	s2, err2 := r.Select("t2", types.OpRead)
	assert.NoError(t, err2)
	assert.Equal(t, defaultStore, s2)

	// Case 3: Tenant required
	_, err3 := r.Select("", types.OpRead)
	assert.ErrorIs(t, err3, ErrTenantRequired)

	// Case 4: No default router
	rNoDefault := NewTenantRevocationRouter(nil, tenants)
	_, err4 := rNoDefault.Select("t2", types.OpRead)
	assert.ErrorIs(t, err4, ErrTenantNotFound)
}
