// Package buffer provides event buffering with PebbleDB persistence.
package buffer

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
)

type writeRequest struct {
	key   []byte
	value []byte
	token bson.Raw
	event *events.StoreChangeEvent
}

// Write stores an event and updates the checkpoint in the same batch.
func (b *Buffer) Write(evt *events.StoreChangeEvent, token bson.Raw) error {
	b.mu.Lock()
	if b.closed {
		err := b.failure
		b.mu.Unlock()
		if err != nil {
			return err
		}
		return fmt.Errorf("buffer is closed")
	}

	if len(token) == 0 {
		b.mu.Unlock()
		return fmt.Errorf("checkpoint token is required")
	}

	// Key is the buffer key for ordering
	key := []byte(evt.BufferKey())

	// Value is the JSON-encoded event
	value, err := json.Marshal(evt)
	if err != nil {
		b.mu.Unlock()
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	req := &writeRequest{
		key:   key,
		value: value,
		token: append([]byte(nil), token...),
		event: evt,
	}

	b.pending = append(b.pending, req)
	shouldNotify := len(b.pending) >= b.batchSize
	b.mu.Unlock()

	if shouldNotify {
		// Notify batcher, non-blocking
		select {
		case b.notifyCh <- struct{}{}:
		default:
		}
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

func (b *Buffer) startBatcher() {
	b.batcherWG.Add(1)
	go b.runBatcher()
}

func (b *Buffer) runBatcher() {
	defer b.batcherWG.Done()

	ticker := time.NewTicker(b.batchInterval)
	defer ticker.Stop()

	flush := func() error {
		// Swap pending to flushing under lock
		b.mu.Lock()
		if len(b.pending) == 0 {
			b.mu.Unlock()
			return nil
		}
		b.flushing = b.pending
		b.pending = nil
		b.mu.Unlock()

		if len(b.flushing) == 0 {
			return nil
		}

		batch := b.newBatch()
		var commitErr error
		var checkpointToken []byte

		for _, req := range b.flushing {
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

		if err := batch.Close(); err != nil && commitErr == nil {
			commitErr = fmt.Errorf("failed to close batch: %w", err)
		}

		// Clear flushing queue
		b.mu.Lock()
		b.flushing = nil

		if commitErr != nil {
			b.failure = commitErr
			if !b.closed {
				b.closed = true
				// Note: closeCh might be already closed if Close() was called
				select {
				case <-b.closeCh:
				default:
					close(b.closeCh)
				}
			}
			b.mu.Unlock()
			b.logger.Error("failed to flush batch, stopping batcher", "error", commitErr)
			return commitErr
		}
		b.mu.Unlock()
		return nil
	}

	for {
		select {
		case <-b.notifyCh:
			if err := flush(); err != nil {
				return
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return
			}
		case <-b.closeCh:
			// One last flush to drain pending events
			flush()
			return
		}
	}
}
