package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	cfg := Config{
		HTTPPort: 8080,
		GRPCPort: 9090,
	}
	srv := New(cfg, nil)
	require.NotNil(t, srv)
}

func TestServer_StartStop(t *testing.T) {
	// Use random ports to avoid conflicts
	cfg := Config{
		HTTPPort: 0, // Let OS choose
		GRPCPort: 0, // Let OS choose
	}
	srv := New(cfg, nil)
	require.NotNil(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start(ctx)
	}()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Stop the server
	err := srv.Stop(context.Background())
	assert.NoError(t, err)

	cancel() // Signal Start to exit

	// Wait for Start to return
	select {
	case err := <-errChan:
		assert.NoError(t, err) // Should be nil on normal shutdown
	case <-time.After(1 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestServer_RegisterHTTP(t *testing.T) {
	cfg := Config{
		HTTPPort: 0,
	}
	srv := New(cfg, nil)

	srv.RegisterHTTPHandler("/test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// We can't easily test if it's registered without starting,
	// but we can ensure it doesn't panic.
}

func TestServer_Start_AlreadyStarted(t *testing.T) {
	cfg := Config{HTTPPort: 0, GRPCPort: 0}
	srv := New(cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	err := srv.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server already started")
}

func TestServer_Start_PortConflict(t *testing.T) {
	// Start a listener to occupy a port
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	cfg := Config{
		HTTPPort: port, // Conflict
		GRPCPort: 0,
	}
	srv := New(cfg, nil)

	// Should fail immediately or shortly after
	err = srv.Start(context.Background())
	assert.Error(t, err)
}

func TestServer_Start_GRPC_PortConflict(t *testing.T) {
	// Start a listener to occupy a port
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	cfg := Config{
		HTTPPort: 0,
		GRPCPort: port, // Conflict
	}
	srv := New(cfg, nil)

	// Should fail immediately or shortly after
	err = srv.Start(context.Background())
	assert.Error(t, err)
}

func TestGlobal(t *testing.T) {
	cfg := Config{
		HTTPPort: 0,
	}
	InitDefault(cfg, nil)
	assert.NotNil(t, Default())

	// Clean up
	defaultService = nil
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 8000, cfg.HTTPPort)
	assert.Equal(t, 9000, cfg.GRPCPort)
	assert.NotZero(t, cfg.HTTPReadTimeout)
	assert.NotZero(t, cfg.HTTPWriteTimeout)
	assert.NotZero(t, cfg.HTTPIdleTimeout)
	assert.NotZero(t, cfg.ShutdownTimeout)
}

func TestGlobalHelpers(t *testing.T) {
	InitDefault(Config{}, nil)
	defer func() { defaultService = nil }()

	// Test RegisterHTTP
	RegisterHTTP("/test-http", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// Test HandleFunc
	HandleFunc("/test-func", func(w http.ResponseWriter, r *http.Request) {})

	// RegisterGRPC is tested in grpc_test.go
}

func TestGlobalHelpers_NoInit(t *testing.T) {
	defaultService = nil

	// Should not panic
	RegisterHTTP("/test", nil)
	HandleFunc("/test", nil)
	RegisterGRPC(nil, nil)
}
