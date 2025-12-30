package core

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/events"
)

func TestNew(t *testing.T) {
	t.Parallel()
	cfg := config.PullerConfig{}

	// Test with nil logger
	p := New(cfg, nil)
	if p == nil {
		t.Fatal("New() returned nil")
	}
	if p.backends == nil {
		t.Error("backends map should be initialized")
	}

	// Test with provided logger
	logger := slog.Default()
	p2 := New(cfg, logger)
	if p2 == nil {
		t.Fatal("New() with logger returned nil")
	}
}

func TestPuller_SetEventHandler(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	called := false
	handler := func(ctx context.Context, backendName string, event *events.NormalizedEvent) error {
		called = true
		return nil
	}

	p.SetEventHandler(handler)

	if p.eventHandler == nil {
		t.Error("eventHandler should be set")
	}

	// Verify handler can be called
	_ = p.eventHandler(context.Background(), "test", &events.NormalizedEvent{})
	if !called {
		t.Error("eventHandler should have been called")
	}
}

func TestPuller_BackendNames_Empty(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	names := p.BackendNames()
	if len(names) != 0 {
		t.Errorf("BackendNames() = %v, want empty", names)
	}
}

func TestPuller_Start_NoBackends(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	err := p.Start(context.Background())
	if err == nil {
		t.Error("Start() should fail with no backends")
	}
	if err.Error() != "no backends configured" {
		t.Errorf("Start() error = %v, want 'no backends configured'", err)
	}
}

func TestPuller_Stop_NotStarted(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Should not panic or error when stopping without starting
	err := p.Stop(ctx)
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}

func TestPuller_Subscribe(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx := context.Background()
	ch, err := p.Subscribe(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if ch == nil {
		t.Error("Subscribe() returned nil channel")
	}

	// Verify the event handler was set
	if p.eventHandler == nil {
		t.Error("Subscribe should set event handler")
	}
}

func TestPuller_Subscribe_SendsEvents(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Subscribe(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// Send an event through the handler
	evt := &events.NormalizedEvent{
		EventID:    "test-event-1",
		Collection: "test",
	}

	err = p.eventHandler(ctx, "backend-1", evt)
	if err != nil {
		t.Errorf("eventHandler() error = %v", err)
	}

	// Verify event was received
	select {
	case received := <-ch:
		if received.EventID != evt.EventID {
			t.Errorf("received event ID = %v, want %v", received.EventID, evt.EventID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestPuller_Subscribe_NonBlocking(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx := context.Background()
	_, err := p.Subscribe(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// When channel is not full, handler should return nil (event sent)
	evt := &events.NormalizedEvent{EventID: "test"}
	err = p.eventHandler(ctx, "backend-1", evt)
	if err != nil {
		t.Errorf("eventHandler() error = %v, want nil", err)
	}
}

func TestPuller_Subscribe_ChannelFull(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx := context.Background()
	ch, _ := p.Subscribe(ctx, "consumer-1", "")

	// Fill the channel (buffer size is 1000)
	for i := 0; i < 1000; i++ {
		evt := &events.NormalizedEvent{EventID: "test"}
		_ = p.eventHandler(ctx, "backend", evt)
	}

	// The channel should be full now, sending more should not block
	evt := &events.NormalizedEvent{EventID: "overflow"}
	err := p.eventHandler(ctx, "backend", evt)
	// Should return nil (drops the event silently)
	if err != nil {
		t.Errorf("eventHandler() error = %v, want nil when channel full", err)
	}

	// Drain channel to avoid leaks
	for len(ch) > 0 {
		<-ch
	}
}

func TestBuildWatchPipeline_NoFilter(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	cfg := config.PullerBackendConfig{}
	pipeline := p.buildWatchPipeline(cfg)

	if pipeline != nil {
		t.Error("buildWatchPipeline() should return nil for no filter")
	}
}

func TestBuildWatchPipeline_IncludeCollections(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	cfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users", "orders"},
	}
	pipeline := p.buildWatchPipeline(cfg)

	if pipeline == nil {
		t.Fatal("buildWatchPipeline() returned nil for include filter")
	}
	if len(pipeline) != 1 {
		t.Errorf("pipeline length = %d, want 1", len(pipeline))
	}
}

func TestBuildWatchPipeline_ExcludeCollections(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	cfg := config.PullerBackendConfig{
		ExcludeCollections: []string{"logs", "temp"},
	}
	pipeline := p.buildWatchPipeline(cfg)

	if pipeline == nil {
		t.Fatal("buildWatchPipeline() returned nil for exclude filter")
	}
	if len(pipeline) != 1 {
		t.Errorf("pipeline length = %d, want 1", len(pipeline))
	}
}

func TestBuildWatchPipeline_IncludeTakesPrecedence(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	// If both include and exclude are set, include takes precedence
	cfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users"},
		ExcludeCollections: []string{"logs"},
	}
	pipeline := p.buildWatchPipeline(cfg)

	if pipeline == nil {
		t.Fatal("buildWatchPipeline() returned nil")
	}
	// The pipeline should match include (not exclude) since include is checked first
}

func TestPuller_AddBackend(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)

	backendCfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users"},
	}

	// Test AddBackend
	err := p.AddBackend("backend1", env.Client, env.DBName, backendCfg)
	if err != nil {
		t.Fatalf("AddBackend failed: %v", err)
	}

	if len(p.backends) != 1 {
		t.Errorf("Expected 1 backend, got %d", len(p.backends))
	}

	// Test duplicate backend
	err = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)
	if err == nil {
		t.Error("Expected error for duplicate backend")
	}
}

func TestPuller_StartStop(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)

	backendCfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users"},
	}

	err := p.AddBackend("backend1", env.Client, env.DBName, backendCfg)
	if err != nil {
		t.Fatalf("AddBackend failed: %v", err)
	}

	// Start
	ctx := context.Background()
	err = p.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait a bit for goroutines to start
	time.Sleep(100 * time.Millisecond)

	// Stop
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = p.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}
