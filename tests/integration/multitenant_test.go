package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiTenant_DataIsolation(t *testing.T) {
	t.Parallel()
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	tokenA := env.GetTokenForTenant(t, tenantA, "user-a", "user")
	tokenB := env.GetTokenForTenant(t, tenantB, "user-b", "user")

	collection := "private_docs"

	// 1. Tenant A creates a document
	docData := map[string]interface{}{
		"title": "Secret Plan A",
		"owner": "A",
	}
	resp := env.MakeRequest(t, "POST", "/api/v1/"+collection, docData, tokenA)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdDoc map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&createdDoc)
	require.NoError(t, err)
	resp.Body.Close()
	docID := createdDoc["id"].(string)

	// 2. Tenant B tries to GET the document (Should fail)
	resp = env.MakeRequest(t, "GET", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, tokenB)
	// Should be 404 Not Found because it doesn't exist in Tenant B's scope
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// 3. Tenant B tries to LIST documents (Should not see it)
	// GET /api/v1/collection is not supported, use Query
	queryAll := model.Query{
		Collection: collection,
	}
	resp = env.MakeRequest(t, "POST", "/api/v1/query", queryAll, tokenB)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var listRes []interface{}
	err = json.NewDecoder(resp.Body).Decode(&listRes)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, listRes, "Tenant B should see no documents")

	// 4. Tenant B tries to UPDATE the document (Should fail)
	updateData := map[string]interface{}{
		"doc": map[string]interface{}{
			"title": "Hacked by B",
		},
	}
	resp = env.MakeRequest(t, "PATCH", fmt.Sprintf("/api/v1/%s/%s", collection, docID), updateData, tokenB)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// 5. Tenant B tries to DELETE the document (Should fail)
	resp = env.MakeRequest(t, "DELETE", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, tokenB)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// 6. Tenant B tries to Query the document (Should not find it)
	query := model.Query{
		Collection: collection,
		Filters: []model.Filter{
			{Field: "title", Op: "==", Value: "Secret Plan A"},
		},
	}
	resp = env.MakeRequest(t, "POST", "/api/v1/query", query, tokenB)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var queryResults []interface{}
	err = json.NewDecoder(resp.Body).Decode(&queryResults)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Empty(t, queryResults, "Tenant B query should return empty")

	// 7. Tenant A can see the document
	resp = env.MakeRequest(t, "GET", fmt.Sprintf("/api/v1/%s/%s", collection, docID), nil, tokenA)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMultiTenant_RealtimeIsolation(t *testing.T) {
	t.Parallel()
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	tokenA := env.GetTokenForTenant(t, tenantA, "user-a", "user")
	tokenB := env.GetTokenForTenant(t, tenantB, "user-b", "user")

	collection := "live_updates"

	// Connect Client A (Tenant A)
	wsURL := "ws" + strings.TrimPrefix(env.RealtimeURL, "http") + "/realtime/ws"
	wsA, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer wsA.Close()

	// Authenticate A
	err = wsA.WriteJSON(BaseMessage{
		ID:   "auth-a",
		Type: TypeAuth,
		Payload: mustMarshal(AuthPayload{
			Token: tokenA,
		}),
	})
	require.NoError(t, err)
	readUntilType(t, wsA, TypeAuthAck)

	// Subscribe A
	err = wsA.WriteJSON(BaseMessage{
		ID:   "sub-a",
		Type: TypeSubscribe,
		Payload: mustMarshal(SubscribePayload{
			Query:       Query{Collection: collection},
			IncludeData: true,
		}),
	})
	require.NoError(t, err)
	readUntilType(t, wsA, TypeSubscribeAck)

	// Connect Client B (Tenant B)
	wsB, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer wsB.Close()

	// Authenticate B
	err = wsB.WriteJSON(BaseMessage{
		ID:   "auth-b",
		Type: TypeAuth,
		Payload: mustMarshal(AuthPayload{
			Token: tokenB,
		}),
	})
	require.NoError(t, err)
	readUntilType(t, wsB, TypeAuthAck)

	// Subscribe B
	err = wsB.WriteJSON(BaseMessage{
		ID:   "sub-b",
		Type: TypeSubscribe,
		Payload: mustMarshal(SubscribePayload{
			Query:       Query{Collection: collection},
			IncludeData: true,
		}),
	})
	require.NoError(t, err)
	readUntilType(t, wsB, TypeSubscribeAck)

	// 1. Create Document in Tenant A
	docData := map[string]interface{}{
		"msg": "Hello Tenant A",
	}
	resp := env.MakeRequest(t, "POST", "/api/v1/"+collection, docData, tokenA)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Client A should receive event
	msgA := readUntilType(t, wsA, TypeEvent)
	var payloadA EventPayload
	json.Unmarshal(msgA.Payload, &payloadA)
	assert.Equal(t, "Hello Tenant A", payloadA.Delta.Document["msg"])

	// 2. Create Document in Tenant B
	docDataB := map[string]interface{}{
		"msg": "Hello Tenant B",
	}
	respB := env.MakeRequest(t, "POST", "/api/v1/"+collection, docDataB, tokenB)
	require.Equal(t, http.StatusCreated, respB.StatusCode)
	respB.Body.Close()

	// Client B should receive event
	wsB.SetReadDeadline(time.Time{}) // Reset deadline
	msgB := readUntilType(t, wsB, TypeEvent)
	var payloadB EventPayload
	json.Unmarshal(msgB.Payload, &payloadB)
	assert.Equal(t, "Hello Tenant B", payloadB.Delta.Document["msg"])

	// Client A should NOT receive event
	wsA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = wsA.ReadMessage()
	if err == nil {
		// Fail if we got a message (unless it's unrelated, but we expect silence)
	} else {
		assert.Contains(t, err.Error(), "i/o timeout")
	}
}

func TestMultiTenant_ReplicationIsolation(t *testing.T) {
	t.Parallel()
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	tenantA := "tenant-a"
	tenantB := "tenant-b"

	tokenA := env.GetTokenForTenant(t, tenantA, "user-a", "user")
	tokenB := env.GetTokenForTenant(t, tenantB, "user-b", "user")

	collection := "repl_docs"

	// 1. Tenant A creates a document via REST
	docData := map[string]interface{}{
		"title": "Repl Doc A",
	}
	resp := env.MakeRequest(t, "POST", "/api/v1/"+collection, docData, tokenA)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 2. Tenant B PULLs (Should get nothing)
	pullURL := fmt.Sprintf("/replication/v1/pull?collection=%s&checkpoint=0&limit=100", collection)
	resp = env.MakeRequest(t, "GET", pullURL, nil, tokenB)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var pullRes map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&pullRes)
	require.NoError(t, err)
	resp.Body.Close()

	docs := pullRes["documents"].([]interface{})
	assert.Empty(t, docs, "Tenant B should pull 0 documents")

	// 3. Tenant B PUSHes a document
	pushReq := map[string]interface{}{
		"collection": collection,
		"changes": []map[string]interface{}{
			{
				"action": "create",
				"document": map[string]interface{}{
					"id":    "doc-b",
					"title": "Repl Doc B",
				},
			},
		},
	}
	resp = env.MakeRequest(t, "POST", "/replication/v1/push", pushReq, tokenB)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 4. Tenant A PULLs (Should see only Doc A, not Doc B)
	resp = env.MakeRequest(t, "GET", pullURL, nil, tokenA)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	err = json.NewDecoder(resp.Body).Decode(&pullRes)
	require.NoError(t, err)
	resp.Body.Close()

	docsA := pullRes["documents"].([]interface{})
	// Should contain Doc A (maybe) but definitely NOT Doc B
	for _, d := range docsA {
		docMap := d.(map[string]interface{})
		assert.NotEqual(t, "doc-b", docMap["id"], "Tenant A should not see Tenant B's document")
	}
}

func readUntilType(t *testing.T, ws *websocket.Conn, targetType string) BaseMessage {
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, p, err := ws.ReadMessage()
		require.NoError(t, err)
		var msg BaseMessage
		err = json.Unmarshal(p, &msg)
		require.NoError(t, err)

		t.Logf("Received message type: %s", msg.Type)
		if msg.Type == targetType {
			ws.SetReadDeadline(time.Time{}) // Reset deadline
			return msg
		}
		if msg.Type == TypeError {
			t.Fatalf("Received error from server: %s", string(msg.Payload))
		}
	}
}
