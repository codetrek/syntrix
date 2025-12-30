// Package client implements the gRPC client for the puller service.
package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is a gRPC client for the puller service.
type Client struct {
	conn   *grpc.ClientConn
	client pullerv1.PullerServiceClient
	logger *slog.Logger
}

// New creates a new puller client.
func New(address string, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to puller service: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pullerv1.NewPullerServiceClient(conn),
		logger: logger.With("component", "puller-client"),
	}, nil
}

// Subscribe subscribes to events from the puller service.
// The after parameter is the progress marker to resume from.
// Returns a channel of events that will be closed when the subscription ends.
func (c *Client) Subscribe(ctx context.Context, consumerID string, after string) (<-chan *events.NormalizedEvent, error) {
	req := &pullerv1.SubscribeRequest{
		ConsumerId: consumerID,
		After:      after,
	}

	stream, err := c.client.Subscribe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	ch := make(chan *events.NormalizedEvent, 1000)

	go func() {
		defer close(ch)

		for {
			evt, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					c.logger.Info("subscription stream closed")
					return
				}
				c.logger.Error("subscription stream error", "error", err)
				return
			}

			normalized := c.convertEvent(evt)
			select {
			case ch <- normalized:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// SubscribeWithCoalesce subscribes with catch-up coalescing enabled.
func (c *Client) SubscribeWithCoalesce(ctx context.Context, consumerID string, after string) (<-chan *events.NormalizedEvent, error) {
	req := &pullerv1.SubscribeRequest{
		ConsumerId:        consumerID,
		After:             after,
		CoalesceOnCatchUp: true,
	}

	stream, err := c.client.Subscribe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	ch := make(chan *events.NormalizedEvent, 1000)

	go func() {
		defer close(ch)

		for {
			evt, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					c.logger.Info("subscription stream closed")
					return
				}
				c.logger.Error("subscription stream error", "error", err)
				return
			}

			normalized := c.convertEvent(evt)
			select {
			case ch <- normalized:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// convertEvent converts a gRPC event to a NormalizedEvent.
func (c *Client) convertEvent(evt *pullerv1.Event) *events.NormalizedEvent {
	normalized := &events.NormalizedEvent{
		EventID:    evt.Id,
		TenantID:   evt.Tenant,
		Collection: evt.Collection,
		DocumentID: evt.DocumentId,
		Type:       events.OperationType(evt.OperationType),
		Timestamp:  evt.Timestamp,
	}

	if evt.ClusterTime != nil {
		normalized.ClusterTime = events.ClusterTime{
			T: evt.ClusterTime.T,
			I: evt.ClusterTime.I,
		}
	}

	// Note: FullDocument and UpdateDesc would need JSON unmarshaling
	// For now, we don't decode them since they're bytes

	return normalized
}
