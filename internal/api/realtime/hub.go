package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
)

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan storage.Event

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	mu sync.RWMutex

	runCtx   context.Context
	runCtxMu sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan storage.Event),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) Run(ctx context.Context) {
	h.setRunCtx(ctx)

	for {
		select {
		case <-ctx.Done():
			h.shutdownClients()
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				client.mu.Lock()
				msgTenant := determineEventTenant(message)
				if !client.allowAllTenants {
					if client.tenant == "" || msgTenant == "" || msgTenant != client.tenant {
						client.mu.Unlock()
						continue
					}
				}

				for subID, sub := range client.subscriptions {
					// Determine collection from event
					eventCollection := ""
					if message.Document != nil {
						eventCollection = message.Document.Collection
					} else {
						parts := strings.Split(message.Id, "/")
						if len(parts) >= 2 {
							eventCollection = parts[len(parts)-2]
						}
					}

					if sub.Query.Collection == "" || eventCollection == sub.Query.Collection {
						if sub.CelProgram != nil {
							if message.Document == nil {
								continue
							}
							out, _, err := sub.CelProgram.Eval(map[string]interface{}{
								"doc": message.Document.Data,
							})
							if err != nil {
								continue
							}
							if val, ok := out.Value().(bool); !ok || !val {
								continue
							}
						}

						var doc map[string]interface{}
						if sub.IncludeData {
							doc = flattenDocument(message.Document)
						}

						payload := EventPayload{
							SubID: subID,
							Delta: PublicEvent{
								Type:      message.Type,
								Document:  doc,
								ID:        message.Id,
								Timestamp: message.Timestamp,
							},
						}
						msg := BaseMessage{Type: TypeEvent, Payload: mustMarshal(payload)}

						select {
						case client.send <- msg:
						default:
							select {
							case client.send <- msg:
							case <-time.After(50 * time.Millisecond):
							}
						}
					}
				}
				client.mu.Unlock()
			}
			h.mu.RUnlock()
		}
	}
}

func determineEventTenant(evt storage.Event) string {
	if evt.TenantID != "" {
		return evt.TenantID
	}
	if evt.Document != nil && evt.Document.TenantID != "" {
		return evt.Document.TenantID
	}
	if evt.Before != nil && evt.Before.TenantID != "" {
		return evt.Before.TenantID
	}
	return ""
}

func (h *Hub) Broadcast(event storage.Event) {
	select {
	case <-h.Done():
		return
	default:
	}

	select {
	case h.broadcast <- event:
	case <-h.Done():
	}
}

func (h *Hub) Register(client *Client) bool {
	select {
	case h.register <- client:
		return true
	case <-h.Done():
		return false
	}
}

func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.Done():
	}
}

func flattenDocument(doc *storage.Document) map[string]interface{} {
	if doc == nil {
		return nil
	}
	flat := make(map[string]interface{})
	// Copy data
	for k, v := range doc.Data {
		flat[k] = v
	}

	// Ensure ID is present (extract from Fullpath)
	if _, ok := flat["id"]; !ok {
		if idx := strings.LastIndex(doc.Fullpath, "/"); idx != -1 {
			flat["id"] = doc.Fullpath[idx+1:]
		}
	}

	// Add system fields
	flat["version"] = doc.Version
	flat["updatedAt"] = doc.UpdatedAt
	flat["createdAt"] = doc.CreatedAt
	flat["collection"] = doc.Collection
	return flat
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v) // Should not fail for internal types
	return b
}

func (h *Hub) setRunCtx(ctx context.Context) {
	h.runCtxMu.Lock()
	h.runCtx = ctx
	h.runCtxMu.Unlock()
}

func (h *Hub) Done() <-chan struct{} {
	h.runCtxMu.RLock()
	defer h.runCtxMu.RUnlock()
	if h.runCtx == nil {
		return nil
	}
	return h.runCtx.Done()
}

func (h *Hub) shutdownClients() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		close(client.send)
		delete(h.clients, client)
	}
}
