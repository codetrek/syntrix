package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMicroservices_FullFlow(t *testing.T) {
	t.Parallel()
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	token := env.GetToken(t, "test-user", "user")
	collection := "test_collection"

	// 1. Create Document via API Gateway
	docData := map[string]interface{}{
		"msg": "Hello Microservices",
	}

	resp := env.MakeRequest(t, "POST", "/api/v1/"+collection, docData, token)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdDoc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&createdDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "Hello Microservices", createdDoc["msg"])
	docID := createdDoc["id"].(string)
	docPath := fmt.Sprintf("%s/%s", collection, docID)

	// 2. Verify via Query Service directly (bypass API Gateway)
	// Call /internal/v1/document/get
	queryReqBody, _ := json.Marshal(map[string]string{"path": docPath})
	queryResp, err := client.Post(fmt.Sprintf("%s/internal/v1/document/get", env.QueryURL), "application/json", bytes.NewBuffer(queryReqBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, queryResp.StatusCode)

	var fetchedDoc map[string]interface{}
	err = json.NewDecoder(queryResp.Body).Decode(&fetchedDoc)
	require.NoError(t, err)
	queryResp.Body.Close()

	// Check content
	assert.Equal(t, "Hello Microservices", fetchedDoc["msg"])

	// 3. Update Document via API Gateway
	patchData := map[string]interface{}{
		"doc": map[string]interface{}{
			"msg": "Updated Message",
		},
	}
	resp = env.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, docID), patchData, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
