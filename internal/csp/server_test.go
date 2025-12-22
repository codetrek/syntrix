package csp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

type fakeStorage struct{}

func (f *fakeStorage) Get(ctx context.Context, path string) (*storage.Document, error) {
	return nil, nil
}
func (f *fakeStorage) Create(ctx context.Context, doc *storage.Document) error { return nil }
func (f *fakeStorage) Update(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	return nil
}
func (f *fakeStorage) Patch(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) error {
	return nil
}
func (f *fakeStorage) Delete(ctx context.Context, path string, pred storage.Filters) error {
	return nil
}
func (f *fakeStorage) Query(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (f *fakeStorage) Watch(ctx context.Context, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	ch := make(chan storage.Event, 1)
	ch <- storage.Event{Id: collection + "/1", Type: storage.EventCreate}
	close(ch)
	return ch, nil
}
func (f *fakeStorage) Transaction(ctx context.Context, fn func(ctx context.Context, tx storage.StorageBackend) error) error {
	return fn(ctx, f)
}
func (f *fakeStorage) Close(ctx context.Context) error { return nil }

func TestServerHealth(t *testing.T) {
	srv := NewServer(&fakeStorage{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "OK")
}

func TestServerHandleWatch_BadJSON(t *testing.T) {
	srv := NewServer(&fakeStorage{})
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/watch", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// recorder without Flusher to trigger streaming unsupported branch
type noFlushRecorder struct{ rec *httptest.ResponseRecorder }

func (n *noFlushRecorder) Header() http.Header         { return n.rec.Header() }
func (n *noFlushRecorder) Write(b []byte) (int, error) { return n.rec.Write(b) }
func (n *noFlushRecorder) WriteHeader(statusCode int)  { n.rec.WriteHeader(statusCode) }

func TestServerHandleWatch_NoFlusher(t *testing.T) {
	srv := NewServer(&fakeStorage{})
	body := []byte(`{"collection":"users"}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/watch", bytes.NewReader(body))
	w := &noFlushRecorder{rec: httptest.NewRecorder()}

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.rec.Code)
}

func TestServerHandleWatch_Stream(t *testing.T) {
	srv := NewServer(&fakeStorage{})
	body := map[string]string{"collection": "users"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/watch", bytes.NewReader(b))
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Body should contain one encoded event
	assert.Contains(t, w.Body.String(), "users/1")
}
