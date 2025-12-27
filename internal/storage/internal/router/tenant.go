package router

import (
	"errors"

	"github.com/codetrek/syntrix/internal/storage/types"
)

var (
	ErrTenantNotFound = errors.New("tenant not found")
	ErrTenantRequired = errors.New("tenant is required")
)

// TenantDocumentRouter routes operations based on tenant
type TenantDocumentRouter struct {
	defaultRouter types.DocumentRouter
	tenants       map[string]types.DocumentRouter
}

func NewTenantDocumentRouter(defaultRouter types.DocumentRouter, tenants map[string]types.DocumentRouter) types.DocumentRouter {
	return &TenantDocumentRouter{
		defaultRouter: defaultRouter,
		tenants:       tenants,
	}
}

func (r *TenantDocumentRouter) Select(tenant string, op types.OpKind) (types.DocumentStore, error) {
	router, ok := r.tenants[tenant]
	if !ok {
		router = r.defaultRouter
	}

	if router == nil {
		if tenant == "" {
			return nil, ErrTenantRequired
		}
		return nil, ErrTenantNotFound
	}

	return router.Select(tenant, op)
}

// TenantUserRouter routes operations based on tenant
type TenantUserRouter struct {
	defaultRouter types.UserRouter
	tenants       map[string]types.UserRouter
}

func NewTenantUserRouter(defaultRouter types.UserRouter, tenants map[string]types.UserRouter) types.UserRouter {
	return &TenantUserRouter{
		defaultRouter: defaultRouter,
		tenants:       tenants,
	}
}

func (r *TenantUserRouter) Select(tenant string, op types.OpKind) (types.UserStore, error) {
	if tenant == "" {
		return nil, ErrTenantRequired
	}

	router, ok := r.tenants[tenant]
	if !ok {
		router = r.defaultRouter
	}

	if router == nil {
		return nil, ErrTenantNotFound
	}

	return router.Select(tenant, op)
}

// TenantRevocationRouter routes operations based on tenant
type TenantRevocationRouter struct {
	defaultRouter types.RevocationRouter
	tenants       map[string]types.RevocationRouter
}

func NewTenantRevocationRouter(defaultRouter types.RevocationRouter, tenants map[string]types.RevocationRouter) types.RevocationRouter {
	return &TenantRevocationRouter{
		defaultRouter: defaultRouter,
		tenants:       tenants,
	}
}

func (r *TenantRevocationRouter) Select(tenant string, op types.OpKind) (types.TokenRevocationStore, error) {
	if tenant == "" {
		return nil, ErrTenantRequired
	}

	router, ok := r.tenants[tenant]
	if !ok {
		router = r.defaultRouter
	}

	if router == nil {
		return nil, ErrTenantNotFound
	}

	return router.Select(tenant, op)
}
