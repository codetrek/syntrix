package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthzIntegration(t *testing.T) {
	// 1. Define Rules
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /public/{doc=**}:
        allow:
          read, write: "true"
      /private/{doc=**}:
        allow:
          read, write: "request.auth.uid != null"
      /admin/{doc=**}:
        allow:
          read, write: "'admin' in request.auth.token.roles"
`

	// 2. Setup Environment
	env := setupServiceEnv(t, rules)
	defer env.Cancel()

	// 3. Insert Test Data
	// We need to connect to Mongo to insert documents directly
	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Backends: map[string]config.BackendConfig{
				"default": {
					Type: "mongo",
					Mongo: config.MongoConfig{
						URI:          env.MongoURI,
						DatabaseName: env.DBName,
					},
				},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					DataCollection: "documents",
					SysCollection:  "sys",
				},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					Collection: "users",
				},
				Revocation: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					Collection: "revocations",
				},
			},
		},
	}
	factory, err := storage.NewFactory(connCtx, cfg)
	require.NoError(t, err)
	defer factory.Close()
	backend := factory.Document()

	docs := []interface{}{
		&storage.Document{Id: storage.CalculateID("public/doc1"), Collection: "public", Data: map[string]interface{}{"foo": "bar"}},
		&storage.Document{Id: storage.CalculateID("private/doc1"), Collection: "private", Data: map[string]interface{}{"secret": "data"}},
		&storage.Document{Id: storage.CalculateID("admin/doc1"), Collection: "admin", Data: map[string]interface{}{"top": "secret"}},
	}
	for _, d := range docs {
		err := backend.Create(ctx, d.(*storage.Document))
		require.NoError(t, err)
	}

	// Helper to make requests
	makeRequest := func(method, path, token string, body interface{}) int {
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, env.APIURL+path, bytes.NewBuffer(bodyBytes))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// 4. Test Scenarios

	// Scenario 1: Public Access (No Token)
	t.Run("Public Access", func(t *testing.T) {
		code := makeRequest("GET", "/api/v1/public/doc1", "", nil)
		assert.Equal(t, http.StatusOK, code)
	})

	// Scenario 2: Private Access (No Token) -> Deny
	t.Run("Private Access No Token", func(t *testing.T) {
		code := makeRequest("GET", "/api/v1/private/doc1", "", nil)
		assert.Equal(t, http.StatusForbidden, code)
	})

	// Scenario 3: Private Access (With Token) -> Allow
	t.Run("Private Access With Token", func(t *testing.T) {
		token := env.GetToken(t, "user1", "user")
		require.NotEmpty(t, token)

		code := makeRequest("GET", "/api/v1/private/doc1", token, nil)
		assert.Equal(t, http.StatusOK, code)
	})

	// Scenario 4: Admin Access (User Token) -> Deny
	t.Run("Admin Access User Token", func(t *testing.T) {
		token := env.GetToken(t, "user2", "user")
		require.NotEmpty(t, token)

		code := makeRequest("GET", "/api/v1/admin/doc1", token, nil)
		assert.Equal(t, http.StatusForbidden, code)
	})

	// Scenario 5: Admin Access (Admin Token) -> Allow
	t.Run("Admin Access Admin Token", func(t *testing.T) {
		token := env.GetToken(t, "syntrix", "admin")
		require.NotEmpty(t, token)

		code := makeRequest("GET", "/api/v1/admin/doc1", token, nil)
		assert.Equal(t, http.StatusOK, code)
	})
}
