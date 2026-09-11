// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// Read retrieves an event by its buffer key.
func (b *Buffer) Read(key string) (*events.StoreChangeEvent, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	value, closer, err := b.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read event: %w", err)
	}
	defer closer.Close()

	evt, err := events.UnmarshalEvent(value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	return evt, nil
}

// ScanFrom returns an iterator starting from the given key (exclusive).
// If afterKey is empty, starts from the beginning.
func (b *Buffer) ScanFrom(afterKey string) (Iterator, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil, fmt.Errorf("buffer is closed")
	}

	iterOpts := &pebble.IterOptions{}
	if afterKey != "" {
		// Start after the given key
		iterOpts.LowerBound = []byte(afterKey + "\x00") // Next key after afterKey
	}

	iter, err := b.db.NewIter(iterOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}

	dbIter := &bufferIterator{
		iter:  iter,
		first: true,
	}

	// Create snapshot iterator over pending events
	snapshotIter := b.newSnapshotIteratorLocked(afterKey)

	// Return a deduplicating iterator that reads from DB then snapshot
	return newDeduplicatingIterator(dbIter, snapshotIter), nil
}

// Head returns the most recent event key.
func (b *Buffer) Head() (string, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return "", fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	iter, err := b.db.NewIter(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.Last(); iter.Valid(); iter.Prev() {
		if isMetadataKey(iter.Key()) {
			continue
		}
		return string(iter.Key()), nil
	}
	return "", nil // Empty buffer
}

// First returns the oldest event key.
func (b *Buffer) First() (string, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return "", fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	iter, err := b.db.NewIter(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if isMetadataKey(iter.Key()) {
			continue
		}
		return string(iter.Key()), nil
	}
	return "", nil // Empty buffer
}

// Size returns the estimated disk usage of the buffer.
func (b *Buffer) Size() (int64, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return 0, fmt.Errorf("buffer is closed")
	}
	// DiskSpaceUsage includes WAL and SSTables
	return int64(b.db.Metrics().DiskSpaceUsage()), nil
}

// Count returns the approximate number of events in the buffer.
func (b *Buffer) Count() (int, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return 0, fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	count := 0
	iter, err := b.db.NewIter(nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if isMetadataKey(iter.Key()) {
			continue
		}
		count++
	}

	return count, nil
}

// CountAfter returns the number of events after the given key.
func (b *Buffer) CountAfter(afterKey string) (int, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return 0, fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	count := 0
	iterOpts := &pebble.IterOptions{}
	if afterKey != "" {
		iterOpts.LowerBound = []byte(afterKey + "\x00")
	}

	iter, err := b.db.NewIter(iterOpts)
	if err != nil {
		return 0, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if isMetadataKey(iter.Key()) {
			continue
		}
		count++
	}

	return count, nil
}

// Revalidate returns true if the buffer is consistent (last event matches last in DB).
func (b *Buffer) Revalidate(ctx time.Duration) error {
	// Not implemented for now, but placeholder if needed to check consistency
	return nil
}

// ValidatePosition rejects a different buffer incarnation or unavailable history.
func (b *Buffer) ValidatePosition(afterKey, lineage string) error {
	b.retentionMu.Lock()
	defer b.retentionMu.Unlock()
	return b.validatePosition(afterKey, lineage)
}

func (b *Buffer) validatePosition(afterKey, lineage string) error {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return fmt.Errorf("buffer is closed")
	}
	if lineage == "" || lineage != b.lineage {
		return fmt.Errorf("buffer lineage changed; offline rebuild required")
	}
	floor, err := b.pruningFloor()
	if err != nil {
		return err
	}
	if floor != "" && afterKey < floor {
		return fmt.Errorf("event history expired; offline rebuild required")
	}
	if afterKey == "" || afterKey == floor {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	value, closer, err := b.db.Get([]byte(afterKey))
	if err == nil {
		_ = value
		return closer.Close()
	}
	if err != pebble.ErrNotFound {
		return err
	}
	for _, queue := range [][]*writeRequest{b.pending, b.flushing} {
		for _, req := range queue {
			if string(req.key) == afterKey {
				return nil
			}
		}
	}
	return fmt.Errorf("unknown event position; offline rebuild required")
}

// ScanFromLineage validates and opens the replay snapshot under the retention lock.
func (b *Buffer) ScanFromLineage(afterKey, lineage string) (Iterator, error) {
	b.retentionMu.Lock()
	defer b.retentionMu.Unlock()
	if err := b.validatePosition(afterKey, lineage); err != nil {
		return nil, err
	}
	return b.ScanFrom(afterKey)
}
