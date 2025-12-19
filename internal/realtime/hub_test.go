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
		subscriptions: make(map[string]Subscription),
	}

	// Add a subscription
	client.subscriptions["sub1"] = Subscription{
		Query:       storage.Query{Collection: "users"},
		IncludeData: true,
	}

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

func TestHub_Broadcast_WithFilter(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	// Create a mock client
	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		subscriptions: make(map[string]Subscription),
	}

	// Add a subscription with filter: age > 20
	filters := []storage.Filter{
		{Field: "age", Op: ">", Value: 20},
	}
	prg, err := compileFiltersToCEL(filters)
	assert.NoError(t, err)

	client.subscriptions["sub1"] = Subscription{
		Query: storage.Query{
			Collection: "users",
			Filters:    filters,
		},
		IncludeData: true,
		CelProgram:  prg,
	}

	// Register client
	hub.register <- client
	time.Sleep(50 * time.Millisecond)

	// 1. Broadcast event that does NOT match (age = 18)
	evtNoMatch := storage.Event{
		Type: storage.EventCreate,
		Path: "users/young",
		Document: &storage.Document{
			Id:         "users/young",
			Collection: "users",
			Data:       map[string]interface{}{"name": "Bob", "age": 18},
		},
	}
	hub.Broadcast(evtNoMatch)

	// Expect NO message
	select {
	case msg := <-client.send:
		t.Fatalf("Received message that should have been filtered out: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// OK
	}

	// 2. Broadcast event that DOES match (age = 25)
	evtMatch := storage.Event{
		Type: storage.EventCreate,
		Path: "users/adult",
		Document: &storage.Document{
			Id:         "users/adult",
			Collection: "users",
			Data:       map[string]interface{}{"name": "Alice", "age": 25},
		},
	}
	hub.Broadcast(evtMatch)

	// Expect message
	select {
	case received := <-client.send:
		assert.Equal(t, TypeEvent, received.Type)
		var payload EventPayload
		err := json.Unmarshal(received.Payload, &payload)
		assert.NoError(t, err)
		assert.Equal(t, "sub1", payload.SubID)
		assert.Equal(t, "Alice", payload.Delta.Document["name"])
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for matching broadcast message")
	}
}
