package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIIntegration runs a full integration test against a real MongoDB.
// It requires MongoDB to be running (e.g. via docker-compose).
func TestAPIIntegration(t *testing.T) {
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	token := env.GetToken(t, "test-user", "user")
	collection := "rooms/room-1/messages"

	// 3. Scenario: Create Document
	docData := map[string]interface{}{
		"name": "Integration User",
		"age":  42,
	}

	resp := env.MakeRequest(t, "POST", "/api/v1/"+collection, docData, token)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdDoc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&createdDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.NotEmpty(t, createdDoc["id"])
	// Collection is not returned in flattened response
	// assert.Equal(t, collection, createdDoc.Collection)
	assert.Equal(t, "Integration User", createdDoc["name"])

	docID := createdDoc["id"].(string)

	// 4. Scenario: Get Document
	resp = env.MakeRequest(t, "GET", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var fetchedDoc map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&fetchedDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, createdDoc["id"], fetchedDoc["id"])
	assert.Equal(t, float64(42), fetchedDoc["age"]) // JSON numbers are floats

	// 4.5 Scenario: Patch Document
	patchData := map[string]interface{}{
		"doc": map[string]interface{}{
			"age": 43,
		},
	}
	resp = env.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, docID), patchData, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var patchedDoc map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&patchedDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "Integration User", patchedDoc["name"]) // Should remain unchanged
	assert.Equal(t, float64(43), patchedDoc["age"])         // Should be updated

	// 5. Scenario: Query Document
	query := storage.Query{
		Collection: collection,
		Filters: []storage.Filter{
			{Field: "name", Op: "==", Value: "Integration User"},
		},
	}
	resp = env.MakeRequest(t, "POST", "/api/v1/query", query, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var queryResults []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&queryResults)
	require.NoError(t, err)
	resp.Body.Close()

	found := false
	for _, d := range queryResults {
		if d["id"] == createdDoc["id"] {
			found = true
			break
		}
	}
	assert.True(t, found, "Created document should be found in query")

	// 5.5 Scenario: Conditional Update (IfMatch)
	// Success case
	ifMatchData := map[string]interface{}{
		"doc": map[string]interface{}{
			"age": 44,
		},
		"ifMatch": []map[string]interface{}{
			{"field": "age", "op": "==", "value": 43},
		},
	}
	resp = env.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, docID), ifMatchData, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Failure case (Condition not met)
	ifMatchFailData := map[string]interface{}{
		"doc": map[string]interface{}{
			"age": 45,
		},
		"ifMatch": []map[string]interface{}{
			{"field": "age", "op": "==", "value": 999}, // Wrong age
		},
	}
	resp = env.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, docID), ifMatchFailData, token)
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)

	// 6. Scenario: Delete Document
	resp = env.MakeRequest(t, "DELETE", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, token)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify Delete
	resp = env.MakeRequest(t, "GET", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, token)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
