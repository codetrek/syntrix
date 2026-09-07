package buffer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func eventRange(generation string, after uint64) *pebble.IterOptions {
	return &pebble.IterOptions{LowerBound: eventKey(generation, after+1), UpperBound: []byte("event/" + generation + "0")}
}

func (b *Buffer) validatePosition(position cursor.Position, state State) error {
	if position.SourceID != state.Position.SourceID {
		return domain("UNKNOWN_SOURCE", state, b.opts.SourceID, errors.New("cursor belongs to another source"))
	}
	if position.Generation != state.Position.Generation {
		return domain("GENERATION_MISMATCH", state, b.opts.SourceID, errors.New("cursor belongs to another log generation"))
	}
	if position.Sequence < state.DiscardedThrough {
		return domain("HISTORY_EXPIRED", state, b.opts.SourceID, errors.New("cursor precedes retained event history"))
	}
	if position.Sequence > state.Position.Sequence {
		return domain("POSITION_AHEAD", state, b.opts.SourceID, errors.New("cursor exceeds the committed event frontier"))
	}
	return nil
}

// ReadPage holds a short safe snapshot lease only while copying a bounded page.
// Pebble's in-flight memtable visibility is never a replay authority.
func (b *Buffer) ReadPage(ctx context.Context, after, through cursor.Position, maxEvents int, maxBytes int64) (Page, error) {
	page := Page{Through: after}
	if maxEvents <= 0 || maxBytes <= 0 {
		return page, errors.New("page event and byte limits must be positive")
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.err != nil {
		return page, b.err
	}
	if b.closed {
		return page, errors.New("event buffer is closed")
	}
	if b.state.Discontinuous {
		return page, domain("CONTINUITY_LOST", b.state, b.opts.SourceID, errors.New("event history is discontinuous"))
	}
	if err := b.validatePosition(after, b.state); err != nil {
		return page, err
	}
	if err := b.validatePosition(through, b.state); err != nil {
		return page, err
	}
	if through.Sequence < after.Sequence {
		return page, domain("INVALID_CURSOR", b.state, b.opts.SourceID, errors.New("replay ceiling precedes starting position"))
	}
	if after.Sequence == through.Sequence {
		return page, nil
	}
	iter, err := b.snapshot.NewIter(eventRange(after.Generation, after.Sequence))
	if err != nil {
		return page, b.storageError(err)
	}
	var bytesRead int64
	var readErr error
	for valid := iter.First(); valid && len(page.Records) < maxEvents; valid = iter.Next() {
		if err := ctx.Err(); err != nil {
			readErr = err
			break
		}
		expected := page.Through.Sequence + 1
		if expected > through.Sequence {
			break
		}
		disk, event, err := readRecord(iter.Key(), iter.Value(), after.Generation, expected, b.opts.SourceID)
		if err != nil {
			readErr = b.storageError(err)
			break
		}
		size := int64(len(disk.Event))
		if size > maxBytes-bytesRead {
			if len(page.Records) == 0 {
				readErr = domain("OVERLOADED", b.state, b.opts.SourceID, fmt.Errorf("event requires %d bytes, exceeding replay page budget %d", size, maxBytes))
			}
			break
		}
		position := after
		position.Sequence = expected
		page.Records = append(page.Records, Record{Position: position, Event: event, EncodedBytes: size})
		bytesRead += size
		page.Through = position
		if expected == through.Sequence {
			break
		}
	}
	if readErr == nil && len(page.Records) == 0 && page.Through.Sequence < through.Sequence {
		readErr = b.storageError(errors.New("committed event sequence is missing"))
	}
	if err := errors.Join(iter.Error(), iter.Close()); err != nil {
		readErr = errors.Join(readErr, b.storageError(err))
	}
	return page, readErr
}

func readRecord(key, value []byte, generation string, expected uint64, source string) (diskRecord, *events.StoreChangeEvent, error) {
	var disk diskRecord
	if !bytes.Equal(key, eventKey(generation, expected)) {
		return disk, nil, fmt.Errorf("committed event sequence %d is missing", expected)
	}
	if err := decode(value, &disk); err != nil {
		return disk, nil, err
	}
	if disk.Sequence != expected {
		return disk, nil, errors.New("stored event position disagrees with its key")
	}
	if err := disk.Token.Validate(); err != nil {
		return disk, nil, fmt.Errorf("invalid stored event identity: %w", err)
	}
	var event events.StoreChangeEvent
	if err := json.Unmarshal(disk.Event, &event); err != nil {
		return disk, nil, err
	}
	if event.EventID != eventIdentity(source, disk.Token) {
		return disk, nil, errors.New("stored event identity does not match source token")
	}
	return disk, &event, nil
}

func (b *Buffer) verifyLog() error {
	if b.state.DiscardedThrough == math.MaxUint64 {
		if b.state.RetainedBytes != 0 {
			return b.storageError(errors.New("empty retained log has nonzero byte accounting"))
		}
		return nil
	}
	iter, err := b.db.NewIter(eventRange(b.state.Position.Generation, b.state.DiscardedThrough))
	if err != nil {
		return b.storageError(err)
	}
	sequence := b.state.DiscardedThrough
	var size int64
	var checkErr error
	for valid := iter.First(); valid; valid = iter.Next() {
		if sequence == b.state.Position.Sequence {
			checkErr = errors.New("event record exceeds stored frontier")
			break
		}
		sequence++
		disk, event, err := readRecord(iter.Key(), iter.Value(), b.state.Position.Generation, sequence, b.opts.SourceID)
		if err != nil {
			checkErr = err
			break
		}
		ikey := identityKey(b.state.Position.Generation, event.EventID)
		value, closer, err := b.db.Get(ikey)
		if err != nil {
			checkErr = err
			break
		}
		var identity identityRecord
		decodeErr := decode(value, &identity)
		size += int64(len(iter.Key()) + len(iter.Value()) + len(ikey) + len(value))
		closeErr := closer.Close()
		if err := errors.Join(decodeErr, closeErr); err != nil {
			checkErr = err
			break
		}
		if identity.Sequence != sequence || !bytes.Equal(identity.Token, disk.Token) {
			checkErr = errors.New("event identity mapping is inconsistent")
			break
		}
		if sequence == b.state.Position.Sequence && !bytes.Equal(disk.Token, b.state.ResumeToken) {
			checkErr = errors.New("resume token disagrees with committed frontier")
			break
		}
	}
	checkErr = errors.Join(checkErr, iter.Error(), iter.Close())
	if checkErr != nil {
		return b.storageError(checkErr)
	}
	if sequence != b.state.Position.Sequence || size != b.state.RetainedBytes {
		return b.storageError(errors.New("stored frontier or retained byte accounting disagrees with event log"))
	}
	return nil
}

func (b *Buffer) retain(cutoff time.Time, maxBytes int64) error {
	for {
		state := cloneState(b.state)
		if state.DiscardedThrough == state.Position.Sequence {
			return nil
		}
		iter, err := b.db.NewIter(eventRange(state.Position.Generation, state.DiscardedThrough))
		if err != nil {
			return err
		}
		batch := b.newBatch()
		deleted := 0
		var batchBytes int64
		var mutationErr error
		for valid := iter.First(); valid; valid = iter.Next() {
			disk, event, err := readRecord(iter.Key(), iter.Value(), state.Position.Generation, state.DiscardedThrough+1, b.opts.SourceID)
			if err != nil {
				mutationErr = err
				break
			}
			expired := !cutoff.IsZero() && event.Timestamp < cutoff.UnixMilli()
			oversized := maxBytes > 0 && state.RetainedBytes > maxBytes
			if !expired && !oversized {
				break
			}
			ikey := identityKey(state.Position.Generation, event.EventID)
			value, closer, err := b.db.Get(ikey)
			if err != nil {
				mutationErr = err
				break
			}
			size := int64(len(iter.Key()) + len(iter.Value()) + len(ikey) + len(value))
			var identity identityRecord
			decodeErr := decode(value, &identity)
			closeErr := closer.Close()
			if err := errors.Join(decodeErr, closeErr); err != nil {
				mutationErr = err
				break
			}
			if identity.Sequence != disk.Sequence || !bytes.Equal(identity.Token, disk.Token) {
				mutationErr = errors.New("retained event identity is inconsistent")
				break
			}
			if deleted == b.opts.BatchSize || (deleted > 0 && size > b.opts.BatchBytes-batchBytes) {
				break
			}
			if err := batch.Delete(iter.Key(), nil); err != nil {
				mutationErr = err
				break
			}
			if err := batch.Delete(ikey, nil); err != nil {
				mutationErr = err
				break
			}
			state.DiscardedThrough = disk.Sequence
			state.RetainedBytes -= size
			batchBytes += size
			deleted++
		}
		mutationErr = errors.Join(mutationErr, iter.Error(), iter.Close())
		if mutationErr == nil && deleted > 0 {
			mutationErr = b.storeState(batch, state)
		}
		if mutationErr == nil && deleted > 0 {
			mutationErr = batch.Commit(pebble.Sync)
		}
		mutationErr = errors.Join(mutationErr, batch.Close())
		if mutationErr != nil {
			return mutationErr
		}
		if deleted == 0 {
			return nil
		}
		if err := b.publish(state, nil); err != nil {
			return err
		}
	}
}
