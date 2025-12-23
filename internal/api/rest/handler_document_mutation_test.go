package rest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandleReplaceDocument_IdMutation(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	// Try to replace document msg-1 with body containing id: msg-2
	body := []byte(`{"doc":{"id": "msg-2", "name": "Bob"}}`)
	req, _ := http.NewRequest("PUT", "/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "Document ID cannot be changed")
}

func TestHandleUpdateDocument_IdMutation(t *testing.T) {
	mockService := new(MockQueryService)
	server := createTestServer(mockService, nil, nil)

	// Try to update document msg-1 with body containing id: msg-2
	body := []byte(`{"doc":{"id": "msg-2", "name": "Bob"}}`)
	req, _ := http.NewRequest("PATCH", "/v1/rooms/room-1/messages/msg-1", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "Document ID cannot be changed")
}
