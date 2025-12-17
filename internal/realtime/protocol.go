package realtime

import (
	"encoding/json"
	"syntrix/internal/storage"
)

// Message types
const (
	TypeAuth           = "auth"
	TypeAuthAck        = "auth_ack"
	TypeSubscribe      = "subscribe"
	TypeUnsubscribe    = "unsubscribe"
	TypeUnsubscribeAck = "unsubscribe_ack"
	TypeEvent          = "event"
	TypeSnapshot       = "snapshot"
	TypeError          = "error"
)

// BaseMessage is the envelope for all messages
type BaseMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// AuthPayload
type AuthPayload struct {
	Token string `json:"token"`
}

// SubscribePayload
type SubscribePayload struct {
	Query        storage.Query `json:"query"`
	IncludeData  bool          `json:"includeData"`  // If true, events will include the full document
	SendSnapshot bool          `json:"sendSnapshot"` // If true, sends current state immediately
}

// UnsubscribePayload
type UnsubscribePayload struct {
	ID string `json:"id"`
}

// EventPayload (Server -> Client)
type EventPayload struct {
	SubID string      `json:"subId"`
	Delta PublicEvent `json:"delta"`
}

type PublicEvent struct {
	Type      storage.EventType      `json:"type"`
	Document  map[string]interface{} `json:"document,omitempty"`
	Path      string                 `json:"path"`
	Timestamp int64                  `json:"timestamp"`
}

// SnapshotPayload (Server -> Client)
type SnapshotPayload struct {
	SubID     string                   `json:"subId"`
	Documents []map[string]interface{} `json:"documents"`
}

// ErrorPayload
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
