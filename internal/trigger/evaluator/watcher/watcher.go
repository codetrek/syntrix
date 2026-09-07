package watcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/pkg/model"
)

const checkpointKey = "sys/checkpoints/trigger_evaluator"

// WatcherOptions configures the watcher.
type WatcherOptions struct {
	StartFromNow       bool
	CheckpointDatabase string
}

type pullerWatcher struct {
	puller puller.Service
	store  storage.DocumentStore
	opts   WatcherOptions

	mu        sync.Mutex
	streams   map[*pullerStream]struct{}
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// NewWatcher receives all databases; each trigger selects its database during evaluation.
func NewWatcher(p puller.Service, store storage.DocumentStore, opts WatcherOptions) DocumentWatcher {
	if opts.CheckpointDatabase == "" {
		opts.CheckpointDatabase = "default"
	}
	return &pullerWatcher{puller: p, store: store, opts: opts, streams: make(map[*pullerStream]struct{})}
}

func (w *pullerWatcher) Watch(ctx context.Context) (WatcherStream, error) {
	// One checkpoint contains Puller's aggregate positions across all sources.
	checkpointDoc, err := w.store.Get(ctx, w.opts.CheckpointDatabase, checkpointKey)
	var resumeToken string
	switch {
	case errors.Is(err, model.ErrNotFound):
		if !w.opts.StartFromNow {
			return nil, fmt.Errorf("checkpoint not found and StartFromNow is false: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("load trigger checkpoint: %w", err)
	default:
		if checkpointDoc == nil {
			return nil, errors.New("trigger checkpoint is missing from successful storage response")
		}
		var ok bool
		resumeToken, ok = checkpointDoc.Data["token"].(string)
		if !ok || resumeToken == "" {
			return nil, errors.New("trigger checkpoint token must be a nonempty string")
		}
	}

	subscription, err := w.puller.Subscribe(ctx, events.SubscribeOptions{
		ConsumerID: "trigger-evaluator",
		After:      resumeToken,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe trigger watcher: %w", err)
	}
	stream := &pullerStream{subscription: subscription, owner: w}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, errors.Join(errors.New("trigger watcher is closed"), subscription.Close())
	}
	w.streams[stream] = struct{}{}
	return stream, nil
}

type pullerStream struct {
	subscription events.Subscription
	owner        *pullerWatcher
	nextMu       sync.Mutex
	terminalErr  error
	closeOnce    sync.Once
	closeErr     error
}

func (s *pullerStream) Next(ctx context.Context) (events.SyntrixChangeEvent, error) {
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.terminalErr != nil {
		return events.SyntrixChangeEvent{}, s.terminalErr
	}
	pEvent, err := s.subscription.Next(ctx)
	if err != nil {
		s.terminalErr = err
		return events.SyntrixChangeEvent{}, err
	}
	if pEvent.Change == nil {
		return events.SyntrixChangeEvent{Progress: pEvent.Progress}, nil
	}
	event, err := events.Transform(pEvent)
	if errors.Is(err, events.ErrDeleteOPIgnored) {
		// Physical deletes are storage cleanup; their position still completes the prefix.
		return events.SyntrixChangeEvent{Progress: pEvent.Progress}, nil
	}
	if err != nil {
		s.terminalErr = fmt.Errorf("transform trigger event %q: %w", pEvent.Change.EventID, err)
		return events.SyntrixChangeEvent{}, s.terminalErr
	}
	return event, nil
}

func (s *pullerStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.subscription.Close()
		s.owner.mu.Lock()
		delete(s.owner.streams, s)
		s.owner.mu.Unlock()
	})
	return s.closeErr
}

func (w *pullerWatcher) SaveCheckpoint(ctx context.Context, token interface{}) error {
	data := map[string]interface{}{"token": token, "updatedAt": time.Now().Unix()}
	err := w.store.Update(ctx, w.opts.CheckpointDatabase, checkpointKey, data, model.Filters{})
	if errors.Is(err, model.ErrNotFound) {
		doc := storage.NewStoredDoc(w.opts.CheckpointDatabase, "sys/checkpoints", "trigger_evaluator", data)
		return w.store.Create(ctx, w.opts.CheckpointDatabase, doc)
	}
	return err
}

func (w *pullerWatcher) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		streams := make([]*pullerStream, 0, len(w.streams))
		for stream := range w.streams {
			streams = append(streams, stream)
		}
		w.mu.Unlock()
		var errs []error
		for _, stream := range streams {
			errs = append(errs, stream.Close())
		}
		w.closeErr = errors.Join(errs...)
	})
	return w.closeErr
}
