package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"syntrix/internal/config"
	"syntrix/internal/services"
	internalmongo "syntrix/internal/storage/mongo"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

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

	// Connect using driver to drop database
	client, err := mongo.Connect(connCtx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	err = client.Database(dbName).Drop(ctx)
	require.NoError(t, err)

	// Verify connection with backend (optional, but good for sanity)
	backend, err := internalmongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB backend: %v", err)
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
	waitForPort(t, apiPort)
	waitForPort(t, queryPort)
	waitForPort(t, realtimePort)
	waitForPort(t, cspPort)

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
	data := fetchedDoc["data"].(map[string]interface{})
	assert.Equal(t, "Hello Microservices", data["msg"])

	// 3. Update Document via API Gateway
	patchData := map[string]interface{}{
		"doc": map[string]interface{}{
			"msg": "Updated Message",
		},
	}
	patchBody, _ := json.Marshal(patchData)
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/%s/%s", env.APIURL, collection, docID), bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func waitForPort(t *testing.T, port int) {
	timeout := 5 * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for port %d to be ready", port)
}
