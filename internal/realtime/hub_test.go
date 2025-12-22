package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub_Broadcast(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

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
	hub.Register(client)

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	// Broadcast an event
	evt := storage.Event{
		Type: storage.EventCreate,
		Id:   "users/123",
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
		assert.Equal(t, evt.Id, payload.Delta.ID)
		assert.Equal(t, evt.Document.Data["name"], payload.Delta.Document["name"])
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for broadcast message")
	}

	// Unregister client
	hub.Unregister(client)
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
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

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
	hub.Register(client)
	time.Sleep(50 * time.Millisecond)

	// 1. Broadcast event that does NOT match (age = 18)
	evtNoMatch := storage.Event{
		Type: storage.EventCreate,
		Id:   "users/young",
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
		Id:   "users/adult",
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

func TestHub_Broadcast_WaitsForSlowClient(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 1),
		subscriptions: map[string]Subscription{"sub": {Query: storage.Query{Collection: "users"}, IncludeData: true}},
	}

	hub.Register(client)

	// Fill the channel to trigger the backpressure path
	client.send <- BaseMessage{Type: TypeEvent}

	// Broadcast while channel is full; reader will drain after a short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		<-client.send
	}()

	hub.Broadcast(storage.Event{
		Type:     storage.EventCreate,
		Id:       "users/123",
		Document: &storage.Document{Id: "users/123", Collection: "users", Data: map[string]interface{}{}}})

	select {
	case <-client.send:
		// received after buffer freed
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected buffered send to succeed")
	}
}

func TestHub_Run_CancelsGracefully(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	hub := NewHub()
	done := make(chan struct{})

	go func() {
		hub.Run(hubCtx)
		close(done)
	}()

	client := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 1),
		subscriptions: map[string]Subscription{"sub": {Query: storage.Query{Collection: "users"}, IncludeData: true}},
	}

	require.True(t, hub.Register(client))
	hubCancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("hub did not exit after cancel")
	}

	// Calls after cancellation should not block
	hub.Unregister(client)
	hub.Broadcast(storage.Event{Id: "users/1", Type: storage.EventCreate})
}
