package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

// errorWriter wraps a ResponseRecorder but forces Write to fail.
type errorWriter struct{ *httptest.ResponseRecorder }

func (e *errorWriter) Write(b []byte) (int, error) { return 0, assert.AnError }

// noFlushWriter implements ResponseWriter without Flusher.
type noFlushWriter struct {
	h http.Header
	b *strings.Builder
	c int
}

func (w *noFlushWriter) Header() http.Header         { return w.h }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.b.WriteString(string(b)) }
func (w *noFlushWriter) WriteHeader(statusCode int)  { w.c = statusCode }

func TestServeSSE_BroadcastFlow(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, AllowDevOrigin: false, EnableAuth: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = context.WithValue(ctx, identity.ContextKeyTenant, "default")
	req := httptest.NewRequest("GET", "/realtime/sse?collection=users", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		ServeSSE(hub, qs, auth, cfg, rr, req)
		close(done)
	}()

	// Wait for registration and send a broadcast
	time.Sleep(20 * time.Millisecond)
	hub.Broadcast(storage.Event{
		Type:     storage.EventCreate,
		Id:       "users/1",
		TenantID: "default",
		Document: &storage.Document{
			Fullpath:   "users/1",
			Collection: "users",
			TenantID:   "default",
			Data:       map[string]interface{}{"name": "Alice"},
		},
	})

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ServeSSE did not exit")
	}

	body := rr.Body.String()
	assert.Contains(t, body, ": connected")
	assert.Contains(t, body, "data:")
}

func TestServeSSE_UnsupportedFlusher(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}

	req := httptest.NewRequest("GET", "/realtime/sse", nil).WithContext(context.WithValue(context.Background(), identity.ContextKeyTenant, "default"))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://example.com")
	w := &noFlushWriter{h: make(http.Header), b: &strings.Builder{}, c: http.StatusOK}

	ServeSSE(hub, qs, auth, cfg, w, req)

	assert.Equal(t, http.StatusInternalServerError, w.c)
}

func TestServeSSE_WriteError(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)
	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}
	original := sseHeartbeatInterval
	sseHeartbeatInterval = 20 * time.Millisecond
	defer func() { sseHeartbeatInterval = original }()

	req := httptest.NewRequest("GET", "/realtime/sse?collection=users", nil).WithContext(context.WithValue(context.Background(), identity.ContextKeyTenant, "default"))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://example.com")
	errRec := &errorWriter{ResponseRecorder: httptest.NewRecorder()}

	ServeSSE(hub, qs, auth, cfg, errRec, req)

	// Should exit without panic; no specific code guaranteed as Write error happens after headers
}

func TestServeSSE_AuthMissing(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}

	req := httptest.NewRequest("GET", "/realtime/sse", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	ServeSSE(hub, qs, auth, cfg, w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeSSE_OriginMissingWithCredentials(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}

	req := httptest.NewRequest("GET", "/realtime/sse", nil).WithContext(context.WithValue(context.Background(), identity.ContextKeyTenant, "default"))
	req.Header.Set("Authorization", "Bearer good")
	w := httptest.NewRecorder()

	ServeSSE(hub, qs, auth, cfg, w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestServeSSE_OriginDisallowed(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}

	req := httptest.NewRequest("GET", "/realtime/sse", nil).WithContext(context.WithValue(context.Background(), identity.ContextKeyTenant, "default"))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://bad.com")
	w := httptest.NewRecorder()

	ServeSSE(hub, qs, auth, cfg, w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestServeSSE_CookieIgnored(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	hub := NewHub()
	go hub.Run(hubCtx)

	qs := &MockQueryService{}
	auth := &mockAuthService{}
	cfg := Config{AllowedOrigins: []string{"http://example.com"}, EnableAuth: true}

	reqCtx, reqCancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/realtime/sse", nil).WithContext(context.WithValue(reqCtx, identity.ContextKeyTenant, "default"))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Cookie", "a=b")
	w := httptest.NewRecorder()

	go func() {
		time.Sleep(100 * time.Millisecond)
		reqCancel()
	}()

	ServeSSE(hub, qs, auth, cfg, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), ": connected")
}
