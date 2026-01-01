package buffer

import (
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

var testToken = bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}

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

func TestBuffer_Close_MultipleTimes(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir()})
	require.NoError(t, err)

	assert.NoError(t, buf.Close())
	assert.NoError(t, buf.Close())
}

func TestBuffer_Close_DBError(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir()})
	require.NoError(t, err)

	// Close the underlying DB early to force the error branch in Buffer.Close.
	require.NoError(t, buf.db.Close())

	err = buf.Close()
	assert.Error(t, err)
}

func TestBuffer_Close_PanicHandled(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir()})
	require.NoError(t, err)

	// Corrupt the db pointer to trigger panic inside Close and ensure we recover.
	buf.db = nil

	err = buf.Close()
	assert.Error(t, err)
}

func TestBufferIterator_Close_NilIter(t *testing.T) {
	t.Parallel()
	it := &bufferIterator{}

	assert.NoError(t, it.Close())
}

func TestBuffer_WriteAndRead(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-*")
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

	if err := buf.Write(evt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
		if err := buf.Write(evt, testToken); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
	if err := buf.Write(evt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
	if err := buf.Write(evt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
		if err := buf.Write(evt, testToken); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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
	err = buf.Write(evt, testToken)
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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
		if err := buf.Write(evt, testToken); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		keys = append(keys, evt.BufferKey())
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
		if err := buf.Write(evt, testToken); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		keys = append(keys, evt.BufferKey())
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
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
	if err := buf.Write(evt, testToken); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

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

func TestBuffer_Write(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
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
	token := bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}

	err = buf.Write(evt, token)
	require.NoError(t, err)

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

	readEvt, err := buf.Read(evt.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, readEvt)

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, token, ckpt)

	count, err := buf.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	head, err := buf.Head()
	require.NoError(t, err)
	assert.Equal(t, evt.BufferKey(), head)

	iter, err := buf.ScanFrom("")
	require.NoError(t, err)
	defer iter.Close()

	iterCount := 0
	for iter.Next() {
		iterCount++
	}
	require.NoError(t, iter.Err())
	assert.Equal(t, 1, iterCount)
}

func TestBuffer_Write_NilToken_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
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

	err = buf.Write(evt, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint token is required")
}

func TestBuffer_SaveCheckpoint_NoEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	token := bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}
	require.NoError(t, buf.SaveCheckpoint(token))

	head, err := buf.Head()
	require.NoError(t, err)
	assert.Equal(t, "", head)

	count, err := buf.Count()
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, token, ckpt)
}

func TestBuffer_SaveCheckpoint_Closed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	require.NoError(t, buf.Close())

	err = buf.SaveCheckpoint(bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer is closed")
}

func TestBuffer_SaveCheckpoint_NilToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	require.NoError(t, buf.SaveCheckpoint(nil))

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Nil(t, ckpt)
}

func TestBuffer_LoadCheckpoint_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Nil(t, ckpt)
}

func TestBuffer_LoadCheckpoint_Closed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	require.NoError(t, buf.Close())

	_, err = buf.LoadCheckpoint()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer is closed")
}

func TestBuffer_Write_BatchesBySize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{
		Path:          dir,
		BatchSize:     2,
		BatchInterval: time.Second,
		QueueSize:     10,
	})
	require.NoError(t, err)
	defer buf.Close()

	evt1 := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	evt2 := &events.NormalizedEvent{
		EventID:    "evt-2",
		Collection: "testcoll",
		DocumentID: "doc-2",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567891,
			I: 1,
		},
	}

	require.NoError(t, buf.Write(evt1, testToken))

	// Should not be in DB yet (batch size 2)
	read1, err := buf.Read(evt1.BufferKey())
	require.NoError(t, err)
	require.Nil(t, read1)

	require.NoError(t, buf.Write(evt2, testToken))

	// Wait for flush
	time.Sleep(50 * time.Millisecond)

	read1, err = buf.Read(evt1.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, read1)

	read2, err := buf.Read(evt2.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, read2)
}

func TestBuffer_Write_FlushesOnInterval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{
		Path:          dir,
		BatchSize:     10,
		BatchInterval: 20 * time.Millisecond,
		QueueSize:     10,
	})
	require.NoError(t, err)
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

	require.NoError(t, buf.Write(evt, testToken))

	// Should not be in DB yet
	readEvt, err := buf.Read(evt.BufferKey())
	require.NoError(t, err)
	require.Nil(t, readEvt)

	// Wait for interval
	time.Sleep(50 * time.Millisecond)

	readEvt, err = buf.Read(evt.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, readEvt)
}

func TestBuffer_DeleteBefore_SkipsCheckpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	defer buf.Close()

	token := bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}
	require.NoError(t, buf.SaveCheckpoint(token))

	evt1 := &events.NormalizedEvent{
		EventID:    "evt-1",
		Collection: "testcoll",
		DocumentID: "doc-1",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1000,
			I: 1,
		},
	}
	evt2 := &events.NormalizedEvent{
		EventID:    "evt-2",
		Collection: "testcoll",
		DocumentID: "doc-2",
		Type:       events.OperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1001,
			I: 1,
		},
	}
	require.NoError(t, buf.Write(evt1, testToken))
	require.NoError(t, buf.Write(evt2, testToken))

	// Wait for batch flush
	time.Sleep(20 * time.Millisecond)

	deleted, err := buf.DeleteBefore(evt2.BufferKey())
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, token, ckpt)
}

func TestBuffer_First(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	defer buf.Close()

	// Empty buffer
	key, err := buf.First()
	require.NoError(t, err)
	assert.Empty(t, key)

	// Write events
	evt1 := &events.NormalizedEvent{
		EventID:     "1",
		ClusterTime: events.ClusterTime{T: 1, I: 1},
	}
	evt2 := &events.NormalizedEvent{
		EventID:     "2",
		ClusterTime: events.ClusterTime{T: 2, I: 2},
	}

	require.NoError(t, buf.Write(evt1, testToken))
	require.NoError(t, buf.Write(evt2, testToken))

	// Wait for flush
	require.Eventually(t, func() bool {
		k, _ := buf.First()
		return k != ""
	}, 1*time.Second, 10*time.Millisecond)

	key, err = buf.First()
	require.NoError(t, err)
	assert.Equal(t, evt1.BufferKey(), key)
}

func TestBuffer_Size(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	defer buf.Close()

	initialSize, err := buf.Size()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, initialSize, int64(0))

	// Write event
	evt := &events.NormalizedEvent{
		EventID:     "1",
		ClusterTime: events.ClusterTime{T: 1, I: 1},
		FullDocument: map[string]any{
			"data": make([]byte, 1024*10), // 10KB data
		},
	}
	require.NoError(t, buf.Write(evt, testToken))

	// Wait for flush
	require.Eventually(t, func() bool {
		s, _ := buf.Size()
		return s > initialSize
	}, 1*time.Second, 10*time.Millisecond)

	size, err := buf.Size()
	require.NoError(t, err)
	assert.Greater(t, size, initialSize)
}

func TestBuffer_DeleteCheckpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	defer buf.Close()

	// Save checkpoint
	err = buf.SaveCheckpoint(testToken)
	require.NoError(t, err)

	// Verify exists
	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, testToken, ckpt)

	// Delete
	err = buf.DeleteCheckpoint()
	require.NoError(t, err)

	// Verify gone
	ckpt, err = buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Nil(t, ckpt)
}

func TestBuffer_ClosedScenarios(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "buffer-test-closed-*")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)

	// Close the buffer immediately
	err = buf.Close()
	require.NoError(t, err)

	// Test all methods that should fail when closed
	t.Run("Write", func(t *testing.T) {
		err := buf.Write(&events.NormalizedEvent{}, testToken)
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("Read", func(t *testing.T) {
		_, err := buf.Read("some-key")
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("ScanFrom", func(t *testing.T) {
		_, err := buf.ScanFrom("")
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("Head", func(t *testing.T) {
		_, err := buf.Head()
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("First", func(t *testing.T) {
		_, err := buf.First()
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("Size", func(t *testing.T) {
		_, err := buf.Size()
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("Delete", func(t *testing.T) {
		err := buf.Delete("some-key")
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("DeleteBefore", func(t *testing.T) {
		_, err := buf.DeleteBefore("some-key")
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("Count", func(t *testing.T) {
		_, err := buf.Count()
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("CountAfter", func(t *testing.T) {
		_, err := buf.CountAfter("some-key")
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("LoadCheckpoint", func(t *testing.T) {
		_, err := buf.LoadCheckpoint()
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("SaveCheckpoint", func(t *testing.T) {
		err := buf.SaveCheckpoint(testToken)
		assert.ErrorContains(t, err, "buffer is closed")
	})

	t.Run("DeleteCheckpoint", func(t *testing.T) {
		err := buf.DeleteCheckpoint()
		assert.ErrorContains(t, err, "buffer is closed")
	})
}
