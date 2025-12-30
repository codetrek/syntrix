// Package grpc provides the gRPC server for the puller service.
package grpc

import (
	"encoding/base64"
	"encoding/json"
	"sync"
)

// ProgressMarker tracks consumer positions across all backends.
// This is an internal structure - consumers see it as an opaque string.
type ProgressMarker struct {
	// Positions maps backend name to last event ID for that backend.
	Positions map[string]string `json:"p"`
}

// NewProgressMarker creates an empty progress marker.
func NewProgressMarker() *ProgressMarker {
	return &ProgressMarker{
		Positions: make(map[string]string),
	}
}

// DecodeProgressMarker decodes a progress marker from its string representation.
// Returns an empty marker if the string is empty or invalid.
func DecodeProgressMarker(s string) *ProgressMarker {
	if s == "" {
		return NewProgressMarker()
	}

	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return NewProgressMarker()
	}

	var pm ProgressMarker
	if err := json.Unmarshal(data, &pm); err != nil {
		return NewProgressMarker()
	}

	if pm.Positions == nil {
		pm.Positions = make(map[string]string)
	}
	return &pm
}

// Encode encodes the progress marker to a string.
func (pm *ProgressMarker) Encode() string {
	if pm == nil || len(pm.Positions) == 0 {
		return ""
	}

	data, err := json.Marshal(pm)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(data)
}

// SetPosition updates the position for a backend.
func (pm *ProgressMarker) SetPosition(backend, eventID string) {
	if pm.Positions == nil {
		pm.Positions = make(map[string]string)
	}
	pm.Positions[backend] = eventID
}

// GetPosition returns the position for a backend.
func (pm *ProgressMarker) GetPosition(backend string) string {
	if pm.Positions == nil {
		return ""
	}
	return pm.Positions[backend]
}

// Clone creates a deep copy of the progress marker.
func (pm *ProgressMarker) Clone() *ProgressMarker {
	if pm == nil {
		return NewProgressMarker()
	}

	clone := NewProgressMarker()
	for k, v := range pm.Positions {
		clone.Positions[k] = v
	}
	return clone
}

// Subscriber represents an active subscription to the event stream.
type Subscriber struct {
	// ID is the consumer ID for logging/monitoring.
	ID string

	// After is the initial position to start from.
	After *ProgressMarker

	// CoalesceOnCatchUp enables catch-up coalescing.
	CoalesceOnCatchUp bool

	// currentPos tracks the current position for each backend.
	currentPos *ProgressMarker

	// mu protects currentPos.
	mu sync.RWMutex

	// done is closed when the subscriber is terminated.
	done chan struct{}
}

// NewSubscriber creates a new subscriber.
func NewSubscriber(id string, after *ProgressMarker, coalesceOnCatchUp bool) *Subscriber {
	if after == nil {
		after = NewProgressMarker()
	}
	return &Subscriber{
		ID:                id,
		After:             after,
		CoalesceOnCatchUp: coalesceOnCatchUp,
		currentPos:        after.Clone(),
		done:              make(chan struct{}),
	}
}

// UpdatePosition updates the current position for a backend.
func (s *Subscriber) UpdatePosition(backend, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentPos.SetPosition(backend, eventID)
}

// CurrentProgress returns the current progress marker.
func (s *Subscriber) CurrentProgress() *ProgressMarker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentPos.Clone()
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
	mu          sync.RWMutex
}

// NewSubscriberManager creates a new subscriber manager.
func NewSubscriberManager() *SubscriberManager {
	return &SubscriberManager{
		subscribers: make(map[string]*Subscriber),
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
