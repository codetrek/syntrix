package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/google/cel-go/cel"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Maximum message size allowed from peer.
	// Increased from 512 to 64KB to accommodate JWT tokens and larger payloads
	maxMessageSize = 64 * 1024
)

// Send pings to peer with this period. Must be less than pongWait.
var pingPeriod = (pongWait * 9) / 10

// Heartbeat interval for SSE clients.
var sseHeartbeatInterval = 15 * time.Second

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     safeCheckOrigin,
}

// safeCheckOrigin validates WebSocket connection origins.
// It allows:
// - Empty origin (non-browser clients)
// - Same host:port as the request
// - Same host (ignoring port) for development scenarios
// This mitigates cross-site WebSocket abuse while keeping same-origin and local dev clients working.
func safeCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	// Compare host (includes port) to ensure exact match with request host.
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}

	// Allow same-host connections across different ports for development.
	// This covers localhost, 127.0.0.1, and LAN IPs like 192.168.x.x.
	originHost := strings.Split(u.Host, ":")[0]
	requestHost := strings.Split(r.Host, ":")[0]

	return strings.EqualFold(originHost, requestHost)
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub          *Hub
	queryService query.Service
	auth         identity.AuthN
	cfg          Config

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan BaseMessage

	// Subscriptions
	subscriptions map[string]Subscription
	mu            sync.Mutex

	tenant          string
	authenticated   bool
	allowAllTenants bool
}

type Subscription struct {
	Query       model.Query
	IncludeData bool
	CelProgram  cel.Program
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	log.Println("[Info][WS] WebSocket connection established")

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[Warning][WS] Websocket connection closed: %v", err)
			} else {
				log.Println("[Info][WS] WebSocket connection closed")
			}
			break
		}

		var msg BaseMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[Warning][WS] unmarshalling message: %v", err)
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg BaseMessage) {
	log.Printf("[Info][WS] Received message type=%s id=%s", msg.Type, msg.ID)
	switch msg.Type {
	case TypeAuth:
		c.handleAuth(msg)
	case TypeSubscribe:
		if !c.authenticated {
			c.send <- BaseMessage{ID: msg.ID, Type: TypeError, Payload: mustMarshal(ErrorPayload{Code: "unauthorized", Message: "auth required"})}
			return
		}
		var payload SubscribePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("[Error][WS] unmarshalling subscribe payload: %v", err)
			return
		}

		// Compile CEL filters
		prg, err := compileFiltersToCEL(payload.Query.Filters)
		if err != nil {
			log.Printf("[Error][WS] Failed to compile filters: %v", err)
			errPayload, _ := json.Marshal(map[string]string{"message": "Invalid filter expression: " + err.Error()})
			c.send <- BaseMessage{
				ID:      msg.ID,
				Type:    TypeError,
				Payload: errPayload,
			}
			return
		}

		c.mu.Lock()
		c.subscriptions[msg.ID] = Subscription{
			Query:       payload.Query,
			IncludeData: payload.IncludeData,
			CelProgram:  prg,
		}
		c.mu.Unlock()
		log.Printf("[Info][WS] Subscribed to collection=%s id=%s includeData=%v", payload.Query.Collection, msg.ID, payload.IncludeData)

		// Send Ack
		c.send <- BaseMessage{ID: msg.ID, Type: TypeSubscribeAck}

		if payload.SendSnapshot {
			// Fetch snapshot
			req := storage.ReplicationPullRequest{
				Collection: payload.Query.Collection,
				Checkpoint: 0,    // From beginning
				Limit:      1000, // Reasonable limit for snapshot
			}
			// Use a background context or create one with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			resp, err := c.queryService.Pull(ctx, c.tenant, req)
			if err != nil {
				log.Printf("[Error][WS] Snapshot pull failed: %v", err)
				return
			}

			flatDocs := make([]map[string]interface{}, len(resp.Documents))
			for i, doc := range resp.Documents {
				flatDocs[i] = flattenDocument(doc)
			}

			snapshotPayload := SnapshotPayload{
				SubID:     msg.ID,
				Documents: flatDocs,
			}

			c.send <- BaseMessage{
				ID:      msg.ID,
				Type:    TypeSnapshot,
				Payload: mustMarshal(snapshotPayload),
			}
		}
	case TypeUnsubscribe:
		if !c.authenticated {
			c.send <- BaseMessage{ID: msg.ID, Type: TypeError, Payload: mustMarshal(ErrorPayload{Code: "unauthorized", Message: "auth required"})}
			return
		}
		var payload UnsubscribePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("[Warning][WS] unmarshalling unsubscribe payload: %v", err)
			return
		}
		c.mu.Lock()
		delete(c.subscriptions, payload.ID)
		c.mu.Unlock()
		log.Printf("[Info][WS] Unsubscribed id=%s", payload.ID)
		c.send <- BaseMessage{ID: msg.ID, Type: TypeUnsubscribeAck}
	}
}

func (c *Client) handleAuth(msg BaseMessage) {
	if c.auth == nil {
		c.authenticated = true
		c.send <- BaseMessage{ID: msg.ID, Type: TypeAuthAck}
		return
	}

	var payload AuthPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		c.send <- BaseMessage{ID: msg.ID, Type: TypeError, Payload: mustMarshal(ErrorPayload{Code: "invalid_auth", Message: "invalid payload"})}
		return
	}

	claims, err := c.auth.ValidateToken(payload.Token)
	if err != nil || claims == nil || claims.TenantID == "" {
		c.send <- BaseMessage{ID: msg.ID, Type: TypeError, Payload: mustMarshal(ErrorPayload{Code: "unauthorized", Message: "invalid token"})}
		return
	}

	c.mu.Lock()
	c.tenant = claims.TenantID
	c.allowAllTenants = hasSystemRoleFromClaims(claims)
	c.authenticated = true
	c.mu.Unlock()

	c.send <- BaseMessage{ID: msg.ID, Type: TypeAuthAck}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			log.Printf("[Debug] Client writePump: received message type=%s id=%s", message.Type, message.ID)
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteJSON(message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func tenantFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	tenant, _ := ctx.Value(identity.ContextKeyTenant).(string)
	if tenant == "" {
		if claims, ok := ctx.Value(identity.ContextKeyClaims).(*identity.Claims); ok && claims != nil {
			tenant = claims.TenantID
		}
	}
	allowAll := hasSystemRole(ctx)
	return tenant, allowAll
}

func tenantFromContextMust(ctx context.Context, w http.ResponseWriter) string {
	tenant, _ := tenantFromContext(ctx)
	if tenant == "" {
		http.Error(w, "tenant required", http.StatusUnauthorized)
	}
	return tenant
}

func hasSystemRole(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if roles, ok := ctx.Value(identity.ContextKeyRoles).([]string); ok {
		for _, r := range roles {
			if strings.EqualFold(r, "system") {
				return true
			}
		}
	}
	return false
}

func hasSystemRoleFromClaims(claims *identity.Claims) bool {
	if claims == nil {
		return false
	}
	for _, r := range claims.Roles {
		if strings.EqualFold(r, "system") {
			return true
		}
	}
	return false
}

func tokenFromQuery(r *http.Request) string {
	if r == nil {
		return ""
	}
	q := r.URL.Query()
	if v := q.Get("access_token"); v != "" {
		return v
	}
	if v := q.Get("token"); v != "" {
		return v
	}
	return ""
}

func hasCredentials(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get("Authorization") != ""
}

func checkAllowedOrigin(origin string, reqHost string, cfg Config, credentialed bool) error {
	if origin == "" {
		if credentialed && !cfg.AllowDevOrigin {
			return errors.New("origin required when using credentials")
		}
		return nil
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return errors.New("origin not allowed")
	}

	// Allow same host origin
	originHost := strings.Split(parsed.Host, ":")[0]
	reqHostPart := strings.Split(reqHost, ":")[0]
	if strings.EqualFold(originHost, reqHostPart) {
		return nil
	}

	if cfg.AllowDevOrigin {
		if originHost == "localhost" || originHost == "127.0.0.1" {
			return nil
		}
	}

	trimmedOrigin := strings.TrimRight(origin, "/")
	for _, allowed := range cfg.AllowedOrigins {
		if allowed == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(allowed, "/"), trimmedOrigin) {
			return nil
		}
	}

	return errors.New("origin not allowed")
}

// ServeReplicationStream handles websocket requests from the peer.
func ServeWs(hub *Hub, qs query.Service, auth identity.AuthN, cfg Config, w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(r.Context())

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	tenant, allowAll := tenantFromContext(r.Context())

	client := &Client{
		hub:             hub,
		queryService:    qs,
		auth:            auth,
		cfg:             cfg,
		conn:            conn,
		send:            make(chan BaseMessage, 256),
		subscriptions:   make(map[string]Subscription),
		tenant:          tenant,
		authenticated:   !cfg.EnableAuth || tenant != "",
		allowAllTenants: allowAll || !cfg.EnableAuth,
	}

	if !client.hub.Register(client) {
		conn.Close()
		return
	}

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}

// ServeSSE handles Server-Sent Events requests.
func ServeSSE(hub *Hub, qs query.Service, auth identity.AuthN, cfg Config, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if tokenFromQuery(r) != "" {
		http.Error(w, "Query token not allowed", http.StatusUnauthorized)
		return
	}

	origin := r.Header.Get("Origin")
	if err := checkAllowedOrigin(origin, r.Host, cfg, hasCredentials(r)); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Add("Vary", "Origin")

	// Create client without websocket connection
	tenant := ""
	allowAll := false
	if cfg.EnableAuth {
		tenant, allowAll = tenantFromContext(ctx)
		if tenant == "" {
			http.Error(w, "tenant required", http.StatusUnauthorized)
			return
		}
	} else {
		tenant, allowAll = tenantFromContext(ctx)
	}

	client := &Client{
		hub:             hub,
		queryService:    qs,
		auth:            auth,
		cfg:             cfg,
		conn:            nil,
		send:            make(chan BaseMessage, 256),
		subscriptions:   make(map[string]Subscription),
		tenant:          tenant,
		authenticated:   !cfg.EnableAuth || tenant != "",
		allowAllTenants: allowAll || !cfg.EnableAuth,
	}

	// Handle initial subscription from query params
	collection := r.URL.Query().Get("collection")
	// If collection is provided, subscribe to it.
	// If not provided, we subscribe to everything (empty string matches all in Hub).
	// We use "default" as the subscription ID.
	client.subscriptions["default"] = Subscription{
		Query:       model.Query{Collection: collection},
		IncludeData: true, // SSE clients typically expect data
	}
	log.Printf("[Info][SSE] connection established. Subscribed to collection=%s", collection)

	if !client.hub.Register(client) {
		return
	}

	// Unregister on exit
	defer func() {
		client.hub.Unregister(client)
		log.Println("[Info][SSE] connection closed")
	}()

	// Send initial comment to establish connection
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Heartbeat ticker
	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Info][SSE] context cancelled, closing connection")
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				log.Println("[Warning][SSE] heartbeat error:", err)
				return
			}
			flusher.Flush()
		case message, ok := <-client.send:
			if !ok {
				log.Println("[Info][SSE] send channel closed")
				return
			}
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				log.Println("[Error][SSE] write error:", err)
				return
			}
			flusher.Flush()
		}
	}
}
