package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/stretchr/testify/assert"
)

func TestNewHandler_Panic(t *testing.T) {
	assert.Panics(t, func() {
		NewHandler(&MockQueryService{}, nil, nil)
	})
}

func TestGetTenantId(t *testing.T) {
	h := &Handler{}

	// Case 1: Tenant present
	ctx := context.WithValue(context.Background(), ContextKeyTenant, "t1")
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	tenant, err := h.getTenantId(req)
	assert.NoError(t, err)
	assert.Equal(t, "t1", tenant)

	// Case 2: Tenant missing
	req2 := httptest.NewRequest("GET", "/", nil)
	tenant2, err2 := h.getTenantId(req2)
	assert.Error(t, err2)
	assert.Equal(t, "", tenant2)
}

func TestTenantOrError(t *testing.T) {
	h := &Handler{}

	// Case 1: Tenant present
	ctx := context.WithValue(context.Background(), ContextKeyTenant, "t1")
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	tenant, ok := h.tenantOrError(w, req)
	assert.True(t, ok)
	assert.Equal(t, "t1", tenant)
	assert.Equal(t, http.StatusOK, w.Code)

	// Case 2: Tenant missing
	req2 := httptest.NewRequest("GET", "/", nil)
	w2 := httptest.NewRecorder()
	tenant2, ok2 := h.tenantOrError(w2, req2)
	assert.False(t, ok2)
	assert.Equal(t, "", tenant2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)
}

func TestClaimsToMap(t *testing.T) {
	// Case 1: Nil claims
	assert.Nil(t, claimsToMap(nil))

	// Case 2: Valid claims
	claims := &identity.Claims{
		TenantID: "t1",
		UserID:   "u1",
		Username: "user1",
		Roles:    []string{"admin"},
	}
	m := claimsToMap(claims)
	assert.Equal(t, "t1", m["tid"])
	assert.Equal(t, "u1", m["oid"])
	assert.Equal(t, "user1", m["username"])
	assert.Equal(t, []string{"admin"}, m["roles"])
}
