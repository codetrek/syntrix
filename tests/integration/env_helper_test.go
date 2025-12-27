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

		// Call again to trigger Login path
		token2 := env.GetToken(t, username, role)
		assert.NotEmpty(t, token2)
	})

	t.Run("UserManagement_AdminRole", func(t *testing.T) {
		username := "testadmin_helper"
		role := "admin"

		// First call: SignUp -> Update Role
		token := env.GetToken(t, username, role)
		assert.NotEmpty(t, token)

		// Second call: Login -> Check Role (already present)
		token2 := env.GetToken(t, username, role)
		assert.NotEmpty(t, token2)
	})

	// Test MakeRequest
	t.Run("MakeRequest", func(t *testing.T) {
		resp := env.MakeRequest(t, http.MethodGet, "/health", nil, "")
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
