package streamer

import (
	"context"
	"io"

	"github.com/syntrixbase/syntrix/pkg/model"
)

// localStream implements the Stream interface for local (in-process) communication.
// It provides direct synchronous calls to the subscription handler, avoiding
// unnecessary message serialization and channel passing.
type localStream struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	gatewayID string
	handler   subscriptionHandler

	// outgoing delivers events to the consumer
	outgoing chan *EventDelivery
}

func newLocalStream(ctx context.Context, gatewayID string, handler subscriptionHandler) *localStream {
	ctx, cancel := context.WithCancelCause(ctx)
	return &localStream{
		ctx:       ctx,
		cancel:    cancel,
		gatewayID: gatewayID,
		handler:   handler,
		outgoing:  make(chan *EventDelivery, 1000),
	}
}

// Subscribe creates a new subscription and returns the subscription ID.
func (ls *localStream) Subscribe(database, collection string, filters []model.Filter) (string, error) {
	if err := context.Cause(ls.ctx); err != nil {
		return "", err
	}

	return ls.handler.subscribe(ls.gatewayID, database, collection, filters)
}

// Unsubscribe removes a subscription by ID.
func (ls *localStream) Unsubscribe(subscriptionID string) error {
	if err := context.Cause(ls.ctx); err != nil {
		return err
	}

	return ls.handler.unsubscribe(subscriptionID)
}

// Recv receives an EventDelivery from the Streamer.
func (ls *localStream) Recv() (*EventDelivery, error) {
	if err := context.Cause(ls.ctx); err != nil {
		return nil, err
	}
	select {
	case delivery, ok := <-ls.outgoing:
		if !ok {
			return nil, io.EOF
		}
		return delivery, nil
	case <-ls.ctx.Done():
		return nil, context.Cause(ls.ctx)
	}
}

// Close closes the stream and releases resources.
func (ls *localStream) Close() error {
	ls.close()
	return nil
}

func (ls *localStream) close() { ls.closeWithError(io.EOF) }

func (ls *localStream) closeWithError(err error) { ls.cancel(err) }

// Compile-time check
var _ Stream = (*localStream)(nil)
