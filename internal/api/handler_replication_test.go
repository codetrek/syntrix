package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandlePull(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	resp := &storage.ReplicationPullResponse{
		Documents: []*storage.Document{
			{
				Id:         "rooms/room-1/messages/msg-1",
				Collection: "rooms/room-1/messages",
				Data:       map[string]interface{}{"name": "Alice"},
				Version:    1,
			},
		},
		Checkpoint: 100,
	}

	mockService.On("Pull", mock.Anything, mock.AnythingOfType("storage.ReplicationPullRequest")).Return(resp, nil)

	req, _ := http.NewRequest("GET", "/v1/replication/pull?collection=rooms/room-1/messages&checkpoint=0&limit=10", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var pullResp ReplicaPullResponse
	json.Unmarshal(rr.Body.Bytes(), &pullResp)
	assert.Len(t, pullResp.Documents, 1)
	assert.Equal(t, "100", pullResp.Checkpoint)
}

func TestHandlePush(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	resp := &storage.ReplicationPushResponse{
		Conflicts: []*storage.Document{},
	}

	mockService.On("Push", mock.Anything, mock.AnythingOfType("storage.ReplicationPushRequest")).Return(resp, nil)

	pushReq := ReplicaPushRequest{
		Collection: "rooms/room-1/messages",
		Changes: []ReplicaChange{
			{
				Doc: Document{"id": "msg-1", "name": "Bob", "_version": float64(1)},
			},
		},
	}
	body, _ := json.Marshal(pushReq)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}
