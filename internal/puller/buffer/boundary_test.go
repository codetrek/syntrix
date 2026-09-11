package buffer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestBufferBoundaryPersistsLineageAndPruningFloor(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	b, err := New(Options{Path: dir, BatchInterval: time.Hour})
	require.NoError(t, err)
	lineage := b.Lineage()
	require.NoError(t, b.ValidatePosition("", lineage))
	require.ErrorContains(t, b.ValidatePosition("", "different"), "lineage")
	eventsToWrite := []*events.StoreChangeEvent{
		{EventID: "1-1-a", ClusterTime: events.ClusterTime{T: 1, I: 1}},
		{EventID: "2-1-b", ClusterTime: events.ClusterTime{T: 2, I: 1}},
	}
	for _, evt := range eventsToWrite {
		require.NoError(t, b.Write(ctx, evt, testToken))
	}
	require.ErrorContains(t, b.SaveCheckpoint(testToken), "flush")
	require.NoError(t, b.Flush(ctx))
	require.NoError(t, b.SaveCheckpoint(testToken))
	require.NoError(t, b.ValidatePosition(eventsToWrite[0].BufferKey(), lineage))
	require.ErrorContains(t, b.ValidatePosition("9999999999-unknown", lineage), "unknown")
	require.NoError(t, b.Delete(eventsToWrite[0].BufferKey()))
	require.ErrorContains(t, b.ValidatePosition("", lineage), "expired")
	require.NoError(t, b.ValidatePosition(eventsToWrite[0].BufferKey(), lineage))
	require.NoError(t, b.Close())
	require.ErrorContains(t, b.Flush(ctx), "buffer is closed")
	b, err = New(Options{Path: dir})
	require.NoError(t, err)
	defer b.Close()
	require.Equal(t, lineage, b.Lineage())
	require.ErrorContains(t, b.ValidatePosition("", lineage), "expired")
	iter, err := b.ScanFromLineage(eventsToWrite[0].BufferKey(), lineage)
	require.NoError(t, err)
	defer iter.Close()
	require.True(t, iter.Next())
	require.Equal(t, eventsToWrite[1].EventID, iter.Event().EventID)
	require.False(t, iter.Next())
	require.NoError(t, iter.Err())
	count, err := b.DeleteBefore(eventsToWrite[1].BufferKey() + "x")
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.ErrorContains(t, b.ValidatePosition(eventsToWrite[0].BufferKey(), lineage), "expired")
	require.NoError(t, b.ValidatePosition(eventsToWrite[1].BufferKey(), lineage))
}

func TestBufferBoundaryRejectsDamagedLineage(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "invalid lineage"
		if missing {
			name = "missing lineage with checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := pebble.Open(dir, nil)
			require.NoError(t, err)
			require.NoError(t, db.Set(formatKeyBytes, []byte(formatVersion), pebble.Sync))
			require.NoError(t, db.Set(checkpointKeyBytes, testToken, pebble.Sync))
			if !missing {
				require.NoError(t, db.Set([]byte(lineageKey), []byte("truncated"), pebble.Sync))
			}
			require.NoError(t, db.Close())

			b, err := New(Options{Path: dir})
			require.Nil(t, b)
			require.ErrorContains(t, err, "lineage")
			require.ErrorContains(t, err, "offline buffer rebuild required")

			db, err = pebble.Open(dir, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			value, closer, err := db.Get(checkpointKeyBytes)
			require.NoError(t, err)
			require.Equal(t, []byte(testToken), value)
			require.NoError(t, closer.Close())
			value, closer, err = db.Get([]byte(lineageKey))
			if missing {
				require.ErrorIs(t, err, pebble.ErrNotFound)
			} else {
				require.NoError(t, err)
				require.Equal(t, "truncated", string(value))
				require.NoError(t, closer.Close())
			}
		})
	}
}

func TestBufferBoundaryCannotPublishUndurableLineage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := pebble.Open(dir, nil)
	require.NoError(t, err)
	require.NoError(t, db.Set(formatKeyBytes, []byte(formatVersion), pebble.Sync))
	require.NoError(t, db.Close())
	db, err = pebble.Open(dir, &pebble.Options{ReadOnly: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lineage, err := loadLineage(db)
	require.ErrorIs(t, err, pebble.ErrReadOnly)
	require.Empty(t, lineage)
}

func TestBufferBoundaryPruningFloorNeverRegresses(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	b, err := New(Options{Path: dir, BatchInterval: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	earlier := &events.StoreChangeEvent{EventID: "earlier", ClusterTime: events.ClusterTime{T: 1}}
	later := &events.StoreChangeEvent{EventID: "later", ClusterTime: events.ClusterTime{T: 2}}
	require.NoError(t, b.Write(ctx, earlier, testToken))
	require.NoError(t, b.Write(ctx, later, testToken))
	require.NoError(t, b.Flush(ctx))
	require.NoError(t, b.Delete(later.BufferKey()))
	require.NoError(t, b.Delete(earlier.BufferKey()))
	require.NoError(t, b.Close())
	b, err = New(Options{Path: dir})
	require.NoError(t, err)
	require.ErrorContains(t, b.ValidatePosition(earlier.BufferKey(), b.Lineage()), "expired")
	require.NoError(t, b.ValidatePosition(later.BufferKey(), b.Lineage()))
	count, err := b.Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBufferFlushCancellationDoesNotDiscardAdmittedBatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	b, err := New(Options{Path: dir, BatchSize: 1, BatchInterval: time.Hour})
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		require.NoError(t, b.Close())
	})
	b.newBatch = func() pebbleBatch {
		real := b.db.NewBatch()
		return &fakeBatch{
			set: func(key, value []byte) error { return real.Set(key, value, pebble.Sync) },
			commit: func() error {
				close(started)
				<-release
				return real.Commit(pebble.Sync)
			},
			close: real.Close,
		}
	}
	evt := &events.StoreChangeEvent{EventID: "admitted", ClusterTime: events.ClusterTime{T: 1}}
	require.NoError(t, b.Write(ctx, evt, testToken))
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("batch did not reach commit")
	}
	flushCtx, cancelFlush := context.WithCancel(ctx)
	cancelFlush()
	require.ErrorIs(t, b.Flush(flushCtx), context.Canceled)
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, b.Flush(ctx))
	require.NoError(t, b.Close())
	b, err = New(Options{Path: dir})
	require.NoError(t, err)
	checkpoint, err := b.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, testToken, checkpoint)
	stored, err := b.Read(evt.BufferKey())
	require.NoError(t, err)
	require.Equal(t, evt.EventID, stored.EventID)
}
