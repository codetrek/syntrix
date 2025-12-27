package realtime

import (
	"context"
	"encoding/json"
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
)

func TestClientHandleMessage_AuthAck(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: &MockQueryService{}, send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription)}
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req"})

	select {
	case msg := <-c.send:
		assert.Equal(t, TypeAuthAck, msg.Type)
		assert.Equal(t, "req", msg.ID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected auth ack")
	}
}

func TestClientHandleMessage_AuthError(t *testing.T) {
	c := &Client{
		hub:          NewHub(),
		queryService: &MockQueryService{},
		send:         make(chan BaseMessage, 1),
		auth:         &mockAuthService{},
	}

	// Case 1: Invalid Payload
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req1", Payload: []byte(`invalid`)})
	select {
	case msg := <-c.send:
		assert.Equal(t, TypeError, msg.Type)
		assert.Equal(t, "req1", msg.ID)
		assert.Contains(t, string(msg.Payload), "invalid_auth")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected error")
	}

	// Case 2: Invalid Token
	payload, _ := json.Marshal(AuthPayload{Token: "bad"})
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req2", Payload: payload})
	select {
	case msg := <-c.send:
		assert.Equal(t, TypeError, msg.Type)
		assert.Equal(t, "req2", msg.ID)
		assert.Contains(t, string(msg.Payload), "unauthorized")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected error")
	}
}

func TestClientHandleMessage_AuthSuccess(t *testing.T) {
	c := &Client{
		hub:          NewHub(),
		queryService: &MockQueryService{},
		send:         make(chan BaseMessage, 1),
		auth:         &mockAuthService{},
	}

	payload, _ := json.Marshal(AuthPayload{Token: "good"})
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req-ok", Payload: payload})

	select {
	case msg := <-c.send:
		assert.Equal(t, TypeAuthAck, msg.Type)
		assert.Equal(t, "req-ok", msg.ID)
		assert.True(t, c.authenticated)
		assert.Equal(t, "default", c.tenant)
		assert.False(t, c.allowAllTenants)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected auth ack")
	}
}

type mockAuthServiceSystem struct {
	mockAuthService
}

func (m *mockAuthServiceSystem) ValidateToken(tokenString string) (*identity.Claims, error) {
	if tokenString == "system" {
		return &identity.Claims{TenantID: "default", Roles: []string{"system"}}, nil
	}
	return m.mockAuthService.ValidateToken(tokenString)
}

func TestClientHandleMessage_AuthSystemRole(t *testing.T) {
	c := &Client{
		hub:          NewHub(),
		queryService: setupMockQuery(),
		send:         make(chan BaseMessage, 1),
		auth:         &mockAuthServiceSystem{},
	}

	payload, _ := json.Marshal(AuthPayload{Token: "system"})
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req", Payload: payload})

	select {
	case msg := <-c.send:
		assert.Equal(t, TypeAuthAck, msg.Type)
		assert.True(t, c.authenticated)
		assert.True(t, c.allowAllTenants)
		assert.Equal(t, "default", c.tenant)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected auth ack")
	}
}

func TestClientHandleMessage_SubscribeSnapshot(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: setupMockQuery(), send: make(chan BaseMessage, 2), subscriptions: make(map[string]Subscription), authenticated: true}
	payload := SubscribePayload{Query: model.Query{Collection: "users"}, IncludeData: true, SendSnapshot: true}
	b, _ := json.Marshal(payload)

	c.handleMessage(BaseMessage{Type: TypeSubscribe, ID: "sub", Payload: b})

	// Expect SubscribeAck then Snapshot
	msg1 := <-c.send
	msg2 := <-c.send
	assert.Equal(t, TypeSubscribeAck, msg1.Type)
	assert.Equal(t, TypeSnapshot, msg2.Type)
}

func TestClientHandleMessage_Unsubscribe(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: setupMockQuery(), send: make(chan BaseMessage, 1), subscriptions: map[string]Subscription{"sub": {}}, authenticated: true}
	payload := UnsubscribePayload{ID: "sub"}
	b, _ := json.Marshal(payload)

	c.handleMessage(BaseMessage{Type: TypeUnsubscribe, ID: "req1", Payload: b})

	_, ok := c.subscriptions["sub"]
	assert.False(t, ok)

	select {
	case msg := <-c.send:
		assert.Equal(t, TypeUnsubscribeAck, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected unsubscribe ack")
	}
}

func TestReadPump_InvalidJSONContinues(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)
	qs := setupMockQuery()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, nil, Config{EnableAuth: false}, w, r)
	}))
	defer server.Close()

	u := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	assert.NoError(t, err)
	defer conn.Close()

	// Send invalid JSON to trigger unmarshal error path; expect no panic and ability to continue
	assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("{invalid")))

	// Follow with valid auth to ensure readPump still processes
	authMsg := BaseMessage{Type: TypeAuth, ID: "auth-2"}
	assert.NoError(t, conn.WriteJSON(authMsg))

	var resp BaseMessage
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeAuthAck, resp.Type)
}

func TestServeWs_RejectsCrossOrigin(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)
	qs := setupMockQuery()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, nil, Config{EnableAuth: false}, w, r)
	}))
	defer server.Close()

	// Origin does not match server host
	dialer := websocket.Dialer{}
	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	_, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), header)
	assert.Error(t, err)
}

func TestHandleMessage_SubscribeCompileError(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: setupMockQuery(), send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription), authenticated: true}
	payload := SubscribePayload{Query: model.Query{Filters: []model.Filter{{Field: "age", Op: "!", Value: 1}}}}
	b, _ := json.Marshal(payload)

	c.handleMessage(BaseMessage{Type: TypeSubscribe, ID: "sub-err", Payload: b})

	msg := <-c.send
	assert.Equal(t, TypeError, msg.Type)
}

func TestHandleMessage_SubscribeBadJSON(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: setupMockQuery(), send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription), authenticated: true}

	c.handleMessage(BaseMessage{Type: TypeSubscribe, ID: "sub-bad", Payload: []byte("{bad")})

	select {
	case <-c.send:
		t.Fatal("should not send when payload invalid")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestWritePump_StopsOnChannelClose(t *testing.T) {
	clientCh := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &Client{conn: conn, send: make(chan BaseMessage, 1)}
		clientCh <- c
		go c.writePump()
	}))
	defer server.Close()

	u := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	assert.NoError(t, err)
	defer conn.Close()

	c := <-clientCh

	// Send one message through writePump
	c.send <- BaseMessage{Type: TypeAuthAck, ID: "x"}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, data, err := conn.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, msgType)
	assert.Contains(t, string(data), "auth_ack")

	// Closing channel should make writePump emit a close frame and exit
	close(c.send)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	assert.Error(t, err)
}

func TestWritePump_SendsPing(t *testing.T) {
	original := pingPeriod
	pingPeriod = 10 * time.Millisecond
	defer func() { pingPeriod = original }()

	_ = NewHub()
	clientCh := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &Client{conn: conn, send: make(chan BaseMessage, 1)}
		clientCh <- c
		go c.writePump()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	assert.NoError(t, err)
	defer conn.Close()

	pings := make(chan struct{}, 1)
	conn.SetPingHandler(func(appData string) error {
		pings <- struct{}{}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// drive control frame processing
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
	}()

	// Wait for ping
	select {
	case <-pings:
	case <-time.After(800 * time.Millisecond):
		t.Fatal("expected ping from writePump")
	}

	// Cleanup writePump goroutine
	c := <-clientCh
	close(c.send)
}

func TestServeWs_ReadWriteCycle(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := setupMockQuery()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, nil, Config{EnableAuth: false}, w, r)
	}))
	defer server.Close()

	u := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	assert.NoError(t, err)
	defer conn.Close()

	// Auth message -> expect ack
	authMsg := BaseMessage{Type: TypeAuth, ID: "auth-1"}
	assert.NoError(t, conn.WriteJSON(authMsg))

	var resp BaseMessage
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeAuthAck, resp.Type)
	assert.Equal(t, "auth-1", resp.ID)

	// Subscribe with snapshot -> expect ack then snapshot
	subPayload := SubscribePayload{Query: model.Query{Collection: "users"}, IncludeData: true, SendSnapshot: true}
	body, _ := json.Marshal(subPayload)
	assert.NoError(t, conn.WriteJSON(BaseMessage{Type: TypeSubscribe, ID: "sub-1", Payload: body}))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeSubscribeAck, resp.Type)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeSnapshot, resp.Type)
}

func setupMockQuery() *MockQueryService {
	m := new(MockQueryService)
	// Mock Pull for Snapshot
	m.On("Pull", mock.Anything, mock.Anything, mock.Anything).Return(&storage.ReplicationPullResponse{
		Documents: []*storage.Document{{Id: "1", Data: map[string]interface{}{"name": "test"}}},
	}, nil).Maybe()
	return m
}
