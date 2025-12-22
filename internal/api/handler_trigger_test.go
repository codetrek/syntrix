package api

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

func TestHandleTriggerGet(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	// Mock Data
	doc1 := common.Document{"id": "alice", "collection": "users", "name": "Alice", "version": int64(1)}
	doc2 := common.Document{"id": "bob", "collection": "users", "name": "Bob", "version": int64(1)}

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

func TestHandleTriggerGet_EmptyPaths(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	reqBody := TriggerGetRequest{Paths: []string{}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/get", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTriggerGet_BadJSON(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	req := httptest.NewRequest("POST", "/v1/trigger/get", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTriggerGet_SkipNotFound(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	doc := common.Document{"id": "bob", "collection": "users", "name": "Bob"}
	mockEngine.On("GetDocument", mock.Anything, "users/missing").Return(nil, storage.ErrNotFound)
	mockEngine.On("GetDocument", mock.Anything, "users/bob").Return(doc, nil)

	reqBody := TriggerGetRequest{Paths: []string{"users/missing", "users/bob"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/get", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TriggerGetResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp.Documents, 1)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerGet_EngineError(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	mockEngine.On("GetDocument", mock.Anything, "users/alice").Return(nil, errors.New("boom"))

	reqBody := TriggerGetRequest{Paths: []string{"users/alice"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/get", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	// Mock Expectations
	mockEngine.On("CreateDocument", mock.Anything, mock.MatchedBy(func(doc common.Document) bool {
		return doc.GetCollection() == "users" && doc.GetID() == "charlie" && doc["name"] == "Charlie"
	})).Return(nil)

	mockEngine.On("PatchDocument", mock.Anything, mock.MatchedBy(func(data common.Document) bool {
		return data.GetCollection() == "users" && data.GetID() == "alice" && data["active"] == true
	}), mock.Anything).Return(common.Document{"active": true}, nil)

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

func TestHandleTriggerWrite_UpdateError(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	mockEngine.On("PatchDocument", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "update", Path: "users/alice", Data: map[string]interface{}{"active": true}}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite_ReplacePathInvalid(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "replace", Path: "invalid", Data: map[string]interface{}{"name": "x"}}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleTriggerWrite_ReplaceError(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	mockEngine.On("ReplaceDocument", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "replace", Path: "users/alice", Data: map[string]interface{}{"name": "Alice"}}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite_DeleteError(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	mockEngine.On("DeleteDocument", mock.Anything, "users/bob").Return(assert.AnError)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "delete", Path: "users/bob"}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite_BadJSON(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTriggerWrite_InvalidType(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "unknown", Path: "users/x", Data: map[string]interface{}{"a": 1}}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleTriggerWrite_InvalidPath(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	reqBody := TriggerWriteRequest{
		Writes: []TriggerWriteOp{{Type: "create", Path: "invalid", Data: map[string]interface{}{"name": "x"}}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/trigger/write", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleTriggerQuery(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	// Mock Data
	docs := []common.Document{
		{"id": "1", "collection": "users", "a": 1, "version": int64(1)},
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

	var resp []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 1)
	assert.Equal(t, float64(1), resp[0]["a"])
	assert.Equal(t, "1", resp[0]["id"])
	assert.Equal(t, "users", resp[0]["collection"])
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerQuery_BadJSON(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	req := httptest.NewRequest("POST", "/v1/trigger/query", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTriggerQuery_ValidateError(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	q := storage.Query{Collection: ""} // invalid
	body, _ := json.Marshal(q)
	req := httptest.NewRequest("POST", "/v1/trigger/query", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTriggerQuery_Error(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

	q := storage.Query{Collection: "users"}
	mockEngine.On("ExecuteQuery", mock.Anything, q).Return(nil, assert.AnError)

	body, _ := json.Marshal(q)
	req := httptest.NewRequest("POST", "/v1/trigger/query", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEngine.AssertExpectations(t)
}

func TestHandleTriggerWrite_TransactionFailure(t *testing.T) {
	mockEngine := new(MockQueryService)
	server := NewServer(mockEngine, nil, nil)

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
