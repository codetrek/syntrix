package watcher

import (
	"context"

	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// WatcherStream preserves progress-only deliveries and reports terminal failures.
type WatcherStream interface {
	Next(context.Context) (events.SyntrixChangeEvent, error)
	Close() error
}

// DocumentWatcher watches for document changes in the storage.
type DocumentWatcher interface {
	Watch(ctx context.Context) (WatcherStream, error)

	// SaveCheckpoint saves the resume token for the watcher.
	SaveCheckpoint(ctx context.Context, token interface{}) error

	// Close releases resources held by the watcher.
	Close() error
}
