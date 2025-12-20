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

	"syntrix/internal/api"
	"syntrix/internal/config"
	"syntrix/internal/services"
	"syntrix/internal/storage/mongo"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerAPIIntegration(t *testing.T) {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_trigger_api_test"
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 1. Setup Backend (Just for cleanup)
	backend, err := mongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	backend.DB().Drop(ctx)
	backend.Close(context.Background())

	// 2. Configure and Start Service Manager
	apiPort := 18082 // Use a different port
	cfg := &config.Config{
		API: config.APIConfig{
			Port:            apiPort,
			QueryServiceURL: "",
		},
		Query: config.QueryConfig{
			Port:          18083,
			CSPServiceURL: "",
		},
		Storage: config.StorageConfig{
			MongoURI:       mongoURI,
			DatabaseName:   dbName,
			DataCollection: "documents",
			SysCollection:  "sys",
		},
		Trigger: config.TriggerConfig{
			NatsURL:     natsURL,
			RulesFile:   "", // No rules needed for this test
			WorkerCount: 0,
		},
		Auth: config.AuthConfig{
			AccessTokenTTL:  time.Hour,
			RefreshTokenTTL: time.Hour,
			AuthCodeTTL:     time.Minute,
		},
	}

	opts := services.Options{
		RunAPI:   true,
		RunQuery: true,
		RunAuth:  false, // Disable auth service initialization
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)
	defer func() {
		mgrCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Shutdown(shutdownCtx)
	}()

	waitForPort(t, apiPort)

	apiURL := fmt.Sprintf("http://localhost:%d", apiPort)

	t.Run("Transactional Write - Success", func(t *testing.T) {
		reqBody := api.TriggerWriteRequest{
			Writes: []api.TriggerWriteOp{
				{Type: "create", Path: "api/doc1", Data: map[string]interface{}{"val": 1}},
				{Type: "create", Path: "api/doc2", Data: map[string]interface{}{"val": 2}},
			},
		}
		body, _ := json.Marshal(reqBody)

		resp, err := http.Post(apiURL+"/v1/trigger/write", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify via API
		resp, err = http.Get(apiURL + "/v1/api/doc1")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var doc map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&doc)
		assert.EqualValues(t, 1, doc["val"])

		resp, err = http.Get(apiURL + "/v1/api/doc2")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Transactional Write - Rollback on Error", func(t *testing.T) {
		// First create doc3
		// POST to collection /v1/api with ID in body
		doc3Body := `{"id": "doc3", "val": 3}`
		resp, err := http.Post(apiURL+"/v1/api", "application/json", bytes.NewBufferString(doc3Body))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// Now try to create doc4 AND doc3 (duplicate) in one transaction
		reqBody := api.TriggerWriteRequest{
			Writes: []api.TriggerWriteOp{
				{Type: "create", Path: "api/doc4", Data: map[string]interface{}{"val": 4}},
				{Type: "create", Path: "api/doc3", Data: map[string]interface{}{"val": 33}}, // Should fail
			},
		}
		body, _ := json.Marshal(reqBody)

		resp, err = http.Post(apiURL+"/v1/trigger/write", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		// Verify doc4 was NOT created
		resp, err = http.Get(apiURL + "/v1/api/doc4")
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		// Verify doc3 is unchanged
		resp, err = http.Get(apiURL + "/v1/api/doc3")
		require.NoError(t, err)
		var doc3 map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&doc3)
		assert.EqualValues(t, 3, doc3["val"])
	})
}
