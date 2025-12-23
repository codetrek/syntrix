package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"syntrix/internal/api/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplication_Delete(t *testing.T) {
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	// Get Token
	token := env.GetToken(t, "user1", "user")

	collectionName := "replication_delete_test"

	// 1. Push a Document
	docID := "doc-1"
	docData := map[string]interface{}{
		"id":      docID,
		"msg":     "to be deleted",
		"version": float64(0),
	}

	pushBody := rest.ReplicaPushRequest{
		Collection: collectionName,
		Changes: []rest.ReplicaChange{
			{
				Doc: docData,
			},
		},
	}

	bodyBytes, _ := json.Marshal(pushBody)
	pushURL := fmt.Sprintf("%s/v1/replication/push?collection=%s", env.APIURL, collectionName)

	client := &http.Client{}
	pushReq, err := http.NewRequest("POST", pushURL, bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.Header.Set("Authorization", "Bearer "+token)

	pushResp, err := client.Do(pushReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pushResp.StatusCode)
	pushResp.Body.Close()

	// 2. Push Delete
	deleteDocData := map[string]interface{}{
		"id":      docID,
		"version": float64(1), // Assuming version incremented
	}

	deleteBody := rest.ReplicaPushRequest{
		Collection: collectionName,
		Changes: []rest.ReplicaChange{
			{
				Action: "delete",
				Doc:    deleteDocData,
			},
		},
	}

	deleteBytes, _ := json.Marshal(deleteBody)
	deleteReq, err := http.NewRequest("POST", pushURL, bytes.NewBuffer(deleteBytes))
	require.NoError(t, err)
	deleteReq.Header.Set("Content-Type", "application/json")
	deleteReq.Header.Set("Authorization", "Bearer "+token)

	deleteResp, err := client.Do(deleteReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, deleteResp.StatusCode)
	deleteResp.Body.Close()

	// 3. Pull to verify deletion (should be returned as deleted)
	pullURL := fmt.Sprintf("%s/v1/replication/pull?collection=%s&checkpoint=0", env.APIURL, collectionName)
	pullReq, err := http.NewRequest("GET", pullURL, nil)
	require.NoError(t, err)
	pullReq.Header.Set("Authorization", "Bearer "+token)

	pullResp, err := client.Do(pullReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pullResp.StatusCode)

	var pullResult struct {
		Documents  []map[string]interface{} `json:"documents"`
		Checkpoint string                   `json:"checkpoint"`
	}
	err = json.NewDecoder(pullResp.Body).Decode(&pullResult)
	pullResp.Body.Close()
	require.NoError(t, err)

	// Should find the deleted document
	found := false
	for _, doc := range pullResult.Documents {
		if doc["id"] == docID {
			found = true
			deleted, ok := doc["deleted"].(bool)
			assert.True(t, ok, "deleted field should be a boolean")
			assert.True(t, deleted, "Document should be marked as deleted")
			assert.Empty(t, doc["data"], "Data should be empty")
			break
		}
	}
	assert.True(t, found, "Deleted document should be returned in pull")
}
