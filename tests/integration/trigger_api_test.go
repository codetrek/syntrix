package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"syntrix/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerAPIIntegration(t *testing.T) {
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	token := env.GenerateSystemToken(t)

	t.Run("Transactional Write - Success", func(t *testing.T) {
		reqBody := api.TriggerWriteRequest{
			Writes: []api.TriggerWriteOp{
				{Type: "create", Path: "api/doc1", Data: map[string]interface{}{"val": 1}},
				{Type: "create", Path: "api/doc2", Data: map[string]interface{}{"val": 2}},
			},
		}

		resp := env.MakeRequest(t, "POST", "/v1/trigger/write", reqBody, token)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify via API
		resp = env.MakeRequest(t, "GET", "/v1/api/doc1", nil, token)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var doc map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&doc)
		assert.EqualValues(t, 1, doc["val"])

		resp = env.MakeRequest(t, "GET", "/v1/api/doc2", nil, token)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Transactional Write - Rollback on Error", func(t *testing.T) {
		// First create doc3
		// POST to collection /v1/api with ID in body
		doc3Body := map[string]interface{}{
			"id":  "doc3",
			"val": 3,
		}
		resp := env.MakeRequest(t, "POST", "/v1/api", doc3Body, token)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// Now try to create doc4 AND doc3 (duplicate) in one transaction
		reqBody := api.TriggerWriteRequest{
			Writes: []api.TriggerWriteOp{
				{Type: "create", Path: "api/doc4", Data: map[string]interface{}{"val": 4}},
				{Type: "create", Path: "api/doc3", Data: map[string]interface{}{"val": 33}}, // Should fail
			},
		}

		resp = env.MakeRequest(t, "POST", "/v1/trigger/write", reqBody, token)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		// Verify doc4 was NOT created
		resp = env.MakeRequest(t, "GET", "/v1/api/doc4", nil, token)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		// Verify doc3 is unchanged
		resp = env.MakeRequest(t, "GET", "/v1/api/doc3", nil, token)
		var doc3 map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&doc3)
		assert.EqualValues(t, 3, doc3["val"])
	})
}
