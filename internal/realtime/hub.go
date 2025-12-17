package realtime

import (
	"encoding/json"
	"strings"
	"sync"
	"syntrix/internal/storage"
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
}

func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan storage.Event),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
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
				// Check if client is subscribed to this event
				// For now, we just broadcast to all, but Client.send is now chan BaseMessage
				// We need to wrap it or let Client filter it?
				// Better: Hub filters. But Hub needs to know subscriptions.

				// Let's iterate and check subscriptions
				client.mu.Lock()
				for subID, sub := range client.subscriptions {
					// Determine collection from event
					eventCollection := ""
					if message.Document != nil {
						eventCollection = message.Document.Collection
					} else {
						// Fallback for delete events: extract from path
						parts := strings.Split(message.Path, "/")
						if len(parts) >= 2 {
							eventCollection = parts[len(parts)-2]
						}
					}

					// Simple filter: Collection match
					if sub.Query.Collection == "" || eventCollection == sub.Query.Collection {
						// Send event
						var doc map[string]interface{}
						if sub.IncludeData {
							doc = flattenDocument(message.Document)
						}

						payload := EventPayload{
							SubID: subID,
							Delta: PublicEvent{
								Type:      message.Type,
								Document:  doc,
								Path:      message.Path,
								Timestamp: message.Timestamp,
							},
						}
						msg := BaseMessage{
							Type:    TypeEvent,
							Payload: mustMarshal(payload),
						}

						select {
						case client.send <- msg:
						default:
							// close(client.send)
							// delete(h.clients, client)
						}
					}
				}
				client.mu.Unlock()
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) Broadcast(event storage.Event) {
	h.broadcast <- event
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
	// Add system fields
	flat["_version"] = doc.Version
	flat["_updated_at"] = doc.UpdatedAt
	return flat
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v) // Should not fail for internal types
	return b
}
