package core

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/puller/internal/cursor"
	"github.com/codetrek/syntrix/internal/puller/internal/recovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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
	handler := func(ctx context.Context, backendName string, event *events.ChangeEvent) error {
		called = true
		return nil
	}

	p.SetEventHandler(handler)

	if p.eventHandler == nil {
		t.Error("eventHandler should be set")
	}

	// Verify handler can be called
	_ = p.eventHandler(context.Background(), "test", &events.ChangeEvent{})
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
	evt := &events.ChangeEvent{
		EventID: "test-event-1",
		MgoColl: "test",
	}

	p.subs.Broadcast(evt)

	// Verify event was received
	select {
	case received := <-ch:
		if received.Change.EventID != evt.EventID {
			t.Errorf("received event ID = %v, want %v", received.Change.EventID, evt.EventID)
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
	evt := &events.ChangeEvent{EventID: "test"}
	p.subs.Broadcast(evt)
}

func TestPuller_Subscribe_ChannelFull(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx := context.Background()
	ch, _ := p.Subscribe(ctx, "consumer-1", "")

	// Fill the channel (buffer size is 1000)
	for i := 0; i < 1000; i++ {
		evt := &events.ChangeEvent{EventID: "test"}
		p.subs.Broadcast(evt)
	}

	// The channel should be full now, sending more should not block
	evt := &events.ChangeEvent{EventID: "overflow"}
	p.subs.Broadcast(evt)

	// Drain channel to avoid leaks
	for len(ch) > 0 {
		<-ch
	}
}

func TestPuller_SetDelayOverrides(t *testing.T) {
	p := New(config.PullerConfig{}, nil)

	p.SetRetryDelay(5 * time.Second)
	p.SetBackpressureSlowDownDelay(7 * time.Millisecond)
	p.SetBackpressurePauseDelay(9 * time.Millisecond)

	if p.retryDelay != 5*time.Second {
		t.Fatalf("retryDelay = %v, want %v", p.retryDelay, 5*time.Second)
	}
	if p.backpressureSlowDownDelay != 7*time.Millisecond {
		t.Fatalf("backpressureSlowDownDelay = %v, want %v", p.backpressureSlowDownDelay, 7*time.Millisecond)
	}
	if p.backpressurePauseDelay != 9*time.Millisecond {
		t.Fatalf("backpressurePauseDelay = %v, want %v", p.backpressurePauseDelay, 9*time.Millisecond)
	}
}

func TestPuller_runBackend_RecoveryActions(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Buffer.BatchInterval = 5 * time.Millisecond
	p := New(cfg, nil)

	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	backendCfg := config.PullerBackendConfig{Name: "primary"}
	if err := p.AddBackend("primary", client, "testdb", backendCfg); err != nil {
		t.Fatalf("AddBackend error: %v", err)
	}

	backend := p.backends["primary"]
	backend.recoveryHandler = recovery.NewHandler(recovery.HandlerOptions{Checkpoint: backend.buffer, MaxConsecutiveErrors: 2})
	p.retryDelay = 1 * time.Millisecond

	errs := []error{
		errors.New("resume token was not found"),
		nil,
		errors.New("connection reset by peer"),
		errors.New("unexpected failure"),
	}
	var calls atomic.Int32
	p.watchFunc = func(ctx context.Context, backend *Backend, logger *slog.Logger) error {
		idx := int(calls.Add(1) - 1)
		if idx >= len(errs) {
			return errors.New("stop")
		}
		return errs[idx]
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.wg.Add(1)
	go p.runBackend(ctx, "primary", backend)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runBackend did not return in time")
	}

	if calls.Load() < int32(len(errs)) {
		t.Fatalf("watchFunc called %d times, want at least %d", calls.Load(), len(errs))
	}
}

func TestPuller_watchChangeStream_WithResumeToken(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1"}
	if err := p.AddBackend("backend1", env.Client, env.DBName, backendCfg); err != nil {
		t.Fatalf("AddBackend error: %v", err)
	}
	backend := p.backends["backend1"]

	if err := backend.buffer.SaveCheckpoint(bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}); err != nil {
		t.Fatalf("SaveCheckpoint error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := p.watchChangeStream(ctx, backend, p.logger); err == nil {
		t.Fatal("expected watchChangeStream to return error for invalid resume token")
	}
}

func TestPuller_watchChangeStream_FromBeginning(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	cfg.Bootstrap.Mode = "from_beginning"
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1"}
	if err := p.AddBackend("backend1", env.Client, env.DBName, backendCfg); err != nil {
		t.Fatalf("AddBackend error: %v", err)
	}
	backend := p.backends["backend1"]

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := p.watchChangeStream(ctx, backend, p.logger)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Logf("watchChangeStream returned error: %v", err)
	}
}

func TestPuller_watchChangeStream_ProcessesEvent(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1", Collections: []string{"users"}}
	require.NoError(t, p.AddBackend("backend1", env.Client, env.DBName, backendCfg))
	backend := p.backends["backend1"]

	var handled atomic.Int32
	p.SetEventHandler(func(ctx context.Context, backendName string, evt *events.ChangeEvent) error {
		handled.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.watchChangeStream(ctx, backend, p.logger) }()

	// Wait briefly for stream to open
	time.Sleep(150 * time.Millisecond)

	_, err := backend.db.Collection("users").InsertOne(ctx, bson.M{"u": "v"})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return handled.Load() > 0
	}, 3*time.Second, 20*time.Millisecond, "expected event handler to fire")

	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("watchChangeStream error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchChangeStream did not return")
	}

	require.NoError(t, backend.buffer.Close())
}

func TestPuller_watchChangeStream_BufferErrorAndOpenFail(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1"}
	client, err := mongo.NewClient(options.Client().ApplyURI(testMongoURI))
	require.NoError(t, err)
	require.NoError(t, p.AddBackend("backend1", client, env.DBName, backendCfg))
	backend := p.backends["backend1"]

	// Force LoadCheckpoint to fail
	require.NoError(t, backend.buffer.Close())

	// Do not connect the client so Watch fails fast with disconnected error

	err = p.watchChangeStream(context.Background(), backend, p.logger)
	assert.Error(t, err)
}

func TestPuller_watchChangeStream_StreamErr(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1"}
	require.NoError(t, p.AddBackend("backend1", env.Client, env.DBName, backendCfg))
	backend := p.backends["backend1"]

	fakeStream := &fakeChangeStream{err: errors.New("stream error")}
	p.openStream = func(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		return fakeStream, nil
	}

	err := p.watchChangeStream(context.Background(), backend, p.logger)
	require.Error(t, err)
	assert.True(t, fakeStream.closed.Load(), "expected stream.Close to be called")
}

func TestPuller_watchChangeStream_DecodeError(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{Name: "backend1"}
	require.NoError(t, p.AddBackend("backend1", env.Client, env.DBName, backendCfg))
	backend := p.backends["backend1"]

	scripted := &decodeErrorStream{}
	p.openStream = func(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		return scripted, nil
	}

	err := p.watchChangeStream(context.Background(), backend, p.logger)
	require.NoError(t, err)
	assert.True(t, scripted.closed.Load(), "expected stream.Close to be called")
	assert.Equal(t, int32(1), scripted.nextCalls.Load(), "expected a single Next call")
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

func TestBuildWatchPipeline_Collections(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	cfg := config.PullerBackendConfig{
		Collections: []string{"users", "orders"},
	}
	pipeline := p.buildWatchPipeline(cfg)

	if pipeline == nil {
		t.Fatal("buildWatchPipeline() returned nil for include filter")
	}
	if len(pipeline) != 1 {
		t.Errorf("pipeline length = %d, want 1", len(pipeline))
	}
}

func TestPuller_AddBackend(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{
		Collections: []string{"users"},
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

func TestPuller_AddBackend_InvalidMaxSizeIsGraceful(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	cfg.Buffer.MaxSize = "not-a-size"
	p := New(cfg, nil)

	err := p.AddBackend("backend1", env.Client, env.DBName, config.PullerBackendConfig{Collections: []string{"users"}})
	require.NoError(t, err)
}

func TestPuller_StartStop(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{
		Collections: []string{"users"},
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

func TestPuller_Stop_TimesOutWhenWorkersHang(t *testing.T) {
	p := New(newTestConfig(t), nil)

	// Simulate a worker that never finishes.
	p.wg.Add(1)
	p.cancel = func() {}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := p.Stop(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Release the waitgroup to avoid leaking goroutine from Stop's wait.
	p.wg.Done()
}

func TestPuller_Replay_IteratorErrorClosesExisting(t *testing.T) {
	env := setupTestEnv(t)
	cfg := newTestConfig(t)
	p := New(cfg, nil)

	require.NoError(t, p.AddBackend("backend1", env.Client, env.DBName, config.PullerBackendConfig{Collections: []string{"users"}}))
	require.NoError(t, p.AddBackend("backend2", env.Client, env.DBName, config.PullerBackendConfig{Collections: []string{"users"}}))

	backend2 := p.backends["backend2"]
	require.NoError(t, backend2.buffer.Close())

	_, err := p.Replay(context.Background(), nil, false)
	require.Error(t, err)
}

type fakeChangeStream struct {
	err    error
	closed atomic.Bool
}

func (f *fakeChangeStream) Next(context.Context) bool { return false }

func (f *fakeChangeStream) Decode(any) error { return nil }

func (f *fakeChangeStream) Err() error { return f.err }

func (f *fakeChangeStream) Close(context.Context) error {
	f.closed.Store(true)
	return nil
}

type decodeErrorStream struct {
	nextCalls atomic.Int32
	closed    atomic.Bool
}

func (d *decodeErrorStream) Next(context.Context) bool {
	if d.nextCalls.Load() > 0 {
		return false
	}
	d.nextCalls.Add(1)
	return true
}

func (d *decodeErrorStream) Decode(any) error { return errors.New("decode fail") }

func (d *decodeErrorStream) Err() error { return nil }

func (d *decodeErrorStream) Close(context.Context) error {
	d.closed.Store(true)
	return nil
}

func TestPuller_Subscribe_WithAfter(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	pm := cursor.NewProgressMarker()
	pm.SetPosition("backend1", "pos1")
	after := pm.Encode()

	ctx := context.Background()
	ch, err := p.Subscribe(ctx, "consumer-1", after)
	require.NoError(t, err)
	require.NotNil(t, ch)
}

func TestPuller_Subscribe_WithInvalidAfter(t *testing.T) {
	t.Parallel()
	p := New(config.PullerConfig{}, nil)

	ctx := context.Background()
	// Should not fail, just ignore invalid token
	ch, err := p.Subscribe(ctx, "consumer-1", "invalid-token")
	require.NoError(t, err)
	require.NotNil(t, ch)
}
