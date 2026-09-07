// Package buffer owns a durable, ordered event log for one source.
package buffer

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/google/uuid"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const formatVersion = 1

var stateKey = []byte("meta/state")

type Options struct {
	Path, SourceID, SourceScope string
	BatchSize                   int
	BatchInterval               time.Duration
	QueueSize                   int
	QueueBytes, BatchBytes      int64
	Logger                      *slog.Logger
	OnCommit                    func(CommittedBatch)
	OnError                     func(error)
	newBatch                    func(*pebble.DB) pebbleBatch
}

type State struct {
	Position         cursor.Position      `json:"position"`
	DiscardedThrough uint64               `json:"discardedThrough"`
	ResumeToken      bson.Raw             `json:"resumeToken"`
	StartAt          *primitive.Timestamp `json:"startAt,omitempty"`
	RetainedBytes    int64                `json:"retainedBytes"`
	Discontinuous    bool                 `json:"discontinuous"`
}

type Record struct {
	Position     cursor.Position
	Event        *events.StoreChangeEvent
	EncodedBytes int64
}

type CommittedBatch struct {
	Records []Record
	State   State
}
type Page struct {
	Records []Record
	Through cursor.Position
}

type diskState struct {
	Version     int    `json:"version"`
	SourceScope string `json:"sourceScope"`
	State       State  `json:"state"`
}
type diskRecord struct {
	Sequence uint64          `json:"sequence"`
	Token    bson.Raw        `json:"token"`
	Event    json.RawMessage `json:"event"`
}
type identityRecord struct {
	Sequence uint64   `json:"sequence"`
	Token    bson.Raw `json:"token"`
}
type pebbleBatch interface {
	Set([]byte, []byte, *pebble.WriteOptions) error
	Delete([]byte, *pebble.WriteOptions) error
	Commit(*pebble.WriteOptions) error
	Close() error
}

type Buffer struct {
	db       *pebble.DB
	opts     Options
	newBatch func() pebbleBatch
	mu       sync.RWMutex
	state    State
	snapshot *pebble.Snapshot
	pending  []*writeRequest
	count    int
	admitted uint64
	bytes    int64
	closing  bool
	closed   bool
	err      error
	changed  chan struct{}
	wake     chan struct{}
	commands chan mutation
	done     chan struct{}
}

func New(opts Options) (*Buffer, error) {
	if opts.Path == "" || opts.SourceID == "" || opts.SourceScope == "" {
		return nil, fmt.Errorf("buffer path, source identity and scope are required")
	}
	if opts.BatchSize <= 0 || opts.BatchInterval <= 0 || opts.QueueSize <= 0 || opts.QueueBytes <= 0 || opts.BatchBytes <= 0 || opts.BatchBytes > opts.QueueBytes {
		return nil, fmt.Errorf("buffer batching and queue budgets must be positive; batch bytes cannot exceed queue bytes")
	}
	db, err := pebble.Open(opts.Path, &pebble.Options{})
	if err != nil {
		return nil, domain("STORAGE_FAILURE", State{}, opts.SourceID, err)
	}
	b := &Buffer{db: db, opts: opts, changed: make(chan struct{}), wake: make(chan struct{}, 1), commands: make(chan mutation), done: make(chan struct{})}
	b.newBatch = func() pebbleBatch { return db.NewBatch() }
	if opts.newBatch != nil {
		b.newBatch = func() pebbleBatch { return opts.newBatch(db) }
	}
	if err := b.initialize(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	b.snapshot = db.NewSnapshot()
	go b.run()
	return b, nil
}

func domain(code string, s State, source string, cause error) error {
	return &events.Error{Code: events.ErrorCode(code), SourceID: source, Generation: s.Position.Generation, DiscardedThrough: s.DiscardedThrough, CommittedThrough: s.Position.Sequence, Cause: cause}
}
func cloneState(s State) State {
	s.ResumeToken = bytes.Clone(s.ResumeToken)
	if s.StartAt != nil {
		startAt := *s.StartAt
		s.StartAt = &startAt
	}
	return s
}
func decode(value []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(value))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		if err != nil {
			return fmt.Errorf("invalid trailing stored data: %w", err)
		}
		return errors.New("unexpected trailing stored data")
	}
	return nil
}

func (b *Buffer) initialize() error {
	value, closer, err := b.db.Get(stateKey)
	if errors.Is(err, pebble.ErrNotFound) {
		iter, err := b.db.NewIter(nil)
		if err != nil {
			return b.storageError(err)
		}
		nonempty := iter.First()
		iterErr := errors.Join(iter.Error(), iter.Close())
		if iterErr != nil {
			return b.storageError(iterErr)
		}
		if nonempty {
			return domain("UNSUPPORTED_FORMAT", b.state, b.opts.SourceID, errors.New("nonempty event store has no versioned metadata; explicit reset or migration is required"))
		}
		generation, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		b.state.Position = cursor.Position{SourceID: b.opts.SourceID, Generation: generation.String()}
		if err := b.commit(func(batch pebbleBatch) error { return b.storeState(batch, b.state) }); err != nil {
			return b.storageError(err)
		}
		return nil
	}
	if err != nil {
		return b.storageError(err)
	}
	var persisted diskState
	decodeErr := decode(value, &persisted)
	closeErr := closer.Close()
	if decodeErr != nil || persisted.Version != formatVersion {
		return domain("UNSUPPORTED_FORMAT", b.state, b.opts.SourceID, errors.Join(decodeErr, closeErr, errors.New("unsupported event store metadata")))
	}
	if closeErr != nil {
		return b.storageError(closeErr)
	}
	b.state = persisted.State
	if b.state.Position.SourceID != b.opts.SourceID || persisted.SourceScope != b.opts.SourceScope {
		return domain("UNKNOWN_SOURCE", b.state, b.opts.SourceID, errors.New("stored source identity or watched scope does not match configuration"))
	}
	if _, err := uuid.Parse(b.state.Position.Generation); err != nil {
		return b.storageError(fmt.Errorf("invalid stored generation: %w", err))
	}
	if b.state.Discontinuous {
		return domain("CONTINUITY_LOST", b.state, b.opts.SourceID, errors.New("event history requires explicit reset or rebuild"))
	}
	if b.state.DiscardedThrough > b.state.Position.Sequence || b.state.RetainedBytes < 0 || (b.state.Position.Sequence > 0 && len(b.state.ResumeToken) == 0) {
		return b.storageError(errors.New("inconsistent stored event frontier"))
	}
	if len(b.state.ResumeToken) > 0 {
		if err := b.state.ResumeToken.Validate(); err != nil {
			return b.storageError(fmt.Errorf("invalid stored resume token: %w", err))
		}
	}
	if b.state.StartAt != nil && (b.state.StartAt.T == 0 || len(b.state.ResumeToken) != 0 || b.state.Position.Sequence != 0) {
		return b.storageError(errors.New("stored initial boundary conflicts with the event frontier"))
	}
	return b.verifyLog()
}
func eventKey(generation string, sequence uint64) []byte {
	return binary.BigEndian.AppendUint64([]byte("event/"+generation+"/"), sequence)
}
func identityKey(generation, id string) []byte { return []byte("identity/" + generation + "/" + id) }
func (b *Buffer) storeState(batch pebbleBatch, s State) error {
	value, err := json.Marshal(diskState{Version: formatVersion, SourceScope: b.opts.SourceScope, State: s})
	if err != nil {
		return err
	}
	return batch.Set(stateKey, value, nil)
}
func (b *Buffer) commit(write func(pebbleBatch) error) error {
	batch := b.newBatch()
	err := write(batch)
	if err == nil {
		err = batch.Commit(pebble.Sync)
	}
	return errors.Join(err, batch.Close())
}
func (b *Buffer) storageError(err error) error {
	return domain("STORAGE_FAILURE", b.state, b.opts.SourceID, err)
}
func (b *Buffer) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
	select {
	case b.wake <- struct{}{}:
	default:
	}
}
func (b *Buffer) State() (State, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.err != nil {
		return State{}, b.err
	}
	if b.closed {
		return State{}, errors.New("event buffer is closed")
	}
	return cloneState(b.state), nil
}
func (b *Buffer) LoadCheckpoint() (bson.Raw, error) { s, err := b.State(); return s.ResumeToken, err }
func (b *Buffer) Path() string                      { return b.opts.Path }
func (b *Buffer) Err() error                        { b.mu.RLock(); defer b.mu.RUnlock(); return b.err }
func (b *Buffer) Done() <-chan struct{}             { return b.done }

// Close leaves the mutation worker responsible for an in-flight Sync even when
// the caller stops waiting. Reopening the same path cannot race that worker.
func (b *Buffer) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closing = true
	b.notifyLocked()
	b.mu.Unlock()
	select {
	case <-b.done:
		return b.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *Buffer) fail(err error) {
	b.mu.Lock()
	if b.err != nil {
		b.mu.Unlock()
		return
	}
	b.err = err
	b.closing = true
	b.notifyLocked()
	b.mu.Unlock()
	if b.opts.OnError != nil {
		b.opts.OnError(err)
	}
}
func (b *Buffer) finish() {
	b.mu.Lock()
	var err error
	if b.snapshot != nil {
		err = b.snapshot.Close()
		b.snapshot = nil
	}
	err = errors.Join(err, b.db.Close())
	b.pending = nil
	b.count, b.bytes = 0, 0
	b.closed = true
	b.notifyLocked()
	b.mu.Unlock()
	if err != nil {
		b.fail(b.storageError(err))
	}
	close(b.done)
}
func (b *Buffer) publish(s State, records []Record) error {
	snapshot := b.db.NewSnapshot()
	b.mu.Lock()
	previous := b.snapshot
	b.snapshot = snapshot
	b.state = cloneState(s)
	// ReadPage holds the read lock until its bounded page has been copied.
	err := previous.Close()
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if b.opts.OnCommit != nil {
		b.opts.OnCommit(CommittedBatch{Records: records, State: cloneState(s)})
	}
	return nil
}
