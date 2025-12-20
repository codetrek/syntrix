package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"syntrix/internal/config"
	"syntrix/internal/services"
	internalmongo "syntrix/internal/storage/mongo"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local definition of DeliveryTask for black-box testing
type DeliveryTask struct {
	TriggerID  string                 `json:"triggerId"`
	Tenant     string                 `json:"tenant"`
	Event      string                 `json:"event"`
	Collection string                 `json:"collection"`
	DocKey     string                 `json:"docKey"`
	LSN        string                 `json:"lsn"`
	Seq        int64                  `json:"seq"`
	Before     map[string]interface{} `json:"before,omitempty"`
	After      map[string]interface{} `json:"after,omitempty"`
	Timestamp  int64                  `json:"ts"`
	URL        string                 `json:"url"`
	Headers    map[string]string      `json:"headers"`
	SecretsRef string                 `json:"secretsRef"`
	// RetryPolicy and Timeout omitted for brevity if not checked
}

func TestTriggerIntegration(t *testing.T) {
	// 1. Setup Dependencies (Mongo & NATS)
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_trigger_test_manager"
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	// Clean up NATS Stream before starting
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to NATS: %v", err)
	}
	defer nc.Close()

	js, _ := jetstream.New(nc)
	ctx := context.Background()
	_ = js.DeleteStream(ctx, "TRIGGERS") // Ensure clean state

	// Clean up Mongo
	// We can just use a unique DB name or collection, but let's try to be clean
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	backend, err := internalmongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	// Ideally drop database, but backend doesn't expose it.
	// We will rely on unique IDs for the test run.
	backend.Close(context.Background())

	// 2. Setup Mock Webhook Server
	var webhookReceived sync.WaitGroup
	webhookReceived.Add(1)
	var receivedTask *DeliveryTask

	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer webhookReceived.Done()

		// Decode body
		var task DeliveryTask
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		receivedTask = &task
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	// 3. Create Temporary Trigger Rules File
	tmpDir := t.TempDir()
	rulesFile := filepath.Join(tmpDir, "triggers.yaml")
	rulesContent := fmt.Sprintf(`
- triggerId: "integration-test-trigger"
  tenant: "test-tenant"
  collection: "users"
  events: ["create"]
  condition: "event.document.age >= 18"
  url: "%s"
  headers:
    X-Test: "true"
`, webhookServer.URL)

	err = os.WriteFile(rulesFile, []byte(rulesContent), 0644)
	require.NoError(t, err)

	// 4. Configure and Start Service Manager
	apiPort := 18080 // Use a specific port for testing
	cfg := &config.Config{
		API: config.APIConfig{
			Port:            apiPort,
			QueryServiceURL: "", // Local
		},
		Query: config.QueryConfig{
			Port:          18081,
			CSPServiceURL: "", // Local
		},
		Storage: config.StorageConfig{
			MongoURI:       mongoURI,
			DatabaseName:   dbName,
			DataCollection: "documents",
			SysCollection:  "sys",
		},
		Trigger: config.TriggerConfig{
			NatsURL:     natsURL,
			RulesFile:   rulesFile,
			WorkerCount: 4,
		},
	}

	opts := services.Options{
		RunAPI:              true,
		RunQuery:            true,
		RunTriggerEvaluator: true,
		RunTriggerWorker:    true,
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	// Start Manager in background
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)
	defer func() {
		mgrCancel()
		// Give it a moment to stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Shutdown(shutdownCtx)
	}()

	// Wait for services to be ready
	waitForPort(t, apiPort)
	waitForPort(t, 18081)

	// 5. Trigger Event via API
	// Create a document that matches the trigger
	userID := fmt.Sprintf("user-%d", time.Now().UnixNano())
	docData := map[string]interface{}{
		"id":   userID,
		"name": "John Doe",
		"age":  20, // Matches condition >= 18
	}
	docBody, _ := json.Marshal(docData)

	apiURL := fmt.Sprintf("http://localhost:%d", apiPort)
	resp, err := http.Post(fmt.Sprintf("%s/v1/users", apiURL), "application/json", bytes.NewBuffer(docBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// 6. Wait for Webhook
	// Wait with timeout
	done := make(chan struct{})
	go func() {
		webhookReceived.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
		require.NotNil(t, receivedTask)
		assert.Equal(t, "integration-test-trigger", receivedTask.TriggerID)
		assert.NotEmpty(t, receivedTask.DocKey)
		assert.Equal(t, "create", receivedTask.Event)
		assert.Equal(t, "users", receivedTask.Collection)
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for webhook execution")
	}
}
