// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"fmt"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// Iterator provides ordered iteration over events.
type Iterator interface {
	// Next advances to the next event. Returns false when done.
	Next() bool

	// Event returns the current event.
	Event() *events.StoreChangeEvent

	// Key returns the current buffer key.
	Key() string

	// Err returns any error encountered during iteration.
	Err() error

	// Close releases the iterator resources.
	Close() error
}

type bufferIterator struct {
	iter  *pebble.Iterator
	evt   *events.StoreChangeEvent
	key   string
	err   error
	first bool
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

		evt, err := events.UnmarshalEvent(value)
		if err != nil {
			i.err = fmt.Errorf("failed to unmarshal event: %w", err)
			return false
		}

		i.evt = evt
		return true
	}
}

func (i *bufferIterator) Event() *events.StoreChangeEvent {
	return i.evt
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
	return b.newSnapshotIteratorLocked(afterKey)
}

// Holding mu across the disk snapshot and queue copy prevents a committed batch
// from disappearing from the queue before it becomes visible to that snapshot.
func (b *Buffer) newSnapshotIteratorLocked(afterKey string) Iterator {
	var evts []*events.StoreChangeEvent
	var keys []string

	// Helper to append matching events
	appendEvents := func(reqs []*writeRequest) {
		for _, req := range reqs {
			k := string(req.key)
			if afterKey == "" || k > afterKey {
				if req.event != nil {
					evts = append(evts, req.event)
					keys = append(keys, k)
				}
			}
		}
	}

	// Order matters: flushing (older) then pending (newer)
	appendEvents(b.flushing)
	appendEvents(b.pending)

	return &sliceIterator{
		events: evts,
		keys:   keys,
		index:  -1,
	}
}

type sliceIterator struct {
	events []*events.StoreChangeEvent
	keys   []string
	index  int
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

		// Current iterator exhausted, move to next
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
