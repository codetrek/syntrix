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

func TestHandleGetDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	doc := common.Document{"id": "msg-1", "collection": "rooms/room-1/messages", "name": "Alice", "version": 1}

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(doc, nil)

	req, _ := http.NewRequest("GET", "/api/v1/rooms/room-1/messages/msg-1", nil)
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
	server := createTestServer(mockService, nil, nil)

	// Note: The API server might be calling CreateDocument or ReplaceDocument depending on implementation.
	// Assuming it calls CreateDocument for POST /api/v1/collection
	mockService.On("CreateDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "rooms/room-1/messages" && doc.GetID() == "msg-1" && doc["name"] == "Bob"
	})).Return(nil)

	createdDoc := common.Document{"id": "msg-1", "collection": "rooms/room-1/messages", "name": "Bob", "version": 1}
	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(createdDoc, nil)

	body := []byte(`{"id":"msg-1","name": "Bob"}`)
	req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestHandleGetDocument_NotFound(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/unknown").Return(nil, storage.ErrNotFound)

	req, _ := http.NewRequest("GET", "/api/v1/rooms/room-1/messages/unknown", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandleReplaceDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	returnedDoc := common.Document{"name": "Bob", "id": "msg-1", "collection": "rooms/room-1/messages", "version": 2}

	mockService.On("ReplaceDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "rooms/room-1/messages" && doc.GetID() == "msg-1" && doc["name"] == "Bob"
	}), mock.Anything).Return(returnedDoc, nil)

	body := []byte(`{"doc":{"name": "Bob"}}`)
	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "Bob", resp["name"])
}

func TestHandleUpdateDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	returnedDoc := common.Document{"name": "Alice", "status": "read", "id": "msg-1", "collection": "rooms/room-1/messages", "version": 2}

	mockService.On("PatchDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "rooms/room-1/messages" && doc.GetID() == "msg-1" && doc["status"] == "read"
	}), mock.Anything).Return(returnedDoc, nil)

	body := []byte(`{"doc":{"status": "read"}}`)
	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "read", resp["status"])
}

func TestHandleDeleteDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("DeleteDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(nil)

	req, _ := http.NewRequest("DELETE", "/api/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandleReplaceDocument_IfMatch(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	filters := storage.Filters{
		{Field: "version", Op: "==", Value: float64(1)},
	}

	returnedDoc := common.Document{"name": "Bob", "id": "msg-1", "collection": "rooms/room-1/messages", "version": 2}

	mockService.On("ReplaceDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "rooms/room-1/messages" && doc.GetID() == "msg-1" && doc["name"] == "Bob"
	}), filters).Return(returnedDoc, nil)

	body := []byte(`{"doc":{"name": "Bob"}, "ifMatch": [{"field": "version", "op": "==", "value": 1}]}`)
	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "Bob", resp["name"])
}

func TestHandlePatchDocument_IfMatch(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	returnedDoc := common.Document{"name": "Alice", "status": "read", "id": "msg-1", "collection": "rooms/room-1/messages", "version": 2}

	filters := storage.Filters{
		{Field: "status", Op: "==", Value: "unread"},
	}

	mockService.On("PatchDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "rooms/room-1/messages" && doc.GetID() == "msg-1" && doc["status"] == "read"
	}), filters).Return(returnedDoc, nil)

	body := []byte(`{"doc":{"status": "read"}, "ifMatch": [{"field": "status", "op": "==", "value": "unread"}]}`)
	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "read", resp["status"])
}

func TestHandleGetDocument_InvalidPath(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("path", "rooms//")
	rr := httptest.NewRecorder()

	server.handleGetDocument(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetDocument_InternalError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(nil, errors.New("boom"))

	req, _ := http.NewRequest("GET", "/api/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandleCreateDocument_InvalidCollection(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("POST", "/api/v1/Invalid!", bytes.NewBufferString(`{"id":"1"}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCreateDocument_BadBody(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBufferString("{invalid"))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCreateDocument_ValidationError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBufferString(`{"id":""}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCreateDocument_Conflict(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("CreateDocument", mock.Anything, mock.AnythingOfType("common.Document")).Return(storage.ErrExists)

	req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBufferString(`{"id":"msg-1"}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandleCreateDocument_GetError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("CreateDocument", mock.Anything, mock.AnythingOfType("common.Document")).Return(nil)
	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(nil, errors.New("fetch"))

	req, _ := http.NewRequest("POST", "/api/v1/rooms/room-1/messages", bytes.NewBufferString(`{"id":"msg-1"}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandleReplaceDocument_InvalidPath(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PUT", "/api/v1/rooms", bytes.NewBufferString(`{"doc":{}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleReplaceDocument_InvalidBody(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString("{invalid"))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleReplaceDocument_ValidationError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"id":""}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleReplaceDocument_IDMismatch(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"id":"msg-2"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleReplaceDocument_VersionConflict(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("ReplaceDocument", mock.Anything, mock.AnythingOfType("common.Document"), mock.Anything).Return(nil, storage.ErrPreconditionFailed)

	req, _ := http.NewRequest("PUT", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"id":"msg-1"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandlePatchDocument_InvalidBody(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString("{invalid"))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePatchDocument_InvalidPath(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req := httptest.NewRequest("PATCH", "/", bytes.NewBufferString(`{"doc":{"status":"read"}}`))
	req.SetPathValue("path", "rooms")
	rr := httptest.NewRecorder()

	server.handlePatchDocument(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePatchDocument_NoData(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"id":"msg-1"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePatchDocument_NotFound(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("PatchDocument", mock.Anything, mock.AnythingOfType("common.Document"), mock.Anything).Return(nil, storage.ErrNotFound)

	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"status":"read"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandlePatchDocument_VersionConflict(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("PatchDocument", mock.Anything, mock.AnythingOfType("common.Document"), mock.Anything).Return(nil, storage.ErrPreconditionFailed)

	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"status":"read"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandlePatchDocument_InternalError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("PatchDocument", mock.Anything, mock.AnythingOfType("common.Document"), mock.Anything).Return(nil, errors.New("boom"))

	req, _ := http.NewRequest("PATCH", "/api/v1/rooms/room-1/messages/msg-1", bytes.NewBufferString(`{"doc":{"status":"read"}}`))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandleDeleteDocument_NotFound(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("DeleteDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(storage.ErrNotFound)

	req, _ := http.NewRequest("DELETE", "/api/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	mockService.AssertExpectations(t)
}

func TestHandleDeleteDocument_InternalError(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	mockService.On("DeleteDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(errors.New("boom"))

	req, _ := http.NewRequest("DELETE", "/api/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}
