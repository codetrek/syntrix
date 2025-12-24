package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"syntrix/internal/common"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandleQuery(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	docs := []common.Document{
		{"id": "msg-1", "collection": "rooms/room-1/messages", "name": "Alice", "version": int64(1)},
		{"id": "msg-2", "collection": "rooms/room-1/messages", "name": "Bob", "version": int64(1)},
	}

	mockService.On("ExecuteQuery", mock.Anything, mock.AnythingOfType("storage.Query")).Return(docs, nil)

	query := storage.Query{
		Collection: "rooms/room-1/messages",
		Filters: []storage.Filter{
			{Field: "name", Op: "==", Value: "Alice"},
		},
	}
	body, _ := json.Marshal(query)
	req, _ := http.NewRequest("POST", "/api/v1/query", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Len(t, resp, 2)
	assert.Equal(t, "Alice", resp[0]["name"])
}

func TestHandleQuery_BadJSON(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/query", bytes.NewReader([]byte("{bad")))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleQuery_ValidateError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	q := storage.Query{} // missing collection triggers validation error
	body, _ := json.Marshal(q)
	req := httptest.NewRequest("POST", "/api/v1/query", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleQuery_EngineError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	q := storage.Query{Collection: "rooms"}
	mockService.On("ExecuteQuery", mock.Anything, q).Return(nil, assert.AnError)

	body, _ := json.Marshal(q)
	req := httptest.NewRequest("POST", "/api/v1/query", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}
