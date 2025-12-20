package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Define local types to avoid importing internal packages (Black Box Testing)
type BaseMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type AuthPayload struct {
	Token string `json:"token"`
}

type SubscribePayload struct {
	Query        Query `json:"query"`
	IncludeData  bool  `json:"includeData"`
	SendSnapshot bool  `json:"sendSnapshot"`
}

type Query struct {
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
}

type Filter struct {
	Field string      `json:"field"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}

type UnsubscribePayload struct {
	ID string `json:"id"`
}

type EventPayload struct {
	SubID string      `json:"subId"`
	Delta PublicEvent `json:"delta"`
}

type PublicEvent struct {
	Type     string                 `json:"type"` // "create", "update", "delete"
	Document map[string]interface{} `json:"document,omitempty"`
	Path     string                 `json:"path"`
}

type SnapshotPayload struct {
	Documents []map[string]interface{} `json:"documents"`
}

const (
	TypeAuth           = "auth"
	TypeAuthAck        = "auth_ack"
	TypeSubscribe      = "subscribe"
	TypeSubscribeAck   = "subscribe_ack"
	TypeUnsubscribe    = "unsubscribe"
	TypeUnsubscribeAck = "unsubscribe_ack"
	TypeEvent          = "event"
	TypeSnapshot       = "snapshot"
	TypeError          = "error"

	EventCreate = "create"
)

func TestRealtime_FullFlow(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(env.RealtimeURL, "http") + "/v1/realtime"

	// Connect to Websocket
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "Failed to connect to websocket")
	defer ws.Close()

	// 1. Authenticate
	authMsg := BaseMessage{
		ID:   "auth-1",
		Type: TypeAuth,
		Payload: mustMarshal(AuthPayload{
			Token: "dummy-token",
		}),
	}
	err = ws.WriteJSON(authMsg)
	require.NoError(t, err)

	// Read Auth Ack
	var ackMsg BaseMessage
	err = ws.ReadJSON(&ackMsg)
	require.NoError(t, err)
	assert.Equal(t, TypeAuthAck, ackMsg.Type)
	assert.Equal(t, "auth-1", ackMsg.ID)

	// 2. Subscribe
	collectionName := "realtime_test_col"
	subID := "sub-1"
	subMsg := BaseMessage{
		ID:   subID,
		Type: TypeSubscribe,
		Payload: mustMarshal(SubscribePayload{
			Query: Query{
				Collection: collectionName,
			},
			IncludeData: true,
		}),
	}
	err = ws.WriteJSON(subMsg)
	require.NoError(t, err)

	// Give some time for subscription to register
	time.Sleep(100 * time.Millisecond)

	// 2.5 Receive Subscribe Ack
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var subAckMsg BaseMessage
	err = ws.ReadJSON(&subAckMsg)
	require.NoError(t, err, "Should receive subscribe ack")
	assert.Equal(t, TypeSubscribeAck, subAckMsg.Type)
	assert.Equal(t, subID, subAckMsg.ID)

	// 3. Trigger Event (Create Document via API Gateway)
	docData := map[string]interface{}{
		"msg": "hello realtime",
	}
	body, _ := json.Marshal(docData)
	resp, err := http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 4. Receive Event
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var eventMsg BaseMessage
	err = ws.ReadJSON(&eventMsg)
	require.NoError(t, err, "Should receive event message")

	assert.Equal(t, TypeEvent, eventMsg.Type)

	var eventPayload EventPayload
	err = json.Unmarshal(eventMsg.Payload, &eventPayload)
	require.NoError(t, err)

	assert.Equal(t, subID, eventPayload.SubID)
	assert.Equal(t, EventCreate, eventPayload.Delta.Type)
	// Path is not in flattened document, but it is in Delta.Path
	// assert.Equal(t, collectionName, eventPayload.Delta.Document.Collection)
	assert.Equal(t, "hello realtime", eventPayload.Delta.Document["msg"])

	// 5. Unsubscribe
	unsubMsg := BaseMessage{
		ID:   "unsub-1",
		Type: TypeUnsubscribe,
		Payload: mustMarshal(UnsubscribePayload{
			ID: subID,
		}),
	}
	err = ws.WriteJSON(unsubMsg)
	require.NoError(t, err)

	// Read Unsubscribe Ack
	var unsubAckMsg BaseMessage
	err = ws.ReadJSON(&unsubAckMsg)
	require.NoError(t, err)
	assert.Equal(t, TypeUnsubscribeAck, unsubAckMsg.Type)
	assert.Equal(t, "unsub-1", unsubAckMsg.ID)

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// 6. Trigger another event
	docData2 := map[string]interface{}{
		"msg": "should not receive",
	}
	body2, _ := json.Marshal(map[string]interface{}{"data": docData2})
	resp, err = http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(body2))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 7. Verify NO event received
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	err = ws.ReadJSON(&eventMsg)
	assert.Error(t, err, "Should timeout and not receive event")
}

func TestRealtime_SSE(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	collectionName := "sse_test_col"
	sseURL := fmt.Sprintf("%s/v1/realtime?collection=%s", env.RealtimeURL, collectionName)

	req, err := http.NewRequest("GET", sseURL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify Headers
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	reader := bufio.NewReader(resp.Body)

	// 1. Read Initial Connection Message
	// Expect ": connected\n\n"
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, ": connected\n", line)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "\n", line)

	// Give some time for subscription to register
	time.Sleep(100 * time.Millisecond)

	// 2. Trigger Event
	docData := map[string]interface{}{
		"msg": "hello sse",
	}
	body, _ := json.Marshal(docData)
	apiResp, err := http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, apiResp.StatusCode)
	apiResp.Body.Close()

	// 3. Receive Event
	done := make(chan bool)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				dataStr := strings.TrimPrefix(line, "data: ")
				dataStr = strings.TrimSpace(dataStr)

				var msg BaseMessage
				err = json.Unmarshal([]byte(dataStr), &msg)
				if err == nil && msg.Type == TypeEvent {
					var eventPayload EventPayload
					if err := json.Unmarshal(msg.Payload, &eventPayload); err == nil {
						if val, ok := eventPayload.Delta.Document["msg"]; ok && val == "hello sse" {
							done <- true
							return
						}
					}
				}
			}
		}
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for SSE event")
	}
}

func TestRealtime_Stream(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	collectionName := "stream_test_col"
	wsURL := "ws" + strings.TrimPrefix(env.RealtimeURL, "http") + "/v1/realtime"

	// 1. Create a document beforehand
	docData := map[string]interface{}{
		"msg": "existing doc",
	}
	body, _ := json.Marshal(docData)
	resp, err := http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Connect to Websocket
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "Failed to connect to websocket")
	defer ws.Close()

	// 2. Send Subscribe Request with Snapshot
	streamID := "stream-1"
	subPayload := SubscribePayload{
		Query:        Query{Collection: collectionName},
		IncludeData:  true,
		SendSnapshot: true,
	}
	subMsg := BaseMessage{
		ID:      streamID,
		Type:    TypeSubscribe,
		Payload: mustMarshal(subPayload),
	}
	err = ws.WriteJSON(subMsg)
	require.NoError(t, err)

	// 3. Expect Snapshot with existing document
	var receivedSnapshot bool
	for i := 0; i < 5; i++ {
		var msg BaseMessage
		err := ws.ReadJSON(&msg)
		require.NoError(t, err)

		if msg.Type == TypeSnapshot && msg.ID == streamID {
			var payload SnapshotPayload
			err := json.Unmarshal(msg.Payload, &payload)
			require.NoError(t, err)

			if len(payload.Documents) > 0 && payload.Documents[0]["msg"] == "existing doc" {
				receivedSnapshot = true
				break
			}
		}
	}
	assert.True(t, receivedSnapshot, "Should receive Snapshot with existing document")

	// 4. Trigger new event
	docData2 := map[string]interface{}{
		"msg": "new doc",
	}
	body2, _ := json.Marshal(docData2)
	resp2, err := http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(body2))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	resp2.Body.Close()

	// 5. Expect Event
	var receivedEvent bool
	for i := 0; i < 5; i++ {
		var msg BaseMessage
		err := ws.ReadJSON(&msg)
		require.NoError(t, err)

		if msg.Type == TypeEvent {
			var payload EventPayload
			err := json.Unmarshal(msg.Payload, &payload)
			require.NoError(t, err)

			if payload.SubID == streamID && payload.Delta.Document["msg"] == "new doc" {
				receivedEvent = true
				break
			}
		}
	}
	assert.True(t, receivedEvent, "Should receive Event for new document")
}

func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestRealtime_Filtering(t *testing.T) {
	env := setupMicroservices(t)
	defer env.Cancel()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(env.RealtimeURL, "http") + "/v1/realtime"

	// Connect to Websocket
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "Failed to connect to websocket")
	defer ws.Close()

	// 1. Authenticate
	authMsg := BaseMessage{
		ID:   "auth-1",
		Type: TypeAuth,
		Payload: mustMarshal(AuthPayload{
			Token: "dummy-token",
		}),
	}
	err = ws.WriteJSON(authMsg)
	require.NoError(t, err)

	// Read Auth Ack
	var ackMsg BaseMessage
	err = ws.ReadJSON(&ackMsg)
	require.NoError(t, err)
	assert.Equal(t, TypeAuthAck, ackMsg.Type)

	// 2. Subscribe with Filter (age > 20)
	collectionName := "realtime_filter_test"
	subID := "sub-filter"
	subMsg := BaseMessage{
		ID:   subID,
		Type: TypeSubscribe,
		Payload: mustMarshal(SubscribePayload{
			Query: Query{
				Collection: collectionName,
				Filters: []Filter{
					{Field: "age", Op: ">", Value: 20},
				},
			},
			IncludeData: true,
		}),
	}
	err = ws.WriteJSON(subMsg)
	require.NoError(t, err)

	// Read Subscribe Ack
	var subAckMsg BaseMessage
	err = ws.ReadJSON(&subAckMsg)
	require.NoError(t, err)
	assert.Equal(t, TypeSubscribeAck, subAckMsg.Type)

	// 3. Create Non-Matching Document (age = 18)
	docNoMatch := map[string]interface{}{
		"name": "Young Bob",
		"age":  18,
	}
	bodyNoMatch, _ := json.Marshal(docNoMatch)
	resp, err := http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(bodyNoMatch))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Verify NO event received (wait a bit)
	ws.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var unexpectedMsg BaseMessage
	err = ws.ReadJSON(&unexpectedMsg)
	if err == nil {
		// If we received a message, check if it's an event for our subscription
		if unexpectedMsg.Type == TypeEvent {
			var payload EventPayload
			json.Unmarshal(unexpectedMsg.Payload, &payload)
			if payload.SubID == subID {
				t.Fatalf("Received event that should have been filtered out: %+v", payload)
			}
		}
	} else {
		// Expected timeout or error
		assert.Contains(t, err.Error(), "i/o timeout")
	}

	// Reconnect to ensure clean state after timeout
	ws.Close()
	ws, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Auth again
	authMsg2 := BaseMessage{
		Type:    TypeAuth,
		ID:      "auth-2",
		Payload: mustMarshal(map[string]string{"token": "dummy-token"}),
	}
	err = ws.WriteJSON(authMsg2)
	require.NoError(t, err)
	// Read auth ack
	ws.ReadJSON(&BaseMessage{})

	// Subscribe again
	subMsg2 := BaseMessage{
		ID:   "sub-filter-2",
		Type: TypeSubscribe,
		Payload: mustMarshal(SubscribePayload{
			Query: Query{
				Collection: collectionName,
				Filters: []Filter{
					{Field: "age", Op: ">", Value: 20},
				},
			},
			IncludeData: true,
		}),
	}
	err = ws.WriteJSON(subMsg2)
	require.NoError(t, err)
	// Read sub ack
	ws.ReadJSON(&BaseMessage{})

	// 4. Create Matching Document (age = 25)
	docMatch := map[string]interface{}{
		"name": "Adult Alice",
		"age":  25,
	}
	bodyMatch, _ := json.Marshal(docMatch)
	resp, err = http.Post(fmt.Sprintf("%s/v1/%s", env.APIURL, collectionName), "application/json", bytes.NewBuffer(bodyMatch))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Verify Event Received
	fmt.Println("Test: Waiting for matching event...")
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var eventMsg BaseMessage
	err = ws.ReadJSON(&eventMsg)
	require.NoError(t, err, "Should receive matching event")
	assert.Equal(t, TypeEvent, eventMsg.Type)

	var eventPayload EventPayload
	err = json.Unmarshal(eventMsg.Payload, &eventPayload)
	require.NoError(t, err)
	assert.Equal(t, "sub-filter-2", eventPayload.SubID)
	assert.Equal(t, "Adult Alice", eventPayload.Delta.Document["name"])
}
