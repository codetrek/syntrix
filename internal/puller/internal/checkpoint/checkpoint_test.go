package checkpoint

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestDefaultPolicy(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	if policy.Interval != time.Second {
		t.Errorf("Interval = %v, want 1s", policy.Interval)
	}
	if policy.EventCount != 1000 {
		t.Errorf("EventCount = %d, want 1000", policy.EventCount)
	}
	if !policy.OnShutdown {
		t.Error("OnShutdown should be true")
	}
}

func TestTracker_RecordEvent_EventCount(t *testing.T) {
	t.Parallel()
	policy := Policy{
		Interval:   time.Hour, // Long interval so we only trigger on count
		EventCount: 5,
	}
	tracker := NewTracker(policy)

	token := bson.Raw{1, 2, 3}

	// First 4 events should not trigger checkpoint
	for i := 0; i < 4; i++ {
		if tracker.RecordEvent(token) {
			t.Errorf("RecordEvent returned true on event %d, want false", i+1)
		}
	}

	// 5th event should trigger checkpoint
	if !tracker.RecordEvent(token) {
		t.Error("RecordEvent returned false on event 5, want true")
	}
}

func TestTracker_RecordEvent_TimeInterval(t *testing.T) {
	t.Parallel()
	policy := Policy{
		Interval:   10 * time.Millisecond,
		EventCount: 1000, // High count so we only trigger on time
	}
	tracker := NewTracker(policy)

	token := bson.Raw{1, 2, 3}

	// First event should not trigger (just started)
	if tracker.RecordEvent(token) {
		t.Error("RecordEvent returned true on first event, want false")
	}

	// Wait for interval
	time.Sleep(15 * time.Millisecond)

	// Next event should trigger checkpoint
	if !tracker.RecordEvent(token) {
		t.Error("RecordEvent returned false after interval, want true")
	}
}

func TestTracker_MarkCheckpointed(t *testing.T) {
	t.Parallel()
	policy := Policy{
		Interval:   10 * time.Millisecond,
		EventCount: 3,
	}
	tracker := NewTracker(policy)

	token := bson.Raw{1, 2, 3}

	// Record 3 events to trigger checkpoint
	tracker.RecordEvent(token)
	tracker.RecordEvent(token)
	triggered := tracker.RecordEvent(token)
	if !triggered {
		t.Fatal("Expected checkpoint trigger")
	}

	// Mark checkpointed
	tracker.MarkCheckpointed()

	// Next event should not trigger (count reset)
	if tracker.RecordEvent(token) {
		t.Error("RecordEvent returned true after MarkCheckpointed, want false")
	}
}

func TestTracker_LastToken(t *testing.T) {
	t.Parallel()
	tracker := NewTracker(DefaultPolicy())

	// Initially nil
	if tracker.LastToken() != nil {
		t.Error("LastToken should be nil initially")
	}

	// After recording
	token := bson.Raw{1, 2, 3}
	tracker.RecordEvent(token)

	if tracker.LastToken() == nil {
		t.Error("LastToken should not be nil after recording")
	}
}

func TestTracker_ShouldCheckpointOnShutdown(t *testing.T) {
	t.Parallel()
	policy := Policy{
		OnShutdown: true,
	}
	tracker := NewTracker(policy)

	// No token recorded
	if tracker.ShouldCheckpointOnShutdown() {
		t.Error("ShouldCheckpointOnShutdown should be false with no token")
	}

	// After recording token
	tracker.RecordEvent(bson.Raw{1, 2, 3})
	if !tracker.ShouldCheckpointOnShutdown() {
		t.Error("ShouldCheckpointOnShutdown should be true with token and OnShutdown=true")
	}
}

// mockStore implements Store for testing
type mockStore struct {
	token     bson.Raw
	saveErr   error
	loadErr   error
	deleteErr error
	deleted   bool
}

func (m *mockStore) Save(ctx context.Context, token bson.Raw) error {
	m.token = token
	return m.saveErr
}

func (m *mockStore) Load(ctx context.Context) (bson.Raw, error) {
	return m.token, m.loadErr
}

func (m *mockStore) Delete(ctx context.Context) error {
	m.deleted = true
	return m.deleteErr
}

func TestMockStore(t *testing.T) {
	t.Parallel()
	store := &mockStore{}
	ctx := context.Background()

	// Test Save and Load
	token := bson.Raw{1, 2, 3}
	if err := store.Save(ctx, token); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != len(token) {
		t.Errorf("Loaded token length = %d, want %d", len(loaded), len(token))
	}

	// Test Delete
	if err := store.Delete(ctx); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !store.deleted {
		t.Error("deleted flag should be true")
	}
}

func TestMongoStore(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	backendID := "test-backend"

	store := NewMongoStore(env.DB, backendID)

	// 1. Test Load empty
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded != nil {
		t.Error("Expected nil token for empty store")
	}

	// 2. Test Save
	token := bson.Raw{1, 2, 3, 4}
	err = store.Save(ctx, token)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 3. Test Load existing
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("Expected non-nil token")
	}
	if string(loaded) != string(token) {
		t.Errorf("Loaded token mismatch")
	}

	// 4. Test Save nil (should be no-op)
	err = store.Save(ctx, nil)
	if err != nil {
		t.Fatalf("Save nil failed: %v", err)
	}
	// Should still have the old token
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if string(loaded) != string(token) {
		t.Errorf("Token changed after saving nil")
	}

	// 5. Test Delete
	err = store.Delete(ctx)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded != nil {
		t.Error("Expected nil token after delete")
	}

	// 6. Test EnsureIndexes
	err = store.EnsureIndexes(ctx)
	if err != nil {
		t.Fatalf("EnsureIndexes failed: %v", err)
	}
}
