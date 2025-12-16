package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"syntrix/internal/api"
	"syntrix/internal/csp"
	"syntrix/internal/query"
	"syntrix/internal/realtime"
	"syntrix/internal/storage"
	"syntrix/internal/storage/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MicroservicesEnv struct {
	Backend        storage.StorageBackend
	QueryServer    *httptest.Server
	APIServer      *httptest.Server
	RealtimeServer *httptest.Server
	RealtimeSrvObj *realtime.Server
	Cancel         context.CancelFunc
}

func setupMicroservices(t *testing.T) *MicroservicesEnv {
	// 1. Setup Storage Backend
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_microservices_test"

	ctx, cancel := context.WithCancel(context.Background())

	// Use a timeout for connection
	connCtx, connCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connCancel()

	backend, err := mongo.NewMongoBackend(connCtx, mongoURI, dbName)
	if err != nil {
		cancel()
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}

	// Clean DB
	_ = backend.Delete(ctx, "test_collection/doc1") // Simple cleanup attempt, better to drop db if possible

	// 1.5 Setup CSP Service
	cspSrv := csp.NewServer(backend)
	tsCSP := httptest.NewServer(cspSrv)

	// 2. Setup Query Service
	engine := query.NewEngine(backend, tsCSP.URL)
	querySrv := query.NewServer(engine)
	tsQuery := httptest.NewServer(querySrv)

	// 3. Setup Client (used by API and Realtime)
	qClient := query.NewClient(tsQuery.URL)

	// 4. Setup API Gateway
	apiSrv := api.NewServer(qClient)
	tsAPI := httptest.NewServer(apiSrv)

	// 5. Setup Realtime Gateway
	rtSrv := realtime.NewServer(qClient)
	err = rtSrv.StartBackgroundTasks(ctx)
	require.NoError(t, err)
	tsRealtime := httptest.NewServer(rtSrv)

	return &MicroservicesEnv{
		Backend:        backend,
		QueryServer:    tsQuery,
		APIServer:      tsAPI,
		RealtimeServer: tsRealtime,
		RealtimeSrvObj: rtSrv,
		Cancel: func() {
			cancel()
			tsAPI.Close()
			tsRealtime.Close()
			tsQuery.Close()
			tsCSP.Close()
			backend.Close(context.Background())
		},
	}
}

func TestMicroservices_FullFlow(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	client := env.APIServer.Client()
	collection := "test_collection"

	// 1. Create Document via API Gateway
	docData := map[string]interface{}{
		"msg": "Hello Microservices",
	}
	body, _ := json.Marshal(docData)

	resp, err := client.Post(fmt.Sprintf("%s/v1/%s", env.APIServer.URL, collection), "application/json", bytes.NewBuffer(body))
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
	qClient := query.NewClient(env.QueryServer.URL)
	fetchedDoc, err := qClient.GetDocument(context.Background(), docPath)
	require.NoError(t, err)
	assert.Equal(t, docPath, fetchedDoc.Id)

	// 3. Update Document via API Gateway
	patchData := map[string]interface{}{
		"msg": "Updated Message",
	}
	patchBody, _ := json.Marshal(patchData)
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/%s/%s", env.APIServer.URL, collection, docID), bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
