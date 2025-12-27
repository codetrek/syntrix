package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub_Broadcast_TableDriven(t *testing.T) {
	type clientSetup struct {
		id              string
		tenant          string
		allowAllTenants bool
		subscriptions   map[string]Subscription
		bufferSize      int
		preFillBuffer   bool
		slowReader      bool
	}

	type testCase struct {
		name           string
		clients        []clientSetup
		event          storage.Event
		expectedEvents map[string]bool // map[clientID]shouldReceive
		checkPayload   func(t *testing.T, payload EventPayload)
	}

	tests := []testCase{
		{
			name: "Basic Broadcast",
			clients: []clientSetup{
				{
					id:              "c1",
					allowAllTenants: true,
					subscriptions: map[string]Subscription{
						"sub1": {Query: model.Query{Collection: "users"}, IncludeData: true},
					},
				},
			},
			event: storage.Event{
				Type: storage.EventCreate,
				Id:   "users/123",
				Document: &storage.Document{
					Id:         "users/123",
					Collection: "users",
					Data:       map[string]interface{}{"name": "Alice"},
				},
			},
			expectedEvents: map[string]bool{"c1": true},
			checkPayload: func(t *testing.T, payload EventPayload) {
				assert.Equal(t, "sub1", payload.SubID)
				assert.Equal(t, "Alice", payload.Delta.Document["name"])
			},
		},
		{
			name: "Filter Match",
			clients: []clientSetup{
				{
					id:              "c1",
					allowAllTenants: true,
					subscriptions: map[string]Subscription{
						"sub1": {
							Query: model.Query{
								Collection: "users",
								Filters:    []model.Filter{{Field: "age", Op: ">", Value: 20}},
							},
							IncludeData: true,
							CelProgram: func() cel.Program {
								p, _ := compileFiltersToCEL([]model.Filter{{Field: "age", Op: ">", Value: 20}})
								return p
							}(),
						},
					},
				},
			},
			event: storage.Event{
				Type: storage.EventCreate,
				Id:   "users/adult",
				Document: &storage.Document{
					Id:         "users/adult",
					Collection: "users",
					Data:       map[string]interface{}{"name": "Alice", "age": 25},
				},
			},
			expectedEvents: map[string]bool{"c1": true},
		},
		{
			name: "Filter No Match",
			clients: []clientSetup{
				{
					id:              "c1",
					allowAllTenants: true,
					subscriptions: map[string]Subscription{
						"sub1": {
							Query: model.Query{
								Collection: "users",
								Filters:    []model.Filter{{Field: "age", Op: ">", Value: 20}},
							},
							IncludeData: true,
							CelProgram: func() cel.Program {
								p, _ := compileFiltersToCEL([]model.Filter{{Field: "age", Op: ">", Value: 20}})
								return p
							}(),
						},
					},
				},
			},
			event: storage.Event{
				Type: storage.EventCreate,
				Id:   "users/young",
				Document: &storage.Document{
					Id:         "users/young",
					Collection: "users",
					Data:       map[string]interface{}{"name": "Bob", "age": 18},
				},
			},
			expectedEvents: map[string]bool{"c1": false},
		},
		{
			name: "Tenant Isolation - Match",
			clients: []clientSetup{
				{
					id:            "c1",
					tenant:        "t1",
					subscriptions: map[string]Subscription{"s": {IncludeData: false}},
				},
			},
			event:          storage.Event{TenantID: "t1", Type: storage.EventCreate, Id: "users/1"},
			expectedEvents: map[string]bool{"c1": true},
		},
		{
			name: "Tenant Isolation - No Match",
			clients: []clientSetup{
				{
					id:            "c1",
					tenant:        "t2",
					subscriptions: map[string]Subscription{"s": {IncludeData: false}},
				},
			},
			event:          storage.Event{TenantID: "t1", Type: storage.EventCreate, Id: "users/1"},
			expectedEvents: map[string]bool{"c1": false},
		},
		{
			name: "Slow Client Backpressure",
			clients: []clientSetup{
				{
					id:              "c1",
					allowAllTenants: true,
					subscriptions:   map[string]Subscription{"sub": {Query: model.Query{Collection: "users"}, IncludeData: true}},
					bufferSize:      1,
					preFillBuffer:   true,
					slowReader:      true,
				},
			},
			event: storage.Event{
				Type:     storage.EventCreate,
				Id:       "users/123",
				Document: &storage.Document{Id: "users/123", Collection: "users", Data: map[string]interface{}{}},
			},
			expectedEvents: map[string]bool{"c1": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hubCtx, hubCancel := context.WithCancel(context.Background())
			defer hubCancel()

			hub := NewHub()
			go hub.Run(hubCtx)

			clientMap := make(map[string]*Client)

			for _, cs := range tc.clients {
				bufSize := 10
				if cs.bufferSize > 0 {
					bufSize = cs.bufferSize
				}
				c := &Client{
					hub:             hub,
					send:            make(chan BaseMessage, bufSize),
					subscriptions:   cs.subscriptions,
					tenant:          cs.tenant,
					allowAllTenants: cs.allowAllTenants,
				}
				hub.Register(c)
				clientMap[cs.id] = c

				if cs.preFillBuffer {
					c.send <- BaseMessage{Type: TypeEvent}
				}

				if cs.slowReader {
					go func(client *Client) {
						time.Sleep(10 * time.Millisecond)
						// Drain buffer
						for {
							select {
							case <-client.send:
							default:
								return
							}
						}
					}(c)
				}
			}

			// Wait for registration
			time.Sleep(20 * time.Millisecond)

			hub.Broadcast(tc.event)

			for clientID, shouldReceive := range tc.expectedEvents {
				client := clientMap[clientID]
				if shouldReceive {
					select {
					case received := <-client.send:
						if received.Type == TypeEvent {
							var payload EventPayload
							err := json.Unmarshal(received.Payload, &payload)
							assert.NoError(t, err)
							if tc.checkPayload != nil {
								tc.checkPayload(t, payload)
							}
						}
					case <-time.After(200 * time.Millisecond):
						t.Fatalf("Client %s expected message but timed out", clientID)
					}
				} else {
					select {
					case msg := <-client.send:
						t.Fatalf("Client %s received unexpected message: %+v", clientID, msg)
					case <-time.After(50 * time.Millisecond):
						// OK
					}
				}
			}
		})
	}
}

func TestHub_Lifecycle_TableDriven(t *testing.T) {
	t.Run("Graceful Shutdown", func(t *testing.T) {
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
			subscriptions: map[string]Subscription{"sub": {Query: model.Query{Collection: "users"}, IncludeData: true}},
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
	})
}
