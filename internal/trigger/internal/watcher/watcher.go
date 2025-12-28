package watcher

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// WatcherOptions configures the watcher.
type WatcherOptions struct {
	StartFromNow bool
}

type defaultWatcher struct {
	store  storage.DocumentStore
	tenant string
	opts   WatcherOptions
}

// NewWatcher creates a new DocumentWatcher.
func NewWatcher(store storage.DocumentStore, tenant string, opts WatcherOptions) DocumentWatcher {
	return &defaultWatcher{
		store:  store,
		tenant: tenant,
		opts:   opts,
	}
}

func (w *defaultWatcher) Watch(ctx context.Context) (<-chan storage.Event, error) {
	// 1. Load Checkpoint
	// We use "default" tenant for system checkpoints as per design implication of tenant-scoped keys.
	// Key format: sys/checkpoints/trigger_evaluator/<tenant>
	checkpointKey := fmt.Sprintf("sys/checkpoints/trigger_evaluator/%s", w.tenant)
	checkpointTenant := "default"

	var resumeToken interface{}
	checkpointDoc, err := w.store.Get(ctx, checkpointTenant, checkpointKey)
	if err == nil && checkpointDoc != nil {
		if token, ok := checkpointDoc.Data["token"]; ok {
			resumeToken = token
			log.Printf("Resuming trigger watcher for tenant %s from checkpoint", w.tenant)
		}
	} else if err == model.ErrNotFound {
		if !w.opts.StartFromNow {
			return nil, fmt.Errorf("checkpoint not found for tenant %s and StartFromNow is false", w.tenant)
		}
		log.Printf("AUDIT: Starting watch from NOW for tenant %s (checkpoint missing)", w.tenant)
	} else {
		log.Printf("Failed to load checkpoint for tenant %s: %v", w.tenant, err)
		return nil, err
	}

	// 2. Start Watch
	// We watch the target tenant.
	return w.store.Watch(ctx, w.tenant, "", resumeToken, storage.WatchOptions{IncludeBefore: true})
}

func (w *defaultWatcher) SaveCheckpoint(ctx context.Context, token interface{}) error {
	checkpointKey := fmt.Sprintf("sys/checkpoints/trigger_evaluator/%s", w.tenant)
	checkpointTenant := "default"

	data := map[string]interface{}{
		"token":     token,
		"updatedAt": time.Now().Unix(),
	}

	err := w.store.Update(ctx, checkpointTenant, checkpointKey, data, model.Filters{})
	if err != nil {
		if err == model.ErrNotFound {
			// Create if not exists
			doc := storage.NewDocument(checkpointTenant, checkpointKey, "sys/checkpoints/trigger_evaluator", data)
			return w.store.Create(ctx, checkpointTenant, doc)
		}
		return err
	}
	return nil
}
