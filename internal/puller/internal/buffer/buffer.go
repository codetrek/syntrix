// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/codetrek/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
)

// Buffer stores events in PebbleDB for durability and replay.
type Buffer struct {
	db       *pebble.DB
	path     string
	logger   *slog.Logger
	newBatch func() pebbleBatch
	writeCh  chan *writeRequest

	// mu protects writes
	mu sync.RWMutex

	// closed indicates if the buffer is closed
	closed bool

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
	}
	buf.writeCh = make(chan *writeRequest, buf.queueSize)
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

// Write stores an event and updates the checkpoint in the same batch.
func (b *Buffer) Write(evt *events.ChangeEvent, token bson.Raw) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("buffer is closed")
	}
	b.mu.RUnlock()

	if len(token) == 0 {
		return fmt.Errorf("checkpoint token is required")
	}

	// Key is the buffer key for ordering
	key := []byte(evt.BufferKey())

	// Value is the JSON-encoded event
	value, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	req := &writeRequest{
		key:   key,
		value: value,
		token: append([]byte(nil), token...),
	}

	select {
	case b.writeCh <- req:
		return nil
	case <-b.closeCh:
		return fmt.Errorf("buffer is closed")
	}
}

// Read retrieves an event by its buffer key.
func (b *Buffer) Read(key string) (*events.ChangeEvent, error) {
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

	var evt events.ChangeEvent
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
	Event() *events.ChangeEvent

	// Key returns the current buffer key.
	Key() string

	// Err returns any error encountered during iteration.
	Err() error

	// Close releases the iterator resources.
	Close() error
}

type bufferIterator struct {
	iter  *pebble.Iterator
	evt   *events.ChangeEvent
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
			return false
		}

		if isCheckpointKey(i.iter.Key()) {
			continue
		}

		i.key = string(i.iter.Key())
		value := i.iter.Value()

		var evt events.ChangeEvent
		if err := json.Unmarshal(value, &evt); err != nil {
			i.err = fmt.Errorf("failed to unmarshal event: %w", err)
			return false
		}

		i.evt = &evt
		return true
	}
}

func (i *bufferIterator) Event() *events.ChangeEvent {
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
		return i.iter.Close()
	}
	return nil
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

	for iter.Last(); iter.Valid(); iter.Prev() {
		if isCheckpointKey(iter.Key()) {
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
		if isCheckpointKey(iter.Key()) {
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
		if isCheckpointKey(iter.Key()) {
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
		if isCheckpointKey(iter.Key()) {
			continue
		}
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

	close(b.closeCh)
	close(b.writeCh)
	b.batcherWG.Wait()

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
		return fmt.Errorf("failed to close pebble database: %w", closeErr)
	}

	return nil
}

// Path returns the buffer storage path.
func (b *Buffer) Path() string {
	return b.path
}

// LoadCheckpoint returns the last saved checkpoint token.
func (b *Buffer) LoadCheckpoint() (bson.Raw, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
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

func (b *Buffer) applyBatch(apply func(batch pebbleBatch) error) error {
	batch := b.newBatch()
	defer batch.Close()

	if err := apply(batch); err != nil {
		return err
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit batch: %w", err)
	}

	return nil
}

func isCheckpointKey(key []byte) bool {
	return bytes.Equal(key, checkpointKeyBytes)
}

type writeRequest struct {
	key   []byte
	value []byte
	token bson.Raw
}

func (b *Buffer) startBatcher() {
	b.batcherWG.Add(1)
	go b.runBatcher()
}

func (b *Buffer) runBatcher() {
	defer b.batcherWG.Done()

	ticker := time.NewTicker(b.batchInterval)
	defer ticker.Stop()

	var pending []*writeRequest

	flush := func() {
		if len(pending) == 0 {
			return
		}

		batch := b.newBatch()
		var commitErr error
		var checkpointToken []byte

		for _, req := range pending {
			if err := batch.Set(req.key, req.value, pebble.Sync); err != nil {
				commitErr = fmt.Errorf("failed to batch write event: %w", err)
				break
			}
			if req.token != nil {
				checkpointToken = append([]byte(nil), req.token...)
			}
		}

		if commitErr == nil && checkpointToken != nil {
			if err := batch.Set(checkpointKeyBytes, checkpointToken, pebble.Sync); err != nil {
				commitErr = fmt.Errorf("failed to batch write checkpoint: %w", err)
			}
		}

		if commitErr == nil {
			if err := batch.Commit(pebble.Sync); err != nil {
				commitErr = fmt.Errorf("failed to commit batch: %w", err)
			}
		}

		_ = batch.Close()

		if commitErr != nil {
			b.logger.Error("failed to flush batch, stopping batcher", "error", commitErr)
			b.mu.Lock()
			if !b.closed {
				b.closed = true
				close(b.closeCh)
			}
			b.mu.Unlock()
			return
		}

		pending = pending[:0]
	}

	for {
		select {
		case req, ok := <-b.writeCh:
			if !ok {
				flush()
				return
			}
			pending = append(pending, req)
			if len(pending) >= b.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.closeCh:
			for {
				select {
				case req, ok := <-b.writeCh:
					if !ok {
						flush()
						return
					}
					pending = append(pending, req)
				default:
					flush()
					return
				}
			}
		}
	}
}
