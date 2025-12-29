package pubsub

import (
	"context"

	"github.com/codetrek/syntrix/internal/trigger/types"
)

// TaskPublisher publishes delivery tasks.
type TaskPublisher interface {
	Publish(ctx context.Context, task *types.DeliveryTask) error

	// Close releases resources held by the publisher.
	Close() error
}

// TaskConsumer consumes delivery tasks.
type TaskConsumer interface {
	Start(ctx context.Context) error
}
