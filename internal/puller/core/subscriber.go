package core

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

type delivery struct {
	event    *events.StoreChangeEvent
	position cursor.Position
	advance  bool
}

type subscription struct {
	owner      *Puller
	id         uint64
	ctx        context.Context
	cancel     context.CancelFunc
	stopParent func() bool
	nextMu     sync.Mutex

	// Everything below is protected by the owner's coordinator lock.
	terminal     error
	delivered    *cursor.ProgressMarker
	cut          *cursor.ProgressMarker
	sourceOrder  []string
	replaySource int
	coalesce     bool
	queue        []buffer.Record
	queueBytes   int64
	overflow     bool
	pending      []delivery
	wake         chan struct{}
}

func (p *Puller) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var after *cursor.ProgressMarker
	if opts.After != "" {
		decoded, err := cursor.DecodeProgressMarker(opts.After)
		if err != nil {
			return nil, err
		}
		after = decoded
	}
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return nil, context.Canceled
	}
	if err := p.errLocked(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if len(p.backends) == 0 {
		p.mu.Unlock()
		return nil, &events.Error{Code: events.CodeSourceUnavailable, Cause: errors.New("no sources configured")}
	}
	if len(p.subscribers) >= p.cfg.GRPC.MaxConnections {
		p.mu.Unlock()
		return nil, &events.Error{Code: events.CodeOverloaded, Cause: errors.New("subscription limit reached")}
	}
	if after == nil {
		after = p.heads.Clone()
	}
	if err := p.validateSourcesLocked(after); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	p.nextRegistration++
	sub := &subscription{owner: p, id: p.nextRegistration,
		ctx: lifetime, cancel: cancel, delivered: after.Clone(), cut: p.heads.Clone(), wake: make(chan struct{}, 1),
		coalesce: opts.CoalesceOnCatchUp && p.cfg.Consumer.CoalesceOnCatchUp, pending: []delivery{{}}}
	for source := range p.sources {
		sub.sourceOrder = append(sub.sourceOrder, source)
	}
	slices.Sort(sub.sourceOrder)
	p.subscribers[sub.id] = sub
	p.health.SetConsumerCount(len(p.subscribers))
	sub.stopParent = context.AfterFunc(lifetime, func() { sub.terminate(lifetime.Err()) })
	p.mu.Unlock()
	p.logger.Debug("subscription registered", "consumer", opts.ConsumerID, "registration", sub.id)

	// Registration precedes validation so commits occurring during the short read
	// leases already belong to this subscription's live queue.
	for _, source := range sub.sourceOrder {
		position, _ := after.GetPosition(source)
		backend := p.sources[source]
		state, err := backend.buffer.State()
		if err != nil {
			err = p.readFailure(backend, err)
			sub.terminate(err)
			return nil, err
		}
		if len(state.ResumeToken) == 0 && state.StartAt == nil {
			err := &events.Error{Code: events.CodeSourceUnavailable, Backend: backend.name, SourceID: source,
				Cause: errors.New("source initial boundary is not initialized; start ingestion before subscribing")}
			sub.terminate(err)
			return nil, err
		}
		if _, err := backend.buffer.ReadPage(ctx, position, position, 1, p.cfg.Consumer.PageBytes); err != nil {
			err = p.readFailure(backend, err)
			sub.terminate(err)
			return nil, err
		}
	}
	p.mu.Lock()
	terminal := sub.terminal
	p.mu.Unlock()
	if terminal != nil {
		return nil, terminal
	}
	return sub, nil
}

func (p *Puller) validateSourcesLocked(marker *cursor.ProgressMarker) error {
	for source, position := range marker.Positions {
		head, known := p.heads.GetPosition(source)
		if !known {
			return &events.Error{Code: events.CodeUnknownSource, SourceID: source, Cause: errors.New("cursor source is not configured")}
		}
		if position.Generation != head.Generation {
			return &events.Error{Code: events.CodeGenerationMismatch, SourceID: source, Generation: head.Generation,
				CommittedThrough: head.Sequence, Cause: errors.New("cursor generation differs from the published log")}
		}
		if position.Sequence > head.Sequence {
			return &events.Error{Code: events.CodePositionAhead, SourceID: source, Generation: head.Generation,
				CommittedThrough: head.Sequence, Cause: errors.New("cursor exceeds the published frontier")}
		}
	}
	for source := range p.sources {
		if _, ok := marker.GetPosition(source); !ok {
			return &events.Error{Code: events.CodeUnknownSource, SourceID: source, Cause: errors.New("cursor omits a configured source; explicit resynchronization is required")}
		}
	}
	return nil
}

func (p *Puller) publish(name string, batch buffer.CommittedBatch) {
	p.mu.Lock()
	defer p.mu.Unlock()
	backend := p.backends[name]
	if backend == nil || backend.err != nil {
		return
	}
	p.heads.SetPosition(batch.State.Position)
	for range batch.Records {
		p.health.RecordEvent(name)
	}
	for _, sub := range p.subscribers {
		if sub.overflow {
			continue
		}
		cut, _ := sub.cut.GetPosition(batch.State.Position.SourceID)
		for _, record := range batch.Records {
			if record.Position.Sequence <= cut.Sequence {
				continue
			}
			size := record.EncodedBytes + int64(len(record.Position.SourceID)+len(record.Position.Generation)) + 64
			if len(sub.queue) >= p.cfg.GRPC.ChannelSize || size > p.cfg.Consumer.QueueBytes-sub.queueBytes {
				sub.queue = nil
				sub.queueBytes = 0
				sub.overflow = true
				break
			}
			sub.queue = append(sub.queue, record)
			sub.queueBytes += size
		}
		sub.signalLocked()
	}
}

func (s *subscription) signalLocked() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (p *Puller) terminateLocked(sub *subscription, err error) {
	if sub.terminal != nil {
		return
	}
	sub.terminal = err
	sub.queue = nil
	sub.pending = nil
	sub.queueBytes = 0
	delete(p.subscribers, sub.id)
	p.health.SetConsumerCount(len(p.subscribers))
	sub.cancel()
	if sub.stopParent != nil {
		sub.stopParent()
	}
	sub.signalLocked()
}

func (s *subscription) terminate(err error) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.owner.terminateLocked(s, err)
}

func (s *subscription) Close() error {
	s.terminate(context.Canceled)
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if errors.Is(s.terminal, context.Canceled) || errors.Is(s.terminal, context.DeadlineExceeded) {
		return nil
	}
	return s.terminal
}

func (s *subscription) Next(callCtx context.Context) (*events.PullerEvent, error) {
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	ctx, cancel := context.WithCancel(callCtx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stop()
	p := s.owner
	for {
		p.mu.Lock()
		if s.terminal != nil {
			err := s.terminal
			p.mu.Unlock()
			return nil, err
		}
		if ctx.Err() != nil || s.ctx.Err() != nil {
			err := callCtx.Err()
			if err == nil {
				err = s.ctx.Err()
			}
			p.terminateLocked(s, err)
			p.mu.Unlock()
			return nil, err
		}
		if len(s.pending) != 0 {
			next := s.pending[0]
			s.pending[0] = delivery{}
			s.pending = s.pending[1:]
			if len(s.pending) == 0 {
				s.pending = nil
			}
			if next.advance {
				s.delivered.SetPosition(next.position)
			}
			progress, err := s.delivered.Encode()
			if err != nil {
				p.terminateLocked(s, err)
				p.mu.Unlock()
				return nil, err
			}
			p.mu.Unlock()
			return &events.PullerEvent{Change: next.event, Progress: progress}, nil
		}
		if s.overflow {
			s.cut = p.heads.Clone()
			s.queue = nil
			s.queueBytes = 0
			s.overflow = false
		}
		backend, after, through, replay := s.nextReplayLocked()
		if replay {
			coalesce := s.coalesce && through.Sequence-after.Sequence >= uint64(p.cfg.Consumer.CatchUpThreshold)
			p.mu.Unlock()
			page, err := backend.readPage(ctx, after, through, p.cfg.Consumer.PageSize, p.cfg.Consumer.PageBytes)
			if err != nil {
				err = p.readFailure(backend, err)
				if ctx.Err() != nil {
					err = callCtx.Err()
					if err == nil {
						err = s.ctx.Err()
					}
				}
				s.terminate(err)
				continue
			}
			if page.Through.Sequence <= after.Sequence || page.Through.Sequence > through.Sequence {
				s.terminate(&events.Error{Code: events.CodeStorageFailure, SourceID: after.SourceID,
					Cause: errors.New("event log page did not advance within its replay cut")})
				continue
			}
			deliveries := exactDeliveries(page.Records)
			if coalesce {
				deliveries = coalescedDeliveries(page.Records, page.Through)
			}
			p.mu.Lock()
			if s.terminal == nil {
				s.pending = deliveries
			}
			p.mu.Unlock()
			continue
		}
		if len(s.queue) != 0 {
			record := s.queue[0]
			s.queue[0] = buffer.Record{}
			s.queue = s.queue[1:]
			if len(s.queue) == 0 {
				s.queue = nil
			}
			s.queueBytes -= record.EncodedBytes + int64(len(record.Position.SourceID)+len(record.Position.Generation)) + 64
			position, _ := s.delivered.GetPosition(record.Position.SourceID)
			if record.Position.Sequence <= position.Sequence {
				p.mu.Unlock()
				continue
			}
			if record.Position.Generation != position.Generation || record.Position.Sequence != position.Sequence+1 {
				s.overflow = true
				p.mu.Unlock()
				continue
			}
			s.pending = []delivery{{event: record.Event, position: record.Position, advance: true}}
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-s.wake:
		}
	}
}

func (s *subscription) nextReplayLocked() (*Backend, cursor.Position, cursor.Position, bool) {
	for range len(s.sourceOrder) {
		source := s.sourceOrder[s.replaySource]
		s.replaySource = (s.replaySource + 1) % len(s.sourceOrder)
		after, _ := s.delivered.GetPosition(source)
		through, _ := s.cut.GetPosition(source)
		if after.Sequence < through.Sequence {
			return s.owner.sources[source], after, through, true
		}
	}
	return nil, cursor.Position{}, cursor.Position{}, false
}

func exactDeliveries(records []buffer.Record) []delivery {
	result := make([]delivery, len(records))
	for i, record := range records {
		result[i] = delivery{event: record.Event, position: record.Position, advance: true}
	}
	return result
}

func (p *Puller) readFailure(backend *Backend, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	err = p.backendError(backend, events.CodeStorageFailure, err)
	var domain *events.Error
	if errors.As(err, &domain) && (domain.Code == events.CodeStorageFailure || domain.Code == events.CodeContinuityLost) {
		p.failBackend(backend.name, err)
	}
	return err
}
