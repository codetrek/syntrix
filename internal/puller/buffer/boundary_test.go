package buffer

import (
	"context"
	"testing"
	"time"

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
