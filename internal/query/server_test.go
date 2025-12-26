package query

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func setupTestServer() (*Server, *MockStorageBackend) {
	mockStorage := new(MockStorageBackend)
	// Use 127.0.0.1:1 to avoid DNS lookup timeouts on invalid hostnames.
	// Port 1 is privileged and will fail immediately with connection refused.
	engine := NewEngine(mockStorage, "http://127.0.0.1:1")
	server := NewServer(engine)
	return server, mockStorage
}

func TestServer_GetDocument(t *testing.T) {
	server, mockStorage := setupTestServer()

	path := "test/1"
	doc := &storage.Document{Fullpath: path, Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 1, UpdatedAt: 2, CreatedAt: 1}
	mockStorage.On("Get", mock.Anything, path).Return(doc, nil)

	reqBody, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respDoc model.Document
	err := json.Unmarshal(w.Body.Bytes(), &respDoc)
	assert.NoError(t, err)
	assert.Equal(t, "1", respDoc.GetID())
}

func TestServer_CreateDocument(t *testing.T) {
	server, mockStorage := setupTestServer()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	mockStorage.On("Create", mock.Anything, mock.AnythingOfType("*types.Document")).Return(nil)

	reqBody, _ := json.Marshal(doc)
	req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}
func TestServer_WatchCollection_InvalidBody(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_WatchCollection_WatchError(t *testing.T) {
	server, mockStorage := setupTestServer()

	// Watch returns channel and error.
	// We need to mock Watch to return error.
	// Note: Watch returns <-chan Event, error
	var ch <-chan storage.Event
	mockStorage.On("Watch", mock.Anything, "test", mock.Anything, mock.Anything).Return(ch, assert.AnError)

	reqBody, _ := json.Marshal(map[string]string{"collection": "test"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

type NonFlusherWriter struct {
	http.ResponseWriter
}

func TestServer_WatchCollection_NoFlusher(t *testing.T) {
	server, _ := setupTestServer()

	reqBody, _ := json.Marshal(map[string]string{"collection": "test"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	nw := &NonFlusherWriter{ResponseWriter: w}

	server.ServeHTTP(nw, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_WatchCollection_Success(t *testing.T) {
	server, mockStorage := setupTestServer()

	ch := make(chan storage.Event, 1)
	ch <- storage.Event{Type: storage.EventCreate, Id: "1"}
	close(ch)

	// Cast to read-only channel
	readCh := (<-chan storage.Event)(ch)
	mockStorage.On("Watch", mock.Anything, "test", mock.Anything, mock.Anything).Return(readCh, nil)

	reqBody, _ := json.Marshal(map[string]string{"collection": "test"})
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
func TestServer_CreateDocument_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBuffer([]byte("{bad")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("create error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Create", mock.Anything, mock.AnythingOfType("*types.Document")).Return(assert.AnError)

		reqBody, _ := json.Marshal(model.Document{"id": "1", "collection": "test"})
		req := httptest.NewRequest("POST", "/internal/v1/document/create", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_DeleteDocument(t *testing.T) {
	server, mockStorage := setupTestServer()

	path := "test/1"
	mockStorage.On("Delete", mock.Anything, path, model.Filters(nil)).Return(nil)

	reqBody, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestServer_DeleteDocument_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer([]byte("{bad")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Delete", mock.Anything, "test/1", model.Filters(nil)).Return(model.ErrNotFound)

		reqBody, _ := json.Marshal(map[string]string{"path": "test/1"})
		req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("delete error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Delete", mock.Anything, "test/1", model.Filters(nil)).Return(assert.AnError)

		reqBody, _ := json.Marshal(map[string]string{"path": "test/1"})
		req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("precondition failed", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		pred := model.Filters{{Field: "version", Op: "==", Value: float64(1)}}
		mockStorage.On("Delete", mock.Anything, "test/1", pred).Return(model.ErrPreconditionFailed)

		reqBody, _ := json.Marshal(map[string]interface{}{"path": "test/1", "pred": pred})
		req := httptest.NewRequest("POST", "/internal/v1/document/delete", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	})
}

func TestServer_GetDocument_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Get", mock.Anything, "missing").Return(nil, model.ErrNotFound)

		reqBody, _ := json.Marshal(map[string]string{"path": "missing"})
		req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("storage error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Get", mock.Anything, "err").Return(nil, assert.AnError)

		reqBody, _ := json.Marshal(map[string]string{"path": "err"})
		req := httptest.NewRequest("POST", "/internal/v1/document/get", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_ReplaceDocument_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/document/replace", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("engine error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Get", mock.Anything, "test/1").Return(nil, model.ErrNotFound)
		mockStorage.On("Create", mock.Anything, mock.AnythingOfType("*types.Document")).Return(assert.AnError)

		reqBody, _ := json.Marshal(map[string]interface{}{"data": model.Document{"id": "1", "collection": "test"}})
		req := httptest.NewRequest("POST", "/internal/v1/document/replace", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_PatchDocument_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Patch", mock.Anything, "test/1", mock.Anything, model.Filters(nil)).Return(model.ErrNotFound)

		reqBody, _ := json.Marshal(map[string]interface{}{"data": model.Document{"id": "1", "collection": "test"}})
		req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("engine error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Patch", mock.Anything, "test/1", mock.Anything, model.Filters(nil)).Return(assert.AnError)

		reqBody, _ := json.Marshal(map[string]interface{}{"data": model.Document{"id": "1", "collection": "test"}})
		req := httptest.NewRequest("POST", "/internal/v1/document/patch", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_ExecuteQuery_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/query/execute", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("engine error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Query", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		reqBody, _ := json.Marshal(model.Query{Collection: "c"})
		req := httptest.NewRequest("POST", "/internal/v1/query/execute", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// recorder without Flusher
type noFlusher struct{ rec *httptest.ResponseRecorder }

func (n *noFlusher) Header() http.Header         { return n.rec.Header() }
func (n *noFlusher) Write(b []byte) (int, error) { return n.rec.Write(b) }
func (n *noFlusher) WriteHeader(status int)      { n.rec.WriteHeader(status) }

func TestServer_WatchCollection_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no flusher", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"collection": "c"})
		req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(body))
		w := &noFlusher{rec: httptest.NewRecorder()}

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.rec.Code)
	})

	t.Run("watch error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Watch", mock.Anything, "c", nil, storage.WatchOptions{}).Return(nil, assert.AnError)

		body, _ := json.Marshal(map[string]string{"collection": "c"})
		req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code) // headers flushed before error
	})
}

func TestServer_PullPush_Errors(t *testing.T) {
	server, mockStorage := setupTestServer()

	t.Run("pull bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/replication/v1/pull", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pull error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Query", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		body, _ := json.Marshal(storage.ReplicationPullRequest{Collection: "c"})
		req := httptest.NewRequest("POST", "/internal/replication/v1/pull", bytes.NewBuffer(body))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("push bad json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/replication/v1/push", bytes.NewBuffer([]byte("{bad")))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("push error", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Update", mock.Anything, mock.Anything, mock.Anything, model.Filters{}).Return(assert.AnError)
		mockStorage.On("Get", mock.Anything, mock.Anything).Return(&storage.Document{Id: "c/1", Version: 1}, nil)

		body, _ := json.Marshal(storage.ReplicationPushRequest{Collection: "c", Changes: []storage.ReplicationPushChange{{Doc: &storage.Document{Id: "c/1", Fullpath: "c/1", Data: map[string]interface{}{}}}}})
		req := httptest.NewRequest("POST", "/internal/replication/v1/push", bytes.NewBuffer(body))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_WatchCollection_ContextCancel(t *testing.T) {
	server, mockStorage := setupTestServer()

	ch := make(chan storage.Event)
	readCh := (<-chan storage.Event)(ch)
	mockStorage.On("Watch", mock.Anything, "test", mock.Anything, mock.Anything).Return(readCh, nil)

	reqBody, _ := json.Marshal(map[string]string{"collection": "test"})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/internal/v1/watch", bytes.NewBuffer(reqBody)).WithContext(ctx)
	w := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
