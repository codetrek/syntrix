package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/syntrixbase/syntrix/api/gen/streamer/v1"
	"github.com/syntrixbase/syntrix/internal/helper"
	"github.com/syntrixbase/syntrix/internal/puller"
	pullerclient "github.com/syntrixbase/syntrix/internal/puller/client"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	celengine "github.com/syntrixbase/syntrix/internal/streamer/cel"
	"github.com/syntrixbase/syntrix/internal/streamer/manager"
	"github.com/syntrixbase/syntrix/pkg/model"
	"google.golang.org/grpc"
)

// ServerConfig configures the Streamer service.
type ServerConfig struct {
	// PullerAddr is the address of the Puller gRPC server.
	// If empty, Streamer runs in standalone mode without event ingestion.
	PullerAddr string `yaml:"puller_addr"`

	// SendTimeout is the maximum time to wait for a gateway to accept an event.
	// If the timeout is exceeded, the gateway is considered slow and should be disconnected.
	// Default: 5 seconds.
	SendTimeout time.Duration `yaml:"send_timeout"`

	// pullerClient is an optional pre-configured Puller client.
	// If provided, it will be used instead of creating a new client from PullerAddr.
	// This is primarily for testing purposes.
	pullerClient puller.Service
}

func DefaultServiceConfig() ServerConfig {
	return ServerConfig{
		PullerAddr:  "http://localhost:9000",
		SendTimeout: 5 * time.Second,
	}
}

// ApplyDefaults fills in zero values with defaults.
func (c *ServerConfig) ApplyDefaults() {
	defaults := DefaultServiceConfig()
	if c.PullerAddr == "" {
		c.PullerAddr = defaults.PullerAddr
	}
	if c.SendTimeout == 0 {
		c.SendTimeout = defaults.SendTimeout
	}
}

// ServiceConfigOption is a functional option for ServiceConfig.
type ServiceConfigOption func(*ServerConfig)

// WithPullerClient sets a pre-configured Puller client (for testing).
func WithPullerClient(client puller.Service) ServiceConfigOption {
	return func(c *ServerConfig) {
		c.pullerClient = client
	}
}

// streamerService implements the Streamer gRPC service and StreamerServer interface.
type streamerService struct {
	pb.UnimplementedStreamerServiceServer

	config  ServerConfig
	manager *manager.Manager

	// streams maps gatewayID to its localStream
	streams   map[string]*localStream
	streamsMu sync.RWMutex

	// Puller integration (created internally on Start)
	pullerClient puller.Service
	progress     string

	lifecycleMu sync.Mutex
	started     bool
	consumeDone chan struct{}
	cleanupErr  error

	ctx    context.Context
	cancel context.CancelCauseFunc

	logger *slog.Logger
}

// NewService creates a new streamerService and returns the StreamerServer interface.
func NewService(config ServerConfig, logger *slog.Logger, opts ...ServiceConfigOption) (StreamerServer, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply functional options
	for _, opt := range opts {
		opt(&config)
	}

	// Set default timeout if not configured
	if config.SendTimeout == 0 {
		config.SendTimeout = 5 * time.Second
	}

	celCompiler, err := celengine.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	return &streamerService{
		config:  config,
		manager: manager.New(manager.WithCELCompiler(celCompiler)),
		streams: make(map[string]*localStream),
		ctx:     ctx,
		cancel:  cancel,
		logger:  logger.With("component", "streamer"),
	}, nil
}

// Stream implements the Service interface.
// Returns a bidirectional stream for Gateway communication.
func (s *streamerService) Stream(ctx context.Context) (Stream, error) {
	gatewayID := uuid.New().String()
	s.logger.Info("New local stream", "gatewayID", gatewayID)

	ls := newLocalStream(ctx, gatewayID, s)

	s.streamsMu.Lock()
	if err := s.Err(); err != nil {
		s.streamsMu.Unlock()
		ls.closeWithError(err)
		return nil, err
	}
	s.streams[gatewayID] = ls
	s.streamsMu.Unlock()

	// Monitor context and clean up when done
	go func() {
		<-ls.ctx.Done()
		s.removeStream(gatewayID)
	}()

	return ls, nil
}

// Start begins the streamer service, connecting to Puller and consuming events.
func (s *streamerService) Start(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if err := s.Err(); err != nil {
		return err
	}
	if s.started {
		return errors.New("streamer already started")
	}

	var ownedClient *pullerclient.Client
	if s.config.pullerClient != nil {
		s.pullerClient = s.config.pullerClient
	} else if s.config.PullerAddr != "" {
		client, err := pullerclient.New(s.config.PullerAddr, s.logger)
		if err != nil {
			return fmt.Errorf("create puller client: %w", err)
		}
		s.pullerClient = client
		ownedClient = client
	}

	if s.pullerClient == nil {
		s.started = true
		return nil
	}

	consumeCtx, cancel := context.WithCancelCause(s.ctx)
	stopCancellation := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	subscription, err := s.pullerClient.Subscribe(consumeCtx, puller.SubscribeOptions{
		ConsumerID: "streamer",
		After:      s.progress,
	})
	if err != nil {
		stopCancellation()
		cancel(err)
		if ownedClient != nil {
			err = errors.Join(err, ownedClient.Close())
		}
		err = fmt.Errorf("subscribe to puller: %w", err)
		s.terminate(err)
		return err
	}

	s.started = true
	s.consumeDone = make(chan struct{})
	go func() {
		defer close(s.consumeDone)
		defer stopCancellation()
		defer cancel(context.Canceled)
		defer func() {
			err := subscription.Close()
			if ownedClient != nil {
				err = errors.Join(err, ownedClient.Close())
			}
			s.cleanupErr = err
			if err != nil {
				s.logger.Error("Failed to close Puller resources", "error", err)
				s.terminate(fmt.Errorf("close puller resources: %w", err))
			}
		}()
		s.consumePullerEvents(consumeCtx, subscription)
	}()
	return nil
}

func (s *streamerService) consumePullerEvents(ctx context.Context, subscription puller.Subscription) {
	for {
		evt, err := subscription.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				s.terminate(context.Cause(ctx))
			} else {
				s.terminate(fmt.Errorf("consume puller event: %w", err))
			}
			return
		}
		if evt.Change != nil {
			event, err := events.Transform(evt)
			if err != nil && !errors.Is(err, events.ErrDeleteOPIgnored) {
				s.terminate(fmt.Errorf("transform puller event %s: %w", evt.Change.EventID, err))
				return
			}
			if err == nil {
				if err := s.ProcessEvent(event); err != nil {
					s.terminate(fmt.Errorf("process puller event %s: %w", evt.Change.EventID, err))
					return
				}
			}
		}
		if err := context.Cause(ctx); err != nil {
			s.terminate(err)
			return
		}
		s.progress = evt.Progress
	}
}

func (s *streamerService) Err() error { return context.Cause(s.ctx) }

func (s *streamerService) Done() <-chan struct{} { return s.ctx.Done() }

func (s *streamerService) terminate(err error) {
	if s.Err() == nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("Streamer ingestion stopped", "error", err)
	}
	s.cancel(err)
	s.streamsMu.Lock()
	for id, ls := range s.streams {
		ls.closeWithError(s.Err())
		delete(s.streams, id)
		s.manager.UnregisterGateway(id)
	}
	s.streamsMu.Unlock()
}

// Stop waits for the owned Puller subscription to release its resources.
func (s *streamerService) Stop(ctx context.Context) error {
	s.terminate(context.Canceled)
	s.lifecycleMu.Lock()
	done := s.consumeDone
	s.lifecycleMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return s.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// removeStream removes a stream from the service.
func (s *streamerService) removeStream(gatewayID string) {
	s.streamsMu.Lock()
	if ls, ok := s.streams[gatewayID]; ok {
		ls.close()
		delete(s.streams, gatewayID)
	}
	s.streamsMu.Unlock()

	s.manager.UnregisterGateway(gatewayID)
	s.logger.Info("Stream removed", "gatewayID", gatewayID)
}

// --- gRPC Server Methods ---

// GRPCStream implements the gRPC bidirectional streaming endpoint.
// This is called by gRPC framework, not directly by users.
func (s *streamerService) GRPCStream(stream grpc.BidiStreamingServer[pb.GatewayMessage, pb.StreamerMessage]) error {
	gatewayID := uuid.New().String()
	s.logger.Info("New gRPC stream connection", "gatewayID", gatewayID)

	// Create a gRPC stream adapter
	gs := newGRPCStreamAdapter(stream.Context(), gatewayID, stream, s)

	s.streamsMu.Lock()
	if err := s.Err(); err != nil {
		s.streamsMu.Unlock()
		gs.localStream.closeWithError(err)
		return err
	}
	s.streams[gatewayID] = gs.localStream
	s.streamsMu.Unlock()
	defer s.removeStream(gatewayID)

	return gs.run()
}

// ProcessEvent processes a single SyntrixChangeEvent, matching it to subscriptions
// and delivering to streams.
// Backpressure: Uses blocking send with timeout. If a gateway cannot accept
// the event within the timeout, it is considered slow and will be disconnected.
func (s *streamerService) ProcessEvent(event events.SyntrixChangeEvent) error {
	if err := s.Err(); err != nil {
		return err
	}
	if event.Document == nil {
		return nil // Skip events without document
	}

	s.logger.Debug("Streamer: processing event",
		"database", event.Database,
		"collection", event.Document.Collection,
	)

	doc := helper.FlattenStorageDocument(event.Document)
	matches := s.manager.Match(event.Database, event.Document.Collection, doc.GetID(), doc)

	if len(matches) == 0 {
		return nil
	}

	s.streamsMu.RLock()
	defer s.streamsMu.RUnlock()

	for gatewayID, subIDs := range matches {
		ls, ok := s.streams[gatewayID]
		if !ok {
			continue
		}

		// Convert SyntrixChangeEvent to EventDelivery
		delivery := syntrixEventToDelivery(event, doc, subIDs)

		// Blocking send with timeout - no drop allowed
		select {
		case ls.outgoing <- delivery:
			// Successfully sent
		case <-time.After(s.config.SendTimeout):
			// Gateway is too slow, mark for disconnection
			s.logger.Warn("Gateway too slow, timeout sending event",
				"gatewayID", gatewayID,
				"timeout", s.config.SendTimeout)
			go s.removeStream(gatewayID)
		case <-ls.ctx.Done():
			continue
		case <-s.ctx.Done():
			return s.Err()
		}
	}

	return nil
}

// ProcessEventJSON processes a JSON-encoded StoreChangeEvent from Puller.
// It transforms the event to SyntrixChangeEvent before processing.
func (s *streamerService) ProcessEventJSON(data []byte) error {
	var pEvent events.PullerEvent
	if err := json.Unmarshal(data, &pEvent); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	event, err := events.Transform(&pEvent)
	if err != nil {
		if errors.Is(err, events.ErrDeleteOPIgnored) {
			return nil // Delete operations are handled differently
		}
		return fmt.Errorf("failed to transform event: %w", err)
	}

	return s.ProcessEvent(event)
}

// Compile-time checks
var _ Service = (*streamerService)(nil)
var _ StreamerServer = (*streamerService)(nil)
var _ EventProcessor = (*streamerService)(nil)
var _ subscriptionHandler = (*streamerService)(nil)

// --- subscriptionHandler implementation ---

// subscribe implements subscriptionHandler for localStream.
func (s *streamerService) subscribe(gatewayID, database, collection string, filters []model.Filter) (string, error) {
	s.streamsMu.RLock()
	defer s.streamsMu.RUnlock()
	if err := s.Err(); err != nil {
		return "", err
	}
	subID := uuid.New().String()
	protoReq := &pb.SubscribeRequest{
		SubscriptionId: subID,
		Database:       database,
		Collection:     collection,
		Filters:        filtersToProto(filters),
	}

	resp, err := s.manager.Subscribe(gatewayID, protoReq)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("subscribe failed: %s", resp.Error)
	}
	return resp.SubscriptionId, nil
}

// unsubscribe implements subscriptionHandler for localStream.
func (s *streamerService) unsubscribe(subscriptionID string) error {
	return s.manager.Unsubscribe(subscriptionID)
}
