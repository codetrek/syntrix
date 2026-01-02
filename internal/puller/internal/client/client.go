// Package client implements the gRPC client for the puller service.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/storage"
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
func (c *Client) Subscribe(ctx context.Context, consumerID string, after string) (<-chan *events.PullerEvent, error) {
	req := &pullerv1.SubscribeRequest{
		ConsumerId: consumerID,
		After:      after,
	}

	stream, err := c.client.Subscribe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	ch := make(chan *events.PullerEvent, 1000)

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
func (c *Client) SubscribeWithCoalesce(ctx context.Context, consumerID string, after string) (<-chan *events.PullerEvent, error) {
	req := &pullerv1.SubscribeRequest{
		ConsumerId:        consumerID,
		After:             after,
		CoalesceOnCatchUp: true,
	}

	stream, err := c.client.Subscribe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	ch := make(chan *events.PullerEvent, 1000)

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

// convertEvent converts a gRPC event to a PullerEvent.
func (c *Client) convertEvent(evt *pullerv1.PullerEvent) *events.PullerEvent {
	change := evt.ChangeEvent

	normalized := &events.ChangeEvent{
		EventID:   change.EventId,
		TenantID:  change.Tenant,
		MgoColl:   change.MgoColl,
		MgoDocID:  change.MgoDocId,
		OpType:    events.OperationType(change.OpType),
		Timestamp: change.Timestamp,
		TxnNumber: &change.TxnNumber,
		Backend:   change.Backend,
	}

	if change.ClusterTime != nil {
		normalized.ClusterTime = events.ClusterTime{
			T: change.ClusterTime.T,
			I: change.ClusterTime.I,
		}
	}

	if len(change.FullDoc) > 0 {
		var doc storage.Document
		if err := json.Unmarshal(change.FullDoc, &doc); err == nil {
			normalized.FullDocument = &doc
		} else {
			c.logger.Error("failed to unmarshal full document", "error", err)
		}
	}

	if len(change.UpdateDesc) > 0 {
		var desc events.UpdateDescription
		if err := json.Unmarshal(change.UpdateDesc, &desc); err == nil {
			normalized.UpdateDesc = &desc
		} else {
			c.logger.Error("failed to unmarshal update description", "error", err)
		}
	}

	return &events.PullerEvent{
		Change:   normalized,
		Progress: evt.Progress,
	}
}
