package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"syntrix/internal/config"
	"syntrix/internal/services"
	"syntrix/internal/storage"
	"syntrix/internal/storage/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIIntegration runs a full integration test against a real MongoDB.
// It requires MongoDB to be running (e.g. via docker-compose).
func TestAPIIntegration(t *testing.T) {
	// 1. Setup Config
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_api_test"

	// Clean DB (Optional, relying on unique IDs mostly, but good practice to ensure connection works)
	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	backend, err := mongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	backend.Close(ctx)

	apiPort := 18082
	queryPort := 18083

	cfg := &config.Config{
		API: config.APIConfig{
			Port:            apiPort,
			QueryServiceURL: fmt.Sprintf("http://localhost:%d", queryPort),
		},
		Query: config.QueryConfig{
			Port:          queryPort,
			CSPServiceURL: "http://dummy-csp",
		},
		Storage: config.StorageConfig{
			MongoURI:       mongoURI,
			DatabaseName:   dbName,
			DataCollection: "documents",
			SysCollection:  "sys",
		},
	}

	opts := services.Options{
		RunAPI:   true,
		RunQuery: true,
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	// Start Manager
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)
	defer func() {
		mgrCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Shutdown(shutdownCtx)
	}()

	// Wait for startup
	time.Sleep(1 * time.Second)

	apiURL := fmt.Sprintf("http://localhost:%d", apiPort)
	client := &http.Client{Timeout: 5 * time.Second}
	collection := "rooms/room-1/messages"

	// 3. Scenario: Create Document
	docData := map[string]interface{}{
		"name": "Integration User",
		"age":  42,
	}
	body, _ := json.Marshal(docData)

	resp, err := client.Post(fmt.Sprintf("%s/v1/%s", apiURL, collection), "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdDoc map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&createdDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.NotEmpty(t, createdDoc["id"])
	// Collection is not returned in flattened response
	// assert.Equal(t, collection, createdDoc.Collection)
	assert.Equal(t, "Integration User", createdDoc["name"])

	docID := createdDoc["id"].(string)

	// 4. Scenario: Get Document
	resp, err = client.Get(fmt.Sprintf("%s/v1/%s/%s", apiURL, collection, docID))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var fetchedDoc map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&fetchedDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, createdDoc["id"], fetchedDoc["id"])
	assert.Equal(t, float64(42), fetchedDoc["age"]) // JSON numbers are floats

	// 4.5 Scenario: Patch Document
	patchData := map[string]interface{}{
		"age": 43,
	}
	patchBody, _ := json.Marshal(patchData)
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/%s/%s", apiURL, collection, docID), bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	require.NoError(t, err)
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
	queryBody, _ := json.Marshal(query)
	resp, err = client.Post(fmt.Sprintf("%s/v1/query", apiURL), "application/json", bytes.NewBuffer(queryBody))
	require.NoError(t, err)
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

	// 6. Scenario: Delete Document
	delReq, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/%s/%s", apiURL, collection, docID), nil)
	resp, err = client.Do(delReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify Delete
	resp, err = client.Get(fmt.Sprintf("%s/v1/%s/%s", apiURL, collection, docID))
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
