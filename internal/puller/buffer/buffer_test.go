package buffer

import (
	"encoding/json"
	"github.com/cockroachdb/pebble"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
)

func testCheckpoint(t *testing.T, position string) checkpoint.Checkpoint {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "opaque", Value: position}})
	require.NoError(t, err)
	cp, err := checkpoint.EncodeMongo(checkpoint.MongoSource{
		ID: "backend-a", Database: "physical",
		Collections: []checkpoint.MongoCollection{{Name: "documents", UUID: "01010101010101010101010101010101"}},
	}, raw)
	require.NoError(t, err)
	return cp
}

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

// TestBuffer_Close_MultipleTimes REMOVED - duplicate of TestBuffer_Close_Idempotent

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

	// Save real DB to close it later manually
	// This is necessary because setting buf.db to nil makes us lose the reference,
	// keeping the DB file open and causing t.TempDir cleanup to fail on Windows.
	realDB := buf.db
	defer realDB.Close()

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

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		Database: "database-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
		Timestamp: time.Now().UnixMilli(),
		FullDocument: &storage.StoredDoc{
			Id:       "doc-1",
			Database: "database-1",
		},
	}

	if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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
	if readEvt.MgoColl != evt.MgoColl {
		t.Errorf("Collection = %s, want %s", readEvt.MgoColl, evt.MgoColl)
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
		evt := &events.StoreChangeEvent{
			EventID:  "evt-" + string(rune('a'+i)),
			Database: "database-1",
			MgoColl:  "testcoll",
			MgoDocID: "doc-" + string(rune('a'+i)),
			OpType:   events.StoreOperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1234567890 + i),
				I: 1,
			},
			Timestamp: time.Now().UnixMilli(),
		}
		if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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
	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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
		evt := &events.StoreChangeEvent{
			EventID:  "evt-" + string(rune('a'+i)),
			MgoColl:  "testcoll",
			MgoDocID: "doc-" + string(rune('a'+i)),
			OpType:   events.StoreOperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1234567890 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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

	expectedPath := filepath.Join(dir, "backend-1")
	if buf.Path() != expectedPath {
		t.Errorf("Path() = %s, want %s", buf.Path(), expectedPath)
	}
}

// TestBuffer_Write_Closed REMOVED - covered by TestBuffer_ClosedScenarios

// TestBuffer_Read_Closed REMOVED - covered by TestBuffer_ClosedScenarios

// TestBuffer_ScanFrom_Closed REMOVED - covered by TestBuffer_ClosedScenarios

// TestBuffer_Head_Closed REMOVED - covered by TestBuffer_ClosedScenarios

// TestBuffer_Delete_Closed REMOVED - covered by TestBuffer_ClosedScenarios

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
		evt := &events.StoreChangeEvent{
			EventID:  "evt-" + string(rune('a'+i)),
			MgoColl:  "testcoll",
			MgoDocID: "doc-" + string(rune('a'+i)),
			OpType:   events.StoreOperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1000 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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

// TestBuffer_DeleteBefore_Closed REMOVED - covered by TestBuffer_ClosedScenarios

// TestBuffer_Count_Closed REMOVED - covered by TestBuffer_ClosedScenarios

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
		evt := &events.StoreChangeEvent{
			EventID:  "evt-" + string(rune('a'+i)),
			MgoColl:  "testcoll",
			MgoDocID: "doc-" + string(rune('a'+i)),
			OpType:   events.StoreOperationInsert,
			ClusterTime: events.ClusterTime{
				T: uint32(1000 + i),
				I: 1,
			},
		}
		if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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

// TestBuffer_CountAfter_Closed REMOVED - covered by TestBuffer_ClosedScenarios

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

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	if err := buf.Write(evt, testCheckpoint(t, "default")); err != nil {
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

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	token := testCheckpoint(t, "default")

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

func TestBuffer_Write_EmptyCheckpointRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}

	err = buf.Write(evt, "")
	require.Error(t, err)
	assert.Error(t, err)
}

func TestBuffer_SaveCheckpoint_NoEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	token := testCheckpoint(t, "default")
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

	err = buf.SaveCheckpoint(testCheckpoint(t, "default"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer is closed")
}

func TestBuffer_SaveCheckpoint_EmptyRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	require.Error(t, buf.SaveCheckpoint(""))

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Empty(t, ckpt)
}

func TestBuffer_LoadCheckpoint_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Empty(t, ckpt)
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

	evt1 := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}
	evt2 := &events.StoreChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-2",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567891,
			I: 1,
		},
	}

	require.NoError(t, buf.Write(evt1, testCheckpoint(t, "default")))

	// Should not be in DB yet (batch size 2)
	read1, err := buf.Read(evt1.BufferKey())
	require.NoError(t, err)
	require.Nil(t, read1)

	require.NoError(t, buf.Write(evt2, testCheckpoint(t, "default")))

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

	evt := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1234567890,
			I: 1,
		},
	}

	require.NoError(t, buf.Write(evt, testCheckpoint(t, "default")))

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

	token := testCheckpoint(t, "default")
	require.NoError(t, buf.SaveCheckpoint(token))

	evt1 := &events.StoreChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1000,
			I: 1,
		},
	}
	evt2 := &events.StoreChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-2",
		OpType:   events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{
			T: 1001,
			I: 1,
		},
	}
	require.NoError(t, buf.Write(evt1, testCheckpoint(t, "default")))
	require.NoError(t, buf.Write(evt2, testCheckpoint(t, "default")))

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
	evt1 := &events.StoreChangeEvent{
		EventID:     "1",
		ClusterTime: events.ClusterTime{T: 1, I: 1},
	}
	evt2 := &events.StoreChangeEvent{
		EventID:     "2",
		ClusterTime: events.ClusterTime{T: 2, I: 2},
	}

	require.NoError(t, buf.Write(evt1, testCheckpoint(t, "default")))
	require.NoError(t, buf.Write(evt2, testCheckpoint(t, "default")))

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
	evt := &events.StoreChangeEvent{
		EventID:     "1",
		ClusterTime: events.ClusterTime{T: 1, I: 1},
		FullDocument: &storage.StoredDoc{
			Id: string(make([]byte, 1024*10)),
		},
	}
	require.NoError(t, buf.Write(evt, testCheckpoint(t, "default")))

	// Wait for flush
	require.Eventually(t, func() bool {
		s, _ := buf.Size()
		return s > initialSize
	}, 1*time.Second, 10*time.Millisecond)

	size, err := buf.Size()
	require.NoError(t, err)
	assert.Greater(t, size, initialSize)
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
		err := buf.Write(&events.StoreChangeEvent{}, testCheckpoint(t, "default"))
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
		err := buf.SaveCheckpoint(testCheckpoint(t, "default"))
		assert.ErrorContains(t, err, "buffer is closed")
	})

}

func TestBuffer_RecordCheckpointsSurviveReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buf, err := New(Options{Path: dir, BatchInterval: time.Hour})
	require.NoError(t, err)
	cp1, cp2, cp3 := testCheckpoint(t, "first"), testCheckpoint(t, "progress"), testCheckpoint(t, "second")
	first := &events.StoreChangeEvent{EventID: "first", ClusterTime: events.ClusterTime{T: 1}}
	second := &events.StoreChangeEvent{EventID: "second", ClusterTime: events.ClusterTime{T: 2}}
	require.NoError(t, buf.Write(first, cp1))
	require.NoError(t, buf.SaveCheckpoint(cp2))
	require.NoError(t, buf.Write(second, cp3))
	require.NoError(t, buf.Close())
	reopened, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer reopened.Close()
	for _, tc := range []struct {
		evt *events.StoreChangeEvent
		cp  checkpoint.Checkpoint
	}{{first, cp1}, {second, cp3}} {
		record, err := reopened.ReadRecord(tc.evt.BufferKey())
		require.NoError(t, err)
		require.Equal(t, recordVersion, record.Version)
		require.Equal(t, tc.evt, record.Event)
		require.Equal(t, tc.cp, record.Checkpoint)
		payload, err := reopened.Read(tc.evt.BufferKey())
		require.NoError(t, err)
		require.Equal(t, tc.evt, payload)
	}
	latest, err := reopened.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, cp3, latest)
	iter, err := reopened.ScanFrom("")
	require.NoError(t, err)
	defer iter.Close()
	require.True(t, iter.Next())
	require.Equal(t, cp1, iter.Checkpoint())
	require.True(t, iter.Next())
	require.Equal(t, cp3, iter.Checkpoint())
	require.False(t, iter.Next())
	require.NoError(t, iter.Err())
	count, err := reopened.CountAfter("")
	require.NoError(t, err)
	require.Equal(t, 2, count)
	deleted, err := reopened.DeleteBefore("z")
	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	latest, err = reopened.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, cp3, latest)
	require.NoError(t, reopened.Close())
	reopened, err = New(Options{Path: dir})
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}

func TestBuffer_RejectsIncompatibleCacheWithoutChangingContents(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries map[string][]byte
	}{
		{name: "legacy event", entries: map[string][]byte{"event": []byte(`{"eventId":"old"}`)}},
		{name: "legacy checkpoint", entries: map[string][]byte{checkpointKey: {5, 0, 0, 0, 0}}},
		{name: "unknown version", entries: map[string][]byte{formatKey: []byte("999"), "event": []byte("retained")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := pebble.Open(dir, &pebble.Options{})
			require.NoError(t, err)
			for key, value := range tc.entries {
				require.NoError(t, db.Set([]byte(key), value, pebble.Sync))
			}
			require.NoError(t, db.Close())
			buf, err := New(Options{Path: dir})
			require.Nil(t, buf)
			var cpErr *checkpoint.Error
			require.ErrorAs(t, err, &cpErr)
			require.Equal(t, checkpoint.IncompatibleState, cpErr.Code)
			db, err = pebble.Open(dir, &pebble.Options{})
			require.NoError(t, err)
			defer db.Close()
			iter, err := db.NewIter(nil)
			require.NoError(t, err)
			defer iter.Close()
			actual := make(map[string][]byte)
			for iter.First(); iter.Valid(); iter.Next() {
				actual[string(iter.Key())] = append([]byte(nil), iter.Value()...)
			}
			require.NoError(t, iter.Error())
			require.Equal(t, tc.entries, actual)
		})
	}
}

func TestBuffer_RejectsInvalidPersistedRecords(t *testing.T) {
	t.Parallel()
	cp := testCheckpoint(t, "valid")
	valid, err := json.Marshal(Record{Version: recordVersion, Event: &events.StoreChangeEvent{EventID: "event"}, Checkpoint: cp})
	require.NoError(t, err)
	for _, value := range [][]byte{
		[]byte("broken"), []byte(`{"eventId":"legacy"}`),
		[]byte(`{"version":2,"event":{}}`), []byte(`{"version":1,"event":null}`),
		[]byte(`{"version":1,"event":{},"checkpoint":"invalid"}`), valid,
	} {
		buf, err := New(Options{Path: t.TempDir()})
		require.NoError(t, err)
		require.NoError(t, buf.db.Set([]byte("event"), value, pebble.Sync))
		record, err := buf.ReadRecord("event")
		iter, iterErr := buf.ScanFrom("")
		require.NoError(t, iterErr)
		if string(value) == string(valid) {
			require.NoError(t, err)
			require.Equal(t, cp, record.Checkpoint)
			require.True(t, iter.Next())
			require.Equal(t, cp, iter.Checkpoint())
		} else {
			var cpErr *checkpoint.Error
			require.ErrorAs(t, err, &cpErr)
			require.Equal(t, checkpoint.IncompatibleState, cpErr.Code)
			require.False(t, iter.Next())
			require.ErrorAs(t, iter.Err(), &cpErr)
			require.Equal(t, checkpoint.IncompatibleState, cpErr.Code)
		}
		require.NoError(t, iter.Close())
		require.NoError(t, buf.Close())
	}
}

func TestBuffer_ValidatesCheckpointsAndHidesMetadata(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir(), BatchInterval: time.Hour})
	require.NoError(t, err)
	defer buf.Close()
	for _, cp := range []checkpoint.Checkpoint{"", "invalid"} {
		require.Error(t, buf.Write(&events.StoreChangeEvent{EventID: "bad"}, cp))
		require.Error(t, buf.SaveCheckpoint(cp))
	}
	require.Error(t, buf.Write(nil, testCheckpoint(t, "valid")))
	for _, key := range []string{checkpointKey, formatKey} {
		require.Error(t, buf.Delete(key))
		record, err := buf.ReadRecord(key)
		require.NoError(t, err)
		require.Nil(t, record)
	}
	cp, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	require.Empty(t, cp)
	require.NoError(t, buf.db.Set(checkpointKeyBytes, []byte("invalid"), pebble.Sync))
	_, err = buf.LoadCheckpoint()
	var cpErr *checkpoint.Error
	require.ErrorAs(t, err, &cpErr)
	require.Equal(t, checkpoint.IncompatibleState, cpErr.Code)
}

func TestBuffer_CorruptRecordStopsBeforePendingEvents(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir(), BatchInterval: time.Hour})
	require.NoError(t, err)
	defer buf.Close()
	require.NoError(t, buf.db.Set([]byte("0000"), []byte("invalid record"), pebble.Sync))
	require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "pending", ClusterTime: events.ClusterTime{T: 10}}, testCheckpoint(t, "pending")))
	iter, err := buf.ScanFrom("")
	require.NoError(t, err)
	defer iter.Close()
	require.False(t, iter.Next())
	var cpErr *checkpoint.Error
	require.ErrorAs(t, iter.Err(), &cpErr)
	require.Equal(t, checkpoint.IncompatibleState, cpErr.Code)
	require.False(t, iter.Next())
}
