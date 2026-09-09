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
	"go.mongodb.org/mongo-driver/bson"
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
	// capacityCh is replaced after a batch releases queue capacity.
	capacityCh chan struct{}

	// mu protects pending, flushing, capacityCh, closed, and failure
	mu sync.RWMutex

	// closed indicates if the buffer is closed
	closed  bool
	failure error

	// shutdownOnce ensures resources are fewer closed exactly once
	shutdownOnce sync.Once

	// batcher manages batched writes

	batchSize     int
	batchInterval time.Duration
	queueSize     int
	closeCh       chan struct{}
	batcherWG     sync.WaitGroup
}

const checkpointKey = "!checkpoint/resume_token"

var checkpointKeyBytes = []byte(checkpointKey)

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

	// QueueSize limits the total queued and flushing events.
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
		capacityCh:    make(chan struct{}),
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

// Close closes the buffer.
func (b *Buffer) Close() error {
	b.mu.Lock()
	wasClosed := b.closed
	b.closed = true
	b.mu.Unlock()

	if !wasClosed {
		// Signal batcher to stop
		close(b.closeCh)
	}

	// Wait for batcher to finish flushing all pending writes.
	b.batcherWG.Wait()

	b.shutdownOnce.Do(func() {
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
			b.mu.Lock()
			b.failure = errors.Join(b.failure, fmt.Errorf("failed to close pebble database: %w", closeErr))
			b.mu.Unlock()
		}
	})

	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.failure
}

// Path returns the buffer storage path.
func (b *Buffer) Path() string {
	return b.path
}

// LoadCheckpoint returns the last saved checkpoint token.
func (b *Buffer) LoadCheckpoint() (bson.Raw, error) {
	b.mu.RLock()
	if b.closed {
		err := b.failure
		b.mu.RUnlock()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	value, closer, err := b.db.Get(checkpointKeyBytes)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	defer closer.Close()

	copied := append([]byte(nil), value...)
	return bson.Raw(copied), nil
}

// SaveCheckpoint writes the checkpoint token without an accompanying event.
func (b *Buffer) SaveCheckpoint(token bson.Raw) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	if token == nil {
		return nil
	}

	if err := b.applyBatch(func(batch pebbleBatch) error {
		if err := batch.Set(checkpointKeyBytes, token, pebble.Sync); err != nil {
			return fmt.Errorf("failed to batch write checkpoint: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	return nil
}

// DeleteCheckpoint deletes the checkpoint token.
func (b *Buffer) DeleteCheckpoint() error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	if err := b.applyBatch(func(batch pebbleBatch) error {
		if err := batch.Delete(checkpointKeyBytes, pebble.Sync); err != nil {
			return fmt.Errorf("failed to batch delete checkpoint: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to delete checkpoint: %w", err)
	}

	return nil
}

// Delete removes an event from the buffer.
func (b *Buffer) Delete(key string) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

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

	batch := b.newBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if isCheckpointKey(iter.Key()) {
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

func isCheckpointKey(key []byte) bool {
	return bytes.Equal(key, checkpointKeyBytes)
}
