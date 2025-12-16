package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestHub_Broadcast(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	// Create a mock client
	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		subscriptions: make(map[string]storage.Query),
	}

	// Add a subscription
	client.subscriptions["sub1"] = storage.Query{Collection: "users"}

	// Register client
	hub.register <- client

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	// Broadcast an event
	evt := storage.Event{
		Type: storage.EventCreate,
		Path: "users/123",
		Document: &storage.Document{
			Id:         "users/123",
			Collection: "users",
			Data:       map[string]interface{}{"name": "Alice"},
		},
	}
	hub.Broadcast(evt)

	// Check if client received it
	select {
	case received := <-client.send:
		assert.Equal(t, TypeEvent, received.Type)

		var payload EventPayload
		err := json.Unmarshal(received.Payload, &payload)
		assert.NoError(t, err)

		assert.Equal(t, "sub1", payload.SubID)
		assert.Equal(t, evt.Type, payload.Delta.Type)
		assert.Equal(t, evt.Path, payload.Delta.Path)
		assert.Equal(t, evt.Document.Data["name"], payload.Delta.Document["name"])
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for broadcast message")
	}

	// Unregister client
	hub.unregister <- client
	time.Sleep(100 * time.Millisecond)

	// Broadcast another event
	hub.Broadcast(evt)

	// Client should not receive it (channel might be closed or just empty)
	select {
	case _, ok := <-client.send:
		if ok {
			t.Fatal("Client received message after unregister")
		}
	default:
		// OK
	}
}
