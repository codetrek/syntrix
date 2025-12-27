package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthProvider(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider, err := NewAuthProvider(ctx, testMongoURI, env.DBName)
	if err != nil {
		t.Skipf("Skipping test: MongoDB not available: %v", err)
	}
	require.NoError(t, err)
	defer provider.Close(ctx)

	assert.NotNil(t, provider.Users())
	assert.NotNil(t, provider.Revocations())
}
