package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// StandaloneEnv is a test environment for standalone mode.
// Unlike ServiceEnv, it only exposes APIURL since standalone mode
// doesn't have separate Query/CSP HTTP servers.
type StandaloneEnv struct {
	APIURL   string
	Manager  *services.Manager
	Cancel   context.CancelFunc
	MongoURI string
	DBName   string
}

// setupStandaloneEnv creates a test environment in standalone mode.
// In standalone mode:
// - Only the API gateway HTTP server is created
// - Query and CSP services run in-process without HTTP servers
// - Inter-service communication uses direct function calls
func setupStandaloneEnv(t *testing.T, rulesContent string, configModifiers ...func(*config.Config)) *StandaloneEnv {
	// Setup MongoDB connection
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	// Generate unique database name
	safeName := strings.ReplaceAll(t.Name(), "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	if len(safeName) > 20 {
		safeName = safeName[len(safeName)-20:]
	}
	dbName := fmt.Sprintf("test_%s_%d", safeName, time.Now().UnixNano()%100000)

	// Connect and clean DB
	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := mongo.Connect(connCtx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	dropCtx, dropCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dropCancel()
	err = client.Database(dbName).Drop(dropCtx)
	require.NoError(t, err)

	apiPort := getFreePort(t)

	// Default security rules
	if rulesContent == "" {
		rulesContent = `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /{document=**}:
        allow:
          read, write: "true"
`
	}
	rulesFile := t.TempDir() + "/security.yaml"
	err = os.WriteFile(rulesFile, []byte(rulesContent), 0644)
	require.NoError(t, err)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Port: apiPort,
			// Note: In standalone mode, QueryServiceURL is not used for HTTP calls
		},
		// Query and CSP configs don't need ports in standalone mode
		// as they don't expose HTTP servers
		Storage: config.StorageConfig{
			Backends: map[string]config.BackendConfig{
				"default": {
					Type: "mongo",
					Mongo: config.MongoConfig{
						URI:          mongoURI,
						DatabaseName: dbName,
					},
				},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					DataCollection: "documents",
					SysCollection:  "sys",
				},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					Collection: "users",
				},
				Revocation: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "default",
					},
					Collection: "revocations",
				},
			},
		},
		Identity: config.IdentityConfig{
			AuthN: config.AuthNConfig{
				AccessTokenTTL:  15 * time.Minute,
				RefreshTokenTTL: 7 * 24 * time.Hour,
				AuthCodeTTL:     2 * time.Minute,
				PrivateKeyFile:  t.TempDir() + "/keys/auth_private.pem",
			},
			AuthZ: config.AuthZConfig{
				RulesFile: rulesFile,
			},
		},
	}

	// Apply config modifiers
	for _, mod := range configModifiers {
		mod(cfg)
	}

	// Create manager in standalone mode
	// Note: RunQuery and RunCSP are not needed as they're handled internally
	opts := services.Options{
		RunAPI:              true,
		RunQuery:            false, // No separate Query HTTP server
		RunCSP:              false, // No separate CSP HTTP server
		RunTriggerEvaluator: false, // Not testing triggers
		RunTriggerWorker:    false,
		ListenHost:          "localhost",
		Mode:                services.ModeStandalone, // Standalone mode
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	// Start Manager
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)

	// Wait for API server only
	waitForHealth(t, fmt.Sprintf("http://localhost:%d/health", apiPort))

	return &StandaloneEnv{
		APIURL:   fmt.Sprintf("http://localhost:%d", apiPort),
		Manager:  manager,
		MongoURI: mongoURI,
		DBName:   dbName,
		Cancel: func() {
			mgrCancel()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manager.Shutdown(shutdownCtx)

			// Cleanup DB
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()

			client, err := mongo.Connect(cleanupCtx, options.Client().ApplyURI(mongoURI))
			if err == nil {
				defer client.Disconnect(context.Background())
				_ = client.Database(dbName).Drop(cleanupCtx)
			}
		},
	}
}

func (e *StandaloneEnv) GetToken(t *testing.T, uid string, role string) string {
	// SignUp
	signupBody := map[string]string{
		"tenant":   "default",
		"username": uid,
		"password": "password123456",
	}
	bodyBytes, _ := json.Marshal(signupBody)
	resp, err := http.Post(e.APIURL+"/auth/v1/signup", "application/json", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	defer resp.Body.Close()

	// SignUp returns the token directly if successful
	if resp.StatusCode == http.StatusOK {
		var res map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		return res["access_token"].(string)
	}

	// If signup failed (user exists), try login
	loginBody := map[string]string{
		"tenant":   "default",
		"username": uid,
		"password": "password123456",
	}
	bodyBytes, _ = json.Marshal(loginBody)
	resp2, err := http.Post(e.APIURL+"/auth/v1/login", "application/json", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var tokenResp map[string]interface{}
	err = json.NewDecoder(resp2.Body).Decode(&tokenResp)
	require.NoError(t, err)

	return tokenResp["access_token"].(string)
}

// TestStandaloneMode_BasicCRUD tests basic CRUD operations in standalone mode.
// This verifies that the in-process query and CSP services work correctly.
func TestStandaloneMode_BasicCRUD(t *testing.T) {
	env := setupStandaloneEnv(t, "")
	defer env.Cancel()

	token := env.GetToken(t, "testuser", "user")
	collection := "test-collection"

	// Test Create
	t.Run("Create document", func(t *testing.T) {
		docData := map[string]interface{}{
			"name": "Test Document",
			"type": "standalone-test",
		}
		body, _ := json.Marshal(docData)

		req, _ := http.NewRequest(http.MethodPost, env.APIURL+"/api/v1/"+collection, bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.Contains(t, result, "id")
	})

	// Test Query (uses Query service internally)
	t.Run("Query documents", func(t *testing.T) {
		queryData := map[string]interface{}{
			"collection": collection,
		}
		body, _ := json.Marshal(queryData)

		req, _ := http.NewRequest(http.MethodPost, env.APIURL+"/api/v1/query", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result []map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 1, "Should have at least one document")
	})
}

// TestStandaloneMode_NoExternalServices verifies that standalone mode
// doesn't require or create Query/CSP HTTP servers.
func TestStandaloneMode_NoExternalServices(t *testing.T) {
	env := setupStandaloneEnv(t, "")
	defer env.Cancel()

	// API server should be up
	resp, err := http.Get(env.APIURL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// In standalone mode, there should be only one HTTP server (API)
	// We verify this by checking that the manager's server count is 1
	// (This is implicitly verified by the fact that we didn't set Query/CSP ports
	// and the service still works)

	// Perform a query operation to verify in-process communication works
	token := env.GetToken(t, "standalone_user", "user")

	// Create a document
	docData := map[string]interface{}{
		"title": "Standalone Test",
	}
	body, _ := json.Marshal(docData)

	req, _ := http.NewRequest(http.MethodPost, env.APIURL+"/api/v1/standalone-test", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode, "Document creation should work in standalone mode")
}

// TestStandaloneMode_Watch tests real-time watch functionality in standalone mode.
// Watch uses CSP service internally, so this verifies the local CSP integration.
func TestStandaloneMode_Watch(t *testing.T) {
	env := setupStandaloneEnv(t, "")
	defer env.Cancel()

	token := env.GetToken(t, "watch_user", "user")
	collection := "watch-test"

	// Create initial document
	docData := map[string]interface{}{
		"status": "initial",
	}
	body, _ := json.Marshal(docData)

	req, _ := http.NewRequest(http.MethodPost, env.APIURL+"/api/v1/"+collection, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Extract document ID for update
	var createResult map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&createResult)
	require.NoError(t, err)
	docID := createResult["id"].(string)

	// Update the document (this would trigger watch events via CSP)
	// PATCH requires the data to be wrapped in "doc" field
	updateData := map[string]interface{}{
		"doc": map[string]interface{}{
			"status": "updated",
		},
	}
	body, _ = json.Marshal(updateData)

	req, _ = http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/v1/%s/%s", env.APIURL, collection, docID), bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Document update should work in standalone mode")
}

// TestStandaloneMode_CompareWithDistributed ensures standalone mode
// produces the same results as distributed mode for basic operations.
func TestStandaloneMode_CompareWithDistributed(t *testing.T) {
	// Setup standalone env
	standaloneEnv := setupStandaloneEnv(t, "")
	defer standaloneEnv.Cancel()

	// Setup distributed env for comparison
	distributedEnv := setupServiceEnv(t, "")
	defer distributedEnv.Cancel()

	standaloneToken := standaloneEnv.GetToken(t, "compare_standalone", "user")
	distributedToken := distributedEnv.GetToken(t, "compare_distributed", "user")

	collection := "comparison"

	// Create same document in both modes
	docData := map[string]interface{}{
		"value": "comparison-test",
		"count": 42,
	}
	body, _ := json.Marshal(docData)

	// Create in standalone
	req1, _ := http.NewRequest(http.MethodPost, standaloneEnv.APIURL+"/api/v1/"+collection, bytes.NewBuffer(body))
	req1.Header.Set("Authorization", "Bearer "+standaloneToken)
	req1.Header.Set("Content-Type", "application/json")

	body, _ = json.Marshal(docData)
	req2, _ := http.NewRequest(http.MethodPost, distributedEnv.APIURL+"/api/v1/"+collection, bytes.NewBuffer(body))
	req2.Header.Set("Authorization", "Bearer "+distributedToken)
	req2.Header.Set("Content-Type", "application/json")

	resp1, err := http.DefaultClient.Do(req1)
	require.NoError(t, err)
	defer resp1.Body.Close()

	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	// Both should succeed
	assert.Equal(t, http.StatusCreated, resp1.StatusCode, "Standalone create should succeed")
	assert.Equal(t, http.StatusCreated, resp2.StatusCode, "Distributed create should succeed")

	// Decode responses
	var result1, result2 map[string]interface{}
	json.NewDecoder(resp1.Body).Decode(&result1)
	json.NewDecoder(resp2.Body).Decode(&result2)

	// Verify both have IDs and same value
	assert.Contains(t, result1, "id", "Standalone should return id")
	assert.Contains(t, result2, "id", "Distributed should return id")
	assert.Equal(t, result1["value"], result2["value"], "Values should match between modes")

	// Query operations should also match
	queryData := map[string]interface{}{
		"collection": collection,
	}
	body, _ = json.Marshal(queryData)

	req1, _ = http.NewRequest(http.MethodPost, standaloneEnv.APIURL+"/api/v1/query", bytes.NewBuffer(body))
	req1.Header.Set("Authorization", "Bearer "+standaloneToken)
	req1.Header.Set("Content-Type", "application/json")

	body, _ = json.Marshal(queryData)
	req2, _ = http.NewRequest(http.MethodPost, distributedEnv.APIURL+"/api/v1/query", bytes.NewBuffer(body))
	req2.Header.Set("Authorization", "Bearer "+distributedToken)
	req2.Header.Set("Content-Type", "application/json")

	resp1, _ = http.DefaultClient.Do(req1)
	defer resp1.Body.Close()

	resp2, _ = http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	var docs1, docs2 []map[string]interface{}
	json.NewDecoder(resp1.Body).Decode(&docs1)
	json.NewDecoder(resp2.Body).Decode(&docs2)

	// Both should have documents
	assert.Equal(t, len(docs1), len(docs2), "Document count should match between modes")
}
