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

	// Note: 'Before' image is not always available in standard change streams unless configured.
	// We assume it might be populated in the event if available.

	return &DeliveryTask{
		TriggerID:  t.ID,
		Tenant:     t.Tenant,
		Event:      string(event.Type),
		Collection: event.Document.Collection,
		DocKey:     event.Document.Id,
		// LSN and Seq would come from the event metadata in a real implementation
		LSN:        "0:0",
		Seq:        0,
		Before:     before,
		After:      after,
		Timestamp:  time.Now().Unix(),
		URL:        t.URL,
		Headers:    t.Headers,
		SecretsRef: t.SecretsRef,
	}
}
