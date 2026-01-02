package core

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/stretchr/testify/assert"
)

func TestSubscriber_ShouldSend(t *testing.T) {
	sub := NewSubscriber("test-sub", nil, false, 100)

	// Initial state: no history for backend "db1"
	// Should send any event
	ct1 := events.ClusterTime{T: 100, I: 1}
	assert.True(t, sub.ShouldSend("db1", ct1), "Should send first event")

	// Update position
	sub.UpdatePosition("db1", "evt1", ct1)

	// Test older event
	ctOld := events.ClusterTime{T: 99, I: 1}
	assert.False(t, sub.ShouldSend("db1", ctOld), "Should not send older event")

	// Test same event
	assert.False(t, sub.ShouldSend("db1", ct1), "Should not send same event")

	// Test newer event
	ctNew := events.ClusterTime{T: 100, I: 2}
	assert.True(t, sub.ShouldSend("db1", ctNew), "Should send newer event")

	// Test different backend
	assert.True(t, sub.ShouldSend("db2", ctOld), "Should send event for new backend")
}

func TestSubscriber_Overflow(t *testing.T) {
	sub := NewSubscriber("test-sub", nil, false, 100)

	assert.False(t, sub.GetAndResetOverflow())

	sub.SetOverflow()
	assert.True(t, sub.GetAndResetOverflow())
	assert.False(t, sub.GetAndResetOverflow())

	// Concurrency test
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub.SetOverflow()
		}()
	}
	wg.Wait()
	assert.True(t, sub.GetAndResetOverflow())
}

func TestSubscriberManager(t *testing.T) {
	logger := slog.Default() // Use default logger for tests
	mgr := NewSubscriberManager(logger)

	// Test Add/Get/Count
	sub1 := NewSubscriber("sub1", nil, false, 10)
	mgr.Add(sub1)
	assert.Equal(t, 1, mgr.Count())
	assert.Equal(t, sub1, mgr.Get("sub1"))

	// Test Broadcast
	evt := &events.ChangeEvent{
		Backend: "db1",
		EventID: "evt1",
	}
	mgr.Broadcast(evt)

	select {
	case received := <-sub1.ch:
		assert.Equal(t, evt, received)
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for event")
	}

	// Test Overflow
	// Fill the channel
	for i := 0; i < 10; i++ {
		sub1.ch <- evt
	}

	// Broadcast one more, should trigger overflow
	mgr.Broadcast(evt)
	assert.True(t, sub1.GetAndResetOverflow())

	// Test Remove
	mgr.Remove("sub1")
	assert.Equal(t, 0, mgr.Count())

	select {
	case <-sub1.Done():
	// Success, subscriber closed
	case <-time.After(time.Second):
		t.Fatal("Subscriber not closed after remove")
	}
}

func TestSubscriberManager_Race(t *testing.T) {
	mgr := NewSubscriberManager(nil)
	sub := NewSubscriber("sub1", nil, false, 1000)
	mgr.Add(sub)

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				mgr.Broadcast(&events.ChangeEvent{})
			}
		}
	}()

	go func() {
		for i := 0; i < 100; i++ {
			mgr.Add(NewSubscriber("sub-race", nil, false, 10))
			mgr.Remove("sub-race")
		}
		close(done)
	}()

	<-done
}

func TestSubscriberManager_All(t *testing.T) {
	m := NewSubscriberManager(nil)
	sub1 := NewSubscriber("sub1", nil, false, 100)
	sub2 := NewSubscriber("sub2", nil, false, 100)

	m.Add(sub1)
	m.Add(sub2)

	all := m.All()
	assert.Len(t, all, 2)
	assert.Contains(t, all, sub1)
	assert.Contains(t, all, sub2)
}

func TestSubscriberManager_CloseAll(t *testing.T) {
	m := NewSubscriberManager(nil)
	sub1 := NewSubscriber("sub1", nil, false, 100)
	sub2 := NewSubscriber("sub2", nil, false, 100)

	m.Add(sub1)
	m.Add(sub2)

	m.CloseAll()

	assert.Equal(t, 0, m.Count())

	select {
	case <-sub1.Done():
	case <-time.After(time.Second):
		t.Fatal("sub1 not closed")
	}

	select {
	case <-sub2.Done():
	case <-time.After(time.Second):
		t.Fatal("sub2 not closed")
	}
}
