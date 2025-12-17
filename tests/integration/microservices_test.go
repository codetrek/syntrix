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
	"syntrix/internal/query"
	"syntrix/internal/services"
	"syntrix/internal/storage/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MicroservicesEnv struct {
	APIURL      string
	QueryURL    string
	RealtimeURL string
	CSPURL      string
	Manager     *services.Manager
	Cancel      context.CancelFunc
}

func setupMicroservices(t *testing.T) *MicroservicesEnv {
	// 1. Setup Config
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_microservices_test"

	// Clean DB
	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	backend, err := mongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	backend.Close(ctx)

	apiPort := 18084
	queryPort := 18085
	realtimePort := 18086
	cspPort := 18087

	cfg := &config.Config{
		API: config.APIConfig{
			Port:            apiPort,
			QueryServiceURL: fmt.Sprintf("http://localhost:%d", queryPort),
		},
		Query: config.QueryConfig{
			Port:          queryPort,
			CSPServiceURL: fmt.Sprintf("http://localhost:%d", cspPort),
		},
		Realtime: config.RealtimeConfig{
			Port:            realtimePort,
			QueryServiceURL: fmt.Sprintf("http://localhost:%d", queryPort),
		},
		CSP: config.CSPConfig{
			Port: cspPort,
		},
		Storage: config.StorageConfig{
			MongoURI:       mongoURI,
			DatabaseName:   dbName,
			DataCollection: "documents",
			SysCollection:  "sys",
		},
	}

	opts := services.Options{
		RunAPI:      true,
		RunQuery:    true,
		RunRealtime: true,
		RunCSP:      true,
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	// Start Manager
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)

	// Wait for startup
	time.Sleep(1 * time.Second)

	return &MicroservicesEnv{
		APIURL:      fmt.Sprintf("http://localhost:%d", apiPort),
		QueryURL:    fmt.Sprintf("http://localhost:%d", queryPort),
		RealtimeURL: fmt.Sprintf("http://localhost:%d", realtimePort),
		CSPURL:      fmt.Sprintf("http://localhost:%d", cspPort),
		Manager:     manager,
		Cancel: func() {
			mgrCancel()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manager.Shutdown(shutdownCtx)
		},
	}
}

func TestMicroservices_FullFlow(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	collection := "test_collection"

	// 1. Create Document via API Gateway
	docData := map[string]interface{}{
		"msg": "Hello Microservices",
	}
	body, _ := json.Marshal(docData)

	resp, err := client.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collection), "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdDoc map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&createdDoc)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "Hello Microservices", createdDoc["msg"])
	docID := createdDoc["id"].(string)
	docPath := fmt.Sprintf("%s/%s", collection, docID)

	// 2. Verify via Query Service directly (bypass API Gateway)
	// We can use the internal client for this
	qClient := query.NewClient(env.QueryURL)
	fetchedDoc, err := qClient.GetDocument(context.Background(), docPath)
	require.NoError(t, err)
	assert.Equal(t, docPath, fetchedDoc.Id)

	// 3. Update Document via API Gateway
	patchData := map[string]interface{}{
		"msg": "Updated Message",
	}
	patchBody, _ := json.Marshal(patchData)
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/%s/%s", env.APIURL, collection, docID), bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
