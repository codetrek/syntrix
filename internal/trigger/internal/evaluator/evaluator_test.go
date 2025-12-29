package evaluator

import (
	"context"
	"fmt"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELEvaluator(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name      string
		trigger   *trigger.Trigger
		event     *storage.Event
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "Simple match",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "users",
				Condition:  "event.document.age > 18",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "users",
					Data:       map[string]interface{}{"age": 20},
				},
			},
			wantMatch: true,
			wantErr:   false,
		},
		{
			name: "Simple no match",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "users",
				Condition:  "event.document.age > 18",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "users",
					Data:       map[string]interface{}{"age": 16},
				},
			},
			wantMatch: false,
			wantErr:   false,
		},
		{
			name: "Event type mismatch",
			trigger: &trigger.Trigger{
				Events:     []string{"delete"},
				Collection: "users",
				Condition:  "true",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "users",
				},
			},
			wantMatch: false,
			wantErr:   false,
		},
		{
			name: "Collection mismatch",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "orders",
				Condition:  "true",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "users",
				},
			},
			wantMatch: false,
			wantErr:   false,
		},
		{
			name: "Collection wildcard match",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "chats/*/members",
				Condition:  "true",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "chats/123/members",
				},
			},
			wantMatch: true,
			wantErr:   false,
		},
		{
			name: "Collection wildcard no match",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "chats/*/members",
				Condition:  "true",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "chats/123/messages",
				},
			},
			wantMatch: false,
			wantErr:   false,
		},
		{
			name: "Complex condition",
			trigger: &trigger.Trigger{
				Events:     []string{"update"},
				Collection: "users",
				Condition:  "event.document.role == 'admin' && event.document.active == true",
			},
			event: &storage.Event{
				Type: storage.EventUpdate,
				Document: &storage.Document{
					Collection: "users",
					Data: map[string]interface{}{
						"role":   "admin",
						"active": true,
					},
				},
			},
			wantMatch: true,
			wantErr:   false,
		},
		{
			name: "Invalid CEL",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "users",
				Condition:  "invalid syntax ???",
			},
			event: &storage.Event{
				Type: storage.EventCreate,
				Document: &storage.Document{
					Collection: "users",
				},
			},
			wantMatch: false,
			wantErr:   true,
		},
		{
			name: "Check Before State",
			trigger: &trigger.Trigger{
				Events:     []string{"update"},
				Collection: "users",
				Condition:  "event.before.status == 'pending' && event.document.status == 'active'",
			},
			event: &storage.Event{
				Type: storage.EventUpdate,
				Document: &storage.Document{
					Collection: "users",
					Data:       map[string]interface{}{"status": "active"},
				},
				Before: &storage.Document{
					Collection: "users",
					Data:       map[string]interface{}{"status": "pending"},
				},
			},
			wantMatch: true,
			wantErr:   false,
		},
		{
			name: "Check Timestamp",
			trigger: &trigger.Trigger{
				Events:     []string{"create"},
				Collection: "logs",
				Condition:  "event.timestamp > 0",
			},
			event: &storage.Event{
				Type:      storage.EventCreate,
				Timestamp: 1234567890,
				Document: &storage.Document{
					Collection: "logs",
				},
			},
			wantMatch: true,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := evaluator.Evaluate(context.Background(), tt.trigger, tt.event)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantMatch, match)
			}
		})
	}
}

func TestCELEvaluator_CacheEviction(t *testing.T) {
	// Test cache eviction by filling the cache to its limit
	// Note: MaxCacheSize is 1000, so we'll test the eviction logic differently
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	celEval := evaluator.(*celeEvaluator)

	// Directly populate the cache to near capacity
	// We'll manually add entries to test eviction behavior
	celEval.cacheMutex.Lock()
	for i := 0; i < MaxCacheSize-1; i++ {
		cond := fmt.Sprintf("event.document.field%d == %d", i, i)
		celEval.prgCache[cond] = nil // placeholder, not a real program
		celEval.cacheOrder = append(celEval.cacheOrder, cond)
	}
	celEval.cacheMutex.Unlock()

	// Now add one more via Evaluate to trigger eviction check
	event := &storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Collection: "test",
			Data:       map[string]interface{}{"newfield": 1},
		},
	}

	trig := &trigger.Trigger{
		Events:     []string{"create"},
		Collection: "test",
		Condition:  "event.document.newfield == 1",
	}

	_, err = evaluator.Evaluate(context.Background(), trig, event)
	assert.NoError(t, err)

	// Cache should be at max capacity
	assert.Equal(t, MaxCacheSize, len(celEval.prgCache))

	// Add another one to trigger eviction
	trig2 := &trigger.Trigger{
		Events:     []string{"create"},
		Collection: "test",
		Condition:  "event.document.newfield == 2",
	}
	_, err = evaluator.Evaluate(context.Background(), trig2, event)
	assert.NoError(t, err)

	// Cache should still be at max capacity after eviction
	assert.Equal(t, MaxCacheSize, len(celEval.prgCache))

	// The oldest entry should be evicted
	_, exists := celEval.prgCache["event.document.field0 == 0"]
	assert.False(t, exists, "Oldest condition should be evicted")
}

func TestCELEvaluator_DeleteEventWithBeforeOnly(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	trig := &trigger.Trigger{
		Events:     []string{"delete"},
		Collection: "users",
		Condition:  "true",
	}

	event := &storage.Event{
		Type:     storage.EventDelete,
		Document: nil,
		Before: &storage.Document{
			Collection: "users",
			Data:       map[string]interface{}{"name": "deleted-user"},
		},
	}

	match, err := evaluator.Evaluate(context.Background(), trig, event)
	assert.NoError(t, err)
	assert.True(t, match)
}

func TestCELEvaluator_CollectionMismatchWithBeforeOnly(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	trig := &trigger.Trigger{
		Events:     []string{"delete"},
		Collection: "orders",
		Condition:  "true",
	}

	event := &storage.Event{
		Type:     storage.EventDelete,
		Document: nil,
		Before: &storage.Document{
			Collection: "users",
			Data:       map[string]interface{}{"name": "deleted-user"},
		},
	}

	match, err := evaluator.Evaluate(context.Background(), trig, event)
	assert.NoError(t, err)
	assert.False(t, match)
}

func TestCELEvaluator_CELNonBoolReturn(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	// CEL that returns non-boolean
	trig := &trigger.Trigger{
		Events:     []string{"create"},
		Collection: "users",
		Condition:  "event.document.name", // Returns string, not bool
	}

	event := &storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Collection: "users",
			Data:       map[string]interface{}{"name": "test"},
		},
	}

	_, err = evaluator.Evaluate(context.Background(), trig, event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must return boolean")
}

func TestCELEvaluator_CacheHit(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	trig := &trigger.Trigger{
		Events:     []string{"create"},
		Collection: "users",
		Condition:  "event.document.age > 18",
	}

	event := &storage.Event{
		Type: storage.EventCreate,
		Document: &storage.Document{
			Collection: "users",
			Data:       map[string]interface{}{"age": 25},
		},
	}

	// First call - cache miss
	match1, err := evaluator.Evaluate(context.Background(), trig, event)
	assert.NoError(t, err)
	assert.True(t, match1)

	// Second call - cache hit
	match2, err := evaluator.Evaluate(context.Background(), trig, event)
	assert.NoError(t, err)
	assert.True(t, match2)
}
