package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"syntrix/internal/storage"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandleTriggerGet(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil)

	// Mock Data
	doc1 := &storage.Document{
		Id:         "doc1",
		Fullpath:   "users/alice",
		Collection: "users",
		Data:       map[string]interface{}{"name": "Alice"},
	}
	doc2 := &storage.Document{
		Id:         "doc2",
		Fullpath:   "users/bob",
		Collection: "users",
		Data:       map[string]interface{}{"name": "Bob"},
	}

	mockEngine.On("GetDocument", mock.Anything, "users/alice").Return(doc1, nil)
	mockEngine.On("GetDocument", mock.Anything, "users/bob").Return(doc2, nil)

	// Request
	reqBody := TriggerGetRequest{
		Paths: []string{"users/alice", "users/bob"},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/get", bytes.NewReader(body))
	w := httptest.NewRecorder()

	// Execute
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	var resp TriggerGetResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp.Documents, 2)
	assert.Equal(t, "Alice", resp.Documents[0]["name"])
	assert.Equal(t, "Bob", resp.Documents[1]["name"])
}

func TestHandleTriggerWrite(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil)

	// Mock Expectations
	mockEngine.On("CreateDocument", mock.Anything, mock.MatchedBy(func(doc *storage.Document) bool {
		return doc.Fullpath == "users/charlie" && doc.Data["name"] == "Charlie"
	})).Return(nil)

	mockEngine.On("PatchDocument", mock.Anything, "users/alice", mock.MatchedBy(func(data map[string]interface{}) bool {
		return data["active"] == true
	}), mock.Anything).Return(&storage.Document{}, nil)

	mockEngine.On("DeleteDocument", mock.Anything, "users/bob").Return(nil)

	// Request
	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{
			{Type: "create", Path: "users/charlie", Data: map[string]interface{}{"name": "Charlie"}},
			{Type: "update", Path: "users/alice", Data: map[string]interface{}{"active": true}},
			{Type: "delete", Path: "users/bob"},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	// Execute
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerQuery(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil)

	// Mock Data
	docs := []*storage.Document{
		{Id: "1", Data: map[string]interface{}{"a": 1}},
	}
	mockEngine.On("ExecuteQuery", mock.Anything, mock.Anything).Return(docs, nil)

	// Request
	q := storage.Query{Collection: "users"}
	body, _ := json.Marshal(q)
	req := httptest.NewRequest("POST", "/v1/trigger/query", bytes.NewReader(body))
	w := httptest.NewRecorder()

	// Execute
	server.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite_TransactionFailure(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil)

	// Mock RunTransaction to simulate failure
	// The mock implementation executes the closure.
	// We need the closure to return an error.
	// The closure calls tx.CreateDocument. So if tx.CreateDocument returns error, the closure returns error.

	mockEngine.On("CreateDocument", mock.Anything, mock.Anything).Return(assert.AnError)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{
			{Type: "create", Path: "users/fail", Data: map[string]interface{}{"name": "Fail"}},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
