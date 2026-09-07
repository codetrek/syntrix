// Package client implements the gRPC client for the puller service.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"strconv"
	"sync"
	"time"

	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ConnectionState int

const (
	StateConnected ConnectionState = iota
	StateReconnecting
	StateDisconnected
)

type StateChangeCallback func(state ConnectionState, err error)

type ClientConfig struct {
	// MaxReceiveBytes bounds the protobuf message accepted by each stream.
	MaxReceiveBytes   int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	// MaxRetries bounds reconnect attempts without an event or cursor advance; zero is unlimited.
	MaxRetries    int
	OnStateChange StateChangeCallback
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{MaxReceiveBytes: 64 << 20, InitialBackoff: time.Second, MaxBackoff: 30 * time.Second, BackoffMultiplier: 2}
}

// Client shares one connection; reconnecting a subscription never replaces it.
type Client struct {
	conn      *grpc.ClientConn
	client    pullerv1.PullerServiceClient
	cfg       ClientConfig
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func New(address string, logger *slog.Logger) (*Client, error) {
	return NewWithConfig(address, logger, DefaultClientConfig())
}

func NewWithConfig(address string, _ *slog.Logger, cfg ClientConfig) (*Client, error) {
	defaults := DefaultClientConfig()
	if cfg.MaxReceiveBytes == 0 {
		cfg.MaxReceiveBytes = defaults.MaxReceiveBytes
	}
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = defaults.InitialBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = defaults.MaxBackoff
	}
	if cfg.BackoffMultiplier == 0 {
		cfg.BackoffMultiplier = defaults.BackoffMultiplier
	}
	if cfg.MaxReceiveBytes <= 0 || cfg.InitialBackoff < 0 || cfg.MaxBackoff < cfg.InitialBackoff || cfg.BackoffMultiplier < 1 || math.IsNaN(cfg.BackoffMultiplier) || math.IsInf(cfg.BackoffMultiplier, 0) || cfg.MaxRetries < 0 {
		return nil, errors.New("invalid puller reconnect configuration")
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(cfg.MaxReceiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("create puller connection: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{conn: conn, client: pullerv1.NewPullerServiceClient(conn), cfg: cfg, ctx: ctx, cancel: cancel}, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		if c.conn != nil {
			c.closeErr = c.conn.Close()
		}
	})
	return c.closeErr
}

func (c *Client) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	sub := &subscription{client: c, opts: opts, ctx: lifetime, cancel: cancel, progress: opts.After, backoff: c.cfg.InitialBackoff}
	sub.stopClient = context.AfterFunc(c.ctx, func() { cancel(context.Cause(c.ctx)) })
	if err := sub.connect(); err != nil {
		if !retryable(err) {
			_ = sub.Close()
			return nil, decodeError(err)
		}
		sub.pendingError = err
	}
	return sub, nil
}

type subscription struct {
	client       *Client
	opts         events.SubscribeOptions
	ctx          context.Context
	cancel       context.CancelCauseFunc
	stopClient   func() bool
	closeOnce    sync.Once
	nextMu       sync.Mutex
	terminal     error
	stream       pullerv1.PullerService_SubscribeClient
	streamCancel context.CancelFunc
	progress     string
	pendingError error
	failures     int
	backoff      time.Duration
}

func (s *subscription) Close() error {
	s.closeOnce.Do(func() { s.cancel(context.Canceled); s.stopClient() })
	return nil
}

func (s *subscription) connect() error {
	ctx, cancel := context.WithCancel(s.ctx)
	stream, err := s.client.client.Subscribe(ctx, &pullerv1.SubscribeRequest{
		ConsumerId: s.opts.ConsumerID, After: s.progress, CoalesceOnCatchUp: s.opts.CoalesceOnCatchUp,
	})
	if err == nil {
		var header metadata.MD
		header, err = stream.Header()
		if err == nil && header == nil {
			// grpc.Header suppresses trailers-only failures; Recv exposes their status.
			_, err = stream.Recv()
			if err == nil {
				err = &events.Error{Code: events.CodeUnsupportedFormat, Cause: errors.New("missing subscription registration header")}
			}
		} else if err == nil {
			ready := header.Get("syntrix-puller-subscription")
			initial := header.Get("syntrix-puller-initial-progress")
			if len(ready) != 1 || ready[0] != "ready" || len(initial) != 1 || initial[0] == "" {
				err = &events.Error{Code: events.CodeUnsupportedFormat, Cause: errors.New("invalid subscription registration header")}
			} else {
				if s.progress != "" {
					err = compareStartProgress(s.progress, initial[0])
				}
				if err == nil {
					s.progress = initial[0]
				}
			}
		}
	}
	if err != nil {
		cancel()
		if cause := context.Cause(s.ctx); cause != nil {
			return cause
		}
		return err
	}
	s.stream, s.streamCancel = stream, cancel
	s.notify(StateConnected, nil)
	return nil
}

func (s *subscription) Next(ctx context.Context) (*events.PullerEvent, error) {
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.terminal != nil {
		return nil, s.terminal
	}
	if err := ctx.Err(); err != nil {
		return nil, s.finish(err)
	}
	// A canceled Next terminates only this stream, including a blocked Recv or Header.
	stop := context.AfterFunc(ctx, func() { s.cancel(context.Cause(ctx)) })
	defer stop()
	for {
		if err := context.Cause(s.ctx); err != nil {
			return nil, s.finish(err)
		}
		if s.pendingError != nil {
			err := s.pendingError
			s.pendingError = nil
			if err := s.waitRetry(err); err != nil {
				return nil, s.finish(err)
			}
		}
		if s.stream == nil {
			if err := s.connect(); err != nil {
				if !retryable(err) {
					return nil, s.finish(decodeError(err))
				}
				s.pendingError = err
				continue
			}
		}
		envelope, err := s.stream.Recv()
		if err != nil {
			s.streamCancel()
			s.stream, s.streamCancel = nil, nil
			if cause := context.Cause(s.ctx); cause != nil {
				return nil, s.finish(cause)
			}
			if !retryable(err) {
				return nil, s.finish(decodeError(err))
			}
			s.pendingError = err
			continue
		}
		event, err := convertEvent(envelope)
		if err != nil {
			return nil, s.finish(&events.Error{Code: events.CodeUnsupportedFormat, Cause: err})
		}
		if cause := context.Cause(s.ctx); cause != nil {
			return nil, s.finish(cause)
		}
		if err := ctx.Err(); err != nil {
			return nil, s.finish(err)
		}
		progressed := event.Change != nil || event.Progress != s.progress
		s.progress = event.Progress
		if progressed {
			s.failures = 0
			s.backoff = s.client.cfg.InitialBackoff
		}
		return event, nil
	}
}

func compareStartProgress(requested, initial string) error {
	if requested == initial {
		return nil
	}
	requestedMarker, err := cursor.DecodeProgressMarker(requested)
	if err != nil {
		return err
	}
	initialMarker, err := cursor.DecodeProgressMarker(initial)
	if err != nil {
		return err
	}
	if requestedMarker.Version != initialMarker.Version || !maps.Equal(requestedMarker.Positions, initialMarker.Positions) {
		return &events.Error{Code: events.CodeInvalidCursor, Cause: errors.New("subscription start differs from requested progress")}
	}
	return nil
}

func (s *subscription) waitRetry(err error) error {
	if s.client.cfg.MaxRetries > 0 && s.failures >= s.client.cfg.MaxRetries {
		return fmt.Errorf("puller reconnect attempts exhausted: %w", err)
	}
	s.failures++
	s.notify(StateReconnecting, err)
	timer := time.NewTimer(s.backoff)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	case <-timer.C:
	}
	next := float64(s.backoff) * s.client.cfg.BackoffMultiplier
	if next >= float64(s.client.cfg.MaxBackoff) {
		s.backoff = s.client.cfg.MaxBackoff
	} else {
		s.backoff = time.Duration(next)
	}
	return nil
}

func (s *subscription) finish(err error) error {
	s.terminal = err
	s.cancel(err)
	s.stopClient()
	s.notify(StateDisconnected, err)
	return err
}

func (s *subscription) notify(state ConnectionState, err error) {
	if s.client.cfg.OnStateChange != nil {
		s.client.cfg.OnStateChange(state, err)
	}
}

func retryable(err error) bool {
	var domainErr *events.Error
	if errors.As(decodeError(err), &domainErr) {
		return false
	}
	return err == io.EOF || status.Code(err) == codes.Unavailable
}

func decodeError(err error) error {
	for _, detail := range status.Convert(err).Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Domain != "syntrix.puller" {
			continue
		}
		floor, floorErr := strconv.ParseUint(info.Metadata["discarded_through"], 10, 64)
		head, headErr := strconv.ParseUint(info.Metadata["committed_through"], 10, 64)
		if floorErr != nil || headErr != nil || info.Reason == "" {
			return &events.Error{Code: events.CodeUnsupportedFormat, Cause: fmt.Errorf("invalid puller error details: %w", err)}
		}
		return &events.Error{
			Code: events.ErrorCode(info.Reason), Backend: info.Metadata["backend"], SourceID: info.Metadata["source_id"],
			Generation: info.Metadata["generation"], DiscardedThrough: floor, CommittedThrough: head, Cause: err,
		}
	}
	return err
}

func convertEvent(envelope *pullerv1.PullerEvent) (*events.PullerEvent, error) {
	if envelope == nil {
		return nil, errors.New("nil puller envelope")
	}
	event := &events.PullerEvent{Progress: envelope.Progress}
	change := envelope.ChangeEvent
	if change == nil {
		return event, nil
	}
	normalized := &events.StoreChangeEvent{
		EventID: change.EventId, Database: change.Database, MgoColl: change.MgoColl, MgoDocID: change.MgoDocId,
		OpType: events.StoreOperationType(change.OpType), Timestamp: change.Timestamp, TxnNumber: &change.TxnNumber, Backend: change.Backend,
	}
	if change.ClusterTime != nil {
		normalized.ClusterTime = events.ClusterTime{T: change.ClusterTime.T, I: change.ClusterTime.I}
	}
	if len(change.FullDoc) > 0 {
		var doc storage.StoredDoc
		if err := json.Unmarshal(change.FullDoc, &doc); err != nil {
			return nil, fmt.Errorf("decode full document: %w", err)
		}
		normalized.FullDocument = &doc
	}
	if len(change.UpdateDesc) > 0 {
		var desc events.UpdateDescription
		if err := json.Unmarshal(change.UpdateDesc, &desc); err != nil {
			return nil, fmt.Errorf("decode update description: %w", err)
		}
		normalized.UpdateDesc = &desc
	}
	event.Change = normalized
	return event, nil
}
