package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentProvider(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider, err := NewDocumentProvider(ctx, testMongoURI, env.DBName, "docs", "sys", 0)
	if err != nil {
		t.Skipf("Skipping test: MongoDB not available: %v", err)
	}
	require.NoError(t, err)

	assert.NotNil(t, provider.Document())

	err = provider.Close(ctx)
	assert.NoError(t, err)
}
