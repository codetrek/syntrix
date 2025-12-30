package grpc

import (
	"testing"
)

func TestProgressMarker_NewProgressMarker(t *testing.T) {
	t.Parallel()
	pm := NewProgressMarker()
	if pm == nil {
		t.Fatal("NewProgressMarker() returned nil")
	}
	if pm.Positions == nil {
		t.Error("Positions should not be nil")
	}
}

func TestProgressMarker_SetAndGetPosition(t *testing.T) {
	t.Parallel()
	pm := NewProgressMarker()
	pm.SetPosition("backend-1", "evt-123")

	pos := pm.GetPosition("backend-1")
	if pos != "evt-123" {
		t.Errorf("GetPosition() = %q, want 'evt-123'", pos)
	}

	// Non-existent
	pos = pm.GetPosition("backend-2")
	if pos != "" {
		t.Errorf("GetPosition() = %q, want empty string", pos)
	}
}

func TestProgressMarker_EncodeAndDecode(t *testing.T) {
	t.Parallel()
	pm := NewProgressMarker()
	pm.SetPosition("backend-1", "evt-123")
	pm.SetPosition("backend-2", "evt-456")

	encoded := pm.Encode()
	if encoded == "" {
		t.Fatal("Encode() returned empty string")
	}

	decoded := DecodeProgressMarker(encoded)
	if decoded.GetPosition("backend-1") != "evt-123" {
		t.Errorf("backend-1 position = %q, want 'evt-123'", decoded.GetPosition("backend-1"))
	}
	if decoded.GetPosition("backend-2") != "evt-456" {
		t.Errorf("backend-2 position = %q, want 'evt-456'", decoded.GetPosition("backend-2"))
	}
}

func TestProgressMarker_DecodeEmpty(t *testing.T) {
	t.Parallel()
	pm := DecodeProgressMarker("")
	if pm == nil {
		t.Fatal("DecodeProgressMarker('') returned nil")
	}
	if len(pm.Positions) != 0 {
		t.Errorf("Positions should be empty, got %d", len(pm.Positions))
	}
}

func TestProgressMarker_DecodeInvalid(t *testing.T) {
	t.Parallel()
	pm := DecodeProgressMarker("not-valid-base64!!!")
	if pm == nil {
		t.Fatal("DecodeProgressMarker() returned nil for invalid input")
	}
	// Should return empty marker
	if len(pm.Positions) != 0 {
		t.Errorf("Positions should be empty for invalid input, got %d", len(pm.Positions))
	}
}

func TestProgressMarker_Clone(t *testing.T) {
	t.Parallel()
	pm := NewProgressMarker()
	pm.SetPosition("backend-1", "evt-123")

	clone := pm.Clone()
	if clone.GetPosition("backend-1") != "evt-123" {
		t.Error("Clone should have same positions")
	}

	// Modify original
	pm.SetPosition("backend-1", "evt-456")

	// Clone should be unchanged
	if clone.GetPosition("backend-1") != "evt-123" {
		t.Error("Clone should be independent of original")
	}
}

func TestProgressMarker_EncodeEmpty(t *testing.T) {
	t.Parallel()
	pm := NewProgressMarker()
	encoded := pm.Encode()
	if encoded != "" {
		t.Errorf("Encode() = %q, want empty string for empty marker", encoded)
	}

	// nil marker
	var nilPm *ProgressMarker
	encoded = nilPm.Encode()
	if encoded != "" {
		t.Errorf("Encode() = %q, want empty string for nil marker", encoded)
	}
}

func TestSubscriber_NewSubscriber(t *testing.T) {
	t.Parallel()
	sub := NewSubscriber("consumer-1", nil, false)
	if sub == nil {
		t.Fatal("NewSubscriber() returned nil")
	}
	if sub.ID != "consumer-1" {
		t.Errorf("ID = %q, want 'consumer-1'", sub.ID)
	}
	if sub.After == nil {
		t.Error("After should not be nil")
	}
}

func TestSubscriber_UpdatePosition(t *testing.T) {
	t.Parallel()
	sub := NewSubscriber("consumer-1", nil, false)
	sub.UpdatePosition("backend-1", "evt-123")

	progress := sub.CurrentProgress()
	if progress.GetPosition("backend-1") != "evt-123" {
		t.Errorf("Position = %q, want 'evt-123'", progress.GetPosition("backend-1"))
	}
}

func TestSubscriber_CurrentProgress_Clone(t *testing.T) {
	t.Parallel()
	sub := NewSubscriber("consumer-1", nil, false)
	sub.UpdatePosition("backend-1", "evt-123")

	progress1 := sub.CurrentProgress()
	sub.UpdatePosition("backend-1", "evt-456")
	progress2 := sub.CurrentProgress()

	if progress1.GetPosition("backend-1") != "evt-123" {
		t.Error("First progress should not change")
	}
	if progress2.GetPosition("backend-1") != "evt-456" {
		t.Error("Second progress should have updated value")
	}
}

func TestSubscriber_Done(t *testing.T) {
	t.Parallel()
	sub := NewSubscriber("consumer-1", nil, false)

	select {
	case <-sub.Done():
		t.Error("Done should not be closed initially")
	default:
		// Expected
	}

	sub.Close()

	select {
	case <-sub.Done():
		// Expected
	default:
		t.Error("Done should be closed after Close()")
	}
}

func TestSubscriber_CloseIdempotent(t *testing.T) {
	t.Parallel()
	sub := NewSubscriber("consumer-1", nil, false)

	// Should not panic
	sub.Close()
	sub.Close()
	sub.Close()
}

func TestSubscriberManager_AddAndRemove(t *testing.T) {
	t.Parallel()
	m := NewSubscriberManager()

	sub := NewSubscriber("consumer-1", nil, false)
	m.Add(sub)

	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1", m.Count())
	}

	got := m.Get("consumer-1")
	if got != sub {
		t.Error("Get() returned wrong subscriber")
	}

	m.Remove("consumer-1")
	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}

	got = m.Get("consumer-1")
	if got != nil {
		t.Error("Get() should return nil after remove")
	}
}

func TestSubscriberManager_All(t *testing.T) {
	m := NewSubscriberManager()

	sub1 := NewSubscriber("consumer-1", nil, false)
	sub2 := NewSubscriber("consumer-2", nil, false)
	m.Add(sub1)
	m.Add(sub2)

	all := m.All()
	if len(all) != 2 {
		t.Errorf("All() returned %d, want 2", len(all))
	}
}

func TestSubscriberManager_CloseAll(t *testing.T) {
	m := NewSubscriberManager()

	sub1 := NewSubscriber("consumer-1", nil, false)
	sub2 := NewSubscriber("consumer-2", nil, false)
	m.Add(sub1)
	m.Add(sub2)

	m.CloseAll()

	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}

	// Check subscribers are closed
	select {
	case <-sub1.Done():
		// Expected
	default:
		t.Error("sub1 should be closed")
	}
	select {
	case <-sub2.Done():
		// Expected
	default:
		t.Error("sub2 should be closed")
	}
}
