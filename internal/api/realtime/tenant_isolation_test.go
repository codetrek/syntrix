package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestHub_TenantIsolation(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	// Client A: Tenant A
	clientA := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		subscriptions: make(map[string]Subscription),
		tenant:        "tenantA",
	}
	clientA.subscriptions["subA"] = Subscription{
		Query:       model.Query{Collection: "users"},
		IncludeData: true,
	}
	hub.Register(clientA)

	// Client B: Tenant B
	clientB := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		subscriptions: make(map[string]Subscription),
		tenant:        "tenantB",
	}
	clientB.subscriptions["subB"] = Subscription{
		Query:       model.Query{Collection: "users"},
		IncludeData: true,
	}
	hub.Register(clientB)

	// Client C: Tenant A (Another client in Tenant A)
	clientC := &Client{
		hub:           hub,
		send:          make(chan BaseMessage, 10),
		subscriptions: make(map[string]Subscription),
		tenant:        "tenantA",
	}
	clientC.subscriptions["subC"] = Subscription{
		Query:       model.Query{Collection: "users"},
		IncludeData: true,
	}
	hub.Register(clientC)

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// 1. Broadcast event for Tenant A
	// The ID format "tenant:id" is used by determineEventTenant to extract tenant
	evtA := storage.Event{
		Type:     storage.EventCreate,
		Id:       "tenantA:user1",
		TenantID: "tenantA",
		Document: &storage.Document{
			Id:         "tenantA:user1",
			Collection: "users",
			Data:       map[string]interface{}{"name": "User A"},
		},
	}
	hub.Broadcast(evtA)

	// Verify Client A received it
	select {
	case msg := <-clientA.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Client A should have received the event")
	}

	// Verify Client C received it
	select {
	case msg := <-clientC.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Client C should have received the event")
	}

	// Verify Client B did NOT receive it
	select {
	case <-clientB.send:
		t.Fatal("Client B should NOT have received the event for Tenant A")
	default:
		// OK
	}

	// 2. Broadcast event for Tenant B
	evtB := storage.Event{
		Type:     storage.EventCreate,
		Id:       "tenantB:user2",
		TenantID: "tenantB",
		Document: &storage.Document{
			Id:         "tenantB:user2",
			Collection: "users",
			Data:       map[string]interface{}{"name": "User B"},
		},
	}
	hub.Broadcast(evtB)

	// Verify Client B received it
	select {
	case msg := <-clientB.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Client B should have received the event")
	}

	// Verify Client A did NOT receive it
	select {
	case <-clientA.send:
		t.Fatal("Client A should NOT have received the event for Tenant B")
	default:
		// OK
	}

	// Verify Client C did NOT receive it
	select {
	case <-clientC.send:
		t.Fatal("Client C should NOT have received the event for Tenant B")
	default:
		// OK
	}
}

func TestHub_SystemRole_CrossTenantAccess(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	// System Client: Has allowAllTenants = true
	sysClient := &Client{
		hub:             hub,
		send:            make(chan BaseMessage, 10),
		subscriptions:   make(map[string]Subscription),
		tenant:          "default", // Primary tenant
		allowAllTenants: true,      // Can see all tenants
	}
	sysClient.subscriptions["subSys"] = Subscription{
		Query:       model.Query{Collection: "users"},
		IncludeData: true,
	}
	hub.Register(sysClient)

	time.Sleep(50 * time.Millisecond)

	// Broadcast event for Tenant A
	evtA := storage.Event{
		Type:     storage.EventCreate,
		Id:       "tenantA:user1",
		TenantID: "tenantA",
		Document: &storage.Document{
			Id:         "tenantA:user1",
			Collection: "users",
			Data:       map[string]interface{}{"name": "User A"},
		},
	}
	hub.Broadcast(evtA)

	// System client SHOULD receive it
	select {
	case msg := <-sysClient.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("System client should see event from Tenant A")
	}

	// Broadcast event for Tenant B
	evtB := storage.Event{
		Type:     storage.EventCreate,
		Id:       "tenantB:user2",
		TenantID: "tenantB",
		Document: &storage.Document{
			Id:         "tenantB:user2",
			Collection: "users",
			Data:       map[string]interface{}{"name": "User B"},
		},
	}
	hub.Broadcast(evtB)

	// System client SHOULD receive it
	select {
	case msg := <-sysClient.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("System client should see event from Tenant B")
	}
}
