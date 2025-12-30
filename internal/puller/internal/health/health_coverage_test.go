package health

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestStartServer(t *testing.T) {
	checker := NewChecker(nil)
	ctx, cancel := context.WithCancel(context.Background())

	// Start server in goroutine
	go func() {
		_ = StartServer(ctx, ":8099", checker)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Make a request
	resp, err := http.Get("http://localhost:8099/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Stop server
	cancel()
	time.Sleep(100 * time.Millisecond)
}
