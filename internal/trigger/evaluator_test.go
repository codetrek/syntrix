package trigger

import (
	"context"
	"syntrix/internal/storage"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELEvaluator(t *testing.T) {
	evaluator, err := NewEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name      string
		trigger   *Trigger
		event     *storage.Event
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "Simple match",
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
			trigger: &Trigger{
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
