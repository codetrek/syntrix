package buffer

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestBuffer_Write_CommitErrorUsesBatch(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir(), BatchInterval: time.Hour})
	require.NoError(t, err)
	batchErr := errors.New("commit failed")
	mockBatch := &fakeBatch{commitErr: batchErr}
	buf.newBatch = func() pebbleBatch { return mockBatch }
	cp := testCheckpoint(t, "failed")
	require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-1"}, cp))
	require.ErrorIs(t, buf.SaveCheckpoint(cp), batchErr)
	require.ErrorIs(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-2"}, cp), batchErr)
	require.ErrorIs(t, buf.SaveCheckpoint(cp), batchErr)
	_, err = buf.LoadCheckpoint()
	require.ErrorIs(t, err, batchErr)
	require.ErrorIs(t, buf.Close(), batchErr)
	require.ErrorIs(t, buf.Close(), batchErr)
}

func TestBuffer_Delete_CommitErrorUsesBatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	batchErr := errors.New("commit failed")
	mockBatch := &fakeBatch{commitErr: batchErr}
	buf.newBatch = func() pebbleBatch {
		return mockBatch
	}

	err = buf.Delete("evt-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
	assert.Equal(t, 1, mockBatch.deleteCalls)
	assert.True(t, mockBatch.closed)
}

type fakeBatch struct {
	setCalls    int
	deleteCalls int
	setErr      error
	deleteErr   error
	commitErr   error
	closeErr    error
	closed      bool
}

func (f *fakeBatch) Set(key, value []byte, opts *pebble.WriteOptions) error {
	f.setCalls++
	return f.setErr
}

func (f *fakeBatch) Delete(key []byte, opts *pebble.WriteOptions) error {
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeBatch) Commit(opts *pebble.WriteOptions) error {
	return f.commitErr
}

func (f *fakeBatch) Close() error {
	f.closed = true
	return f.closeErr
}

type blockedBatch struct {
	pebbleBatch
	started   chan struct{}
	release   chan struct{}
	commitErr error
}

func (b *blockedBatch) Commit(opts *pebble.WriteOptions) error {
	close(b.started)
	<-b.release
	if b.commitErr != nil {
		return b.commitErr
	}
	return b.pebbleBatch.Commit(opts)
}

func TestBuffer_CheckpointWaitsForEarlierEventBatch(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir(), BatchSize: 1, BatchInterval: time.Hour})
	require.NoError(t, err)
	cp0, cp1, cp2 := testCheckpoint(t, "initial"), testCheckpoint(t, "event"), testCheckpoint(t, "progress")
	require.NoError(t, buf.SaveCheckpoint(cp0))
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); require.NoError(t, buf.Close()) })
	batches := 0
	buf.newBatch = func() pebbleBatch {
		batches++
		if batches == 1 {
			return &blockedBatch{pebbleBatch: buf.db.NewBatch(), started: started, release: release}
		}
		return buf.db.NewBatch()
	}
	evt := &events.StoreChangeEvent{EventID: "evt-ordered"}
	require.NoError(t, buf.Write(evt, cp1))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("event batch did not start")
	}
	saved := make(chan error, 1)
	go func() { saved <- buf.SaveCheckpoint(cp2) }()
	require.Eventually(t, func() bool {
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		return len(buf.pending) == 1
	}, time.Second, time.Millisecond)
	select {
	case err := <-saved:
		t.Fatalf("checkpoint completed before earlier batch: %v", err)
	default:
	}
	current, err := buf.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, cp0, current)
	record, err := buf.ReadRecord(evt.BufferKey())
	require.NoError(t, err)
	require.Nil(t, record)
	iter, err := buf.ScanFrom("")
	require.NoError(t, err)
	require.True(t, iter.Next())
	require.Equal(t, cp1, iter.Checkpoint())
	require.False(t, iter.Next())
	require.Empty(t, iter.Checkpoint())
	require.NoError(t, iter.Close())
	unblock()
	select {
	case err := <-saved:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("checkpoint receipt did not complete")
	}
	record, err = buf.ReadRecord(evt.BufferKey())
	require.NoError(t, err)
	require.Equal(t, cp1, record.Checkpoint)
	current, err = buf.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, cp2, current)
}

func TestBuffer_FailedBatchReleasesQueuedCheckpointAndClose(t *testing.T) {
	t.Parallel()
	buf, err := New(Options{Path: t.TempDir(), BatchSize: 1, BatchInterval: time.Hour})
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	batchErr := errors.New("disk unavailable")
	t.Cleanup(func() { unblock(); require.ErrorIs(t, buf.Close(), batchErr) })
	buf.newBatch = func() pebbleBatch {
		return &blockedBatch{pebbleBatch: buf.db.NewBatch(), started: started, release: release, commitErr: batchErr}
	}
	cp := testCheckpoint(t, "event")
	require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-failed"}, cp))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("event batch did not start")
	}
	saved := make(chan error, 2)
	for range 2 {
		go func() { saved <- buf.SaveCheckpoint(cp) }()
	}
	require.Eventually(t, func() bool {
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		return len(buf.pending) == 2
	}, time.Second, time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- buf.Close() }()
	unblock()
	for range 2 {
		select {
		case err := <-saved:
			require.ErrorIs(t, err, batchErr)
		case <-time.After(time.Second):
			t.Fatal("checkpoint receipt was stranded")
		}
	}
	select {
	case err := <-closed:
		require.ErrorIs(t, err, batchErr)
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	require.ErrorIs(t, buf.Close(), batchErr)
}

func TestBuffer_BatchPreparationAndCloseFailures(t *testing.T) {
	for _, tc := range []struct {
		name             string
		event            bool
		setErr, closeErr error
	}{
		{name: "event set", event: true, setErr: errors.New("event set failure")},
		{name: "checkpoint set", setErr: errors.New("checkpoint set failure")},
		{name: "batch close", closeErr: errors.New("batch close failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := New(Options{Path: t.TempDir(), BatchInterval: time.Hour})
			require.NoError(t, err)
			fake := &fakeBatch{setErr: tc.setErr, closeErr: tc.closeErr}
			buf.newBatch = func() pebbleBatch { return fake }
			cp := testCheckpoint(t, "failure")
			if tc.event {
				require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "event"}, cp))
			}
			cause := errors.Join(tc.setErr, tc.closeErr)
			require.Error(t, cause)
			err = buf.SaveCheckpoint(cp)
			if tc.setErr != nil {
				require.ErrorIs(t, err, tc.setErr)
			}
			if tc.closeErr != nil {
				require.ErrorIs(t, err, tc.closeErr)
			}
			require.True(t, fake.closed)
			closeErr := buf.Close()
			if tc.setErr != nil {
				require.ErrorIs(t, closeErr, tc.setErr)
			}
			if tc.closeErr != nil {
				require.ErrorIs(t, closeErr, tc.closeErr)
			}
		})
	}
}
