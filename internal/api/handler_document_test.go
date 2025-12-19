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

func TestHandleGetDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	doc := &storage.Document{
		Id:         "rooms/room-1/messages/msg-1",
		Collection: "rooms/room-1/messages",
		Data:       map[string]interface{}{"name": "Alice"},
		Version:    1,
	}

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(doc, nil)

	req, _ := http.NewRequest("GET", "/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	// Path is not returned in flattened response unless it's in data.id
	// assert.Equal(t, "rooms/room-1/messages/msg-1", resp.Path)
	assert.Equal(t, "Alice", resp["name"])
}

func TestHandleCreateDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	// Note: The API server might be calling CreateDocument or ReplaceDocument depending on implementation.
	// Assuming it calls CreateDocument for POST /v1/collection
	mockService.On("CreateDocument", mock.Anything, mock.AnythingOfType("*storage.Document")).Return(nil)

	body := []byte(`{"name": "Bob"}`)
	req, _ := http.NewRequest("POST", "/v1/rooms/room-1/messages", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestHandleGetDocument_NotFound(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/unknown").Return(nil, storage.ErrNotFound)

	req, _ := http.NewRequest("GET", "/v1/rooms/room-1/messages/unknown", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandleReplaceDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	doc := &storage.Document{
		Id:         "rooms/room-1/messages/msg-1",
		Collection: "rooms/room-1/messages",
		Data:       map[string]interface{}{"name": "Bob", "id": "msg-1"},
		Version:    2,
	}

	mockService.On("ReplaceDocument", mock.Anything, "rooms/room-1/messages/msg-1", "rooms/room-1/messages", mock.Anything).Return(doc, nil)

	body := []byte(`{"name": "Bob"}`)
	req, _ := http.NewRequest("PUT", "/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "Bob", resp["name"])
}

func TestHandleUpdateDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	doc := &storage.Document{
		Id:         "rooms/room-1/messages/msg-1",
		Collection: "rooms/room-1/messages",
		Data:       map[string]interface{}{"name": "Alice", "status": "read", "id": "msg-1"},
		Version:    2,
	}

	mockService.On("PatchDocument", mock.Anything, "rooms/room-1/messages/msg-1", mock.Anything).Return(doc, nil)

	body := []byte(`{"status": "read"}`)
	req, _ := http.NewRequest("PATCH", "/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "read", resp["status"])
}

func TestHandleDeleteDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	mockService.On("DeleteDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(nil)

	req, _ := http.NewRequest("DELETE", "/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}
