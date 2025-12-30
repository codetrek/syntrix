package buffer

import (
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/events"
)

func TestBuffer_NewAndClose(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if buf.Path() != dir {
		t.Errorf("Path() = %s, want %s", buf.Path(), dir)
	}

	if err := buf.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestBuffer_WriteAndRead(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	evt := &events.NormalizedEvent{
		EventID:    "evt-1",
		TenantID:   "tenant-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
		Timestamp: time.Now().UnixMilli(),
		FullDocument: map[string]any{
			"name": "test",
		},
	}

	if err := buf.Write(evt); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	key := evt.BufferKey()
	readEvt, err := buf.Read(key)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if readEvt == nil {
		t.Fatal("Read() returned nil")
	}

	if readEvt.EventID != evt.EventID {
		t.Errorf("EventID = %s, want %s", readEvt.EventID, evt.EventID)
	}
	if readEvt.Collection != evt.Collection {
		t.Errorf("Collection = %s, want %s", readEvt.Collection, evt.Collection)
	}
}

func TestBuffer_ScanFrom(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write multiple events
	for i := 0; i < 5; i++ {
		evt := &events.NormalizedEvent{
			EventID:    "evt-" + string(rune('a'+i)),
			TenantID:   "tenant-1",
			Collection: "testcoll",
			DocumentID: "doc-" + string(rune('a'+i)),
			Type:       events.OperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1234567890 + i),
				I: 1,
			},
			Timestamp: time.Now().UnixMilli(),
		}
		if err := buf.Write(evt); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	// Scan from beginning
	iter, err := buf.ScanFrom("")
	if err != nil {
		t.Fatalf("ScanFrom() error = %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		count++
		if iter.Event() == nil {
			t.Error("Event() returned nil")
		}
	}
	if iter.Err() != nil {
		t.Errorf("Iterator error = %v", iter.Err())
	}

	if count != 5 {
		t.Errorf("Iterated %d events, want 5", count)
	}
}

func TestBuffer_Head(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Empty buffer
	head, err := buf.Head()
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head != "" {
		t.Errorf("Head() = %q, want empty string", head)
	}

	// Write an event
	evt := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	head, err = buf.Head()
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head == "" {
		t.Error("Head() returned empty string, want key")
	}
}

func TestBuffer_Delete(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	evt := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	key := evt.BufferKey()

	// Delete
	if err := buf.Delete(key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Should not exist
	readEvt, err := buf.Read(key)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if readEvt != nil {
		t.Error("Read() returned event after delete, want nil")
	}
}

func TestBuffer_Count(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	count, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 0 {
		t.Errorf("Count() = %d, want 0", count)
	}

	// Write 3 events
	for i := 0; i < 3; i++ {
		evt := &events.NormalizedEvent{
			EventID:    "evt-" + string(rune('a'+i)),
			Collection: "testcoll",
			DocumentID: "doc-" + string(rune('a'+i)),
			Type:       events.OperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1234567890 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	count, err = buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 3 {
		t.Errorf("Count() = %d, want 3", count)
	}
}

func TestBuffer_RequiresPath(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	if err == nil {
		t.Error("New() should fail without path")
	}
}

func TestNewForBackend(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := NewForBackend(dir, "backend-1", nil)
	if err != nil {
		t.Fatalf("NewForBackend() error = %v", err)
	}
	defer buf.Close()

	expectedPath := dir + "/backend-1"
	if buf.Path() != expectedPath {
		t.Errorf("Path() = %s, want %s", buf.Path(), expectedPath)
	}
}

func TestBuffer_Write_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	evt := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	err = buf.Write(evt)
	if err == nil {
		t.Error("Write() should fail on closed buffer")
	}
}

func TestBuffer_Read_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.Read("some-key")
	if err == nil {
		t.Error("Read() should fail on closed buffer")
	}
}

func TestBuffer_ScanFrom_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.ScanFrom("")
	if err == nil {
		t.Error("ScanFrom() should fail on closed buffer")
	}
}

func TestBuffer_Head_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.Head()
	if err == nil {
		t.Error("Head() should fail on closed buffer")
	}
}

func TestBuffer_Delete_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	err = buf.Delete("some-key")
	if err == nil {
		t.Error("Delete() should fail on closed buffer")
	}
}

func TestBuffer_DeleteBefore(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write 5 events with increasing timestamps
	var keys []string
	for i := 0; i < 5; i++ {
		evt := &events.NormalizedEvent{
			EventID:    "evt-" + string(rune('a'+i)),
			Collection: "testcoll",
			DocumentID: "doc-" + string(rune('a'+i)),
			Type:       events.OperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1000 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		keys = append(keys, evt.BufferKey())
	}

	// Delete before the 3rd key (index 2)
	deleted, err := buf.DeleteBefore(keys[2])
	if err != nil {
		t.Fatalf("DeleteBefore() error = %v", err)
	}
	if deleted != 2 {
		t.Errorf("DeleteBefore() deleted %d, want 2", deleted)
	}

	// Count should now be 3
	count, err := buf.Count()
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 3 {
		t.Errorf("Count() = %d, want 3", count)
	}
}

func TestBuffer_DeleteBefore_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.DeleteBefore("some-key")
	if err == nil {
		t.Error("DeleteBefore() should fail on closed buffer")
	}
}

func TestBuffer_Count_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.Count()
	if err == nil {
		t.Error("Count() should fail on closed buffer")
	}
}

func TestBuffer_CountAfter(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	// Write 5 events
	var keys []string
	for i := 0; i < 5; i++ {
		evt := &events.NormalizedEvent{
			EventID:    "evt-" + string(rune('a'+i)),
			Collection: "testcoll",
			DocumentID: "doc-" + string(rune('a'+i)),
			Type:       events.OperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1000 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		keys = append(keys, evt.BufferKey())
	}

	// Count after empty key should return all
	count, err := buf.CountAfter("")
	if err != nil {
		t.Fatalf("CountAfter() error = %v", err)
	}
	if count != 5 {
		t.Errorf("CountAfter('') = %d, want 5", count)
	}

	// Count after 2nd key should return 3
	count, err = buf.CountAfter(keys[1])
	if err != nil {
		t.Fatalf("CountAfter() error = %v", err)
	}
	if count != 3 {
		t.Errorf("CountAfter(key[1]) = %d, want 3", count)
	}
}

func TestBuffer_CountAfter_Closed(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	buf.Close()

	_, err = buf.CountAfter("some-key")
	if err == nil {
		t.Error("CountAfter() should fail on closed buffer")
	}
}

func TestBuffer_Close_Idempotent(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Should not panic or error on multiple closes
	if err := buf.Close(); err != nil {
		t.Errorf("First Close() error = %v", err)
	}
	if err := buf.Close(); err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

func TestIterator_Key(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer buf.Close()

	evt := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	iter, err := buf.ScanFrom("")
	if err != nil {
		t.Fatalf("ScanFrom() error = %v", err)
	}
	defer iter.Close()

	if !iter.Next() {
		t.Fatal("Expected at least one item")
	}

	key := iter.Key()
	if key == "" {
		t.Error("Key() returned empty string")
	}
	if key != evt.BufferKey() {
		t.Errorf("Key() = %s, want %s", key, evt.BufferKey())
	}
}
