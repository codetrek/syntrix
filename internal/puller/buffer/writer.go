// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

type writeRequest struct {
	key        []byte
	value      []byte
	checkpoint checkpoint.Checkpoint
	event      *events.StoreChangeEvent
	receipt    chan error
}

// Write admits an event and its validated source checkpoint to the batch queue.
// The record and source frontier commit atomically; admission does not wait for
// durability. ScanFrom retains visibility of admitted in-memory events.
func (b *Buffer) Write(evt *events.StoreChangeEvent, cp checkpoint.Checkpoint) error {
	b.mu.Lock()
	if err := b.stateError(); err != nil {
		b.mu.Unlock()
		return err
	}
	if err := checkpoint.Validate(cp); err != nil {
		b.mu.Unlock()
		return err
	}
	if evt == nil {
		b.mu.Unlock()
		return fmt.Errorf("buffer event is required")
	}
	value, err := json.Marshal(Record{Version: recordVersion, Event: evt, Checkpoint: cp})
	if err != nil {
		b.mu.Unlock()
		return fmt.Errorf("failed to marshal buffer record: %w", err)
	}
	b.pending = append(b.pending, &writeRequest{
		key: []byte(evt.BufferKey()), value: value, checkpoint: cp, event: evt,
	})
	shouldNotify := len(b.pending) >= b.batchSize
	b.mu.Unlock()
	if shouldNotify {
		b.notifyBatcher()
	}
	return nil
}

func (b *Buffer) notifyBatcher() {
	select {
	case b.notifyCh <- struct{}{}:
	default:
	}
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

func (b *Buffer) startBatcher() {
	b.batcherWG.Add(1)
	go b.runBatcher()
}

func (b *Buffer) runBatcher() {
	defer b.batcherWG.Done()
	ticker := time.NewTicker(b.batchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.notifyCh:
		case <-ticker.C:
		case <-b.closeCh:
			b.flushPending()
			return
		}
		if err := b.flushPending(); err != nil {
			return
		}
	}
}

func (b *Buffer) flushPending() error {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	requests := b.pending
	b.pending = nil
	b.flushing = requests
	b.mu.Unlock()

	err := b.commitRequests(requests)
	b.mu.Lock()
	b.flushing = nil
	if err != nil {
		b.failure = err
		requests = append(requests, b.pending...)
		b.pending = nil
	}
	b.mu.Unlock()
	for _, req := range requests {
		if req.receipt != nil {
			req.receipt <- err
		}
	}
	if err != nil {
		b.logger.Error("failed to flush buffer batch", "error", err)
	}
	return err
}

func (b *Buffer) commitRequests(requests []*writeRequest) (err error) {
	batch := b.newBatch()
	defer func() { err = errors.Join(err, batch.Close()) }()
	for _, req := range requests {
		if req.event != nil {
			if err := batch.Set(req.key, req.value, pebble.Sync); err != nil {
				return fmt.Errorf("failed to batch write event: %w", err)
			}
		}
	}
	cp := requests[len(requests)-1].checkpoint
	if err := batch.Set(checkpointKeyBytes, []byte(cp), pebble.Sync); err != nil {
		return fmt.Errorf("failed to batch write checkpoint: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit batch: %w", err)
	}
	return nil
}
