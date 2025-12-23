package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"syntrix/internal/common"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandlePull(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	resp := &storage.ReplicationPullResponse{
		Documents: []*storage.Document{
			{
				Id:         "hash-1",
				Fullpath:   "rooms/room-1/messages/msg-1",
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
	server := createTestServer(mockService, nil, nil)

	resp := &storage.ReplicationPushResponse{
		Conflicts: []*storage.Document{},
	}

	mockService.On("Push", mock.Anything, mock.AnythingOfType("storage.ReplicationPushRequest")).Return(resp, nil)

	pushReq := ReplicaPushRequest{
		Collection: "rooms/room-1/messages",
		Changes: []ReplicaChange{
			{
				Doc: common.Document{"id": "msg-1", "name": "Bob", "version": float64(1)},
			},
		},
	}
	body, _ := json.Marshal(pushReq)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandlePull_InvalidQuery(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("GET", "/v1/replication/pull?limit=abc", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePull_MissingCollection(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("GET", "/v1/replication/pull?checkpoint=0", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePull_InvalidCheckpoint(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("GET", "/v1/replication/pull?collection=rooms&checkpoint=abc", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePull_ValidateError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("GET", "/v1/replication/pull?collection=rooms&checkpoint=0&limit=2001", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePull_EngineError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("Pull", mock.Anything, mock.AnythingOfType("storage.ReplicationPullRequest")).Return(nil, errors.New("boom"))

	req, _ := http.NewRequest("GET", "/v1/replication/pull?collection=rooms&checkpoint=0&limit=1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandlePush_InvalidBody(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBufferString("{invalid"))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_MissingCollection(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	reqBody := ReplicaPushRequest{Collection: "", Changes: []ReplicaChange{{Doc: common.Document{"id": "1"}}}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_InvalidCollection(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	reqBody := ReplicaPushRequest{Collection: "rooms!", Changes: []ReplicaChange{{Doc: common.Document{"id": "1"}}}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_DocValidationFail(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	reqBody := ReplicaPushRequest{Collection: "rooms", Changes: []ReplicaChange{{Doc: common.Document{"id": ""}}}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_MissingDocID(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	reqBody := ReplicaPushRequest{Collection: "rooms", Changes: []ReplicaChange{{Doc: common.Document{"name": "Bob"}}}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_NoChanges(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	reqBody := ReplicaPushRequest{Collection: "rooms", Changes: []ReplicaChange{}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePush_EngineError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("Push", mock.Anything, mock.AnythingOfType("storage.ReplicationPushRequest")).Return(nil, errors.New("boom"))

	reqBody := ReplicaPushRequest{Collection: "rooms", Changes: []ReplicaChange{{Doc: common.Document{"id": "1"}}}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/v1/replication/push", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}
