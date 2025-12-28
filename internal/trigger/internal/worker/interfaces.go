package worker

import (
	"context"

	"github.com/codetrek/syntrix/internal/trigger/types"
)

// SecretProvider resolves secret references.
type SecretProvider interface {
	GetSecret(ctx context.Context, ref string) (string, error)
}
// DeliveryWorker processes delivery tasks by making HTTP requests.
type DeliveryWorker interface {
	// ProcessTask executes the delivery task.
	ProcessTask(ctx context.Context, task *types.DeliveryTask) error
}
