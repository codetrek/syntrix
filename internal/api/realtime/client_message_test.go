package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

type mockQueryForClient struct{}

func (m *mockQueryForClient) GetDocument(ctx context.Context, path string) (common.Document, error) {
	return nil, nil
}
func (m *mockQueryForClient) CreateDocument(ctx context.Context, doc common.Document) error {
	return nil
}
func (m *mockQueryForClient) ReplaceDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}
func (m *mockQueryForClient) PatchDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}
func (m *mockQueryForClient) DeleteDocument(ctx context.Context, path string) error { return nil }
func (m *mockQueryForClient) ExecuteQuery(ctx context.Context, q storage.Query) ([]common.Document, error) {
	return nil, nil
}
func (m *mockQueryForClient) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return nil, nil
}
func (m *mockQueryForClient) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return &storage.ReplicationPullResponse{Documents: []*storage.Document{{Fullpath: "users/1", Collection: "users", Data: map[string]interface{}{"foo": "bar"}}}}, nil
}
func (m *mockQueryForClient) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}
func (m *mockQueryForClient) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	return fn(ctx, m)
}

func TestClientHandleMessage_AuthAck(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: &mockQueryForClient{}, send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription)}
	c.handleMessage(BaseMessage{Type: TypeAuth, ID: "req"})

	select {
	case msg := <-c.send:
		assert.Equal(t, TypeAuthAck, msg.Type)
		assert.Equal(t, "req", msg.ID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected auth ack")
	}
}

func TestClientHandleMessage_SubscribeSnapshot(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: &mockQueryForClient{}, send: make(chan BaseMessage, 2), subscriptions: make(map[string]Subscription)}
	payload := SubscribePayload{Query: storage.Query{Collection: "users"}, IncludeData: true, SendSnapshot: true}
	b, _ := json.Marshal(payload)

	c.handleMessage(BaseMessage{Type: TypeSubscribe, ID: "sub", Payload: b})

	// Expect SubscribeAck then Snapshot
	msg1 := <-c.send
	msg2 := <-c.send
	assert.Equal(t, TypeSubscribeAck, msg1.Type)
	assert.Equal(t, TypeSnapshot, msg2.Type)
}

func TestClientHandleMessage_Unsubscribe(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: &mockQueryForClient{}, send: make(chan BaseMessage, 1), subscriptions: map[string]Subscription{"sub": {}}}
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
	qs := &mockQueryForClient{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, w, r)
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
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeAuthAck, resp.Type)
}

func TestServeWs_RejectsCrossOrigin(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)
	qs := &mockQueryForClient{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, w, r)
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
	c := &Client{hub: NewHub(), queryService: &mockQueryForClient{}, send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription)}
	payload := SubscribePayload{Query: storage.Query{Filters: []storage.Filter{{Field: "age", Op: "!", Value: 1}}}}
	b, _ := json.Marshal(payload)

	c.handleMessage(BaseMessage{Type: TypeSubscribe, ID: "sub-err", Payload: b})

	msg := <-c.send
	assert.Equal(t, TypeError, msg.Type)
}

func TestHandleMessage_SubscribeBadJSON(t *testing.T) {
	c := &Client{hub: NewHub(), queryService: &mockQueryForClient{}, send: make(chan BaseMessage, 1), subscriptions: make(map[string]Subscription)}

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
	msgType, data, err := conn.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, msgType)
	assert.Contains(t, string(data), "auth_ack")

	// Closing channel should make writePump emit a close frame and exit
	close(c.send)
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

	qs := &mockQueryForClient{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, qs, w, r)
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
	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeAuthAck, resp.Type)
	assert.Equal(t, "auth-1", resp.ID)

	// Subscribe with snapshot -> expect ack then snapshot
	subPayload := SubscribePayload{Query: storage.Query{Collection: "users"}, IncludeData: true, SendSnapshot: true}
	body, _ := json.Marshal(subPayload)
	assert.NoError(t, conn.WriteJSON(BaseMessage{Type: TypeSubscribe, ID: "sub-1", Payload: body}))

	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeSubscribeAck, resp.Type)

	assert.NoError(t, conn.ReadJSON(&resp))
	assert.Equal(t, TypeSnapshot, resp.Type)
}
