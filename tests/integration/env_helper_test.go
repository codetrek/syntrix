package integration

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestMustMarshal(t *testing.T) {
	// Case 1: Valid input
	input := map[string]string{"key": "value"}
	output := mustMarshal(input)
	var result map[string]string
	err := json.Unmarshal(output, &result)
	require.NoError(t, err)
	assert.Equal(t, input, result)

	// Case 2: Invalid input (channel cannot be marshaled)
	assert.Panics(t, func() {
		mustMarshal(make(chan int))
	})
}

func TestParseTokenClaims(t *testing.T) {
	// Case 1: Valid token
	claims := map[string]interface{}{"sub": "123", "name": "test"}
	claimsBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsBytes)
	token := "header." + payload + ".signature"

	parsed, err := parseTokenClaims(token)
	require.NoError(t, err)
	assert.Equal(t, "123", parsed["sub"])
	assert.Equal(t, "test", parsed["name"])

	// Case 2: Invalid format (not enough parts)
	_, err = parseTokenClaims("invalid.token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token format")

	// Case 3: Invalid base64
	_, err = parseTokenClaims("header.invalid-base64.signature")
	assert.Error(t, err)

	// Case 4: Invalid JSON in payload
	badPayload := base64.RawURLEncoding.EncodeToString([]byte("{invalid-json"))
	_, err = parseTokenClaims("header." + badPayload + ".signature")
	assert.Error(t, err)
}
