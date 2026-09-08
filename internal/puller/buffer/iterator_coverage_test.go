package buffer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestBuffer_Revalidate(t *testing.T) {
	b := &Buffer{}
	assert.NoError(t, b.Revalidate(time.Second))
}

func TestSliceIterator_Coverage(t *testing.T) {
	// Setup checks for sliceIterator
	evts := []*events.StoreChangeEvent{
		{EventID: "1"},
		{EventID: "2"},
	}
	keys := []string{"k1", "k2"}
	checkpoints := []checkpoint.Checkpoint{testCheckpoint(t, "first"), testCheckpoint(t, "second")}

	it := &sliceIterator{
		events:      evts,
		keys:        keys,
		checkpoints: checkpoints,
		index:       -1,
	}

	// Test Next/Event/Key sequence
	// Initial state
	assert.Nil(t, it.Event())
	assert.Equal(t, "", it.Key())
	assert.Empty(t, it.Checkpoint())

	// First
	assert.True(t, it.Next())
	assert.Equal(t, "1", it.Event().EventID)
	assert.Equal(t, "k1", it.Key())
	assert.Equal(t, checkpoints[0], it.Checkpoint())

	// Second
	assert.True(t, it.Next())
	assert.Equal(t, "2", it.Event().EventID)
	assert.Equal(t, "k2", it.Key())
	assert.Equal(t, checkpoints[1], it.Checkpoint())

	// End
	assert.False(t, it.Next())
	// When Next returns false, the iterator is exhausted.
	// We don't assert strict state of Event()/Key() here as implementation leaves them at last valid.

	assert.NoError(t, it.Err())
	assert.NoError(t, it.Close())
}

func TestSliceIterator_EdgeCases(t *testing.T) {
	// Mismatch length or empty
	it := &sliceIterator{
		events: []*events.StoreChangeEvent{},
		keys:   []string{"k1"}, // Mismatch
		index:  -1,
	}
	assert.False(t, it.Next())

	it2 := &sliceIterator{
		events: []*events.StoreChangeEvent{{}},
		keys:   []string{}, // Mismatch
		index:  -1,
	}
	it2.Next()
	// event exists, key missing
	assert.NotNil(t, it2.Event())
	assert.Equal(t, "", it2.Key())
}

func TestNewSnapshotIterator_Methods(t *testing.T) {
	// verify the creation logic uses flushing/pending
	// easier to verify by behavior if we can't easily mock Buffer internals which are private.
	// But we are in package buffer! we can access private fields.

	b := &Buffer{} // empty buffer
	b.pending = []*writeRequest{
		{key: []byte("k1"), event: &events.StoreChangeEvent{EventID: "e1"}},
		{key: []byte("k0"), event: nil}, // should be skipped (key check? or nil event check?)
		{key: []byte("k2"), event: &events.StoreChangeEvent{EventID: "e2"}},
	}

	// pending isn't sorted by default?
	// The implementation of newSnapshotIterator iterators over pending slice directly.
	// It assumes time order?
	// `appendEvents` iterates the slice.

	iter := b.newSnapshotIterator("k1") // Should skip k1 (strictly greater?)
	// Code: if afterKey == "" || k > afterKey
	// "k1" > "k1" is false. "k2" > "k1" is true.

	assert.True(t, iter.Next())
	assert.Equal(t, "e2", iter.Event().EventID)
	assert.False(t, iter.Next())
}

func TestDeduplicatingIterator_Coverage(t *testing.T) {
	done := make(chan bool)
	go func() {
		// Create two slice iterators with overlapping keys
		it1 := &sliceIterator{
			events: []*events.StoreChangeEvent{{EventID: "1"}},
			keys:   []string{"100"},
			index:  -1,
		}
		it2 := &sliceIterator{
			events: []*events.StoreChangeEvent{{EventID: "2"}, {EventID: "3"}},
			keys:   []string{"100", "200"}, // "100" is duplicate (le), "200" is new
			index:  -1,
		}

		dedup := newDeduplicatingIterator(it1, it2)

		// 1. from it1: key=100
		assert.True(t, dedup.Next())
		assert.Equal(t, "1", dedup.Event().EventID)
		assert.Equal(t, "100", dedup.Key())

		// 2. from it2: key=100 -> SKIP (<= 100)
		//    from it2: key=200 -> OK
		assert.True(t, dedup.Next())
		assert.Equal(t, "3", dedup.Event().EventID)
		assert.Equal(t, "200", dedup.Key())

		// 3. End
		assert.False(t, dedup.Next())
		assert.Nil(t, dedup.Event())
		assert.Equal(t, "", dedup.Key())
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("TestDeduplicatingIterator_Coverage timed out")
	}
}
