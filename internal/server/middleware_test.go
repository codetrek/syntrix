package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIDMiddleware(t *testing.T) {
	srv := New(Config{}, nil).(*serverImpl)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := GetRequestID(r.Context())
		assert.NotEmpty(t, id)
		w.Header().Set("X-Test-Request-ID", id)
	})

	handler := srv.requestIDMiddleware(nextHandler)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"))
	assert.Equal(t, resp.Header.Get("X-Request-ID"), resp.Header.Get("X-Test-Request-ID"))
}

func TestRequestIDMiddleware_ExistingID(t *testing.T) {
	srv := New(Config{}, nil).(*serverImpl)

	existingID := "existing-id"
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := GetRequestID(r.Context())
		assert.Equal(t, existingID, id)
	})

	handler := srv.requestIDMiddleware(nextHandler)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", existingID)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, existingID, resp.Header.Get("X-Request-ID"))
}

func TestRecoveryMiddleware(t *testing.T) {
	srv := New(Config{}, nil).(*serverImpl)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("oops")
	})

	handler := srv.recoveryMiddleware(nextHandler)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Should not panic
	assert.NotPanics(t, func() {
		handler.ServeHTTP(w, req)
	})

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestCORSMiddleware(t *testing.T) {
	cfg := Config{EnableCORS: true}
	srv := New(cfg, nil).(*serverImpl)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.corsMiddleware(nextHandler)

	// Test Preflight
	req := httptest.NewRequest("OPTIONS", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "GET")

	// Test Normal Request
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestWrapMiddleware(t *testing.T) {
	cfg := Config{EnableCORS: true}
	srv := New(cfg, nil).(*serverImpl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := srv.wrapMiddleware(handler)
	require.NotNil(t, wrapped)

	// Verify it works
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestTimeoutMiddleware(t *testing.T) {
	timeout := 10 * time.Millisecond
	handler := TimeoutMiddleware(timeout)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if deadline is set
		_, ok := r.Context().Deadline()
		assert.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid input")

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var errResp APIError
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	assert.NoError(t, err)
	assert.Equal(t, "BAD_REQUEST", errResp.Code)
	assert.Equal(t, "Invalid input", errResp.Message)
}

type FaultyResponseWriter struct {
	http.ResponseWriter
}

func (f *FaultyResponseWriter) Write(b []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteError_WriteFailure(t *testing.T) {
	w := httptest.NewRecorder()
	fw := &FaultyResponseWriter{ResponseWriter: w}

	// Should not panic and cover the error path
	writeError(fw, http.StatusInternalServerError, "ERROR", "msg")
}
