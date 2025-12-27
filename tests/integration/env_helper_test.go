package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvHelper(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test setupServiceEnv
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	// Test GenerateSystemToken
	t.Run("GenerateSystemToken", func(t *testing.T) {
		token := env.GenerateSystemToken(t)
		assert.NotEmpty(t, token)
	})

	// Test GetToken
	t.Run("UserManagement", func(t *testing.T) {
		username := "testuser_helper"
		role := "user"

		token := env.GetToken(t, username, role)
		assert.NotEmpty(t, token)
	})

	// Test MakeRequest
	t.Run("MakeRequest", func(t *testing.T) {
		resp := env.MakeRequest(t, http.MethodGet, "/health", nil, "")
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
