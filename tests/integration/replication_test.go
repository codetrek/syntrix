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

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/api/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplication_FullFlow(t *testing.T) {
	t.Parallel()
	env := setupServiceEnv(t, "")
	defer env.Cancel()

	// Get Token
	token := env.GetToken(t, "user1", "user")

	collectionName := "replication_test_col"

	// 1. Setup Realtime (SSE) Connection
	sseURL := fmt.Sprintf("%s/realtime/sse?collection=%s", env.RealtimeURL, collectionName)
	req, err := http.NewRequest("GET", sseURL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", env.RealtimeURL)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	reader := bufio.NewReader(resp.Body)

	readLine := func() string {
		type result struct {
			line string
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			line, err := reader.ReadString('\n')
			ch <- result{line, err}
		}()
		select {
		case res := <-ch:
			require.NoError(t, res.err)
			return res.line
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout reading SSE stream")
			return ""
		}
	}

	// Read connection message
	line := readLine()
	assert.Equal(t, ": connected\n", line)

	// Wait for subscription
	time.Sleep(50 * time.Millisecond)

	// 2. Push a Document
	docID := "doc-1"
	docData := map[string]interface{}{
		"id":      docID,
		"msg":     "hello replication",
		"version": float64(0), // New document
	}

	pushBody := rest.ReplicaPushRequest{
		Collection: collectionName,
		Changes: []rest.ReplicaChange{
			{
				Doc: docData,
			},
		},
	}

	bodyBytes, _ := json.Marshal(pushBody)
	pushURL := fmt.Sprintf("%s/replication/v1/push?collection=%s", env.APIURL, collectionName)

	pushReq, err := http.NewRequest("POST", pushURL, bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.Header.Set("Authorization", "Bearer "+token)

	pushResp, err := client.Do(pushReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pushResp.StatusCode)

	var pushResult map[string]interface{}
	json.NewDecoder(pushResp.Body).Decode(&pushResult)
	pushResp.Body.Close()

	// Expect no conflicts
	conflicts := pushResult["conflicts"].([]interface{})
	assert.Empty(t, conflicts)

	// 3. Verify Realtime Event
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
						// Check if it matches our document
						if eventPayload.Delta.Document["id"] == docID {
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

	// 4. Pull to verify storage
	pullURL := fmt.Sprintf("%s/replication/v1/pull?collection=%s&checkpoint=0", env.APIURL, collectionName)
	pullReq, err := http.NewRequest("GET", pullURL, nil)
	require.NoError(t, err)
	pullReq.Header.Set("Authorization", "Bearer "+token)

	pullResp, err := client.Do(pullReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pullResp.StatusCode)

	var pullResult struct {
		Documents  []map[string]interface{} `json:"documents"`
		Checkpoint string                   `json:"checkpoint"`
	}
	err = json.NewDecoder(pullResp.Body).Decode(&pullResult)
	pullResp.Body.Close()
	require.NoError(t, err)

	assert.NotEmpty(t, pullResult.Documents)
	found := false
	for _, doc := range pullResult.Documents {
		if doc["id"] == docID {
			assert.Equal(t, "hello replication", doc["msg"])
			found = true
			break
		}
	}
	assert.True(t, found, "Document should be returned in pull")
}
