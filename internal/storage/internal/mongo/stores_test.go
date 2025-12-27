package mongo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDocumentStore(t *testing.T) {
	env := setupTestEnv(t)

	store := NewDocumentStore(env.Client, env.DB, "docs", "sys", 0)
	assert.NotNil(t, store)
}

func TestUserStore_Close(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	store := NewUserStore(env.DB, "")
	err := store.Close(ctx)
	assert.NoError(t, err)
}

func TestRevocationStore_Close(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	store := NewRevocationStore(env.DB, "")
	err := store.Close(ctx)
	assert.NoError(t, err)
}
