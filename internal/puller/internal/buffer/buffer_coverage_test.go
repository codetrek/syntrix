package buffer

import (
	"os"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/codetrek/syntrix/internal/events"
)

func TestBuffer_New_Error(t *testing.T) {
	t.Parallel()
	// Test empty path
	_, err := New(Options{Path: ""})
	if err == nil {
		t.Error("Expected error for empty path")
	}

	// Test mkdir error (permission denied)
	// Note: This might be hard to test reliably across OSs, but we can try using a file as path
	tmpFile, _ := os.CreateTemp("", "buffer-test-file")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	_, err = New(Options{Path: tmpFile.Name()})
	if err == nil {
		t.Error("Expected error when path is a file")
	}
}

func TestBuffer_Closed_Errors(t *testing.T) {
	t.Parallel()
	dir, _ := os.MkdirTemp("", "buffer-test-*")
	defer os.RemoveAll(dir)

	buf, _ := New(Options{Path: dir})
	buf.Close()

	evt := &events.NormalizedEvent{EventID: "1"}

	if err := buf.Write(evt); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from Write, got %v", err)
	}

	if _, err := buf.Read("key"); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from Read, got %v", err)
	}

	if _, err := buf.ScanFrom(""); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from ScanFrom, got %v", err)
	}

	if _, err := buf.Head(); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from Head, got %v", err)
	}

	if err := buf.Delete("key"); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from Delete, got %v", err)
	}

	if _, err := buf.DeleteBefore("key"); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from DeleteBefore, got %v", err)
	}

	if _, err := buf.Count(); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from Count, got %v", err)
	}

	if _, err := buf.CountAfter(""); err == nil || err.Error() != "buffer is closed" {
		t.Errorf("Expected 'buffer is closed' error from CountAfter, got %v", err)
	}
}

func TestBuffer_Write_MarshalError(t *testing.T) {
	t.Parallel()
	dir, _ := os.MkdirTemp("", "buffer-test-*")
	defer os.RemoveAll(dir)
	buf, _ := New(Options{Path: dir})
	defer buf.Close()

	// Create event with unserializable field
	evt := &events.NormalizedEvent{
		EventID: "1",
		FullDocument: map[string]any{
			"bad": make(chan int),
		},
	}

	if err := buf.Write(evt); err == nil {
		t.Error("Expected error from Write with unserializable event")
	}
}

func TestBuffer_Read_UnmarshalError(t *testing.T) {
	t.Parallel()
	dir, _ := os.MkdirTemp("", "buffer-test-*")
	defer os.RemoveAll(dir)
	buf, _ := New(Options{Path: dir})
	defer buf.Close()

	// Manually write garbage to DB
	// We need to access the private db field. Since we are in the same package, we can.
	key := []byte("evt-1")
	buf.db.Set(key, []byte("garbage"), pebble.Sync)

	// Read should fail
	_, err := buf.Read("evt-1")
	if err == nil {
		t.Error("Expected error from Read with corrupted data")
	}
}

func TestBuffer_Iterator_UnmarshalError(t *testing.T) {
	t.Parallel()
	dir, _ := os.MkdirTemp("", "buffer-test-*")
	defer os.RemoveAll(dir)
	buf, _ := New(Options{Path: dir})
	defer buf.Close()

	// Manually write garbage to DB
	key := []byte("evt-1")
	buf.db.Set(key, []byte("garbage"), pebble.Sync)

	iter, _ := buf.ScanFrom("")
	defer iter.Close()

	if iter.Next() {
		t.Error("Expected Next() to return false on error")
	}
	if iter.Err() == nil {
		t.Error("Expected error from iterator")
	}
}

func TestBuffer_ScanFrom_AfterKey(t *testing.T) {
	t.Parallel()
	dir, _ := os.MkdirTemp("", "buffer-test-*")
	defer os.RemoveAll(dir)
	buf, _ := New(Options{Path: dir})
	defer buf.Close()

	// Write 3 events
	evts := []*events.NormalizedEvent{
		{EventID: "1", Timestamp: 100},
		{EventID: "2", Timestamp: 200},
		{EventID: "3", Timestamp: 300},
	}
	for _, e := range evts {
		buf.Write(e)
	}

	// Scan from event 1 (should get 2 and 3)
	// Note: BufferKey uses timestamp-eventID
	key1 := evts[0].BufferKey()

	iter, err := buf.ScanFrom(key1)
	if err != nil {
		t.Fatalf("ScanFrom failed: %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		count++
		if iter.Event().EventID == "1" {
			t.Error("Should not see event 1")
		}
	}
	if count != 2 {
		t.Errorf("Expected 2 events, got %d", count)
	}
}
