// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/codetrek/syntrix/internal/events"
)

// Buffer stores events in PebbleDB for durability and replay.
type Buffer struct {
	db     *pebble.DB
	path   string
	logger *slog.Logger

	// mu protects writes
	mu sync.RWMutex

	// closed indicates if the buffer is closed
	closed bool
}

// Options configures the event buffer.
type Options struct {
	// Path is the directory to store the buffer.
	Path string

	// MaxSize is the maximum size in bytes (0 = unlimited).
	MaxSize int64

	// Logger for buffer operations.
	Logger *slog.Logger
}

// New creates a new event buffer.
func New(opts Options) (*Buffer, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("buffer path is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "event-buffer")

	// Ensure directory exists
	if err := os.MkdirAll(opts.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer directory: %w", err)
	}

	// Open PebbleDB
	dbOpts := &pebble.Options{
		// Use default comparer for string ordering
	}

	db, err := pebble.Open(opts.Path, dbOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to open pebble database: %w", err)
	}

	return &Buffer{
		db:     db,
		path:   opts.Path,
		logger: logger,
	}, nil
}

// NewForBackend creates a buffer for a specific backend.
func NewForBackend(basePath, backendName string, logger *slog.Logger) (*Buffer, error) {
	path := filepath.Join(basePath, backendName)
	return New(Options{
		Path:   path,
		Logger: logger,
	})
}

// Write stores an event in the buffer.
func (b *Buffer) Write(evt *events.NormalizedEvent) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	// Key is the buffer key for ordering
	key := []byte(evt.BufferKey())

	// Value is the JSON-encoded event
	value, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if err := b.db.Set(key, value, pebble.Sync); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	return nil
}

// Read retrieves an event by its buffer key.
func (b *Buffer) Read(key string) (*events.NormalizedEvent, error) {
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

	var evt events.NormalizedEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	return &evt, nil
}

// Iterator provides ordered iteration over events.
type Iterator interface {
	// Next advances to the next event. Returns false when done.
	Next() bool

	// Event returns the current event.
	Event() *events.NormalizedEvent

	// Key returns the current buffer key.
	Key() string

	// Err returns any error encountered during iteration.
	Err() error

	// Close releases the iterator resources.
	Close()
}

type bufferIterator struct {
	iter  *pebble.Iterator
	evt   *events.NormalizedEvent
	key   string
	err   error
	first bool
}

func (i *bufferIterator) Next() bool {
	if i.err != nil {
		return false
	}

	var valid bool
	if i.first {
		valid = i.iter.First()
		i.first = false
	} else {
		valid = i.iter.Next()
	}

	if !valid {
		return false
	}

	i.key = string(i.iter.Key())
	value := i.iter.Value()

	var evt events.NormalizedEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		i.err = fmt.Errorf("failed to unmarshal event: %w", err)
		return false
	}

	i.evt = &evt
	return true
}

func (i *bufferIterator) Event() *events.NormalizedEvent {
	return i.evt
}

func (i *bufferIterator) Key() string {
	return i.key
}

func (i *bufferIterator) Err() error {
	return i.err
}

func (i *bufferIterator) Close() {
	if i.iter != nil {
		i.iter.Close()
	}
}

// ScanFrom returns an iterator starting from the given key (exclusive).
// If afterKey is empty, starts from the beginning.
func (b *Buffer) ScanFrom(afterKey string) (Iterator, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, fmt.Errorf("buffer is closed")
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

	return &bufferIterator{
		iter:  iter,
		first: true,
	}, nil
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

	if iter.Last() {
		return string(iter.Key()), nil
	}
	return "", nil // Empty buffer
}

// Delete removes an event from the buffer.
func (b *Buffer) Delete(key string) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	if err := b.db.Delete([]byte(key), pebble.Sync); err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
	}
	return nil
}

// DeleteBefore deletes all events with keys before the given key.
// Returns the number of events deleted.
func (b *Buffer) DeleteBefore(beforeKey string) (int, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return 0, fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	count := 0
	iter, err := b.db.NewIter(&pebble.IterOptions{
		UpperBound: []byte(beforeKey),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	batch := b.db.NewBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key(), nil); err != nil {
			return 0, fmt.Errorf("failed to batch delete: %w", err)
		}
		count++
	}

	if count > 0 {
		if err := batch.Commit(pebble.Sync); err != nil {
			return 0, fmt.Errorf("failed to commit deletes: %w", err)
		}
	}

	return count, nil
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
		count++
	}

	return count, nil
}

// Close closes the buffer.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	if err := b.db.Close(); err != nil {
		return fmt.Errorf("failed to close pebble database: %w", err)
	}

	b.logger.Info("event buffer closed")
	return nil
}

// Path returns the buffer storage path.
func (b *Buffer) Path() string {
	return b.path
}
