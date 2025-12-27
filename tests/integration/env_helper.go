package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
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
	// Use a unique database name for each test to allow parallel execution
	// Sanitize test name to be safe for MongoDB and keep it short (max 63 chars)
	// We use the last 8 chars of the test name + timestamp to ensure uniqueness and brevity
	safeName := strings.ReplaceAll(t.Name(), "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	if len(safeName) > 20 {
		safeName = safeName[len(safeName)-20:]
	}
	dbName := fmt.Sprintf("test_%s_%d", safeName, time.Now().UnixNano()%100000)

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

	dropCtx, dropCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dropCancel()
	// Ensure we start with a clean state (though the name is unique)
	err = client.Database(dbName).Drop(dropCtx)
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

	// Check if NATS is available
	natsAvailable := false
	if os.Getenv("NATS_URL") != "" {
		natsAvailable = true
	} else {
		conn, err := net.DialTimeout("tcp", "localhost:4222", 100*time.Millisecond)
		if err == nil {
			conn.Close()
			natsAvailable = true
		}
	}

	opts := services.Options{
		RunAPI:              true,
		RunQuery:            true,
		RunCSP:              true,
		RunTriggerEvaluator: natsAvailable,
		RunTriggerWorker:    natsAvailable,
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

func (e *ServiceEnv) GenerateSystemToken(t *testing.T) string {
	authService := e.Manager.AuthService()
	token, err := authService.GenerateSystemToken("system-worker")
	require.NoError(t, err)
	return token
}

func (e *ServiceEnv) GetToken(t *testing.T, uid string, role string) string {
	return e.GetTokenForTenant(t, "default", uid, role)
}

func (e *ServiceEnv) GetTokenForTenant(t *testing.T, tenant, uid, role string) string {
	// 1. Try SignUp
	signupBody := map[string]string{
		"tenant":   tenant,
		"username": uid,
		"password": "password123456",
	}
	bodyBytes, _ := json.Marshal(signupBody)
	resp, err := http.Post(e.APIURL+"/auth/v1/signup", "application/json", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	defer resp.Body.Close()

	var token string
	if resp.StatusCode == http.StatusOK {
		var res map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		token = res["access_token"].(string)
	} else {
		// Log the signup error
		body, _ := io.ReadAll(resp.Body)
		t.Logf("SignUp failed: status=%d body=%s", resp.StatusCode, string(body))

		// Assume user exists, try Login
		loginBody := map[string]string{
			"tenant":   tenant,
			"username": uid,
			"password": "password123456",
		}
		bodyBytes, _ := json.Marshal(loginBody)
		respLogin, err := http.Post(e.APIURL+"/auth/v1/login", "application/json", bytes.NewBuffer(bodyBytes))
		require.NoError(t, err)
		defer respLogin.Body.Close()

		if respLogin.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(respLogin.Body)
			t.Logf("Login failed: status=%d body=%s", respLogin.StatusCode, string(body))
		}
		require.Equal(t, http.StatusOK, respLogin.StatusCode)

		var res map[string]interface{}
		err = json.NewDecoder(respLogin.Body).Decode(&res)
		require.NoError(t, err)
		token = res["access_token"].(string)
	}

	// 2. Check Role
	if role == "user" {
		return token
	}

	// 3. Update Role if needed
	claims, err := parseTokenClaims(token)
	require.NoError(t, err)

	// Check if role is already present
	if roles, ok := claims["roles"].([]interface{}); ok {
		for _, r := range roles {
			if r.(string) == role {
				return token
			}
		}
	}

	userID := claims["oid"].(string)
	sysToken := e.GenerateSystemToken(t)

	updateBody := map[string]interface{}{
		"roles": []string{role},
	}
	updateBytes, _ := json.Marshal(updateBody)
	req, err := http.NewRequest("PATCH", e.APIURL+"/admin/users/"+userID, bytes.NewBuffer(updateBytes))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+sysToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	respUpdate, err := client.Do(req)
	require.NoError(t, err)
	defer respUpdate.Body.Close()
	require.Equal(t, http.StatusOK, respUpdate.StatusCode)

	// 4. Re-Login to get new token
	loginBody := map[string]string{
		"tenant":   tenant,
		"username": uid,
		"password": "password123456",
	}
	bodyBytes, _ = json.Marshal(loginBody)
	respLogin, err := http.Post(e.APIURL+"/auth/v1/login", "application/json", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	defer respLogin.Body.Close()
	require.Equal(t, http.StatusOK, respLogin.StatusCode)

	var res map[string]interface{}
	err = json.NewDecoder(respLogin.Body).Decode(&res)
	require.NoError(t, err)
	return res["access_token"].(string)

}

func parseTokenClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	err = json.Unmarshal(payload, &claims)
	return claims, err
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
