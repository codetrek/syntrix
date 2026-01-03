package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/puller/internal/core"
	"github.com/codetrek/syntrix/internal/puller/internal/cursor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EventSource provides the event handler setter interface.
// The handler function receives events from the puller for distribution.
type EventSource interface {
	SetEventHandler(handler func(ctx context.Context, backendName string, event *events.ChangeEvent) error)
	Replay(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error)
}

// Server implements the PullerService gRPC interface.
type Server struct {
	pullerv1.UnimplementedPullerServiceServer

	cfg         config.PullerGRPCConfig
	eventSource EventSource
	logger      *slog.Logger
	grpcServer  *grpc.Server
	subs        *core.SubscriberManager

	// eventChan receives events from the puller for broadcasting
	eventChan chan *events.ChangeEvent

	// ctx controls the server lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// mu protects server state
	mu sync.RWMutex

	// running indicates if the server is running
	running bool
}

// NewServer creates a new gRPC server.
func NewServer(cfg config.PullerGRPCConfig, eventSource EventSource, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	channelSize := cfg.ChannelSize
	if channelSize <= 0 {
		channelSize = 10000
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:         cfg,
		eventSource: eventSource,
		logger:      logger.With("component", "puller-grpc"),
		subs:        core.NewSubscriberManager(logger),
		eventChan:   make(chan *events.ChangeEvent, channelSize),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start starts the gRPC server.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}

	// Create gRPC server with options
	opts := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(uint32(s.cfg.MaxConnections)),
	}
	s.grpcServer = grpc.NewServer(opts...)
	pullerv1.RegisterPullerServiceServer(s.grpcServer, s)

	// Start listening
	lis, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.Address, err)
	}

	s.running = true
	s.mu.Unlock()

	// Set up event handler to receive events from puller
	s.eventSource.SetEventHandler(func(ctx context.Context, backendName string, event *events.ChangeEvent) error {
		// Ensure backend name is set
		if event.Backend == "" {
			event.Backend = backendName
		}
		select {
		case s.eventChan <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	s.logger.Info("gRPC server starting", "address", s.cfg.Address)

	// Start event processing loop
	go s.processEvents()

	// Serve in a goroutine
	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", "error", err)
		}
	}()

	return nil
}

// processEvents reads events from the channel and broadcasts them to subscribers.
func (s *Server) processEvents() {
	for {
		select {
		case event, ok := <-s.eventChan:
			if !ok {
				return
			}
			s.subs.Broadcast(event)
		case <-s.ctx.Done():
			return
		}
	}
}

// Stop stops the gRPC server gracefully.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	// Signal shutdown
	s.cancel()

	// Close all subscribers
	s.subs.CloseAll()

	// Stop gRPC server
	if s.grpcServer != nil {
		// Create a channel to signal graceful stop completion
		done := make(chan struct{})
		go func() {
			s.grpcServer.GracefulStop()
			close(done)
		}()

		select {
		case <-done:
			s.logger.Info("gRPC server stopped gracefully")
		case <-ctx.Done():
			s.grpcServer.Stop()
			s.logger.Warn("gRPC server force stopped")
		}
	}

	s.running = false
	return nil
}

// Subscribe implements the PullerService Subscribe RPC.
func (s *Server) Subscribe(req *pullerv1.SubscribeRequest, stream pullerv1.PullerService_SubscribeServer) error {
	ctx := stream.Context()

	// Parse the 'after' progress marker
	after, err := cursor.DecodeProgressMarker(req.GetAfter())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid 'after' progress marker: %v", err)
	}

	channelSize := s.cfg.ChannelSize
	if channelSize <= 0 {
		channelSize = 10000
	}

	// Create subscriber
	sub := core.NewSubscriber(req.GetConsumerId(), after, req.GetCoalesceOnCatchUp(), channelSize)
	s.subs.Add(sub)
	defer s.subs.Remove(sub.ID)

	s.logger.Info("subscriber connected",
		"consumerId", sub.ID,
		"after", req.GetAfter(),
		"coalesceOnCatchUp", sub.CoalesceOnCatchUp,
	)

	// Mode: "catchup" or "live"
	mode := "catchup"
	if req.GetAfter() == "" {
		mode = "live"
		s.logger.Info("starting in live mode (no history requested)", "consumerId", sub.ID)
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("subscriber disconnected", "consumerId", sub.ID)
			return nil
		case <-sub.Done():
			s.logger.Info("subscriber closed", "consumerId", sub.ID)
			return status.Error(codes.Canceled, "subscription closed")
		default:
		}

		if mode == "catchup" {
			// Drain channel to make space for new events
			drainChannel(sub.Events())
			sub.GetAndResetOverflow() // Clear overflow flag

			// Start replay
			iter, err := s.eventSource.Replay(ctx, sub.CurrentProgress().Positions, sub.CoalesceOnCatchUp)
			if err != nil {
				s.logger.Error("failed to start replay", "error", err)
				return status.Errorf(codes.Internal, "failed to start replay: %v", err)
			}

			// Replay loop
			for iter.Next() {
				evt := iter.Event()

				// Deduplication: check if event is already sent
				// This is crucial if Replay restarts or if ScanFrom is inclusive
				if !sub.ShouldSend(evt.Backend, evt.ClusterTime) {
					continue
				}

				if err := s.sendEvent(stream, sub, evt.Backend, evt); err != nil {
					iter.Close()
					return err
				}
			}
			iter.Close()

			if err := iter.Err(); err != nil {
				s.logger.Error("replay error", "error", err)
				return status.Errorf(codes.Internal, "replay error: %v", err)
			}

			// Check if we overflowed during replay
			if sub.GetAndResetOverflow() {
				s.logger.Info("subscriber overflowed during replay, continuing catchup", "consumerId", sub.ID)
				continue
			}

			// Caught up
			mode = "live"
			s.logger.Info("subscriber caught up, switching to live mode", "consumerId", sub.ID)

		} else {
			// Live loop
			select {
			case <-ctx.Done():
				s.logger.Info("subscriber disconnected", "consumerId", sub.ID)
				return nil

			case <-sub.Done():
				s.logger.Info("subscriber closed", "consumerId", sub.ID)
				return status.Error(codes.Canceled, "subscription closed")

			case evt := <-sub.Events():
				// Check for overflow, but don't switch immediately.
				// We need to process the current event 'evt' first to ensure we don't drop it.
				// If we switch immediately, 'evt' would be lost.
				hasOverflow := sub.GetAndResetOverflow()

				if evt != nil && sub.ShouldSend(evt.Backend, evt.ClusterTime) {
					if err := s.sendEvent(stream, sub, evt.Backend, evt); err != nil {
						return err
					}
				}

				if hasOverflow {
					s.logger.Info("subscriber overflowed, switching to catchup", "consumerId", sub.ID)
					mode = "catchup"
					continue
				}
			}
		}
	}
}

func drainChannel(ch <-chan *events.ChangeEvent) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// sendEvent sends a single event to the stream and updates subscriber position.
func (s *Server) sendEvent(stream pullerv1.PullerService_SubscribeServer, sub *core.Subscriber, backend string, evt *events.ChangeEvent) error {
	// Convert to gRPC event
	changeEvt, err := s.convertEvent(backend, evt)
	if err != nil {
		s.logger.Error("failed to convert event", "error", err)
		return nil // Skip invalid events
	}

	// Update subscriber position and add progress marker
	sub.UpdatePosition(backend, evt.EventID, evt.ClusterTime)

	pullerEvt := &pullerv1.PullerEvent{
		ChangeEvent: changeEvt,
		Progress:    sub.CurrentProgress().Encode(),
	}

	// Send to client
	if err := stream.Send(pullerEvt); err != nil {
		s.logger.Error("failed to send event", "error", err, "consumerId", sub.ID)
		return err
	}
	return nil
}

// convertEvent converts a ChangeEvent to a gRPC ChangeEvent.
func (s *Server) convertEvent(backend string, evt *events.ChangeEvent) (*pullerv1.ChangeEvent, error) {
	var fullDocBytes []byte
	var updateDescBytes []byte
	var err error

	if evt.FullDocument != nil {
		fullDocBytes, err = json.Marshal(evt.FullDocument)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal full document: %w", err)
		}
	}

	if evt.UpdateDesc != nil {
		updateDescBytes, err = json.Marshal(evt.UpdateDesc)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal update description: %w", err)
		}
	}

	var txnNumber int64
	if evt.TxnNumber != nil {
		txnNumber = *evt.TxnNumber
	}

	return &pullerv1.ChangeEvent{
		EventId:    evt.EventID,
		Tenant:     evt.TenantID,
		MgoColl:    evt.MgoColl,
		MgoDocId:   evt.MgoDocID,
		OpType:     string(evt.OpType),
		FullDoc:    fullDocBytes,
		UpdateDesc: updateDescBytes,
		ClusterTime: &pullerv1.ClusterTime{
			T: evt.ClusterTime.T,
			I: evt.ClusterTime.I,
		},
		Timestamp: evt.Timestamp,
		TxnNumber: txnNumber,
		Backend:   backend,
	}, nil
}

// SubscriberCount returns the number of active subscribers.
func (s *Server) SubscriberCount() int {
	return s.subs.Count()
}
