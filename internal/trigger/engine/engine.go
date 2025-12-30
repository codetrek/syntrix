package engine

import (
	"context"
	"log"
	"sync"

	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/internal/trigger/internal/evaluator"
	"github.com/codetrek/syntrix/internal/trigger/internal/pubsub"
	"github.com/codetrek/syntrix/internal/trigger/internal/watcher"
	"github.com/codetrek/syntrix/internal/trigger/types"
)

// defaultTriggerEngine implements TriggerEngine.
type defaultTriggerEngine struct {
	evaluator evaluator.Evaluator
	watcher   watcher.DocumentWatcher
	publisher pubsub.TaskPublisher
	triggers  []*trigger.Trigger
	mu        sync.RWMutex
}

// LoadTriggers validates and loads the given triggers.
func (e *defaultTriggerEngine) LoadTriggers(triggers []*trigger.Trigger) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, t := range triggers {
		if err := trigger.ValidateTrigger(t); err != nil {
			return err
		}
	}

	e.triggers = triggers
	return nil
}

// Start begins the trigger processing loop.
func (e *defaultTriggerEngine) Start(ctx context.Context) error {
	stream, err := e.watcher.Watch(ctx)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-stream:
			if !ok {
				return nil
			}

			// Guard: skip events with no document data
			if evt.Document == nil && evt.Before == nil {
				log.Printf("[Warning] Skipping event with nil Document and Before")
				continue
			}

			e.mu.RLock()
			currentTriggers := e.triggers
			e.mu.RUnlock()

			for _, t := range currentTriggers {
				matched, err := e.evaluator.Evaluate(ctx, t, &evt)
				if err != nil {
					log.Printf("[Error] Evaluation failed for trigger %s: %v", t.ID, err)
					continue
				}
				if matched {
					var collection string
					var documentID string
					var payload map[string]interface{}

					if evt.Document != nil {
						collection = evt.Document.Collection
						documentID = evt.Document.Id
						payload = evt.Document.Data
					} else if evt.Before != nil {
						collection = evt.Before.Collection
						documentID = evt.Before.Id
						payload = evt.Before.Data
					}

					task := &trigger.DeliveryTask{
						TriggerID:   t.ID,
						Tenant:      t.Tenant,
						Event:       string(evt.Type),
						Collection:  collection,
						DocumentID:  documentID,
						Payload:     payload,
						URL:         t.URL,
						Headers:     t.Headers,
						RetryPolicy: t.RetryPolicy,
						Timeout:     trigger.Duration(types.DefaultTaskTimeout),
					}
					if e.publisher != nil {
						if err := e.publisher.Publish(ctx, task); err != nil {
							log.Printf("[Error] Failed to publish task for trigger %s: %v", t.ID, err)
						}
					}
				}
			}

			if evt.ResumeToken != nil {
				if err := e.watcher.SaveCheckpoint(ctx, evt.ResumeToken); err != nil {
					log.Printf("[Error] Failed to save checkpoint: %v", err)
				}
			}
		}
	}
}

// Close stops the engine and releases resources.
func (e *defaultTriggerEngine) Close() error {
	var errs []error

	if e.watcher != nil {
		if err := e.watcher.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.publisher != nil {
		if err := e.publisher.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0] // Return first error
	}
	return nil
}
