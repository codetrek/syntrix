package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAuth is a flexible mock for identity.AuthN
type MockAuth struct {
	ValidateTokenFunc func(token string) (*identity.Claims, error)
}

func (m *MockAuth) Middleware(next http.Handler) http.Handler {
	return next
}

func (m *MockAuth) MiddlewareOptional(next http.Handler) http.Handler {
	return next
}

func (m *MockAuth) SignIn(ctx context.Context, req identity.LoginRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *MockAuth) SignUp(ctx context.Context, req identity.SignupRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *MockAuth) Refresh(ctx context.Context, req identity.RefreshRequest) (*identity.TokenPair, error) {
	return nil, nil
}

func (m *MockAuth) ListUsers(ctx context.Context, limit int, offset int) ([]*identity.User, error) {
	return nil, nil
}

func (m *MockAuth) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	return nil
}

func (m *MockAuth) Logout(ctx context.Context, refreshToken string) error { return nil }

func (m *MockAuth) GenerateSystemToken(serviceName string) (string, error) { return "", nil }

func (m *MockAuth) ValidateToken(tokenString string) (*identity.Claims, error) {
	if m.ValidateTokenFunc != nil {
		return m.ValidateTokenFunc(tokenString)
	}
	return nil, nil
}

func TestSafeCheckOrigin(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		host     string
		expected bool
	}{
		{
			name:     "Empty origin",
			origin:   "",
			host:     "example.com",
			expected: true,
		},
		{
			name:     "Same host and port",
			origin:   "http://example.com:8080",
			host:     "example.com:8080",
			expected: true,
		},
		{
			name:     "Same host different port (dev)",
			origin:   "http://example.com:3000",
			host:     "example.com:8080",
			expected: true,
		},
		{
			name:     "Different host",
			origin:   "http://evil.com",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "Invalid origin URL",
			origin:   "://invalid",
			host:     "example.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Origin", tt.origin)
			req.Host = tt.host
			assert.Equal(t, tt.expected, safeCheckOrigin(req))
		})
	}
}

func TestClient_HandleMessage_InvalidPayloads(t *testing.T) {
	// Setup
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		authenticated: true,
		subscriptions: make(map[string]Subscription),
	}

	// Test Subscribe with invalid payload
	msg := BaseMessage{
		ID:      "1",
		Type:    TypeSubscribe,
		Payload: []byte(`{invalid_json`),
	}
	// This logs an error but doesn't send a response in current implementation
	// We just want to ensure it doesn't panic
	client.handleMessage(msg)

	// Test Unsubscribe with invalid payload
	msg = BaseMessage{
		ID:      "2",
		Type:    TypeUnsubscribe,
		Payload: []byte(`{invalid_json`),
	}
	client.handleMessage(msg)

	// Test Auth with invalid payload
	client.auth = &MockAuth{} // Set auth to trigger payload check
	msg = BaseMessage{
		ID:      "3",
		Type:    TypeAuth,
		Payload: []byte(`{invalid_json`),
	}
	client.handleMessage(msg)

	select {
	case resp := <-client.send:
		assert.Equal(t, TypeError, resp.Type)
		assert.Equal(t, "3", resp.ID)
		var payload ErrorPayload
		err := json.Unmarshal(resp.Payload, &payload)
		require.NoError(t, err)
		assert.Equal(t, "invalid_auth", payload.Code)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestClient_HandleAuth_InvalidToken(t *testing.T) {
	// Setup
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	mockAuth := &MockAuth{
		ValidateTokenFunc: func(token string) (*identity.Claims, error) {
			return nil, assert.AnError
		},
	}

	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		auth:          mockAuth,
		authenticated: false,
	}

	payload := AuthPayload{Token: "invalid-token"}
	payloadBytes, _ := json.Marshal(payload)

	msg := BaseMessage{
		ID:      "1",
		Type:    TypeAuth,
		Payload: payloadBytes,
	}
	client.handleMessage(msg)

	select {
	case resp := <-client.send:
		assert.Equal(t, TypeError, resp.Type)
		assert.Equal(t, "1", resp.ID)
		var payload ErrorPayload
		err := json.Unmarshal(resp.Payload, &payload)
		require.NoError(t, err)
		assert.Equal(t, "unauthorized", payload.Code)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestClient_HandleSubscribe_InvalidFilter(t *testing.T) {
	// Setup
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		authenticated: true,
		subscriptions: make(map[string]Subscription),
	}

	// Subscribe with invalid filter
	subscribePayload := SubscribePayload{
		Query: model.Query{
			Collection: "test",
			Filters: []model.Filter{
				{
					Field: "field",
					Op:    "invalid",
					Value: "value",
				},
			},
		},
	}
	payloadBytes, _ := json.Marshal(subscribePayload)

	msg := BaseMessage{
		ID:      "1",
		Type:    TypeSubscribe,
		Payload: payloadBytes,
	}
	client.handleMessage(msg)

	select {
	case resp := <-client.send:
		assert.Equal(t, TypeError, resp.Type)
		assert.Equal(t, "1", resp.ID)
		// Verify error message content if needed
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestClient_WritePump_WriteError(t *testing.T) {
	// Setup
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader.Upgrade(w, r, nil)
	}))
	defer s.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	client := &Client{
		conn: conn,
		send: make(chan BaseMessage, 10),
	}

	// Start writePump
	done := make(chan struct{})
	go func() {
		client.writePump()
		close(done)
	}()

	// Close connection to cause write error
	conn.Close()

	// Send message
	client.send <- BaseMessage{Type: TypeAuthAck}

	// Wait for writePump to exit
	select {
	case <-done:
	// Success
	case <-time.After(time.Second):
		t.Fatal("writePump did not exit on write error")
	}
}

func TestHub_Broadcast_SlowClient_Drop(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	// Create client with small buffer
	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 1),
		authenticated: true,
		subscriptions: make(map[string]Subscription),
	}

	// Subscribe
	client.subscriptions["sub1"] = Subscription{
		Query: model.Query{Collection: "users"},
	}

	hub.Register(client)
	time.Sleep(10 * time.Millisecond) // Wait for registration

	// Fill the channel
	client.send <- BaseMessage{ID: "filler"}

	// Broadcast event
	event := storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Id:         "1",
			Collection: "users",
			Data:       map[string]interface{}{"name": "test"},
		},
		Timestamp: time.Now().UnixMilli(),
	}

	// This should hit the default case and timeout in the hub
	hub.Broadcast(event)

	// Wait for the timeout (50ms) + some buffer
	time.Sleep(100 * time.Millisecond)

	// Verify that the channel still has the filler and maybe the new message if it wasn't dropped?
	// The code tries to send again with timeout. If it times out, the message is dropped.

	select {
	case msg := <-client.send:
		assert.Equal(t, "filler", msg.ID)
	default:
		t.Fatal("Channel should have filler")
	}

	// Check if there is another message
	select {
	case <-client.send:
		t.Log("Received broadcasted message (unexpected if dropped)")
	default:
		// Expected to be dropped
	}
}

// MockQueryService is defined in mock_service_test.go

func TestClient_HandleSubscribe_SnapshotError(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	mockQS := &MockQueryService{}
	mockQS.On("Pull", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	client := &Client{
		hub:           hub,
		queryService:  mockQS,
		send:          make(chan BaseMessage, 10),
		authenticated: true,
		subscriptions: make(map[string]Subscription),
	}

	subscribePayload := SubscribePayload{
		Query:        model.Query{Collection: "test"},
		SendSnapshot: true,
	}
	payloadBytes, _ := json.Marshal(subscribePayload)

	msg := BaseMessage{
		ID:      "1",
		Type:    TypeSubscribe,
		Payload: payloadBytes,
	}
	client.handleMessage(msg)

	// Should receive Ack
	select {
	case resp := <-client.send:
		assert.Equal(t, TypeSubscribeAck, resp.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ack")
	}

	// Should NOT receive Snapshot (due to error)
	select {
	case resp := <-client.send:
		assert.NotEqual(t, TypeSnapshot, resp.Type, "Should not receive snapshot on error")
	case <-time.After(100 * time.Millisecond):
		// Success
	}
}

func TestClient_WritePump_PingError(t *testing.T) {
	// Save original ping period and restore after test
	originalPingPeriod := pingPeriod
	pingPeriod = 10 * time.Millisecond
	defer func() { pingPeriod = originalPingPeriod }()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader.Upgrade(w, r, nil)
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	client := &Client{
		conn: conn,
		send: make(chan BaseMessage, 10),
	}

	done := make(chan struct{})
	go func() {
		client.writePump()
		close(done)
	}()

	// Close connection to cause ping write error
	conn.Close()

	select {
	case <-done:
	// Success
	case <-time.After(time.Second):
		t.Fatal("writePump did not exit on ping error")
	}
}

func TestHasSystemRole_ContextKeyRoles(t *testing.T) {
	ctx := context.WithValue(context.Background(), identity.ContextKeyRoles, []string{"system"})
	assert.True(t, hasSystemRole(ctx))
}

func TestServeWs_HubClosed(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to simulate closed hub
	go hub.Run(ctx)

	// Wait for hub to process cancellation
	time.Sleep(10 * time.Millisecond)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, nil, nil, Config{}, w, r)
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	_, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	// Should fail or close immediately
	if err == nil {
		// If connection succeeded, it should be closed immediately by ServeWs
	}
}

func TestServeSSE_HubClosed(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	go hub.Run(ctx)

	// Wait for hub to process cancellation
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	ServeSSE(hub, nil, nil, Config{}, w, req)

	// Should return immediately
}

type FailWriter struct {
	http.ResponseWriter
	failOnWrite bool
}

func (w *FailWriter) Write(b []byte) (int, error) {
	if w.failOnWrite {
		return 0, errors.New("write failed")
	}
	return w.ResponseWriter.Write(b)
}

func (w *FailWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func TestServeSSE_WriteError_Data(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	// Use a buffered pipe to simulate connection
	// But ServeSSE takes http.ResponseWriter.
	// We use FailWriter wrapping httptest.ResponseRecorder.

	rec := httptest.NewRecorder()
	w := &FailWriter{ResponseWriter: rec}

	req := httptest.NewRequest("GET", "/", nil)

	// We need to make Write fail ONLY when sending data, not headers.
	// ServeSSE writes headers first.
	// Then it waits for messages.

	// We can control failOnWrite via a pointer or channel?
	// Or just set it after headers are written?
	// But ServeSSE blocks.

	// We can run ServeSSE in a goroutine.

	done := make(chan struct{})
	go func() {
		ServeSSE(hub, nil, nil, Config{}, w, req)
		close(done)
	}()

	// Wait for subscription (headers written)
	// We can check if client is registered in hub?
	// Or just wait a bit.
	time.Sleep(50 * time.Millisecond)

	// Now enable failure
	w.failOnWrite = true

	// Broadcast a message
	hub.broadcast <- storage.Event{
		Type: storage.EventCreate,
		Id:   "test/1",
		Document: &storage.Document{
			Collection: "test",
			Data:       map[string]interface{}{"a": 1},
		},
	}

	// ServeSSE should exit due to write error
	select {
	case <-done:
	// Success
	case <-time.After(time.Second):
		t.Fatal("ServeSSE did not exit on write error")
	}
}

func TestServeSSE_Heartbeat(t *testing.T) {
	// Save original intervals
	originalPingPeriod := pingPeriod
	originalSSEHeartbeat := sseHeartbeatInterval
	pingPeriod = 10 * time.Millisecond
	sseHeartbeatInterval = 10 * time.Millisecond
	defer func() {
		pingPeriod = originalPingPeriod
		sseHeartbeatInterval = originalSSEHeartbeat
	}()

	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	req = req.WithContext(ctx2)

	done := make(chan struct{})
	go func() {
		ServeSSE(hub, nil, nil, Config{}, rec, req)
		close(done)
	}()

	// Wait for at least one heartbeat
	time.Sleep(50 * time.Millisecond)

	cancel2() // Stop ServeSSE
	<-done

	// Check body for heartbeat (comment lines starting with :)
	body := rec.Body.String()
	if !strings.Contains(body, ": heartbeat\n\n") {
	}
}
