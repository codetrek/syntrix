// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
)

// Buffer stores events in PebbleDB for durability and replay.
type Buffer struct {
	db       *pebble.DB
	path     string
	logger   *slog.Logger
	newBatch func() pebbleBatch

	// pending is the queue of writes waiting to be batched
	pending []*writeRequest
	// flushing is the queue of writes currently being batched
	flushing []*writeRequest
	// notifyCh is used to wake up the batcher
	notifyCh chan struct{}

	// mu protects pending, flushing, and lifecycle state
	mu sync.RWMutex

	closed   bool
	failure  error
	closeErr error

	// shutdownOnce retains the result for concurrent and repeated Close calls.
	shutdownOnce sync.Once

	// batcher manages batched writes

	batchSize     int
	batchInterval time.Duration
	queueSize     int
	closeCh       chan struct{}
	batcherWG     sync.WaitGroup
}

const (
	checkpointKey      = "!checkpoint/resume_token"
	formatKey          = "!format/version"
	cacheFormatVersion = "1"
)

var (
	checkpointKeyBytes = []byte(checkpointKey)
	formatKeyBytes     = []byte(formatKey)
)

type pebbleBatch interface {
	Set(key, value []byte, opts *pebble.WriteOptions) error
	Delete(key []byte, opts *pebble.WriteOptions) error
	Commit(opts *pebble.WriteOptions) error
	Close() error
}

// Options configures the event buffer.
type Options struct {
	// Path is the directory to store the buffer.
	Path string

	// MaxSize is the maximum size in bytes (0 = unlimited).
	MaxSize int64

	// BatchSize is the max number of events per batch.
	BatchSize int

	// BatchInterval is the max time to wait before flushing a batch.
	BatchInterval time.Duration

	// QueueSize is the buffer for pending writes.
	QueueSize int

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

	if err := initializeFormat(db); err != nil {
		return nil, errors.Join(err, db.Close())
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	batchInterval := opts.BatchInterval
	if batchInterval <= 0 {
		batchInterval = 10000 * time.Millisecond
	}
	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = 10000
	}

	buf := &Buffer{
		db:     db,
		path:   opts.Path,
		logger: logger,
		newBatch: func() pebbleBatch {
			return db.NewBatch()
		},
		batchSize:     batchSize,
		batchInterval: batchInterval,
		queueSize:     queueSize,
		closeCh:       make(chan struct{}),
		notifyCh:      make(chan struct{}, 1),
	}
	buf.startBatcher()

	return buf, nil
}

// NewForBackend creates a buffer for a specific backend.
func NewForBackend(basePath, backendName string, logger *slog.Logger) (*Buffer, error) {
	path := filepath.Join(basePath, backendName)
	return New(Options{
		Path:   path,
		Logger: logger,
	})
}

// Close drains admitted writes and closes storage. A batch failure is retained
// and returned by this and every later Close call.
func (b *Buffer) Close() error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.closeCh)
	}
	b.mu.Unlock()
	b.batcherWG.Wait()

	b.shutdownOnce.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		var closeErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					closeErr = fmt.Errorf("%v", r)
				}
			}()
			closeErr = b.db.Close()
		}()
		if closeErr != nil {
			closeErr = fmt.Errorf("failed to close pebble database: %w", closeErr)
		}
		b.closeErr = errors.Join(b.failure, closeErr)
	})
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closeErr
}

// Path returns the buffer storage path.
func (b *Buffer) Path() string {
	return b.path
}

// LoadCheckpoint returns the last committed source checkpoint. An empty result
// means no source checkpoint has been saved in this fresh-format cache.
func (b *Buffer) LoadCheckpoint() (checkpoint.Checkpoint, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.stateError(); err != nil {
		return "", err
	}
	value, closer, err := b.db.Get(checkpointKeyBytes)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read checkpoint: %w", err)
	}
	defer closer.Close()
	cp := checkpoint.Checkpoint(value)
	if err := checkpoint.Validate(cp); err != nil {
		return "", &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "invalid persisted buffer checkpoint", Cause: err}
	}
	return cp, nil
}

// SaveCheckpoint queues a source progress boundary after earlier admitted events
// and waits for its batch to commit. Event writes and checkpoint saves share
// the same ordered queue.
func (b *Buffer) SaveCheckpoint(cp checkpoint.Checkpoint) error {
	b.mu.Lock()
	if err := b.stateError(); err != nil {
		b.mu.Unlock()
		return err
	}
	if err := checkpoint.Validate(cp); err != nil {
		b.mu.Unlock()
		return err
	}
	receipt := make(chan error, 1)
	b.pending = append(b.pending, &writeRequest{checkpoint: cp, receipt: receipt})
	b.mu.Unlock()
	b.notifyBatcher()
	return <-receipt
}

// stateError requires mu to be held by the caller.
func (b *Buffer) stateError() error {
	if b.failure != nil {
		return b.failure
	}
	if b.closed {
		return fmt.Errorf("buffer is closed")
	}
	return nil
}

// Delete removes an event from the buffer.
func (b *Buffer) Delete(key string) error {
	b.mu.RLock()
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return err
	}
	b.mu.RUnlock()

	if isMetadataKey([]byte(key)) {
		return fmt.Errorf("cannot delete buffer metadata as an event")
	}

	if err := b.applyBatch(func(batch pebbleBatch) error {
		if err := batch.Delete([]byte(key), pebble.Sync); err != nil {
			return fmt.Errorf("failed to batch delete: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
	}
	return nil
}

// DeleteBefore deletes all events with keys before the given key.
// Returns the number of events deleted.
func (b *Buffer) DeleteBefore(beforeKey string) (int, error) {
	b.mu.RLock()
	if err := b.stateError(); err != nil {
		b.mu.RUnlock()
		return 0, err
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

	batch := b.newBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if isMetadataKey(iter.Key()) {
			continue
		}
		if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
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

func isMetadataKey(key []byte) bool {
	return bytes.Equal(key, checkpointKeyBytes) || bytes.Equal(key, formatKeyBytes)
}

// initializeFormat never assigns a new format to existing unversioned data.
func initializeFormat(db *pebble.DB) error {
	value, closer, err := db.Get(formatKeyBytes)
	if err == nil {
		defer closer.Close()
		if string(value) != cacheFormatVersion {
			return &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "unsupported buffer cache format"}
		}
		return nil
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("failed to read buffer format: %w", err)
	}
	iter, err := db.NewIter(nil)
	if err != nil {
		return fmt.Errorf("failed to inspect buffer format: %w", err)
	}
	nonempty := iter.First()
	err = errors.Join(iter.Error(), iter.Close())
	if err != nil {
		return fmt.Errorf("failed to inspect buffer format: %w", err)
	}
	if nonempty {
		return &checkpoint.Error{Code: checkpoint.IncompatibleState, Message: "unversioned buffer cache requires explicit replacement"}
	}
	if err := db.Set(formatKeyBytes, []byte(cacheFormatVersion), pebble.Sync); err != nil {
		return fmt.Errorf("failed to initialize buffer format: %w", err)
	}
	return nil
}
