package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
)

func TestHub_Broadcast_ComplexFilters(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	client := &Client{
		hub:             hub,
		send:            make(chan BaseMessage, 10),
		subscriptions:   make(map[string]Subscription),
		allowAllTenants: true,
	}

	// Test "in" operator
	filtersIn := []model.Filter{
		{Field: "role", Op: "in", Value: []interface{}{"admin", "editor"}},
	}
	prgIn, err := compileFiltersToCEL(filtersIn)
	assert.NoError(t, err)

	client.subscriptions["sub_in"] = Subscription{
		Query: model.Query{
			Collection: "users",
			Filters:    filtersIn,
		},
		IncludeData: true,
		CelProgram:  prgIn,
	}

	// Test "array-contains" operator
	filtersContains := []model.Filter{
		{Field: "tags", Op: "array-contains", Value: "golang"},
	}
	prgContains, err := compileFiltersToCEL(filtersContains)
	assert.NoError(t, err)

	client.subscriptions["sub_contains"] = Subscription{
		Query: model.Query{
			Collection: "posts",
			Filters:    filtersContains,
		},
		IncludeData: true,
		CelProgram:  prgContains,
	}

	hub.Register(client)
	time.Sleep(50 * time.Millisecond)

	// 1. Test "in" - Match
	hub.Broadcast(storage.Event{
		Type: storage.EventCreate,
		Id:   "users/u1",
		Document: &storage.Document{
			Id:         "users/u1",
			Collection: "users",
			Data:       map[string]interface{}{"role": "admin"},
		},
	})

	select {
	case msg := <-client.send:
		var payload EventPayload
		json.Unmarshal(msg.Payload, &payload)
		assert.Equal(t, "sub_in", payload.SubID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for 'in' match")
	}

	// 2. Test "in" - No Match
	hub.Broadcast(storage.Event{
		Type: storage.EventCreate,
		Id:   "users/u2",
		Document: &storage.Document{
			Id:         "users/u2",
			Collection: "users",
			Data:       map[string]interface{}{"role": "guest"},
		},
	})

	select {
	case msg := <-client.send:
		t.Fatalf("Should not receive 'in' mismatch: %+v", msg)
	case <-time.After(100 * time.Millisecond):
		// OK
	}

	// 3. Test "array-contains" - Match
	hub.Broadcast(storage.Event{
		Type: storage.EventCreate,
		Id:   "posts/p1",
		Document: &storage.Document{
			Id:         "posts/p1",
			Collection: "posts",
			Data:       map[string]interface{}{"tags": []interface{}{"rust", "golang"}},
		},
	})

	select {
	case msg := <-client.send:
		var payload EventPayload
		json.Unmarshal(msg.Payload, &payload)
		assert.Equal(t, "sub_contains", payload.SubID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for 'array-contains' match")
	}

	// 4. Test "array-contains" - No Match
	hub.Broadcast(storage.Event{
		Type: storage.EventCreate,
		Id:   "posts/p2",
		Document: &storage.Document{
			Id:         "posts/p2",
			Collection: "posts",
			Data:       map[string]interface{}{"tags": []interface{}{"python", "java"}},
		},
	})

	select {
	case msg := <-client.send:
		t.Fatalf("Should not receive 'array-contains' mismatch: %+v", msg)
	case <-time.After(100 * time.Millisecond):
		// OK
	}
}
