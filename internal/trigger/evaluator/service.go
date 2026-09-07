package evaluator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/internal/trigger/evaluator/watcher"
	"github.com/syntrixbase/syntrix/internal/trigger/types"
)

// Service evaluates document changes against trigger rules and publishes matched tasks.
type Service interface {
	LoadTriggers(triggers []*types.Trigger) error
	// Start returns on cancellation or the first subscription, processing, or checkpoint failure.
	Start(ctx context.Context) error
	Close() error
}

type service struct {
	evaluator Evaluator
	watcher   watcher.DocumentWatcher
	publisher TaskPublisher
	triggers  []*types.Trigger
	mu        sync.RWMutex
}

func (s *service) LoadTriggers(triggers []*types.Trigger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range triggers {
		if err := ValidateTrigger(t); err != nil {
			return err
		}
	}
	s.triggers = triggers
	return nil
}

func (s *service) Start(ctx context.Context) (result error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stream, err := s.watcher.Watch(runCtx)
	if err != nil {
		return err
	}

	checkpoints := &checkpointWriter{watcher: s.watcher, notify: make(chan struct{}, 1)}
	checkpointDone := make(chan error, 1)
	go func() {
		err := checkpoints.run(runCtx)
		if err != nil {
			cancel(err)
		}
		checkpointDone <- err
	}()
	defer func() {
		// Upstream can terminate while the caller remains active. Cancel the saver
		// before joining it; unsaved completed work is safe to replay on restart.
		cancel(nil)
		closeErr := stream.Close()
		saveErr := <-checkpointDone
		if saveErr != nil && !errors.Is(result, saveErr) {
			result = errors.Join(result, saveErr)
		}
		result = errors.Join(result, closeErr)
	}()

	for {
		if err := runCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return context.Cause(runCtx)
		}
		evt, err := stream.Next(runCtx)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			if runCtx.Err() != nil && errors.Is(err, runCtx.Err()) {
				return context.Cause(runCtx)
			}
			return fmt.Errorf("read trigger event: %w", err)
		}
		if err := s.process(runCtx, evt); err != nil {
			return err
		}
		if evt.Progress != "" {
			checkpoints.advance(evt.Progress)
		}
	}
}

func (s *service) process(ctx context.Context, evt events.SyntrixChangeEvent) error {
	if evt.Document == nil && evt.Before == nil {
		if evt.Type == "" && evt.Progress != "" {
			return nil
		}
		return errors.New("trigger event has no document or progress-only delivery")
	}
	s.mu.RLock()
	currentTriggers := s.triggers
	s.mu.RUnlock()
	for _, t := range currentTriggers {
		matched, err := s.evaluator.Evaluate(ctx, t, evt)
		if err != nil {
			return fmt.Errorf("evaluate trigger %q: %w", t.ID, err)
		}
		if !matched {
			continue
		}
		if s.publisher == nil {
			return fmt.Errorf("publish trigger %q: publisher is not configured", t.ID)
		}
		doc := evt.Document
		if doc == nil {
			doc = evt.Before
		}
		task := &types.DeliveryTask{
			TriggerID:   t.ID,
			Database:    t.Database,
			Event:       string(evt.Type),
			Collection:  doc.Collection,
			DocumentID:  doc.Id,
			Payload:     doc.Data,
			URL:         t.URL,
			Headers:     t.Headers,
			SecretsRef:  t.SecretsRef,
			RetryPolicy: t.RetryPolicy,
			Timeout:     types.Duration(types.DefaultTaskTimeout),
		}
		if err := s.publisher.Publish(ctx, task); err != nil {
			return fmt.Errorf("publish trigger %q: %w", t.ID, err)
		}
	}
	return nil
}

type checkpointWriter struct {
	watcher watcher.DocumentWatcher
	mu      sync.Mutex
	latest  string
	notify  chan struct{}
}

func (w *checkpointWriter) advance(progress string) {
	w.mu.Lock()
	w.latest = progress
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *checkpointWriter) run(ctx context.Context) error {
	var saved string
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.notify:
		}
		if ctx.Err() != nil {
			return nil
		}
		w.mu.Lock()
		progress := w.latest
		w.mu.Unlock()
		if progress == saved {
			continue
		}
		if err := w.watcher.SaveCheckpoint(ctx, progress); err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			return fmt.Errorf("save trigger checkpoint: %w", err)
		}
		saved = progress
	}
}

func (s *service) Close() error {
	var errs []error
	if s.watcher != nil {
		errs = append(errs, s.watcher.Close())
	}
	if s.publisher != nil {
		errs = append(errs, s.publisher.Close())
	}
	return errors.Join(errs...)
}
