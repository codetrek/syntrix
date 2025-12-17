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

	"syntrix/internal/realtime"
	"syntrix/internal/storage"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	authMsg := realtime.BaseMessage{
		ID:   "auth-1",
		Type: realtime.TypeAuth,
		Payload: mustMarshal(realtime.AuthPayload{
			Token: "dummy-token",
		}),
	}
	err = ws.WriteJSON(authMsg)
	require.NoError(t, err)

	// Read Auth Ack
	var ackMsg realtime.BaseMessage
	err = ws.ReadJSON(&ackMsg)
	require.NoError(t, err)
	assert.Equal(t, realtime.TypeAuthAck, ackMsg.Type)
	assert.Equal(t, "auth-1", ackMsg.ID)

	// 2. Subscribe
	collectionName := "realtime_test_col"
	subID := "sub-1"
	subMsg := realtime.BaseMessage{
		ID:   subID,
		Type: realtime.TypeSubscribe,
		Payload: mustMarshal(realtime.SubscribePayload{
			Query: storage.Query{
				Collection: collectionName,
			},
		}),
	}
	err = ws.WriteJSON(subMsg)
	require.NoError(t, err)

	// Give some time for subscription to register
	time.Sleep(100 * time.Millisecond)

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
	var eventMsg realtime.BaseMessage
	err = ws.ReadJSON(&eventMsg)
	require.NoError(t, err, "Should receive event message")

	assert.Equal(t, realtime.TypeEvent, eventMsg.Type)

	var eventPayload realtime.EventPayload
	err = json.Unmarshal(eventMsg.Payload, &eventPayload)
	require.NoError(t, err)

	assert.Equal(t, subID, eventPayload.SubID)
	assert.Equal(t, storage.EventCreate, eventPayload.Delta.Type)
	// Path is not in flattened document, but it is in Delta.Path
	// assert.Equal(t, collectionName, eventPayload.Delta.Document.Collection)
	assert.Equal(t, "hello realtime", eventPayload.Delta.Document["msg"])

	// 5. Unsubscribe
	unsubMsg := realtime.BaseMessage{
		ID:   "unsub-1",
		Type: realtime.TypeUnsubscribe,
		Payload: mustMarshal(realtime.UnsubscribePayload{
			ID: subID,
		}),
	}
	err = ws.WriteJSON(unsubMsg)
	require.NoError(t, err)

	// Read Unsubscribe Ack
	var unsubAckMsg realtime.BaseMessage
	err = ws.ReadJSON(&unsubAckMsg)
	require.NoError(t, err)
	assert.Equal(t, realtime.TypeUnsubscribeAck, unsubAckMsg.Type)
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

				var msg realtime.BaseMessage
				err = json.Unmarshal([]byte(dataStr), &msg)
				if err == nil && msg.Type == realtime.TypeEvent {
					var eventPayload realtime.EventPayload
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

	// 2. Send Stream Request
	streamID := "stream-1"
	streamPayload := realtime.StreamPayload{
		Collection: collectionName,
		Checkpoint: 0,
	}
	streamMsg := realtime.BaseMessage{
		ID:      streamID,
		Type:    realtime.TypeStream,
		Payload: mustMarshal(streamPayload),
	}
	err = ws.WriteJSON(streamMsg)
	require.NoError(t, err)

	// 3. Expect StreamEvent with existing document
	var receivedStreamEvent bool
	for i := 0; i < 5; i++ {
		var msg realtime.BaseMessage
		err := ws.ReadJSON(&msg)
		require.NoError(t, err)

		if msg.Type == realtime.TypeStreamEvent && msg.ID == streamID {
			var payload realtime.StreamEventPayload
			err := json.Unmarshal(msg.Payload, &payload)
			require.NoError(t, err)

			if len(payload.Documents) > 0 && payload.Documents[0]["msg"] == "existing doc" {
				receivedStreamEvent = true
				break
			}
		}
	}
	assert.True(t, receivedStreamEvent, "Should receive StreamEvent with existing document")

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
		var msg realtime.BaseMessage
		err := ws.ReadJSON(&msg)
		require.NoError(t, err)

		if msg.Type == realtime.TypeEvent {
			var payload realtime.EventPayload
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
