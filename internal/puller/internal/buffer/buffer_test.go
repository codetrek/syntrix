package buffer

import (
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
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

func TestBuffer_WriteWithCheckpoint(t *testing.T) {
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
	token := bson.Raw{0x05, 0x00, 0x00, 0x00, 0x00}

	err = buf.WriteWithCheckpoint(evt, token, true)
	require.NoError(t, err)

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

func TestBuffer_WriteWithCheckpoint_NilToken(t *testing.T) {
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

	err = buf.WriteWithCheckpoint(evt, nil, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint token is nil")
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

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- buf.Write(evt1)
	}()

	select {
	case err := <-firstDone:
		t.Fatalf("first write finished early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	require.NoError(t, buf.Write(evt2))
	require.NoError(t, <-firstDone)

	read1, err := buf.Read(evt1.BufferKey())
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

	done := make(chan error, 1)
	go func() {
		done <- buf.Write(evt)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for batch flush")
	}

	readEvt, err := buf.Read(evt.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, readEvt)
}

func TestBuffer_DeleteBefore_SkipsCheckpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
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
	require.NoError(t, buf.Write(evt1))
	require.NoError(t, buf.Write(evt2))

	deleted, err := buf.DeleteBefore(evt2.BufferKey())
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	ckpt, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, token, ckpt)
}
