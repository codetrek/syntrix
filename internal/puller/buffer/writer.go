package buffer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type writeRequest struct {
	event      json.RawMessage
	token      bson.Raw
	id         string
	credit     int64
	admittedAt time.Time
	admission  uint64
}

type mutationKind int

const (
	mutationRetention mutationKind = iota
	mutationDiscontinuity
	mutationFlush
	mutationInitialize
)

type mutation struct {
	kind           mutationKind
	cutoff         time.Time
	maxBytes       int64
	discontinuity  error
	through        uint64
	initialToken   bson.Raw
	initialStartAt *primitive.Timestamp
	result         chan mutationResult
}
type mutationResult struct {
	state State
	err   error
}

func eventIdentity(source string, token bson.Raw) string {
	hash := sha256.New()
	hash.Write([]byte("syntrix-puller-event-v1"))
	hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(source))))
	hash.Write([]byte(source))
	hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(token))))
	hash.Write(token)
	return "v1-" + hex.EncodeToString(hash.Sum(nil))
}

// Enqueue freezes the accepted input before returning. Admission credits remain
// owned until Sync and the bounded publication callback have both completed.
func (b *Buffer) Enqueue(ctx context.Context, evt *events.StoreChangeEvent, token bson.Raw) error {
	if evt == nil {
		return errors.New("event is required")
	}
	if len(token) == 0 {
		return errors.New("event resume token is required")
	}
	if err := token.Validate(); err != nil {
		return fmt.Errorf("invalid event resume token: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		if b.err != nil {
			err := b.err
			b.mu.Unlock()
			return err
		}
		if b.closing {
			b.mu.Unlock()
			return errors.New("event buffer is closing")
		}
		changed := b.changed
		if b.count >= b.opts.QueueSize {
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		// Encoding is serialized with admission so waiting callers cannot retain an
		// unbounded collection of frozen payloads outside the configured budget.
		request, err := b.freeze(evt, token)
		if err != nil {
			b.mu.Unlock()
			return err
		}
		if request.credit > b.opts.BatchBytes || request.credit > b.opts.QueueBytes {
			err := domain("OVERLOADED", b.state, b.opts.SourceID, fmt.Errorf("encoded record requires %d bytes, exceeding the configured batch or queue budget", request.credit))
			b.mu.Unlock()
			return err
		}
		if request.credit > b.opts.QueueBytes-b.bytes {
			request = nil
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		request.admittedAt = time.Now()
		b.admitted++
		request.admission = b.admitted
		b.pending = append(b.pending, request)
		b.count++
		b.bytes += request.credit
		b.notifyLocked()
		b.mu.Unlock()
		return nil
	}
}

func (b *Buffer) freeze(evt *events.StoreChangeEvent, token bson.Raw) (*writeRequest, error) {
	frozen := *evt
	frozen.EventID = eventIdentity(b.opts.SourceID, token)
	value, err := json.Marshal(&frozen)
	if err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	ownedToken := bytes.Clone(token)
	record, err := json.Marshal(diskRecord{Sequence: math.MaxUint64, Event: value, Token: ownedToken})
	if err != nil {
		return nil, err
	}
	identity, err := json.Marshal(identityRecord{Sequence: math.MaxUint64, Token: ownedToken})
	if err != nil {
		return nil, err
	}
	credit := int64(len(record) + len(identity) + len(eventKey(b.state.Position.Generation, math.MaxUint64)) + len(identityKey(b.state.Position.Generation, frozen.EventID)))
	return &writeRequest{event: value, token: ownedToken, id: frozen.EventID, credit: credit}, nil
}

func (b *Buffer) run() {
	defer b.finish()
	for {
		select {
		case cmd := <-b.commands:
			if b.execute(cmd) {
				return
			}
		default:
		}
		b.mu.Lock()
		if b.err != nil {
			b.mu.Unlock()
			return
		}
		count := 0
		var size int64
		for _, req := range b.pending {
			if count == b.opts.BatchSize || req.credit > b.opts.BatchBytes-size {
				break
			}
			size += req.credit
			count++
		}
		full := count > 0 && (count == b.opts.BatchSize || count < len(b.pending) || size == b.opts.BatchBytes)
		var delay time.Duration
		if count > 0 {
			delay = time.Until(b.pending[0].admittedAt.Add(b.opts.BatchInterval))
		}
		closing := b.closing
		if count > 0 && (full || closing || delay <= 0) {
			requests := b.takeLocked(count)
			b.mu.Unlock()
			if err := b.appendAndRelease(requests); err != nil {
				b.fail(err)
				return
			}
			continue
		}
		if closing && count == 0 {
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()

		var timer *time.Timer
		var timerCh <-chan time.Time
		if count > 0 {
			timer = time.NewTimer(delay)
			timerCh = timer.C
		}
		select {
		case cmd := <-b.commands:
			if timer != nil {
				timer.Stop()
			}
			if b.execute(cmd) {
				return
			}
		case <-b.wake:
			if timer != nil {
				timer.Stop()
			}
		case <-timerCh:
		}
	}
}

func (b *Buffer) append(requests []*writeRequest) error {
	state := cloneState(b.state)
	seen := make(map[string]bson.Raw)
	records := make([]Record, 0, len(requests))
	err := b.commit(func(batch pebbleBatch) error {
		for _, req := range requests {
			if existing, ok := seen[req.id]; ok {
				if !bytes.Equal(existing, req.token) {
					return errors.New("event identity digest collision within batch")
				}
				continue
			}
			seen[req.id] = req.token
			if state.Position.Sequence > 0 && bytes.Equal(req.token, state.ResumeToken) {
				continue
			}
			ikey := identityKey(state.Position.Generation, req.id)
			value, closer, err := b.db.Get(ikey)
			if err == nil {
				var existing identityRecord
				decodeErr := decode(value, &existing)
				closeErr := closer.Close()
				if err := errors.Join(decodeErr, closeErr); err != nil {
					return err
				}
				if !bytes.Equal(existing.Token, req.token) {
					return errors.New("event identity digest collision with persisted event")
				}
				if existing.Sequence <= state.DiscardedThrough || existing.Sequence > state.Position.Sequence {
					return errors.New("identity mapping points outside retained log")
				}
				continue
			}
			if !errors.Is(err, pebble.ErrNotFound) {
				return err
			}
			if state.Position.Sequence == math.MaxUint64 {
				return errors.New("event sequence exhausted; explicit generation reset is required")
			}
			state.Position.Sequence++
			key := eventKey(state.Position.Generation, state.Position.Sequence)
			recordBytes, err := storeJSON(batch, key, diskRecord{Sequence: state.Position.Sequence, Token: req.token, Event: req.event})
			if err != nil {
				return err
			}
			identityBytes, err := storeJSON(batch, ikey, identityRecord{Sequence: state.Position.Sequence, Token: req.token})
			if err != nil {
				return err
			}
			state.RetainedBytes += int64(len(key)+len(ikey)) + recordBytes + identityBytes

			state.ResumeToken = bytes.Clone(req.token)
			state.StartAt = nil
			var event events.StoreChangeEvent
			if err := json.Unmarshal(req.event, &event); err != nil {
				return err
			}
			records = append(records, Record{Position: state.Position, Event: &event, EncodedBytes: int64(len(req.event))})
		}
		return b.storeState(batch, state)
	})
	if err != nil {
		return err
	}
	return b.publish(state, records)
}

func (b *Buffer) takeLocked(count int) []*writeRequest {
	requests := append([]*writeRequest(nil), b.pending[:count]...)
	copy(b.pending, b.pending[count:])
	clear(b.pending[len(b.pending)-count:])
	b.pending = b.pending[:len(b.pending)-count]
	return requests
}

func (b *Buffer) appendAndRelease(requests []*writeRequest) error {
	if err := b.append(requests); err != nil {
		return b.storageError(err)
	}
	b.mu.Lock()
	for _, request := range requests {
		b.count--
		b.bytes -= request.credit
	}
	b.notifyLocked()
	b.mu.Unlock()
	return nil
}

func (b *Buffer) flushThrough(through uint64) error {
	for {
		b.mu.Lock()
		count := 0
		var size int64
		for _, request := range b.pending {
			if request.admission > through || count == b.opts.BatchSize || request.credit > b.opts.BatchBytes-size {
				break
			}
			size += request.credit
			count++
		}
		if count == 0 {
			b.mu.Unlock()
			return nil
		}
		requests := b.takeLocked(count)
		b.mu.Unlock()
		if err := b.appendAndRelease(requests); err != nil {
			return err
		}
	}
}

func (b *Buffer) execute(command mutation) bool {
	err := b.mutate(command)
	if err != nil {
		b.fail(err)
	}
	command.result <- mutationResult{state: cloneState(b.state), err: err}
	return err != nil
}

func (b *Buffer) request(ctx context.Context, command mutation) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	b.mu.RLock()
	err := b.err
	closing := b.closing
	command.through = b.admitted
	b.mu.RUnlock()
	if err != nil {
		return State{}, err
	}
	if closing {
		return State{}, errors.New("event buffer is closing")
	}
	command.result = make(chan mutationResult, 1)
	select {
	case b.commands <- command:
	case <-ctx.Done():
		return State{}, ctx.Err()
	case <-b.done:
		return State{}, b.terminalError()
	}
	// The worker owns an accepted command through its buffered reply, including
	// shutdown and failure. Cancellation only releases this caller's wait.
	select {
	case result := <-command.result:
		return result.state, result.err
	case <-ctx.Done():
		return State{}, ctx.Err()
	}
}

func (b *Buffer) terminalError() error {
	if err := b.Err(); err != nil {
		return err
	}
	return errors.New("event buffer is closed")
}

// Flush establishes the resume boundary before reopening the source stream.
// Every admission before the call must finish Sync and publication first; this
// prevents source replay from overtaking originals whose identities are pruned.
func (b *Buffer) Flush(ctx context.Context) (State, error) {
	return b.request(ctx, mutation{kind: mutationFlush})
}

// InitializeBoundary records where an empty source history begins. An existing
// boundary or event checkpoint is authoritative and cannot be overwritten by
// a later initialization attempt.
func (b *Buffer) InitializeBoundary(ctx context.Context, token bson.Raw, startAt *primitive.Timestamp) error {
	if (len(token) > 0) == (startAt != nil) {
		return errors.New("exactly one initial resume token or operation time is required")
	}
	if len(token) > 0 {
		if err := token.Validate(); err != nil {
			return fmt.Errorf("invalid initial resume token: %w", err)
		}
	}
	var ownedStartAt *primitive.Timestamp
	if startAt != nil {
		if startAt.T == 0 {
			return errors.New("initial operation time must have a nonzero timestamp")
		}
		value := *startAt
		ownedStartAt = &value
	}
	_, err := b.request(ctx, mutation{kind: mutationInitialize, initialToken: bytes.Clone(token), initialStartAt: ownedStartAt})
	return err
}

func (b *Buffer) MarkDiscontinuous(ctx context.Context, cause error) error {
	if cause == nil {
		return errors.New("continuity loss cause is required")
	}
	_, err := b.request(ctx, mutation{kind: mutationDiscontinuity, discontinuity: cause})
	return err
}
func (b *Buffer) Retain(ctx context.Context, cutoff time.Time, maxRetainedBytes int64) error {
	if maxRetainedBytes < 0 {
		return errors.New("retained byte budget cannot be negative")
	}
	_, err := b.request(ctx, mutation{kind: mutationRetention, cutoff: cutoff, maxBytes: maxRetainedBytes})
	return err
}

func (b *Buffer) mutate(cmd mutation) error {
	switch cmd.kind {
	case mutationFlush:
		return b.flushThrough(cmd.through)
	case mutationInitialize:
		if err := b.flushThrough(cmd.through); err != nil {
			return err
		}
		state := cloneState(b.state)
		if state.Position.Sequence != 0 || len(state.ResumeToken) != 0 || state.StartAt != nil {
			return nil
		}
		state.ResumeToken = cmd.initialToken
		state.StartAt = cmd.initialStartAt
		if err := b.commit(func(batch pebbleBatch) error { return b.storeState(batch, state) }); err != nil {
			return b.storageError(err)
		}
		if err := b.publish(state, nil); err != nil {
			return b.storageError(err)
		}
		return nil
	case mutationDiscontinuity:
		state := cloneState(b.state)
		state.Discontinuous = true
		if err := b.commit(func(batch pebbleBatch) error { return b.storeState(batch, state) }); err != nil {
			return b.storageError(err)
		}
		if err := b.publish(state, nil); err != nil {
			return b.storageError(err)
		}
		return domain("CONTINUITY_LOST", state, b.opts.SourceID, cmd.discontinuity)
	case mutationRetention:
		if err := b.retain(cmd.cutoff, cmd.maxBytes); err != nil {
			return b.storageError(err)
		}
		return nil
	default:
		return b.storageError(errors.New("unknown buffer mutation"))
	}
}
