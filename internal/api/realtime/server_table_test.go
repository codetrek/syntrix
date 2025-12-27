package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTokenFromQueryParam_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"Nil request", "", ""},
		{"access_token", "/?access_token=abc", "abc"},
		{"token", "/?token=xyz", "xyz"},
		{"Both (access_token takes precedence)", "/?access_token=abc&token=xyz", "abc"},
		{"None", "/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.url != "" {
				req = httptest.NewRequest("GET", tt.url, nil)
			}
			assert.Equal(t, tt.expected, tokenFromQueryParam(req))
		})
	}
}

func TestServer_HandleWS_TableDriven(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", &mockAuthService{}, Config{EnableAuth: true})

	// Start Hub
	ctxHub, cancelHub := context.WithCancel(context.Background())
	defer cancelHub()
	go server.hub.Run(ctxHub)

	tests := []struct {
		name           string
		url            string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "Invalid WS Upgrade",
			url:            "/ws",
			expectedStatus: http.StatusBadRequest, // Upgrader usually returns 400
		},
		{
			name:           "Query Token Not Allowed",
			url:            "/ws?access_token=bad",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Query token not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()

			server.HandleWS(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestServer_HandleSSE_TableDriven(t *testing.T) {
	mockQS := new(MockQueryService)
	server := NewServer(mockQS, "docs", &mockAuthService{}, Config{EnableAuth: true, AllowedOrigins: []string{"http://example.com"}})

	// Start Hub
	ctxHub, cancelHub := context.WithCancel(context.Background())
	defer cancelHub()
	go server.hub.Run(ctxHub)

	tests := []struct {
		name           string
		headers        map[string]string
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "Success",
			headers: map[string]string{
				"Authorization": "Bearer good",
				"Origin":        "http://example.com",
			},
			expectedStatus: http.StatusOK,
		},
		// Add more scenarios if needed, e.g., missing auth, invalid origin
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			// Cancel context shortly to unblock HandleSSE
			go func() {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}()

			server.HandleSSE(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}
