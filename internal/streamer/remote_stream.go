package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/syntrixbase/syntrix/api/gen/streamer/v1"
	"github.com/syntrixbase/syntrix/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// subscriptionInfo stores subscription details for recovery on reconnect.
type subscriptionInfo struct {
	database   string
	collection string
	filters    []model.Filter
}

// remoteStream implements the Stream interface for remote gRPC communication
// with automatic reconnection and subscription recovery.
type remoteStream struct {
	ctx        context.Context
	grpcStream pb.StreamerService_StreamClient
	client     *streamerClient
	logger     *slog.Logger

	closedMu    sync.Mutex
	closed      bool
	terminal    chan struct{}
	terminalErr error

	// Connection state
	state   ConnectionState
	stateMu sync.RWMutex

	// Active subscriptions for recovery on reconnect
	activeSubscriptions   map[string]*subscriptionInfo
	activeSubscriptionsMu sync.RWMutex

	// Pending subscribe requests waiting for responses
	pendingSubscribes   map[string]chan *pb.SubscribeResponse
	pendingSubscribesMu sync.Mutex

	// recvLoop runs in background to route event deliveries
	recvChan chan *EventDelivery
	recvErr  error
	recvOnce sync.Once

	// Activity tracking
	lastMessageTime   time.Time
	lastMessageTimeMu sync.RWMutex

	// Heartbeat management
	heartbeatStop chan struct{}
}

// Subscribe creates a new subscription and returns the subscription ID.
// The subscription is automatically restored on reconnection.
func (rs *remoteStream) Subscribe(database, collection string, filters []model.Filter) (string, error) {
	rs.ensureRecvLoop()

	rs.closedMu.Lock()
	if rs.closed || rs.terminalErr != nil {
		err := rs.closedErrorLocked()
		rs.closedMu.Unlock()
		return "", err
	}
	rs.closedMu.Unlock()

	// Generate subscription ID
	subID := uuid.New().String()

	// Create response channel
	respChan := make(chan *pb.SubscribeResponse, 1)
	rs.pendingSubscribesMu.Lock()
	rs.pendingSubscribes[subID] = respChan
	rs.pendingSubscribesMu.Unlock()

	defer func() {
		rs.pendingSubscribesMu.Lock()
		delete(rs.pendingSubscribes, subID)
		rs.pendingSubscribesMu.Unlock()
	}()

	// Send subscribe request
	protoReq := &pb.SubscribeRequest{
		SubscriptionId: subID,
		Database:       database,
		Collection:     collection,
		Filters:        filtersToProto(filters),
	}
	if err := rs.grpcStream.Send(&pb.GatewayMessage{
		Payload: &pb.GatewayMessage_Subscribe{Subscribe: protoReq},
	}); err != nil {
		return "", err
	}

	// Wait for response
	select {
	case resp := <-respChan:
		if !resp.Success {
			return "", fmt.Errorf("subscribe failed: %s", resp.Error)
		}
		// Store subscription info for potential recovery
		rs.activeSubscriptionsMu.Lock()
		rs.activeSubscriptions[subID] = &subscriptionInfo{
			database:   database,
			collection: collection,
			filters:    filters,
		}
		rs.activeSubscriptionsMu.Unlock()

		return resp.SubscriptionId, nil
	case <-rs.ctx.Done():
		return "", rs.ctx.Err()
	case <-rs.terminal:
		rs.closedMu.Lock()
		defer rs.closedMu.Unlock()
		return "", rs.terminalErr
	}
}

// Unsubscribe removes a subscription by ID.
func (rs *remoteStream) Unsubscribe(subscriptionID string) error {
	rs.closedMu.Lock()
	if rs.closed || rs.terminalErr != nil {
		err := rs.closedErrorLocked()
		rs.closedMu.Unlock()
		return err
	}
	rs.closedMu.Unlock()

	// Remove from active subscriptions
	rs.activeSubscriptionsMu.Lock()
	delete(rs.activeSubscriptions, subscriptionID)
	rs.activeSubscriptionsMu.Unlock()

	// Send unsubscribe request
	return rs.grpcStream.Send(&pb.GatewayMessage{
		Payload: &pb.GatewayMessage_Unsubscribe{
			Unsubscribe: &pb.UnsubscribeRequest{
				SubscriptionId: subscriptionID,
			},
		},
	})
}

// Recv receives an EventDelivery from the remote Streamer.
func (rs *remoteStream) Recv() (*EventDelivery, error) {
	rs.ensureRecvLoop()

	select {
	case delivery, ok := <-rs.recvChan:
		if !ok {
			if rs.recvErr != nil {
				return nil, rs.recvErr
			}
			return nil, io.EOF
		}
		return delivery, nil
	case <-rs.ctx.Done():
		return nil, rs.ctx.Err()
	}
}

// Close closes the stream.
func (rs *remoteStream) Close() error {
	rs.closedMu.Lock()
	defer rs.closedMu.Unlock()

	if !rs.closed {
		rs.closed = true
		// Stop heartbeat
		if rs.heartbeatStop != nil {
			close(rs.heartbeatStop)
		}
		rs.setState(StateDisconnected)
		return rs.grpcStream.CloseSend()
	}
	return nil
}

func (rs *remoteStream) closedErrorLocked() error {
	if rs.terminalErr != nil {
		return rs.terminalErr
	}
	return io.EOF
}

// State returns the current connection state.
func (rs *remoteStream) State() ConnectionState {
	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()
	return rs.state
}

// setState updates the connection state and notifies the callback.
func (rs *remoteStream) setState(state ConnectionState) {
	rs.stateMu.Lock()
	oldState := rs.state
	rs.state = state
	rs.stateMu.Unlock()

	if oldState != state {
		rs.logger.Info("connection state changed", "from", oldState.String(), "to", state.String())
		if rs.client != nil && rs.client.config.OnStateChange != nil {
			rs.client.config.OnStateChange(state, nil)
		}
	}
}

// notifyStateWithError updates the connection state with an error.
func (rs *remoteStream) notifyStateWithError(state ConnectionState, err error) {
	rs.stateMu.Lock()
	oldState := rs.state
	rs.state = state
	rs.stateMu.Unlock()

	if oldState != state || err != nil {
		rs.logger.Info("connection state changed", "from", oldState.String(), "to", state.String(), "error", err)
		if rs.client != nil && rs.client.config.OnStateChange != nil {
			rs.client.config.OnStateChange(state, err)
		}
	}
}

// ensureRecvLoop starts the background receive loop if not already running.
func (rs *remoteStream) ensureRecvLoop() {
	rs.recvOnce.Do(func() {
		rs.recvChan = make(chan *EventDelivery, 100)
		rs.terminal = make(chan struct{})
		rs.pendingSubscribes = make(map[string]chan *pb.SubscribeResponse)
		rs.heartbeatStop = make(chan struct{})
		rs.updateLastMessageTime()
		go rs.recvLoop()
		go rs.heartbeatLoop()
		go rs.activityMonitor()
	})
}

// updateLastMessageTime updates the last message timestamp.
func (rs *remoteStream) updateLastMessageTime() {
	rs.lastMessageTimeMu.Lock()
	rs.lastMessageTime = time.Now()
	rs.lastMessageTimeMu.Unlock()
}

// getLastMessageTime returns the last message timestamp.
func (rs *remoteStream) getLastMessageTime() time.Time {
	rs.lastMessageTimeMu.RLock()
	defer rs.lastMessageTimeMu.RUnlock()
	return rs.lastMessageTime
}

// recvLoop reads from grpcStream and routes messages directly.
// On error, it attempts to reconnect with exponential backoff.
func (rs *remoteStream) recvLoop() {
	defer close(rs.recvChan)

	for {
		protoMsg, err := rs.grpcStream.Recv()
		if err != nil {
			// Check if context is canceled
			if rs.ctx.Err() != nil {
				rs.recvErr = rs.ctx.Err()
				rs.notifyStateWithError(StateDisconnected, rs.ctx.Err())
				return
			}

			// Check if stream is closed
			rs.closedMu.Lock()
			if rs.closed {
				rs.closedMu.Unlock()
				return
			}
			rs.closedMu.Unlock()

			if status.Code(err) == codes.FailedPrecondition {
				rs.closedMu.Lock()
				rs.terminalErr = err
				close(rs.terminal)
				rs.closedMu.Unlock()
				rs.recvErr = errors.Join(err, rs.Close())
				rs.notifyStateWithError(StateDisconnected, rs.recvErr)
				return
			}

			// Attempt reconnect
			if !rs.reconnect() {
				rs.recvErr = err
				rs.notifyStateWithError(StateDisconnected, err)
				return
			}
			continue
		}

		// Update activity timestamp
		rs.updateLastMessageTime()

		// Process proto directly and route appropriately
		switch payload := protoMsg.Payload.(type) {
		case *pb.StreamerMessage_Delivery:
			delivery := protoToEventDelivery(payload.Delivery)
			select {
			case rs.recvChan <- delivery:
			case <-rs.ctx.Done():
				return
			}
		case *pb.StreamerMessage_SubscribeResponse:
			rs.handleSubscribeResponse(payload.SubscribeResponse)
		case *pb.StreamerMessage_HeartbeatAck:
			// Heartbeat ack received - connection is alive
			rs.logger.Debug("heartbeat ack received")
		}
	}
}

// reconnect attempts to reconnect with exponential backoff and jitter.
// Returns true if reconnection was successful, false if should stop.
func (rs *remoteStream) reconnect() bool {
	// If client is nil, reconnection is not possible (unit test scenario)
	if rs.client == nil {
		return false
	}

	backoff := rs.client.config.InitialBackoff
	attempts := 0
	maxRetries := rs.client.config.MaxRetries

	for {
		select {
		case <-rs.ctx.Done():
			return false
		default:
		}

		attempts++
		rs.notifyStateWithError(StateReconnecting, nil)
		rs.logger.Info("attempting to reconnect", "attempt", attempts)

		// Apply jitter (±20%)
		jitter := 0.8 + rand.Float64()*0.4 // 0.8 ~ 1.2
		waitTime := time.Duration(float64(backoff) * jitter)

		select {
		case <-rs.ctx.Done():
			return false
		case <-time.After(waitTime):
		}

		// Try to establish new stream
		newStream, err := rs.client.client.Stream(rs.ctx)
		if err != nil {
			rs.logger.Error("reconnect failed", "error", err, "attempt", attempts)

			// Check max retries
			if maxRetries > 0 && attempts >= maxRetries {
				rs.logger.Error("max reconnect attempts reached", "attempts", attempts)
				return false
			}

			// Increase backoff
			backoff = time.Duration(float64(backoff) * rs.client.config.BackoffMultiplier)
			if backoff > rs.client.config.MaxBackoff {
				backoff = rs.client.config.MaxBackoff
			}
			continue
		}

		// Success - update stream
		rs.grpcStream = newStream
		rs.logger.Info("reconnected successfully", "attempts", attempts)

		// Re-subscribe all active subscriptions
		if err := rs.resubscribeAll(); err != nil {
			rs.logger.Error("failed to resubscribe after reconnect", "error", err)
			// Close the new stream and try again
			newStream.CloseSend()
			continue
		}

		rs.setState(StateConnected)
		rs.updateLastMessageTime()
		return true
	}
}

// resubscribeAll re-subscribes all active subscriptions after reconnect.
func (rs *remoteStream) resubscribeAll() error {
	rs.activeSubscriptionsMu.RLock()
	subs := make(map[string]*subscriptionInfo, len(rs.activeSubscriptions))
	for id, info := range rs.activeSubscriptions {
		subs[id] = info
	}
	rs.activeSubscriptionsMu.RUnlock()

	if len(subs) == 0 {
		return nil
	}

	rs.logger.Info("resubscribing after reconnect", "count", len(subs))

	for subID, info := range subs {
		protoReq := &pb.SubscribeRequest{
			SubscriptionId: subID,
			Database:       info.database,
			Collection:     info.collection,
			Filters:        filtersToProto(info.filters),
		}
		if err := rs.grpcStream.Send(&pb.GatewayMessage{
			Payload: &pb.GatewayMessage_Subscribe{Subscribe: protoReq},
		}); err != nil {
			return fmt.Errorf("failed to resubscribe %s: %w", subID, err)
		}
	}

	return nil
}

// heartbeatLoop sends periodic heartbeats to keep the connection alive.
func (rs *remoteStream) heartbeatLoop() {
	// If client is nil, skip heartbeat (unit test scenario)
	if rs.client == nil {
		return
	}

	ticker := time.NewTicker(rs.client.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rs.ctx.Done():
			return
		case <-rs.heartbeatStop:
			return
		case <-ticker.C:
			rs.closedMu.Lock()
			if rs.closed {
				rs.closedMu.Unlock()
				return
			}
			rs.closedMu.Unlock()

			// Only send heartbeat when connected
			if rs.State() != StateConnected {
				continue
			}

			if err := rs.grpcStream.Send(&pb.GatewayMessage{
				Payload: &pb.GatewayMessage_Heartbeat{
					Heartbeat: &pb.Heartbeat{},
				},
			}); err != nil {
				rs.logger.Warn("failed to send heartbeat", "error", err)
				// Don't return - recvLoop will handle the error
			}
		}
	}
}

// activityMonitor detects stale connections by monitoring message activity.
func (rs *remoteStream) activityMonitor() {
	// If client is nil, skip activity monitoring (unit test scenario)
	if rs.client == nil {
		return
	}

	checkInterval := rs.client.config.ActivityTimeout / 3
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rs.ctx.Done():
			return
		case <-rs.heartbeatStop:
			return
		case <-ticker.C:
			// Only check when connected
			if rs.State() != StateConnected {
				continue
			}

			elapsed := time.Since(rs.getLastMessageTime())
			if elapsed > rs.client.config.ActivityTimeout {
				rs.logger.Warn("connection appears stale, no activity",
					"elapsed", elapsed,
					"timeout", rs.client.config.ActivityTimeout)
				// Force close to trigger reconnect
				_ = rs.grpcStream.CloseSend()
			}
		}
	}
}

// handleSubscribeResponse routes a subscribe response to the waiting caller.
func (rs *remoteStream) handleSubscribeResponse(resp *pb.SubscribeResponse) {
	rs.pendingSubscribesMu.Lock()
	if ch, ok := rs.pendingSubscribes[resp.SubscriptionId]; ok {
		select {
		case ch <- resp:
		default:
		}
	}
	rs.pendingSubscribesMu.Unlock()
}

// Compile-time check
var _ Stream = (*remoteStream)(nil)
