package buffer

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/stretchr/testify/assert"
)

func TestNewCleaner(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Hour,
		Interval:  time.Minute,
		Logger:    nil,
	})

	if cleaner == nil {
		t.Fatal("NewCleaner() returned nil")
	}
	if cleaner.buffer != buf {
		t.Error("buffer should be set")
	}
	if cleaner.retention != time.Hour {
		t.Errorf("retention = %v, want 1h", cleaner.retention)
	}
	if cleaner.interval != time.Minute {
		t.Errorf("interval = %v, want 1m", cleaner.interval)
	}
}

func TestCleaner_StartAndStop(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Hour,
		Interval:  100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleaner.Start(ctx)

	// Let it run briefly
	time.Sleep(150 * time.Millisecond)

	// Stop should not block
	done := make(chan struct{})
	go func() {
		cleaner.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		t.Error("Stop() took too long")
	}
}

func TestCleaner_CleanupNow(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write an old event (with very old timestamp)
	oldEvt := &events.ChangeEvent{
		EventID:  "old-evt",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1, // Very old timestamp
			I: 1,
		},
	}
	if err := buf.Write(oldEvt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Write a recent event
	recentEvt := &events.ChangeEvent{
		EventID:  "recent-evt",
		MgoColl:  "testcoll",
		MgoDocID: "doc-2",
		OpType:   events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: uint32(time.Now().Unix()),
			I: 1,
		},
	}
	if err := buf.Write(recentEvt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

	// Create cleaner with very short retention
	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Second, // 1 second retention
		Interval:  time.Hour,
	})

	// Run cleanup
	ctx := context.Background()
	err = cleaner.CleanupNow(ctx)
	if err != nil {
		t.Fatalf("CleanupNow() error = %v", err)
	}

	// Old event should be deleted, recent should remain
	count, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 1 {
		t.Errorf("Count() = %d, want 1", count)
	}
}

func TestCleaner_ContextCancellation(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Hour,
		Interval:  time.Hour, // Long interval so it doesn't trigger
	})

	ctx, cancel := context.WithCancel(context.Background())
	cleaner.Start(ctx)

	// Cancel context - cleaner should stop
	cancel()

	// Wait a bit and then stop to ensure no deadlock
	time.Sleep(150 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		cleaner.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		t.Error("Stop() took too long after context cancellation")
	}
}

func TestCleaner_RunTriggersCleanup(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write an old event
	oldEvt := &events.ChangeEvent{
		EventID:  "old-evt",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1, // Very old timestamp
			I: 1,
		},
	}
	if err := buf.Write(oldEvt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Create cleaner with very short interval to trigger cleanup via ticker
	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Millisecond, // Very short retention
		Interval:  50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleaner.Start(ctx)

	// Wait for at least one cleanup cycle
	time.Sleep(200 * time.Millisecond)

	cleaner.Stop()

	// Old event should be deleted by the ticker-triggered cleanup
	count, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 0 {
		t.Errorf("Count() = %d, want 0 (cleanup should have run)", count)
	}
}

func TestCleaner_CleanupError(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Millisecond,
		Interval:  150 * time.Millisecond,
	})

	// Close buffer before cleanup runs - this will cause cleanup to fail
	buf.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleaner.Start(ctx)

	// Wait for cleanup to be attempted (and fail)
	time.Sleep(100 * time.Millisecond)

	// Stop should still work even after cleanup errors
	done := make(chan struct{})
	go func() {
		cleaner.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Expected - should not hang
	case <-time.After(time.Second):
		t.Error("Stop() took too long after cleanup error")
	}
}

func TestCleaner_MaxSize(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "cleaner-test-maxsize-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write 10 events
	for i := 0; i < 10; i++ {
		evt := &events.ChangeEvent{
			EventID:  "evt-" + string(rune('a'+i)),
			MgoColl:  "testcoll",
			MgoDocID: "doc-1",
			OpType:   events.OperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(time.Now().Unix()) + uint32(i),
				I: 1,
			},
			// Add some payload to increase size
			FullDocument: &storage.Document{
				Id:       "doc-1",
				TenantID: "tenant-1",
				Data:     map[string]any{"data": "some payload"},
			},
		}
		if err := buf.Write(evt, testToken); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	// Wait for flush
	time.Sleep(50 * time.Millisecond)

	// Wait for size to be > 0
	assert.Eventually(t, func() bool {
		s, _ := buf.Size()
		return s > 0
	}, 5*time.Second, 100*time.Millisecond, "Buffer size should be > 0")

	initialCount, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if initialCount != 10 {
		t.Fatalf("Initial count = %d, want 10", initialCount)
	}

	// Create cleaner with MaxSize = 1 (force eviction)
	cleaner := NewCleaner(CleanerOptions{
		Buffer:    buf,
		Retention: time.Hour,
		MaxSize:   1, // Force eviction
		Interval:  time.Hour,
	})

	// Run cleanup
	ctx := context.Background()
	err = cleaner.CleanupNow(ctx)
	if err != nil {
		t.Fatalf("CleanupNow() error = %v", err)
	}

	// Should have evicted events
	finalCount, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}

	// Since MaxSize is 1, it should try to evict everything until empty or size < 0 (impossible)
	// But it stops when buffer is empty.
	// However, PebbleDB size might not update immediately or might not go down to 0.
	// But the loop checks .
	// If size stays high (due to overhead), it will keep deleting until empty.

	if finalCount != 0 {
		t.Errorf("Final count = %d, want 0 (evicted all)", finalCount)
	}
}
