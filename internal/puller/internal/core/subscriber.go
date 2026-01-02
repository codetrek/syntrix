// Package core implements the local puller service.
package core

import (
	"log/slog"
	"sync"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/puller/internal/cursor"
)

// Subscriber represents an active subscription to the event stream.
type Subscriber struct {
	// ID is the consumer ID for logging/monitoring.
	ID string

	// After is the initial position to start from.
	After *cursor.ProgressMarker

	// CoalesceOnCatchUp enables catch-up coalescing.
	CoalesceOnCatchUp bool

	// currentPos tracks the current position for each backend.
	currentPos *cursor.ProgressMarker

	// lastClusterTime tracks the timestamp of the last sent event per backend.
	lastClusterTime map[string]events.ClusterTime

	// mu protects currentPos.
	mu sync.RWMutex

	// done is closed when the subscriber is terminated.
	done chan struct{}

	// ch receives events for this subscriber.
	ch chan *events.ChangeEvent

	// overflow indicates if the subscriber channel has overflowed.
	overflow bool
	// overflowMu protects overflow.
	overflowMu sync.Mutex
}

// NewSubscriber creates a new subscriber.
func NewSubscriber(id string, after *cursor.ProgressMarker, coalesceOnCatchUp bool, channelSize int) *Subscriber {
	if after == nil {
		after = cursor.NewProgressMarker()
	}
	if channelSize <= 0 {
		channelSize = 10000
	}
	return &Subscriber{
		ID:                id,
		After:             after,
		CoalesceOnCatchUp: coalesceOnCatchUp,
		currentPos:        after.Clone(),
		lastClusterTime:   make(map[string]events.ClusterTime),
		done:              make(chan struct{}),
		// Increase buffer size to handle transient spikes and avoid flapping between live and catchup modes.
		// 10000 events * ~1KB/event ~= 10MB memory per subscriber.
		ch: make(chan *events.ChangeEvent, channelSize),
	}
}

// SetOverflow sets the overflow flag.
func (s *Subscriber) SetOverflow() {
	s.overflowMu.Lock()
	defer s.overflowMu.Unlock()
	s.overflow = true
}

// GetAndResetOverflow returns the current overflow state and resets it to false.
func (s *Subscriber) GetAndResetOverflow() bool {
	s.overflowMu.Lock()
	defer s.overflowMu.Unlock()
	overflow := s.overflow
	s.overflow = false
	return overflow
}

// UpdatePosition updates the current position for a backend.
func (s *Subscriber) UpdatePosition(backend, eventID string, clusterTime events.ClusterTime) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentPos.SetPosition(backend, eventID)
	s.lastClusterTime[backend] = clusterTime
}

// ShouldSend checks if an event should be sent based on its timestamp.
func (s *Subscriber) ShouldSend(backend string, clusterTime events.ClusterTime) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	last, ok := s.lastClusterTime[backend]
	if !ok {
		return true
	}
	return clusterTime.Compare(last) > 0
}

// CurrentProgress returns the current progress marker.
func (s *Subscriber) CurrentProgress() *cursor.ProgressMarker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentPos.Clone()
}

// Events returns the channel of events for this subscriber.
func (s *Subscriber) Events() <-chan *events.ChangeEvent {
	return s.ch
}

// Done returns a channel that is closed when the subscriber is terminated.
func (s *Subscriber) Done() <-chan struct{} {
	return s.done
}

// Close terminates the subscriber.
func (s *Subscriber) Close() {
	select {
	case <-s.done:
		// Already closed
	default:
		close(s.done)
	}
}

// SubscriberManager manages active subscribers.
type SubscriberManager struct {
	subscribers map[string]*Subscriber
	logger      *slog.Logger
	mu          sync.RWMutex
}

// NewSubscriberManager creates a new subscriber manager.
func NewSubscriberManager(logger *slog.Logger) *SubscriberManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SubscriberManager{
		subscribers: make(map[string]*Subscriber),
		logger:      logger.With("component", "subscriber-manager"),
	}
}

// Add adds a subscriber.
func (m *SubscriberManager) Add(sub *Subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers[sub.ID] = sub
}

// Remove removes a subscriber.
func (m *SubscriberManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub, ok := m.subscribers[id]; ok {
		sub.Close()
		delete(m.subscribers, id)
	}
}

// Get returns a subscriber by ID.
func (m *SubscriberManager) Get(id string) *Subscriber {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.subscribers[id]
}

// Count returns the number of active subscribers.
func (m *SubscriberManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subscribers)
}

// All returns a snapshot of all subscribers.
func (m *SubscriberManager) All() []*Subscriber {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Subscriber, 0, len(m.subscribers))
	for _, sub := range m.subscribers {
		result = append(result, sub)
	}
	return result
}

// CloseAll closes all subscribers.
func (m *SubscriberManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sub := range m.subscribers {
		sub.Close()
	}
	m.subscribers = make(map[string]*Subscriber)
}

// Broadcast sends an event to all subscribers.
func (m *SubscriberManager) Broadcast(be *events.ChangeEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, sub := range m.subscribers {
		select {
		case sub.ch <- be:
		default:
			// Slow consumer: mark as overflowed instead of disconnecting
			m.logger.Warn("slow consumer detected, marking overflow", "consumerId", id)
			sub.SetOverflow()
		}
	}
}
