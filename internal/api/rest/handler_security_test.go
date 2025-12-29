package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestMaxBodySize_UnderLimit(t *testing.T) {
	// Handler that reads body
	handler := maxBodySize(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}, 1024) // 1KB limit

	// Request with small body
	body := []byte(`{"message": "hello"}`)
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, string(body), rr.Body.String())
}

func TestMaxBodySize_OverLimit(t *testing.T) {
	// Handler that reads body
	handler := maxBodySize(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}, 100) // 100 byte limit

	// Request with large body (over 100 bytes)
	body := strings.Repeat("x", 200)
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	rr := httptest.NewRecorder()

	handler(rr, req)

	// MaxBytesReader returns error which handler converts to BadRequest
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMaxBodySize_NilBody(t *testing.T) {
	// Handler that just returns OK
	handler := maxBodySize(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, 1024)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWriteError(t *testing.T) {
	rr := httptest.NewRecorder()

	writeError(rr, http.StatusBadRequest, ErrCodeBadRequest, "Test error message")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp APIError
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, ErrCodeBadRequest, resp.Code)
	assert.Equal(t, "Test error message", resp.Message)
}

func TestWriteJSON_Success(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"key": "value"}
	writeJSON(rr, http.StatusOK, data)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp map[string]string
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "value", resp["key"])
}

func TestWriteJSON_Created(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"id": "123"}
	writeJSON(rr, http.StatusCreated, data)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

// failingResponseWriter simulates a writer that fails on Write
type failingResponseWriter struct {
	header http.Header
	code   int
}

func newFailingResponseWriter() *failingResponseWriter {
	return &failingResponseWriter{header: make(http.Header)}
}

func (w *failingResponseWriter) Header() http.Header {
	return w.header
}

func (w *failingResponseWriter) Write(b []byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func (w *failingResponseWriter) WriteHeader(code int) {
	w.code = code
}

func TestWriteError_EncodingFailure(t *testing.T) {
	w := newFailingResponseWriter()
	// This should not panic even when encoding fails
	writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Test error")
	assert.Equal(t, http.StatusBadRequest, w.code)
}

func TestWriteJSON_EncodingFailure(t *testing.T) {
	w := newFailingResponseWriter()
	// This should not panic even when encoding fails
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})
	assert.Equal(t, http.StatusOK, w.code)
}

func TestWriteStorageError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode int
		expectedMsg  string
	}{
		{
			name:         "NotFound",
			err:          model.ErrNotFound,
			expectedCode: http.StatusNotFound,
			expectedMsg:  "Document not found",
		},
		{
			name:         "Exists",
			err:          model.ErrExists,
			expectedCode: http.StatusConflict,
			expectedMsg:  "Document already exists",
		},
		{
			name:         "PreconditionFailed",
			err:          model.ErrPreconditionFailed,
			expectedCode: http.StatusPreconditionFailed,
			expectedMsg:  "Version conflict",
		},
		{
			name:         "Unknown",
			err:          io.EOF, // arbitrary error
			expectedCode: http.StatusInternalServerError,
			expectedMsg:  "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeStorageError(rr, tt.err)

			assert.Equal(t, tt.expectedCode, rr.Code)
			assert.Contains(t, rr.Body.String(), tt.expectedMsg)
		})
	}
}

func TestAPIErrorCodes(t *testing.T) {
	// Verify error codes are defined
	assert.Equal(t, "BAD_REQUEST", ErrCodeBadRequest)
	assert.Equal(t, "UNAUTHORIZED", ErrCodeUnauthorized)
	assert.Equal(t, "FORBIDDEN", ErrCodeForbidden)
	assert.Equal(t, "NOT_FOUND", ErrCodeNotFound)
	assert.Equal(t, "CONFLICT", ErrCodeConflict)
	assert.Equal(t, "PRECONDITION_FAILED", ErrCodePreconditionFailed)
	assert.Equal(t, "REQUEST_TOO_LARGE", ErrCodeRequestTooLarge)
	assert.Equal(t, "INTERNAL_ERROR", ErrCodeInternalError)
}

func TestBodySizeConstants(t *testing.T) {
	assert.Equal(t, 1<<20, DefaultMaxBodySize) // 1MB
	assert.Equal(t, 10<<20, LargeMaxBodySize)  // 10MB
}

func TestTimeoutConstants(t *testing.T) {
	assert.Equal(t, 30*time.Second, DefaultRequestTimeout)
	assert.Equal(t, 60*time.Second, LongRequestTimeout)
}

func TestWithRecover_NoPanic(t *testing.T) {
	// Handler that runs normally
	handler := withRecover(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "success", rr.Body.String())
}

func TestWithRecover_Panic(t *testing.T) {
	// Handler that panics
	handler := withRecover(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should not panic, should return 500
	handler(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp APIError
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, ErrCodeInternalError, resp.Code)
	assert.Equal(t, "Internal server error", resp.Message)
}

func TestWithRecover_PanicWithError(t *testing.T) {
	// Handler that panics with an error
	handler := withRecover(func(w http.ResponseWriter, r *http.Request) {
		panic(io.EOF) // panic with error value
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should not panic, should return 500
	handler(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestWithTimeout_Normal(t *testing.T) {
	// Handler that completes quickly
	handler := withTimeout(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}, 1*time.Second)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "success", rr.Body.String())
}

func TestWithTimeout_ContextPropagated(t *testing.T) {
	// Verify timeout is set in context
	var contextTimeout time.Duration
	handler := withTimeout(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if ok {
			contextTimeout = time.Until(deadline)
		}
		w.WriteHeader(http.StatusOK)
	}, 5*time.Second)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	// Context should have a deadline roughly 5 seconds from now
	assert.True(t, contextTimeout > 4*time.Second && contextTimeout <= 5*time.Second,
		"Expected timeout around 5 seconds, got %v", contextTimeout)
}

func TestWithTimeout_ContextCancellation(t *testing.T) {
	// Handler that checks context cancellation
	handler := withTimeout(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		select {
		case <-ctx.Done():
			// Context was cancelled
			w.WriteHeader(http.StatusRequestTimeout)
		case <-time.After(10 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}, 100*time.Millisecond)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	// Handler should complete normally before timeout
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestMiddlewareChain(t *testing.T) {
	// Test that withRecover and withTimeout work together
	handler := withRecover(withTimeout(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}, 1*time.Second))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "success", rr.Body.String())
}

func TestMiddlewareChain_PanicWithTimeout(t *testing.T) {
	// Test that panic recovery works even with timeout middleware
	handler := withRecover(withTimeout(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic in chained middleware")
	}, 1*time.Second))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	var resp APIError
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, ErrCodeInternalError, resp.Code)
}

// Tests for Request ID middleware

func TestWithRequestID_GeneratesID(t *testing.T) {
	handler := withRequestID(func(w http.ResponseWriter, r *http.Request) {
		// Verify request ID is in context
		requestID := getRequestID(r.Context())
		assert.NotEmpty(t, requestID, "Request ID should be generated")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	// Verify request ID is in response header
	assert.NotEmpty(t, rr.Header().Get("X-Request-ID"))
}

func TestWithRequestID_UsesExisting(t *testing.T) {
	existingID := "existing-request-id-123"
	handler := withRequestID(func(w http.ResponseWriter, r *http.Request) {
		requestID := getRequestID(r.Context())
		assert.Equal(t, existingID, requestID)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, existingID, rr.Header().Get("X-Request-ID"))
}

func TestGetRequestID_NoID(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	// No request ID in context
	id := getRequestID(req.Context())
	assert.Empty(t, id)
}

// Tests for Validation Config

func TestValidationConfig_Default(t *testing.T) {
	cfg := DefaultValidationConfig()
	assert.Equal(t, 1000, cfg.MaxQueryLimit)
	assert.Equal(t, 1000, cfg.MaxReplicationLimit)
	assert.Equal(t, 1024, cfg.MaxPathLength)
	assert.Equal(t, 64, cfg.MaxIDLength)
}

func TestSetValidationConfig(t *testing.T) {
	// Save original config
	originalCfg := validationConfig

	// Test custom config
	customCfg := ValidationConfig{
		MaxQueryLimit:       500,
		MaxReplicationLimit: 500,
		MaxPathLength:       512,
		MaxIDLength:         32,
	}
	SetValidationConfig(customCfg)
	assert.Equal(t, 500, validationConfig.MaxQueryLimit)
	assert.Equal(t, 500, validationConfig.MaxReplicationLimit)

	// Test that validation uses new config
	q := model.Query{Collection: "test", Limit: 600}
	err := validateQuery(q)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")

	// Test with valid limit under new config
	q.Limit = 400
	err = validateQuery(q)
	assert.NoError(t, err)

	// Restore original config
	SetValidationConfig(originalCfg)
}

func TestSetValidationConfig_DefaultsForZero(t *testing.T) {
	// Save original config
	originalCfg := validationConfig

	// Test that zero values get defaults
	SetValidationConfig(ValidationConfig{})
	assert.Equal(t, DefaultValidationConfig().MaxQueryLimit, validationConfig.MaxQueryLimit)
	assert.Equal(t, DefaultValidationConfig().MaxReplicationLimit, validationConfig.MaxReplicationLimit)

	// Restore original config
	SetValidationConfig(originalCfg)
}

func TestValidatePathSyntax_MaxLength(t *testing.T) {
	// Save original config
	originalCfg := validationConfig

	// Set a small max path length
	SetValidationConfig(ValidationConfig{
		MaxPathLength: 20,
	})

	// Test path within limit
	err := validatePathSyntax("short/path")
	assert.NoError(t, err)

	// Test path exceeding limit
	err = validatePathSyntax("this/is/a/very/long/path/that/exceeds/limit")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "20")

	// Restore original config
	SetValidationConfig(originalCfg)
}

// Tests for Body Caching

func TestGetParsedBody_WithCache(t *testing.T) {
	cachedData := map[string]interface{}{
		"name": "test",
		"age":  float64(25),
	}

	req := httptest.NewRequest("POST", "/test", nil)
	ctx := context.WithValue(req.Context(), contextKeyParsedBody, cachedData)
	req = req.WithContext(ctx)

	result := getParsedBody(req.Context())
	assert.NotNil(t, result)
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, float64(25), result["age"])
}

func TestGetParsedBody_NoCache(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", nil)
	result := getParsedBody(req.Context())
	assert.Nil(t, result)
}

func TestDecodeBody_WithCache(t *testing.T) {
	cachedData := map[string]interface{}{
		"doc": map[string]interface{}{
			"name": "cached doc",
		},
		"ifMatch": nil,
	}

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"doc":{"name":"from body"}}`))
	ctx := context.WithValue(req.Context(), contextKeyParsedBody, cachedData)
	req = req.WithContext(ctx)

	var data UpdateDocumentRequest
	err := decodeBody(req, &data)
	assert.NoError(t, err)
	assert.Equal(t, "cached doc", data.Doc["name"]) // Should use cached value
}

func TestDecodeBody_NoCache(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"doc":{"name":"from body"}}`))

	var data UpdateDocumentRequest
	err := decodeBody(req, &data)
	assert.NoError(t, err)
	assert.Equal(t, "from body", data.Doc["name"]) // Should parse from body
}

func TestDecodeBody_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{invalid json}`))

	var data UpdateDocumentRequest
	err := decodeBody(req, &data)
	assert.Error(t, err)
}

func TestDecodeBodyAsDocument_WithCache(t *testing.T) {
	cachedData := map[string]interface{}{
		"id":   "doc-1",
		"name": "cached document",
	}

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"name":"from body"}`))
	ctx := context.WithValue(req.Context(), contextKeyParsedBody, cachedData)
	req = req.WithContext(ctx)

	doc, err := decodeBodyAsDocument(req)
	assert.NoError(t, err)
	assert.Equal(t, "doc-1", doc["id"])
	assert.Equal(t, "cached document", doc["name"])
}

func TestDecodeBodyAsDocument_NoCache(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"name":"from body"}`))

	doc, err := decodeBodyAsDocument(req)
	assert.NoError(t, err)
	assert.Equal(t, "from body", doc["name"])
}

func TestDecodeBodyAsDocument_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{invalid}`))

	doc, err := decodeBodyAsDocument(req)
	assert.Error(t, err)
	assert.Nil(t, doc)
}

// Tests for stringError type

func TestStringError(t *testing.T) {
	err := stringError("test error message")
	assert.Equal(t, "test error message", err.Error())
	assert.Implements(t, (*error)(nil), err)
}

func TestErrInvalidPath(t *testing.T) {
	assert.Equal(t, "invalid path", errInvalidPath.Error())
}

// Tests for splitPath function

func TestSplitPath(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		wantCollection string
		wantID         string
		wantErr        bool
	}{
		{
			name:           "valid simple path",
			path:           "users/user-1",
			wantCollection: "users",
			wantID:         "user-1",
			wantErr:        false,
		},
		{
			name:           "valid nested path",
			path:           "rooms/room-1/messages/msg-1",
			wantCollection: "rooms/room-1/messages",
			wantID:         "msg-1",
			wantErr:        false,
		},
		{
			name:    "single segment - invalid",
			path:    "users",
			wantErr: true,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
		},
		{
			name:    "trailing slash - empty id",
			path:    "users/",
			wantErr: true,
		},
		{
			name:    "leading slash - empty collection",
			path:    "/user-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collection, id, err := splitPath(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, errInvalidPath, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantCollection, collection)
				assert.Equal(t, tt.wantID, id)
			}
		})
	}
}

// Tests for logRequest function

func TestLogRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	ctx := context.WithValue(req.Context(), contextKeyRequestID, "test-request-id")
	req = req.WithContext(ctx)

	// This should not panic and should log the request
	// We can't easily verify the log output, but we can verify it doesn't error
	logRequest(req, http.StatusOK, 100*time.Millisecond)
}

func TestLogRequest_NoRequestID(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/docs", nil)
	// No request ID in context
	logRequest(req, http.StatusCreated, 50*time.Millisecond)
}
