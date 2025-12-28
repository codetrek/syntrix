package engine

import (
	"context"

	"github.com/codetrek/syntrix/internal/trigger"
)

// TaskConsumer consumes delivery tasks.
type TaskConsumer interface {
	Start(ctx context.Context) error
}

// TriggerEngine is the main interface for the trigger subsystem.
// It manages the lifecycle of triggers, including loading, starting, and stopping.
type TriggerEngine interface {
	// LoadTriggers validates and loads the given triggers.
	// It returns an error if any trigger is invalid or if the engine is already started.
	LoadTriggers(triggers []*trigger.Trigger) error

	// Start begins the trigger processing loop.
	// It blocks until the context is canceled or a fatal error occurs.
	Start(ctx context.Context) error

	// Close stops the engine and releases resources.
	Close() error
}

// TriggerFactory creates TriggerEngine instances.
type TriggerFactory interface {
	// Engine returns the TriggerEngine instance created by this factory.
	Engine() TriggerEngine

	// Consumer returns a new TaskConsumer.
	Consumer(numWorkers int) (TaskConsumer, error)

	// Close releases any resources held by the factory.
	Close() error
}
