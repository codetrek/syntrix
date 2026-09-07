package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type EventSource interface {
	Subscribe(context.Context, events.SubscribeOptions) (events.Subscription, error)
}

// Server adapts the shared subscription protocol to gRPC.
type Server struct {
	pullerv1.UnimplementedPullerServiceServer
	cfg    config.GRPCConfig
	source EventSource
	logger *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	active int
}

func NewServer(cfg config.GRPCConfig, source EventSource, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{cfg: cfg, source: source, logger: logger.With("component", "puller-grpc"), ctx: ctx, cancel: cancel}
}

func (s *Server) Shutdown() { s.cancel() }

func (s *Server) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Server) Subscribe(req *pullerv1.SubscribeRequest, stream pullerv1.PullerService_SubscribeServer) error {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return status.Error(codes.Unavailable, "puller transport stopped")
	}
	if s.active >= s.cfg.MaxConnections {
		s.mu.Unlock()
		return transportError(&events.Error{Code: events.CodeOverloaded, Cause: errors.New("maximum transport subscriptions reached")})
	}
	s.active++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()

	ctx, cancel := context.WithCancel(stream.Context())
	stopShutdown := context.AfterFunc(s.ctx, cancel)
	defer stopShutdown()
	defer cancel()
	sub, err := s.source.Subscribe(ctx, events.SubscribeOptions{
		ConsumerID: req.GetConsumerId(), After: req.GetAfter(), CoalesceOnCatchUp: req.GetCoalesceOnCatchUp(),
	})
	if err != nil {
		s.logger.Error("subscribe rejected", "consumer_id", req.GetConsumerId(), "error", err)
		return transportError(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			s.logger.Error("close subscription", "error", err)
		}
	}()
	initial, err := sub.Next(ctx)
	if err != nil {
		return s.endError(err)
	}
	if initial == nil || initial.Change != nil || initial.Progress == "" {
		return transportError(&events.Error{Code: events.CodeUnsupportedFormat, Cause: errors.New("subscription did not provide its initial progress")})
	}
	// The starting position anchors reconnection even before the first delivery.
	if err := stream.SendHeader(metadata.Pairs("syntrix-puller-subscription", "ready", "syntrix-puller-initial-progress", initial.Progress)); err != nil {
		return s.endError(err)
	}
	send := streamSender(ctx, stream)
	if err := send(&pullerv1.PullerEvent{Progress: initial.Progress}); err != nil {
		return s.endError(err)
	}

	type result struct {
		event *events.PullerEvent
		err   error
	}
	results := make(chan result)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			event, err := sub.Next(ctx)
			select {
			case results <- result{event, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); <-readerDone }()

	var heartbeat <-chan time.Time
	if s.cfg.HeartbeatInterval > 0 {
		ticker := time.NewTicker(s.cfg.HeartbeatInterval)
		defer ticker.Stop()
		heartbeat = ticker.C
	}
	progress := initial.Progress
	for {
		select {
		case <-ctx.Done():
			if s.ctx.Err() != nil {
				return status.Error(codes.Unavailable, "puller transport stopped")
			}
			return status.FromContextError(ctx.Err()).Err()
		case <-heartbeat:
			if err := send(&pullerv1.PullerEvent{Progress: progress}); err != nil {
				return s.endError(err)
			}
		case next := <-results:
			if s.ctx.Err() != nil {
				return status.Error(codes.Unavailable, "puller transport stopped")
			}
			if next.err == io.EOF {
				return nil
			}
			if next.err != nil {
				s.logger.Error("subscription ended", "consumer_id", req.GetConsumerId(), "error", next.err)
				return transportError(next.err)
			}
			envelope, err := convertEvent(next.event)
			if err != nil {
				return transportError(&events.Error{Code: events.CodeUnsupportedFormat, Cause: err})
			}
			if err := send(envelope); err != nil {
				return s.endError(err)
			}
			progress = next.event.Progress
		}
	}
}

func (s *Server) endError(err error) error {
	if s.ctx.Err() != nil {
		return status.Error(codes.Unavailable, "puller transport stopped")
	}
	return transportError(err)
}

func streamSender(ctx context.Context, stream pullerv1.PullerService_SubscribeServer) func(*pullerv1.PullerEvent) error {
	requests := make(chan *pullerv1.PullerEvent)
	results := make(chan error, 1)
	// Returning the handler lets gRPC cancel its original stream context and
	// release a Send blocked on flow control. We must not join this worker first.
	// https://github.com/grpc/grpc-go/blob/9df039ef2c921978514b600c9d5c6bf25cce54f6/internal/transport/http2_server.go#L1316-L1320
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-requests:
				err := stream.Send(event)
				results <- err
				if err != nil {
					return
				}
			}
		}
	}()
	return func(event *pullerv1.PullerEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case requests <- event:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-results:
			return err
		}
	}
}

func convertEvent(event *events.PullerEvent) (*pullerv1.PullerEvent, error) {
	if event == nil {
		return nil, errors.New("nil subscription delivery")
	}
	envelope := &pullerv1.PullerEvent{Progress: event.Progress}
	evt := event.Change
	if evt == nil {
		return envelope, nil
	}
	var fullDoc, updateDesc []byte
	var err error
	if evt.FullDocument != nil {
		fullDoc, err = json.Marshal(evt.FullDocument)
		if err != nil {
			return nil, fmt.Errorf("marshal full document: %w", err)
		}
	}
	if evt.UpdateDesc != nil {
		updateDesc, err = json.Marshal(evt.UpdateDesc)
		if err != nil {
			return nil, fmt.Errorf("marshal update description: %w", err)
		}
	}
	var txnNumber int64
	if evt.TxnNumber != nil {
		txnNumber = *evt.TxnNumber
	}
	envelope.ChangeEvent = &pullerv1.ChangeEvent{
		EventId: evt.EventID, Database: evt.Database, MgoColl: evt.MgoColl, MgoDocId: evt.MgoDocID,
		OpType: string(evt.OpType), FullDoc: fullDoc, UpdateDesc: updateDesc,
		ClusterTime: &pullerv1.ClusterTime{T: evt.ClusterTime.T, I: evt.ClusterTime.I},
		Timestamp:   evt.Timestamp, TxnNumber: txnNumber, Backend: evt.Backend,
	}
	return envelope, nil
}

func transportError(err error) error {
	var domainErr *events.Error
	if errors.As(err, &domainErr) {
		code := codes.FailedPrecondition
		switch domainErr.Code {
		case events.CodeInvalidCursor, events.CodeUnknownSource, events.CodePositionAhead:
			code = codes.InvalidArgument
		case events.CodeStorageFailure:
			code = codes.Internal
		case events.CodeSourceUnavailable:
			code = codes.Unavailable
		case events.CodeOverloaded:
			code = codes.ResourceExhausted
		}
		detail := &errdetails.ErrorInfo{
			Domain: "syntrix.puller", Reason: string(domainErr.Code),
			Metadata: map[string]string{
				"backend": domainErr.Backend, "source_id": domainErr.SourceID, "generation": domainErr.Generation,
				"discarded_through": strconv.FormatUint(domainErr.DiscardedThrough, 10),
				"committed_through": strconv.FormatUint(domainErr.CommittedThrough, 10),
			},
		}
		st, detailErr := status.New(code, string(domainErr.Code)).WithDetails(detail)
		if detailErr != nil {
			return status.Errorf(codes.Internal, "encode subscription failure: %v", detailErr)
		}
		return st.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
