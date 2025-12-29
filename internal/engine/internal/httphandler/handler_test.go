package httphandler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func setupTestHandler() (*Handler, *MockService) {
	mockService := new(MockService)
	handler := NewWithEngine(mockService)
	return handler, mockService
}

func TestHandler_GetDocument_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	expectedDoc := model.Document{
		"id":         "1",
		"collection": "test",
		"foo":        "bar",
		"version":    float64(1),
	}
	mockService.On("GetDocument", mock.Anything, "default", path).Return(expectedDoc, nil)

	reqBody, _ := json.Marshal(map[string]string{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respDoc model.Document
	err := json.Unmarshal(w.Body.Bytes(), &respDoc)
	assert.NoError(t, err)
	assert.Equal(t, "1", respDoc.GetID())
	mockService.AssertExpectations(t)
}

func TestHandler_GetDocument_NotFound(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/missing"
	mockService.On("GetDocument", mock.Anything, "default", path).Return(nil, model.ErrNotFound)

	reqBody, _ := json.Marshal(map[string]string{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_GetDocument_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_CreateDocument_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	mockService.On("CreateDocument", mock.Anything, "default", mock.MatchedBy(func(d model.Document) bool {
		return d.GetCollection() == "test"
	})).Return(nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_ReplaceDocument_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	expectedResult := model.Document{"id": "1", "collection": "test", "foo": "bar", "version": float64(2)}
	mockService.On("ReplaceDocument", mock.Anything, "default", mock.AnythingOfType("model.Document"), mock.AnythingOfType("model.Filters")).Return(expectedResult, nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/replace", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_PatchDocument_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "updated"}
	expectedResult := model.Document{"id": "1", "collection": "test", "foo": "updated", "version": float64(2)}
	mockService.On("PatchDocument", mock.Anything, "default", mock.AnythingOfType("model.Document"), mock.AnythingOfType("model.Filters")).Return(expectedResult, nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_PatchDocument_NotFound(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "updated"}
	mockService.On("PatchDocument", mock.Anything, "default", mock.AnythingOfType("model.Document"), mock.AnythingOfType("model.Filters")).Return(nil, model.ErrNotFound)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_DeleteDocument_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	mockService.On("DeleteDocument", mock.Anything, "default", path, mock.AnythingOfType("model.Filters")).Return(nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_DeleteDocument_NotFound(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/missing"
	mockService.On("DeleteDocument", mock.Anything, "default", path, mock.AnythingOfType("model.Filters")).Return(model.ErrNotFound)

	reqBody, _ := json.Marshal(map[string]interface{}{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_DeleteDocument_PreconditionFailed(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	mockService.On("DeleteDocument", mock.Anything, "default", path, mock.AnythingOfType("model.Filters")).Return(model.ErrPreconditionFailed)

	reqBody, _ := json.Marshal(map[string]interface{}{"path": path, "tenant": "default", "pred": []model.Filter{{Field: "version", Op: "==", Value: float64(1)}}})
	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_ExecuteQuery_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	expectedDocs := []model.Document{
		{"id": "1", "collection": "test", "foo": "bar1"},
		{"id": "2", "collection": "test", "foo": "bar2"},
	}
	mockService.On("ExecuteQuery", mock.Anything, "default", mock.AnythingOfType("model.Query")).Return(expectedDocs, nil)

	query := model.Query{Collection: "test"}
	reqBody, _ := json.Marshal(map[string]interface{}{"query": query, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/query/execute", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respDocs []model.Document
	err := json.Unmarshal(w.Body.Bytes(), &respDocs)
	assert.NoError(t, err)
	assert.Len(t, respDocs, 2)
	mockService.AssertExpectations(t)
}

func TestHandler_Health(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestHandler_DefaultTenant(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	expectedDoc := model.Document{"id": "1", "collection": "test"}
	// Should use default tenant when not provided
	mockService.On("GetDocument", mock.Anything, "default", path).Return(expectedDoc, nil)

	reqBody, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_Pull_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	pullReq := storage.ReplicationPullRequest{
		Collection: "test",
		Checkpoint: 0,
		Limit:      10,
	}
	expectedResp := &storage.ReplicationPullResponse{
		Documents:  []*storage.Document{},
		Checkpoint: 0,
	}
	mockService.On("Pull", mock.Anything, "default", pullReq).Return(expectedResp, nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"request": pullReq, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/replication/v1/pull", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_Push_Success(t *testing.T) {
	handler, mockService := setupTestHandler()

	pushReq := storage.ReplicationPushRequest{
		Collection: "test",
		Changes:    []storage.ReplicationPushChange{},
	}
	expectedResp := &storage.ReplicationPushResponse{
		Conflicts: nil,
	}
	mockService.On("Push", mock.Anything, "default", pushReq).Return(expectedResp, nil)

	reqBody, _ := json.Marshal(map[string]interface{}{"request": pushReq, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/replication/v1/push", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

// Additional tests for better coverage

func TestNew_ReturnsHTTPHandler(t *testing.T) {
	mockService := new(MockService)
	h := New(mockService)

	assert.NotNil(t, h)
	// Verify it implements http.Handler
	var _ http.Handler = h
}

func TestHandler_GetDocument_InternalError(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	mockService.On("GetDocument", mock.Anything, "default", path).Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]string{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_CreateDocument_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_CreateDocument_Error(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	mockService.On("CreateDocument", mock.Anything, "default", mock.AnythingOfType("model.Document")).Return(assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_ReplaceDocument_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/document/replace", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_ReplaceDocument_Error(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	mockService.On("ReplaceDocument", mock.Anything, "default", mock.AnythingOfType("model.Document"), mock.AnythingOfType("model.Filters")).Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/replace", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_PatchDocument_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_PatchDocument_InternalError(t *testing.T) {
	handler, mockService := setupTestHandler()

	doc := model.Document{"id": "1", "collection": "test", "foo": "updated"}
	mockService.On("PatchDocument", mock.Anything, "default", mock.AnythingOfType("model.Document"), mock.AnythingOfType("model.Filters")).Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"data": doc, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_DeleteDocument_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_DeleteDocument_InternalError(t *testing.T) {
	handler, mockService := setupTestHandler()

	path := "test/1"
	mockService.On("DeleteDocument", mock.Anything, "default", path, mock.AnythingOfType("model.Filters")).Return(assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"path": path, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_ExecuteQuery_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/query/execute", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_ExecuteQuery_Error(t *testing.T) {
	handler, mockService := setupTestHandler()

	mockService.On("ExecuteQuery", mock.Anything, "default", mock.AnythingOfType("model.Query")).Return(nil, assert.AnError)

	query := model.Query{Collection: "test"}
	reqBody, _ := json.Marshal(map[string]interface{}{"query": query, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/query/execute", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_WatchCollection_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// NonFlusherWriter is a ResponseWriter that does not implement http.Flusher
type NonFlusherWriter struct {
	code int
	body []byte
}

func (w *NonFlusherWriter) Header() http.Header {
	return http.Header{}
}

func (w *NonFlusherWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}

func (w *NonFlusherWriter) WriteHeader(code int) {
	w.code = code
}

func TestHandler_WatchCollection_NoFlusher(t *testing.T) {
	handler, _ := setupTestHandler()

	reqBody, _ := json.Marshal(map[string]string{"collection": "test", "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := &NonFlusherWriter{}

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.code)
}

func TestHandler_WatchCollection_ServiceError(t *testing.T) {
	handler, mockService := setupTestHandler()

	mockService.On("WatchCollection", mock.Anything, "default", "test").Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]string{"collection": "test", "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should return 200 OK with headers set before error, then exit
	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_WatchCollection_StreamEvents(t *testing.T) {
	handler, mockService := setupTestHandler()

	// Create a channel that will send events
	eventChan := make(chan storage.Event, 2)
	eventChan <- storage.Event{Type: storage.EventCreate, Id: "1"}
	close(eventChan) // Close to end the stream

	mockService.On("WatchCollection", mock.Anything, "default", "test").Return((<-chan storage.Event)(eventChan), nil)

	reqBody, _ := json.Marshal(map[string]string{"collection": "test", "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"type":"create"`)
	mockService.AssertExpectations(t)
}

func TestHandler_Pull_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/replication/v1/pull", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Pull_Error(t *testing.T) {
	handler, mockService := setupTestHandler()

	pullReq := storage.ReplicationPullRequest{
		Collection: "test",
		Checkpoint: 0,
		Limit:      10,
	}
	mockService.On("Pull", mock.Anything, "default", pullReq).Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"request": pullReq, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/replication/v1/pull", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestHandler_Push_InvalidBody(t *testing.T) {
	handler, _ := setupTestHandler()

	req := httptest.NewRequest("POST", "/internal/replication/v1/push", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Push_Error(t *testing.T) {
	handler, mockService := setupTestHandler()

	pushReq := storage.ReplicationPushRequest{
		Collection: "test",
		Changes:    []storage.ReplicationPushChange{},
	}
	mockService.On("Push", mock.Anything, "default", pushReq).Return(nil, assert.AnError)

	reqBody, _ := json.Marshal(map[string]interface{}{"request": pushReq, "tenant": "default"})
	req := httptest.NewRequest("POST", "/internal/replication/v1/push", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestTenantOrDefault(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", model.DefaultTenantID},
		{"custom", "custom"},
		{"tenant-1", "tenant-1"},
	}

	for _, tc := range tests {
		result := tenantOrDefault(tc.input)
		assert.Equal(t, tc.expected, result)
	}
}
