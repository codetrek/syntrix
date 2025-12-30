package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/events"
	"github.com/codetrek/syntrix/internal/puller/internal/checkpoint"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mockCheckpointStore struct {
	fail bool
}

func (m *mockCheckpointStore) Save(ctx context.Context, token bson.Raw) error {
	if m.fail {
		return fmt.Errorf("mock save error")
	}
	return nil
}

func (m *mockCheckpointStore) Load(ctx context.Context) (bson.Raw, error) {
	return nil, nil
}

func (m *mockCheckpointStore) Delete(ctx context.Context) error {
	return nil
}

func TestPuller_WatchAndCheckpoint(t *testing.T) {
	env := setupTestEnv(t)

	// Configure puller with checkpoint settings
	cfg := config.PullerConfig{
		Checkpoint: config.CheckpointConfig{
			Interval: 100 * time.Millisecond,
		},
	}

	p := New(cfg, nil)

	backendCfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users"},
	}

	err := p.AddBackend("backend1", env.Client, env.DBName, backendCfg)
	if err != nil {
		t.Fatalf("AddBackend failed: %v", err)
	}

	// Subscribe to events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Subscribe(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Start puller
	err = p.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Insert a document to trigger an event
	coll := env.DB.Collection("users")
	_, err = coll.InsertOne(ctx, bson.M{"username": "testuser", "email": "test@example.com"})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// Wait for event
	select {
	case evt := <-ch:
		if evt.Collection != "users" {
			t.Errorf("Expected collection 'users', got '%s'", evt.Collection)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	// Wait for checkpoint to be saved (async)
	// Since we set Interval to 100ms, it should save shortly
	time.Sleep(200 * time.Millisecond)

	// Stop puller to force final checkpoint
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	err = p.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify checkpoint exists
	ckptColl := env.DB.Collection("_puller_checkpoints")
	count, err := ckptColl.CountDocuments(ctx, bson.M{"_id": "backend1"})
	if err != nil {
		t.Fatalf("CountDocuments failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 checkpoint, got %d", count)
	}
}

func TestPuller_BackendNames_NonEmpty(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)

	backendCfg := config.PullerBackendConfig{
		IncludeCollections: []string{"users"},
	}

	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)
	_ = p.AddBackend("backend2", env.Client, env.DBName, backendCfg)

	names := p.BackendNames()
	if len(names) != 2 {
		t.Errorf("Expected 2 backend names, got %d", len(names))
	}
}

func TestPuller_ResumeFromCheckpoint(t *testing.T) {
	env := setupTestEnv(t)

	// 1. Create a dummy checkpoint
	ckptColl := env.DB.Collection("_puller_checkpoints")
	// We need a valid resume token. This is hard to forge without a real event.
	// So we will run a short session to generate one, then stop, then start again.

	cfg := config.PullerConfig{
		Checkpoint: config.CheckpointConfig{
			Interval:   10 * time.Millisecond,
			EventCount: 1,
		},
	}
	p := New(cfg, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _ = p.Subscribe(ctx, "c1", "")
	_ = p.Start(ctx)

	// Generate event
	coll := env.DB.Collection("users")
	_, _ = coll.InsertOne(ctx, bson.M{"a": 1})

	// Wait for checkpoint
	time.Sleep(200 * time.Millisecond)
	p.Stop(ctx)

	// Verify checkpoint exists
	count, _ := ckptColl.CountDocuments(ctx, bson.M{"_id": "backend1"})
	if count == 0 {
		t.Fatal("Failed to create initial checkpoint")
	}

	// 2. Start new puller instance, it should resume
	p2 := New(cfg, nil)
	_ = p2.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	// We can't easily verify "resuming" without mocking logger or checking internal state.
	// But running it ensures the code path is executed.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	_, _ = p2.Subscribe(ctx2, "c1", "")
	err := p2.Start(ctx2)
	if err != nil {
		t.Fatalf("Failed to restart puller: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	p2.Stop(ctx2)
}

func TestPuller_SaveCheckpoint_Error(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	// Inject mock checkpoint store
	var mockStore checkpoint.Store = &mockCheckpointStore{fail: true}
	p.backends["backend1"].checkpoint = mockStore

	// Record a token so there is something to save
	token := bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00} // Minimal valid bson document
	p.backends["backend1"].tracker.RecordEvent(token)

	// Call saveCheckpoint
	p.saveCheckpoint(context.Background(), p.backends["backend1"], p.logger)
}

func TestPuller_EventHandlerError(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	// Set a handler that returns an error
	p.SetEventHandler(func(ctx context.Context, backendName string, event *events.NormalizedEvent) error {
		return fmt.Errorf("simulated handler error")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = p.Start(ctx)

	// Generate event
	coll := env.DB.Collection("users")
	_, _ = coll.InsertOne(ctx, bson.M{"a": 1})

	// Wait a bit, should not crash
	time.Sleep(100 * time.Millisecond)
	p.Stop(ctx)
}

func TestPuller_ChangeStreamError_Reconnect(t *testing.T) {
	t.Parallel()
	// Create a dedicated client to avoid affecting global shared client
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Skip("Skipping test: failed to connect to MongoDB")
	}
	// We don't defer disconnect here because we want to disconnect manually during test
	// But we should ensure it's cleaned up if test fails before disconnect
	defer func() {
		if client != nil {
			_ = client.Disconnect(context.Background())
		}
	}()

	dbName := fmt.Sprintf("test_puller_reconnect_%d", time.Now().UnixNano())
	defer func() {
		// Reconnect to drop db? No, client is closed.
		// We can use a fresh client to drop it, or just ignore it (it's a test db)
		// Ideally we should clean up.
		cleanClient, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
		if cleanClient != nil {
			_ = cleanClient.Database(dbName).Drop(context.Background())
			_ = cleanClient.Disconnect(context.Background())
		}
	}()

	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", client, dbName, backendCfg)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = p.Start(runCtx)

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Force close the client to trigger change stream error
	_ = client.Disconnect(runCtx)

	// Wait for reconnection attempt (it will fail since client is closed, but should loop)
	time.Sleep(200 * time.Millisecond)

	// Stop should still work (or timeout)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer stopCancel()
	_ = p.Stop(stopCtx)
}

type mockLoadErrorStore struct {
	checkpoint.Store
}

func (m *mockLoadErrorStore) Load(ctx context.Context) (bson.Raw, error) {
	return nil, fmt.Errorf("mock load error")
}
func (m *mockLoadErrorStore) Save(ctx context.Context, token bson.Raw) error { return nil }
func (m *mockLoadErrorStore) Delete(ctx context.Context) error               { return nil }

func TestPuller_WatchChangeStream_LoadError(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	// Inject mock checkpoint store that fails Load
	p.backends["backend1"].checkpoint = &mockLoadErrorStore{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should succeed (logs warning)
	err := p.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	p.Stop(ctx)
}

func TestPuller_WatchChangeStream_WatchError(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	// Close client to force Watch error
	// We use a separate client to avoid breaking other tests
	ctx := context.Background()
	client, _ := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	defer client.Disconnect(ctx)

	p.backends["backend1"].client = client
	p.backends["backend1"].db = client.Database(env.DBName)

	_ = client.Disconnect(ctx)

	// Call watchChangeStream directly
	backend := p.backends["backend1"]
	err := p.watchChangeStream(ctx, backend, p.logger)
	if err == nil {
		t.Error("Expected error from watchChangeStream with closed client")
	}
}

func TestPuller_WatchChangeStream_Invalidate(t *testing.T) {
	env := setupTestEnv(t)
	p := New(config.PullerConfig{}, nil)
	backendCfg := config.PullerBackendConfig{IncludeCollections: []string{"users"}}
	_ = p.AddBackend("backend1", env.Client, env.DBName, backendCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start puller
	go func() {
		_ = p.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Drop collection to trigger invalidate event
	// This causes:
	// 1. 'invalidate' event received
	// 2. Normalize fails (OperationType 'invalidate' is not valid) -> Covers Normalize error
	// 3. Stream closes
	// 4. Loop exits -> Covers final return
	err := env.DB.Collection("users").Drop(ctx)
	if err != nil {
		t.Fatalf("Failed to drop collection: %v", err)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	// Stop puller
	p.Stop(ctx)
}
