package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/services"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ServiceEnv struct {
	APIURL      string
	QueryURL    string
	RealtimeURL string
	CSPURL      string
	Manager     *services.Manager
	Cancel      context.CancelFunc
	MongoURI    string
	DBName      string
}

func setupServiceEnv(t *testing.T, rulesContent string, configModifiers ...func(*config.Config)) *ServiceEnv {
	return setupServiceEnvWithOptions(t, rulesContent, configModifiers, nil)
}

func setupServiceEnvWithOptions(t *testing.T, rulesContent string, configModifiers []func(*config.Config), optsModifiers []func(*services.Options)) *ServiceEnv {
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

	apiPort := getFreePort(t)
	queryPort := getFreePort(t)
	realtimePort := apiPort
	cspPort := getFreePort(t)

	// Create security rules file
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
			Port:            apiPort,
			QueryServiceURL: fmt.Sprintf("http://localhost:%d", queryPort),
		},
		Query: config.QueryConfig{
			Port:          queryPort,
			CSPServiceURL: fmt.Sprintf("http://localhost:%d", cspPort),
		},
		CSP: config.CSPConfig{
			Port: cspPort,
		},
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
		Auth: config.AuthConfig{
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			AuthCodeTTL:     2 * time.Minute,
			RulesFile:       rulesFile,
			PrivateKeyFile:  t.TempDir() + "/keys/auth_private.pem",
		},
	}

	// Apply config modifiers
	for _, mod := range configModifiers {
		mod(cfg)
	}

	opts := services.Options{
		RunAPI:              true,
		RunQuery:            true,
		RunCSP:              true,
		RunTriggerEvaluator: true,
		RunTriggerWorker:    true,
		ListenHost:          "localhost",
	}
	for _, mod := range optsModifiers {
		mod(&opts)
	}

	manager := services.NewManager(cfg, opts)
	require.NoError(t, manager.Init(context.Background()))

	// Start Manager
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	manager.Start(mgrCtx)

	// Wait for startup only for enabled services
	if opts.RunAPI {
		waitForPort(t, apiPort)
	}
	if opts.RunQuery {
		waitForPort(t, queryPort)
	}
	if opts.RunCSP {
		waitForPort(t, cspPort)
	}

	return &ServiceEnv{
		APIURL:      fmt.Sprintf("http://localhost:%d", apiPort),
		QueryURL:    fmt.Sprintf("http://localhost:%d", queryPort),
		RealtimeURL: fmt.Sprintf("http://localhost:%d", realtimePort),
		CSPURL:      fmt.Sprintf("http://localhost:%d", cspPort),
		Manager:     manager,
		MongoURI:    mongoURI,
		DBName:      dbName,
		Cancel: func() {
			mgrCancel()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manager.Shutdown(shutdownCtx)
		},
	}
}

func (e *ServiceEnv) GenerateSystemToken(t *testing.T) string {
	tokenService := e.Manager.TokenService()
	token, err := tokenService.GenerateSystemToken("system-worker")
	require.NoError(t, err)
	return token
}

func (e *ServiceEnv) GetToken(t *testing.T, uid string, role string) string {
	// Login
	loginBody := map[string]string{
		"username": uid,
		"password": "password",
	}
	bodyBytes, _ := json.Marshal(loginBody)
	resp, err := http.Post(e.APIURL+"/auth/v1/login", "application/json", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var res map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&res)
	require.NoError(t, err)

	token, ok := res["access_token"].(string)
	require.True(t, ok, "access_token not found in response")
	return token
}

func (e *ServiceEnv) MakeRequest(t *testing.T, method, path string, body interface{}, token string) *http.Response {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, e.APIURL+path, bodyReader)
	require.NoError(t, err)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
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

func getFreePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port
}
