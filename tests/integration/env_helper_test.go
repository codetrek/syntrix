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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
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

func (e *ServiceEnv) CreateDocument(t *testing.T, collection string, data map[string]interface{}, token string) map[string]interface{} {
	resp := e.MakeRequest(t, "POST", "/api/v1/"+collection, data, token)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var doc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&doc)
	require.NoError(t, err)
	resp.Body.Close()
	return doc
}

func (e *ServiceEnv) GetDocument(t *testing.T, collection, id, token string) map[string]interface{} {
	resp := e.MakeRequest(t, "GET", fmt.Sprintf("/api/v1/%s/%s", collection, id), nil, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var doc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&doc)
	require.NoError(t, err)
	resp.Body.Close()
	return doc
}

func (e *ServiceEnv) PatchDocument(t *testing.T, collection, id string, data map[string]interface{}, token string) map[string]interface{} {
	resp := e.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, id), data, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var doc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&doc)
	require.NoError(t, err)
	resp.Body.Close()
	return doc
}

func (e *ServiceEnv) PutDocument(t *testing.T, collection, id string, data map[string]interface{}, token string) map[string]interface{} {
	resp := e.MakeRequest(t, "PUT", fmt.Sprintf("/api/v1/%s/%s", collection, id), data, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var doc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&doc)
	require.NoError(t, err)
	resp.Body.Close()
	return doc
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

// --- Realtime/WebSocket Helpers ---

const (
	TypeAuth           = "auth"
	TypeAuthAck        = "auth_ack"
	TypeSubscribe      = "subscribe"
	TypeSubscribeAck   = "subscribe_ack"
	TypeUnsubscribe    = "unsubscribe"
	TypeUnsubscribeAck = "unsubscribe_ack"
	TypeEvent          = "event"
	TypeSnapshot       = "snapshot"
	TypeError          = "error"

	EventCreate = "create"
)

type BaseMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type AuthPayload struct {
	Token string `json:"token"`
}

type SubscribePayload struct {
	Query        Query `json:"query"`
	IncludeData  bool  `json:"includeData"`
	SendSnapshot bool  `json:"sendSnapshot"`
}

type Query struct {
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
}

type Filter struct {
	Field string      `json:"field"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}

type UnsubscribePayload struct {
	ID string `json:"id"`
}

type EventPayload struct {
	SubID string      `json:"subId"`
	Delta PublicEvent `json:"delta"`
}

type PublicEvent struct {
	Type     string                 `json:"type"` // "create", "update", "delete"
	Document map[string]interface{} `json:"document,omitempty"`
	Path     string                 `json:"path"`
}

type SnapshotPayload struct {
	Documents []map[string]interface{} `json:"documents"`
}

func (e *ServiceEnv) ConnectWebSocket(t *testing.T) *websocket.Conn {
	wsURL := "ws" + strings.TrimPrefix(e.RealtimeURL, "http") + "/realtime/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "Failed to connect to websocket")
	return ws
}

func (e *ServiceEnv) AuthenticateWebSocket(t *testing.T, ws *websocket.Conn, token string) {
	authMsg := BaseMessage{
		ID:   "auth-" + t.Name(),
		Type: TypeAuth,
		Payload: mustMarshal(AuthPayload{
			Token: token,
		}),
	}
	err := ws.WriteJSON(authMsg)
	require.NoError(t, err, "Failed to send auth message")

	// Wait for auth_ack
	msg := e.ReadWebSocketMessage(t, ws)
	require.Equal(t, TypeAuthAck, msg.Type, "Expected auth_ack")
}

func (e *ServiceEnv) ReadWebSocketMessage(t *testing.T, ws *websocket.Conn) BaseMessage {
	var msg BaseMessage
	err := ws.ReadJSON(&msg)
	require.NoError(t, err, "Failed to read websocket message")
	return msg
}

func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestEnvHelper(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test setupServiceEnv
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	// Test GenerateSystemToken
	t.Run("GenerateSystemToken", func(t *testing.T) {
		token := env.GenerateSystemToken(t)
		assert.NotEmpty(t, token)
	})

	// Test GetToken
	t.Run("UserManagement", func(t *testing.T) {
		username := "testuser_helper"
		role := "user"

		token := env.GetToken(t, username, role)
		assert.NotEmpty(t, token)

		// Call again to trigger Login path
		token2 := env.GetToken(t, username, role)
		assert.NotEmpty(t, token2)
	})

	t.Run("UserManagement_AdminRole", func(t *testing.T) {
		username := "testadmin_helper"
		role := "admin"

		// First call: SignUp -> Update Role
		token := env.GetToken(t, username, role)
		assert.NotEmpty(t, token)

		// Second call: Login -> Check Role (already present)
		token2 := env.GetToken(t, username, role)
		assert.NotEmpty(t, token2)
	})

	// Test MakeRequest
	t.Run("MakeRequest", func(t *testing.T) {
		resp := env.MakeRequest(t, http.MethodGet, "/health", nil, "")
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestMustMarshal(t *testing.T) {
	// Case 1: Valid input
	input := map[string]string{"key": "value"}
	output := mustMarshal(input)
	var result map[string]string
	err := json.Unmarshal(output, &result)
	require.NoError(t, err)
	assert.Equal(t, input, result)

	// Case 2: Invalid input (channel cannot be marshaled)
	assert.Panics(t, func() {
		mustMarshal(make(chan int))
	})
}

func TestParseTokenClaims(t *testing.T) {
	// Case 1: Valid token
	claims := map[string]interface{}{"sub": "123", "name": "test"}
	claimsBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsBytes)
	token := "header." + payload + ".signature"

	parsed, err := parseTokenClaims(token)
	require.NoError(t, err)
	assert.Equal(t, "123", parsed["sub"])
	assert.Equal(t, "test", parsed["name"])

	// Case 2: Invalid format (not enough parts)
	_, err = parseTokenClaims("invalid.token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token format")

	// Case 3: Invalid base64
	_, err = parseTokenClaims("header.invalid-base64.signature")
	assert.Error(t, err)

	// Case 4: Invalid JSON in payload
	badPayload := base64.RawURLEncoding.EncodeToString([]byte("{invalid-json"))
	_, err = parseTokenClaims("header." + badPayload + ".signature")
	assert.Error(t, err)
}
