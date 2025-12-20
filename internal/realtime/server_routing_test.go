package realtime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"
)

type MockQueryService struct{}

var _ query.Service = &MockQueryService{}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (*storage.Document, error) {
	return nil, nil
}
func (m *MockQueryService) CreateDocument(ctx context.Context, doc *storage.Document) error {
	return nil
}
func (m *MockQueryService) ReplaceDocument(ctx context.Context, path string, collection string, data common.Document, pred storage.Filters) (*storage.Document, error) {
	return nil, nil
}
func (m *MockQueryService) PatchDocument(ctx context.Context, path string, data map[string]interface{}, pred storage.Filters) (*storage.Document, error) {
	return nil, nil
}
func (m *MockQueryService) DeleteDocument(ctx context.Context, path string) error { return nil }
func (m *MockQueryService) ExecuteQuery(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return make(chan storage.Event), nil
}
func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}
func (m *MockQueryService) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	return fn(ctx, m)
}

func TestServer_Routing(t *testing.T) {
	qs := &MockQueryService{}
	server := NewServer(qs)
	go server.hub.Run()

	tests := []struct {
		name      string
		headers   map[string]string
		expectSSE bool
	}{
		{
			name: "Strict SSE Accept",
			headers: map[string]string{
				"Accept": "text/event-stream",
			},
			expectSSE: true,
		},
		{
			name: "Loose SSE Accept",
			headers: map[string]string{
				"Accept": "text/event-stream; charset=utf-8",
			},
			expectSSE: true,
		},
		{
			name: "Complex SSE Accept",
			headers: map[string]string{
				"Accept": "application/json, text/event-stream; q=0.9",
			},
			expectSSE: true,
		},
		{
			name: "WebSocket Upgrade",
			headers: map[string]string{
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			expectSSE: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("GET", "/v1/realtime", nil).WithContext(ctx)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			w := httptest.NewRecorder()

			// Cancel context after a short delay to unblock ServeSSE
			go func() {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}()

			server.ServeHTTP(w, req)

			resp := w.Result()

			if tt.expectSSE {
				if resp.Header.Get("Content-Type") != "text/event-stream" {
					t.Errorf("Expected Content-Type text/event-stream, got %s", resp.Header.Get("Content-Type"))
				}
			} else {
				if resp.Header.Get("Content-Type") == "text/event-stream" {
					t.Errorf("Did not expect Content-Type text/event-stream")
				}
			}
		})
	}
}
