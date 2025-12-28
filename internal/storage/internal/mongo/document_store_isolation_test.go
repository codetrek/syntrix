package mongo

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentStore_TenantIsolation(t *testing.T) {
	env := setupTestEnv(t)
	store := NewDocumentStore(env.Client, env.DB, "docs_isolation", "sys_isolation", 0)
	ctx := context.Background()

	// Ensure indexes
	if ds, ok := store.(interface{ EnsureIndexes(context.Context) error }); ok {
		require.NoError(t, ds.EnsureIndexes(ctx))
	}

	tenantA := "tenantA"
	tenantB := "tenantB"
	path := "users/user1"

	// 1. Create document in Tenant A
	docA := types.NewDocument(tenantA, path, "users", map[string]interface{}{
		"name": "User A",
	})
	err := store.Create(ctx, tenantA, docA)
	require.NoError(t, err)

	// 2. Verify Tenant A can read it
	retrievedA, err := store.Get(ctx, tenantA, path)
	require.NoError(t, err)
	assert.Equal(t, "User A", retrievedA.Data["name"])

	// 3. Verify Tenant B CANNOT read it
	_, err = store.Get(ctx, tenantB, path)
	assert.ErrorIs(t, err, model.ErrNotFound, "Tenant B should not see Tenant A's document")

	// 4. Verify Tenant B CANNOT update it
	err = store.Update(ctx, tenantB, path, map[string]interface{}{"name": "Hacked"}, nil)
	assert.ErrorIs(t, err, model.ErrNotFound, "Tenant B should not be able to update Tenant A's document")

	// 5. Verify Tenant B CANNOT delete it
	err = store.Delete(ctx, tenantB, path, nil)
	assert.ErrorIs(t, err, model.ErrNotFound, "Tenant B should not be able to delete Tenant A's document")

	// 6. Create document with SAME path in Tenant B
	docB := types.NewDocument(tenantB, path, "users", map[string]interface{}{
		"name": "User B",
	})
	err = store.Create(ctx, tenantB, docB)
	require.NoError(t, err)

	// 7. Verify both exist and are different
	retrievedA, err = store.Get(ctx, tenantA, path)
	require.NoError(t, err)
	assert.Equal(t, "User A", retrievedA.Data["name"])

	retrievedB, err := store.Get(ctx, tenantB, path)
	require.NoError(t, err)
	assert.Equal(t, "User B", retrievedB.Data["name"])

	// 8. Verify IDs are different
	assert.NotEqual(t, retrievedA.Id, retrievedB.Id)
	assert.Contains(t, retrievedA.Id, tenantA+":")
	assert.Contains(t, retrievedB.Id, tenantB+":")
}

func TestDocumentStore_TenantIDGeneration(t *testing.T) {
	tenant := "mytenant"
	path := "some/path"

	// Calculate expected ID
	expectedID := types.CalculateTenantID(tenant, path)

	// Create doc
	doc := types.NewDocument(tenant, path, "some", nil)

	assert.Equal(t, expectedID, doc.Id)
	assert.Equal(t, tenant, doc.TenantID)
}
