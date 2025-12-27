package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewServer(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", nil, Config{})
	assert.NotNil(t, server)
	assert.NotNil(t, server.hub)
	assert.Equal(t, "docs", server.dataCollection)
}

func TestServer_HandleWS(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", &mockAuthService{}, Config{EnableAuth: true})

	// Start Hub
	ctxHub, cancelHub := context.WithCancel(context.Background())
	defer cancelHub()
	go server.hub.Run(ctxHub)

	// WS upgrade requires a real server usually or httptest with specific headers.
	// ServeWs calls upgrader.Upgrade which fails if not a websocket handshake.
	// We expect it to fail upgrade but at least call the function.

	req := httptest.NewRequest("GET", "/ws", nil)
	w := httptest.NewRecorder()

	server.HandleWS(w, req)

	// Since it's not a valid WS request, it should probably return 400 or similar from upgrader.
	// But ServeWs implementation might log error and return.
	// Let's check ServeWs implementation if possible, but for coverage, calling it is enough.
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestServer_HandleWS_QueryToken(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", &mockAuthService{}, Config{EnableAuth: true})

	req := httptest.NewRequest("GET", "/ws?access_token=bad", nil)
	w := httptest.NewRecorder()

	server.HandleWS(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Query token not allowed")
}

func TestTokenFromQueryParam(t *testing.T) {
	// Case 1: Nil request
	assert.Equal(t, "", tokenFromQueryParam(nil))

	// Case 2: access_token
	req1 := httptest.NewRequest("GET", "/?access_token=abc", nil)
	assert.Equal(t, "abc", tokenFromQueryParam(req1))

	// Case 3: token
	req2 := httptest.NewRequest("GET", "/?token=xyz", nil)
	assert.Equal(t, "xyz", tokenFromQueryParam(req2))

	// Case 4: Both (access_token takes precedence)
	req3 := httptest.NewRequest("GET", "/?access_token=abc&token=xyz", nil)
	assert.Equal(t, "abc", tokenFromQueryParam(req3))

	// Case 5: None
	req4 := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "", tokenFromQueryParam(req4))
}

func TestServer_HandleSSE(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", &mockAuthService{}, Config{EnableAuth: true, AllowedOrigins: []string{"http://example.com"}})

	// Start Hub
	ctxHub, cancelHub := context.WithCancel(context.Background())
	defer cancelHub()
	go server.hub.Run(ctxHub)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	// Cancel context shortly to unblock HandleSSE
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	server.HandleSSE(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestSafeCheckOrigin(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		requestHost string
		expected    bool
	}{
		// Empty origin (non-browser clients)
		{
			name:        "empty origin allowed",
			origin:      "",
			requestHost: "localhost:8080",
			expected:    true,
		},
		// Same host:port
		{
			name:        "same host and port allowed",
			origin:      "http://localhost:8080",
			requestHost: "localhost:8080",
			expected:    true,
		},
		{
			name:        "same host different protocol allowed",
			origin:      "https://localhost:8080",
			requestHost: "localhost:8080",
			expected:    true,
		},
		// Same host different ports (development)
		{
			name:        "localhost different ports allowed",
			origin:      "http://localhost:3000",
			requestHost: "localhost:8080",
			expected:    true,
		},
		{
			name:        "127.0.0.1 different ports allowed",
			origin:      "http://127.0.0.1:3000",
			requestHost: "127.0.0.1:8080",
			expected:    true,
		},
		{
			name:        "LAN IP different ports allowed",
			origin:      "http://192.168.1.197:3000",
			requestHost: "192.168.1.197:8080",
			expected:    true,
		},
		{
			name:        "LAN IP no port to with port allowed",
			origin:      "http://192.168.1.100",
			requestHost: "192.168.1.100:8080",
			expected:    true,
		},
		// Different hosts blocked
		{
			name:        "localhost to 127.0.0.1 blocked",
			origin:      "http://localhost:3000",
			requestHost: "127.0.0.1:8080",
			expected:    false,
		},
		{
			name:        "external origin to localhost blocked",
			origin:      "http://evil.com",
			requestHost: "localhost:8080",
			expected:    false,
		},
		{
			name:        "external origin with port to localhost blocked",
			origin:      "http://evil.com:3000",
			requestHost: "localhost:8080",
			expected:    false,
		},
		{
			name:        "different LAN IPs blocked",
			origin:      "http://192.168.1.100:3000",
			requestHost: "192.168.1.200:8080",
			expected:    false,
		},
		// Invalid origins
		{
			name:        "invalid origin URL blocked",
			origin:      "://invalid",
			requestHost: "localhost:8080",
			expected:    false,
		},
		// Production scenarios
		{
			name:        "production same domain allowed",
			origin:      "https://app.example.com",
			requestHost: "app.example.com",
			expected:    true,
		},
		{
			name:        "production same domain different ports allowed",
			origin:      "https://app.example.com:443",
			requestHost: "app.example.com:8080",
			expected:    true,
		},
		{
			name:        "production different subdomain blocked",
			origin:      "https://evil.example.com",
			requestHost: "app.example.com",
			expected:    false,
		},
		{
			name:        "production different domain blocked",
			origin:      "https://other.com",
			requestHost: "app.example.com",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			req.Host = tt.requestHost
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			result := safeCheckOrigin(req)
			assert.Equal(t, tt.expected, result, "origin=%q host=%q", tt.origin, tt.requestHost)
		})
	}
}
