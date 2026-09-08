// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

const recordVersion = 1

// Record retains the source checkpoint associated with a cached event. Its
// checkpoint identifies source progress independently of the cache key.
type Record struct {
	Version    int                      `json:"version"`
	Event      *events.StoreChangeEvent `json:"event"`
	Checkpoint checkpoint.Checkpoint    `json:"checkpoint"`
}

// Read retrieves a committed event by its buffer key, or nil if absent.
func (b *Buffer) Read(key string) (*events.StoreChangeEvent, error) {
	record, err := b.ReadRecord(key)
	if err != nil || record == nil {
		return nil, err
	}
	return record.Event, nil
}

// ReadRecord retrieves a committed event with its source checkpoint, or nil if
// absent. Unsupported or corrupt persisted records fail explicitly.
func (b *Buffer) ReadRecord(key string) (*Record, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.stateError(); err != nil {
		return nil, err
	}
	if isMetadataKey([]byte(key)) {
		return nil, nil
	}
	value, closer, err := b.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read buffer record: %w", err)
	}
	defer closer.Close()
	return decodeRecord(value)
}

func decodeRecord(value []byte) (*Record, error) {
	var record Record
	if err := json.Unmarshal(value, &record); err != nil {
		return nil, &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "invalid buffer record encoding", Cause: err}
	}
	if record.Version != recordVersion || record.Event == nil {
		return nil, &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "unsupported or incomplete buffer record"}
	}
	if err := checkpoint.Validate(record.Checkpoint); err != nil {
		return nil, &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "invalid buffer record checkpoint", Cause: err}
	}
	return &record, nil
}

// ScanFrom returns an iterator starting from the given key (exclusive).
// If afterKey is empty, starts from the beginning.
func (b *Buffer) ScanFrom(afterKey string) (Iterator, error) {
	b.mu.RLock()
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return nil, err
	}
	b.mu.RUnlock()

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
	snapshotIter := b.newSnapshotIterator(afterKey)

	// Return a deduplicating iterator that reads from DB then snapshot
	return newDeduplicatingIterator(dbIter, snapshotIter), nil
}

// Head returns the most recent event key.
func (b *Buffer) Head() (string, error) {
	b.mu.RLock()
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return "", err
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
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return "", err
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
	if err := b.stateError(); err != nil {
		return 0, err
	}
	// DiskSpaceUsage includes WAL and SSTables
	return int64(b.db.Metrics().DiskSpaceUsage()), nil
}

// Count returns the approximate number of events in the buffer.
func (b *Buffer) Count() (int, error) {
	b.mu.RLock()
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return 0, err
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
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return 0, err
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
