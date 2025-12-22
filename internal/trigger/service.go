package trigger

import (
	"context"
	"log"
	"syntrix/internal/storage"
	"time"
)

// TriggerService orchestrates trigger evaluation and task publishing.
type TriggerService struct {
	evaluator Evaluator
	publisher EventPublisher
	triggers  []*Trigger // In-memory cache of triggers. In production, this should be a thread-safe map or cache.
}

// NewTriggerService creates a new TriggerService.
func NewTriggerService(evaluator Evaluator, publisher EventPublisher) *TriggerService {
	return &TriggerService{
		evaluator: evaluator,
		publisher: publisher,
		triggers:  make([]*Trigger, 0),
	}
}

// LoadTriggers updates the in-memory trigger cache.
func (s *TriggerService) LoadTriggers(triggers []*Trigger) {
	s.triggers = triggers
}

// Watch starts watching the storage backend for changes and processes them.
// It blocks until the context is cancelled or the stream is closed.
func (s *TriggerService) Watch(ctx context.Context, backend storage.StorageBackend) error {
	// 1. Load Checkpoint
	var resumeToken interface{}
	checkpointDoc, err := backend.Get(ctx, "sys/checkpoints/trigger_evaluator")
	if err == nil && checkpointDoc != nil {
		if token, ok := checkpointDoc.Data["token"]; ok {
			resumeToken = token
			log.Println("Resuming trigger watcher from checkpoint")
		}
	} else if err != storage.ErrNotFound {
		log.Printf("Failed to load checkpoint: %v", err)
		// Continue without checkpoint? Or fail?
		// For robustness, maybe we should fail, but for now let's log and continue.
	}

	// 2. Start Watch with Resume Token
	stream, err := backend.Watch(ctx, "", resumeToken, storage.WatchOptions{IncludeBefore: true})
	if err != nil {
		return err
	}

	log.Println("Trigger Evaluator Watcher started")
	for {
		select {
		case <-ctx.Done():
			log.Println("Trigger Evaluator Watcher stopped")
			return nil
		case evt, ok := <-stream:
			if !ok {
				log.Println("Trigger watcher stream closed")
				return nil
			}
			if err := s.ProcessEvent(ctx, &evt); err != nil {
				log.Printf("Error processing trigger event: %v", err)
			}

			// 3. Save Checkpoint
			// Optimization: In high throughput, we should batch this or do it asynchronously.
			// For now, we do it synchronously to ensure at-least-once delivery.
			if evt.ResumeToken != nil {
				err := backend.Update(ctx, "sys/checkpoints/trigger_evaluator", map[string]interface{}{
					"token":      evt.ResumeToken,
					"updatedAt": time.Now().Unix(),
				}, storage.Filters{})
				if err != nil {
					// If it doesn't exist, create it
					if err == storage.ErrNotFound {
						backend.Create(ctx, &storage.Document{
							Id:         "sys/checkpoints/trigger_evaluator",
							Collection: "sys",
							Data: map[string]interface{}{
								"token":      evt.ResumeToken,
								"updatedAt": time.Now().Unix(),
							},
						})
					} else {
						log.Printf("Failed to save checkpoint: %v", err)
					}
				}
			}
		}
	}
}

// ProcessEvent evaluates the event against all active triggers and publishes delivery tasks.
func (s *TriggerService) ProcessEvent(ctx context.Context, event *storage.Event) error {
	for _, t := range s.triggers {
		match, err := s.evaluator.Evaluate(ctx, t, event)
		if err != nil {
			log.Printf("[Error] Trigger evaluation failed for %s: %v", t.ID, err)
			continue
		}

		if match {
			task := s.createDeliveryTask(t, event)
			if err := s.publisher.Publish(ctx, task); err != nil {
				log.Printf("[Error] Failed to publish delivery task for %s: %v", t.ID, err)
				return err
			}
		}
	}
	return nil
}

func (s *TriggerService) createDeliveryTask(t *Trigger, event *storage.Event) *DeliveryTask {
	var before, after map[string]interface{}

	if event.Document != nil {
		after = event.Document.Data
	}

	if event.Before != nil {
		before = event.Before.Data
	}

	return &DeliveryTask{
		TriggerID:  t.ID,
		Tenant:     t.Tenant,
		Event:      string(event.Type),
		Collection: event.Document.Collection,
		DocKey:     event.Document.Id,
		// LSN and Seq would come from the event metadata in a real implementation
		LSN:         "0:0",
		Seq:         0,
		Before:      before,
		After:       after,
		Timestamp:   time.Now().Unix(),
		URL:         t.URL,
		Headers:     t.Headers,
		SecretsRef:  t.SecretsRef,
		RetryPolicy: t.RetryPolicy,
		Timeout:     t.Timeout,
	}
}
