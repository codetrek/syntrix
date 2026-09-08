// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// Iterator provides ordered iteration over events.
type Iterator interface {
	// Next advances to the next event. Returns false when done.
	Next() bool

	// Event returns the current event.
	Event() *events.StoreChangeEvent

	// Checkpoint returns the source checkpoint associated with the current event.
	Checkpoint() checkpoint.Checkpoint

	// Key returns the current buffer key.
	Key() string

	// Err returns any error encountered during iteration.
	Err() error

	// Close releases the iterator resources.
	Close() error
}

type bufferIterator struct {
	iter       *pebble.Iterator
	evt        *events.StoreChangeEvent
	checkpoint checkpoint.Checkpoint
	key        string
	err        error
	first      bool
}

func (i *bufferIterator) Next() bool {
	if i.err != nil {
		return false
	}

	for {
		var valid bool
		if i.first {
			valid = i.iter.First()
			i.first = false
		} else {
			valid = i.iter.Next()
		}

		if !valid {
			i.err = i.iter.Error()
			return false
		}

		if isMetadataKey(i.iter.Key()) {
			continue
		}

		i.key = string(i.iter.Key())
		value := i.iter.Value()

		record, err := decodeRecord(value)
		if err != nil {
			i.err = err
			return false
		}
		i.evt = record.Event
		i.checkpoint = record.Checkpoint
		return true
	}
}

func (i *bufferIterator) Event() *events.StoreChangeEvent {
	return i.evt
}

func (i *bufferIterator) Checkpoint() checkpoint.Checkpoint {
	return i.checkpoint
}

func (i *bufferIterator) Key() string {
	return i.key
}

func (i *bufferIterator) Err() error {
	return i.err
}

func (i *bufferIterator) Close() error {
	if i.iter != nil {
		err := i.iter.Close()
		i.iter = nil
		return err
	}
	return nil
}

// newSnapshotIterator returns an iterator over pending/flushing events after the given key.
func (b *Buffer) newSnapshotIterator(afterKey string) Iterator {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var evts []*events.StoreChangeEvent
	var keys []string
	var checkpoints []checkpoint.Checkpoint

	// Helper to append matching events
	appendEvents := func(reqs []*writeRequest) {
		for _, req := range reqs {
			k := string(req.key)
			if afterKey == "" || k > afterKey {
				if req.event != nil {
					evts = append(evts, req.event)
					keys = append(keys, k)
					checkpoints = append(checkpoints, req.checkpoint)
				}
			}
		}
	}

	// Order matters: flushing (older) then pending (newer)
	appendEvents(b.flushing)
	appendEvents(b.pending)

	return &sliceIterator{
		events:      evts,
		keys:        keys,
		checkpoints: checkpoints,
		index:       -1,
	}
}

type sliceIterator struct {
	events      []*events.StoreChangeEvent
	keys        []string
	checkpoints []checkpoint.Checkpoint
	index       int
}

func (i *sliceIterator) Next() bool {
	if i.index < len(i.events)-1 {
		i.index++
		return true
	}
	return false
}

func (i *sliceIterator) Event() *events.StoreChangeEvent {
	if i.index >= 0 && i.index < len(i.events) {
		return i.events[i.index]
	}
	return nil
}

func (i *sliceIterator) Checkpoint() checkpoint.Checkpoint {
	if i.index >= 0 && i.index < len(i.checkpoints) {
		return i.checkpoints[i.index]
	}
	return ""
}

func (i *sliceIterator) Key() string {
	if i.index >= 0 && i.index < len(i.keys) {
		return i.keys[i.index]
	}
	return ""
}

func (i *sliceIterator) Err() error {
	return nil
}

func (i *sliceIterator) Close() error {
	return nil
}

type deduplicatingIterator struct {
	iterators []Iterator
	current   Iterator
	currIdx   int
	lastYield string
}

func newDeduplicatingIterator(iters ...Iterator) *deduplicatingIterator {
	return &deduplicatingIterator{
		iterators: iters,
		currIdx:   0,
	}
}

func (i *deduplicatingIterator) Next() bool {
	for {
		if i.current == nil {
			if i.currIdx >= len(i.iterators) {
				return false
			}
			i.current = i.iterators[i.currIdx]
			i.currIdx++
		}

		if i.current.Next() {
			key := i.current.Key()
			// Skip duplicates or out of order events (must be strictly ascending)
			if i.lastYield != "" && key <= i.lastYield {
				continue
			}
			i.lastYield = key
			return true
		}

		if i.current.Err() != nil {
			return false
		}

		i.current.Close()
		i.current = nil
	}
}

func (i *deduplicatingIterator) Event() *events.StoreChangeEvent {
	if i.current != nil {
		return i.current.Event()
	}
	return nil
}

func (i *deduplicatingIterator) Checkpoint() checkpoint.Checkpoint {
	if i.current != nil {
		return i.current.Checkpoint()
	}
	return ""
}

func (i *deduplicatingIterator) Key() string {
	if i.current != nil {
		return i.current.Key()
	}
	return ""
}

func (i *deduplicatingIterator) Err() error {
	for _, it := range i.iterators {
		if err := it.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (i *deduplicatingIterator) Close() error {
	var err error
	for _, it := range i.iterators {
		if e := it.Close(); e != nil {
			err = e
		}
	}
	i.iterators = nil
	i.current = nil
	return err
}
