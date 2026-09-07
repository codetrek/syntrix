package streamer

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	pb "github.com/syntrixbase/syntrix/api/gen/streamer/v1"
	"google.golang.org/grpc"
)

// grpcStreamAdapter adapts a gRPC stream to work with the streamer service.
// Unlike localStream which uses direct calls, gRPC adapter handles proto messages
// and performs protocol conversion.
type grpcStreamAdapter struct {
	ctx         context.Context
	gatewayID   string
	grpcStream  grpc.BidiStreamingServer[pb.GatewayMessage, pb.StreamerMessage]
	localStream *localStream
	service     *streamerService
	logger      *slog.Logger
	sendMu      sync.Mutex
}

func newGRPCStreamAdapter(
	ctx context.Context,
	gatewayID string,
	stream grpc.BidiStreamingServer[pb.GatewayMessage, pb.StreamerMessage],
	service *streamerService,
) *grpcStreamAdapter {
	return &grpcStreamAdapter{
		ctx:         ctx,
		gatewayID:   gatewayID,
		grpcStream:  stream,
		localStream: newLocalStream(ctx, gatewayID, service),
		service:     service,
		logger:      service.logger.With("gatewayID", gatewayID),
	}
}

func (g *grpcStreamAdapter) run() error {
	defer g.localStream.close()
	errChan := make(chan error, 2)

	// Handle incoming gRPC messages
	go func() {
		for {
			msg, err := g.grpcStream.Recv()
			if err != nil {
				errChan <- err
				return
			}

			// Process the proto message directly
			if err := g.handleProtoMessage(msg); err != nil {
				g.logger.Error("Failed to handle proto message", "error", err)
				errChan <- err
				return
			}
		}
	}()

	go func() {
		for {
			delivery, err := g.localStream.Recv()
			if err != nil {
				errChan <- err
				return
			}
			if err := g.send(&pb.StreamerMessage{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: eventDeliveryToProto(delivery),
				},
			}); err != nil {
				errChan <- err
				return
			}
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-g.ctx.Done():
		return g.ctx.Err()
	case <-g.localStream.ctx.Done():
		return context.Cause(g.localStream.ctx)
	case <-g.service.ctx.Done():
		return g.service.Err()
	}
}

// handleProtoMessage processes a proto GatewayMessage directly.
func (g *grpcStreamAdapter) handleProtoMessage(msg *pb.GatewayMessage) error {
	switch m := msg.Payload.(type) {
	case *pb.GatewayMessage_Subscribe:
		req := m.Subscribe
		if req.SubscriptionId == "" {
			req.SubscriptionId = uuid.New().String()
		}

		g.service.streamsMu.RLock()
		if err := g.service.Err(); err != nil {
			g.service.streamsMu.RUnlock()
			return err
		}
		resp, err := g.service.manager.Subscribe(g.gatewayID, req)
		g.service.streamsMu.RUnlock()
		if err != nil {
			return err
		}

		// Send response back
		return g.send(&pb.StreamerMessage{
			Payload: &pb.StreamerMessage_SubscribeResponse{
				SubscribeResponse: resp,
			},
		})

	case *pb.GatewayMessage_Unsubscribe:
		if err := g.service.manager.Unsubscribe(m.Unsubscribe.SubscriptionId); err != nil {
			g.logger.Warn("Unsubscribe failed",
				"subscriptionID", m.Unsubscribe.SubscriptionId,
				"error", err)
		}
		return nil

	case *pb.GatewayMessage_Heartbeat:
		return g.send(&pb.StreamerMessage{
			Payload: &pb.StreamerMessage_HeartbeatAck{
				HeartbeatAck: &pb.HeartbeatAck{
					Timestamp: m.Heartbeat.Timestamp,
				},
			},
		})
	}
	return nil
}

func (g *grpcStreamAdapter) send(msg *pb.StreamerMessage) error {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	if err := context.Cause(g.localStream.ctx); err != nil {
		return err
	}
	return g.grpcStream.Send(msg)
}
