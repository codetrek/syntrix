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
	"github.com/codetrek/syntrix/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EventSource provides the event handler setter interface.
// The handler function receives events from the puller for distribution.
type EventSource interface {
	SetEventHandler(handler func(ctx context.Context, backendName string, event *events.NormalizedEvent) error)
}

// Server implements the PullerService gRPC interface.
type Server struct {
	pullerv1.UnimplementedPullerServiceServer

	cfg         config.PullerGRPCConfig
	eventSource EventSource
	logger      *slog.Logger
	grpcServer  *grpc.Server
	subs        *SubscriberManager

	// eventChan receives events from the puller for broadcasting
	eventChan chan *backendEvent

	// mu protects server state
	mu sync.RWMutex

	// running indicates if the server is running
	running bool
}

// backendEvent pairs an event with its backend name
type backendEvent struct {
	backend string
	event   *events.NormalizedEvent
}

// NewServer creates a new gRPC server.
func NewServer(cfg config.PullerGRPCConfig, eventSource EventSource, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:         cfg,
		eventSource: eventSource,
		logger:      logger.With("component", "puller-grpc"),
		subs:        NewSubscriberManager(),
		eventChan:   make(chan *backendEvent, 10000),
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
	s.eventSource.SetEventHandler(func(ctx context.Context, backendName string, event *events.NormalizedEvent) error {
		select {
		case s.eventChan <- &backendEvent{backend: backendName, event: event}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel full, log warning
			s.logger.Warn("event channel full, dropping event", "eventId", event.EventID)
			return nil
		}
	})

	s.logger.Info("gRPC server starting", "address", s.cfg.Address)

	// Serve in a goroutine
	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the gRPC server gracefully.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

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
	after := DecodeProgressMarker(req.GetAfter())

	// Create subscriber
	sub := NewSubscriber(req.GetConsumerId(), after, req.GetCoalesceOnCatchUp())
	s.subs.Add(sub)
	defer s.subs.Remove(sub.ID)

	s.logger.Info("subscriber connected",
		"consumerId", sub.ID,
		"after", req.GetAfter(),
		"coalesceOnCatchUp", sub.CoalesceOnCatchUp,
	)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("subscriber disconnected", "consumerId", sub.ID)
			return nil

		case <-sub.Done():
			s.logger.Info("subscriber closed", "consumerId", sub.ID)
			return status.Error(codes.Canceled, "subscription closed")

		case be := <-s.eventChan:
			if be == nil {
				continue
			}

			// Convert to gRPC event
			evt, err := s.convertEvent(be.backend, be.event)
			if err != nil {
				s.logger.Error("failed to convert event", "error", err)
				continue
			}

			// Update subscriber position and add progress marker
			sub.UpdatePosition(be.backend, be.event.EventID)
			evt.Progress = sub.CurrentProgress().Encode()

			// Send to client
			if err := stream.Send(evt); err != nil {
				s.logger.Error("failed to send event", "error", err, "consumerId", sub.ID)
				return err
			}
		}
	}
}

// convertEvent converts a NormalizedEvent to a gRPC Event.
func (s *Server) convertEvent(backend string, evt *events.NormalizedEvent) (*pullerv1.Event, error) {
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

	return &pullerv1.Event{
		Id:                evt.EventID,
		Tenant:            evt.TenantID,
		Collection:        evt.Collection,
		DocumentId:        evt.DocumentID,
		OperationType:     string(evt.Type),
		FullDocument:      fullDocBytes,
		UpdateDescription: updateDescBytes,
		ClusterTime: &pullerv1.ClusterTime{
			T: evt.ClusterTime.T,
			I: evt.ClusterTime.I,
		},
		Timestamp: evt.Timestamp,
	}, nil
}

// SubscriberCount returns the number of active subscribers.
func (s *Server) SubscriberCount() int {
	return s.subs.Count()
}
