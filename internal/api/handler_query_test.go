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

func TestHandleQuery(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService, nil)

	docs := []*storage.Document{
		{
			Id:         "rooms/room-1/messages/msg-1",
			Collection: "rooms/room-1/messages",
			Data:       map[string]interface{}{"name": "Alice"},
			Version:    1,
		},
		{
			Id:         "rooms/room-1/messages/msg-2",
			Collection: "rooms/room-1/messages",
			Data:       map[string]interface{}{"name": "Bob"},
			Version:    1,
		},
	}

	mockService.On("ExecuteQuery", mock.Anything, mock.AnythingOfType("storage.Query")).Return(docs, nil)

	query := storage.Query{
		Collection: "rooms/room-1/messages",
		Filters: []storage.Filter{
			{Field: "name", Op: "==", Value: "Alice"},
		},
	}
	body, _ := json.Marshal(query)
	req, _ := http.NewRequest("POST", "/v1/query", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Len(t, resp, 2)
	assert.Equal(t, "Alice", resp[0]["name"])
}
