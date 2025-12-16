package trigger

import (
	"time"
)

// Trigger represents the configuration for a server-side trigger.
type Trigger struct {
	ID          string            `json:"triggerId"`
	Version     string            `json:"version"`
	Tenant      string            `json:"tenant"`
	Collection  string            `json:"collection"`
	Events      []string          `json:"events"` // create, update, delete
	Condition   string            `json:"condition"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	SecretsRef  string            `json:"secretsRef"`
	Concurrency int               `json:"concurrency"`
	RateLimit   int               `json:"rateLimit"`
	RetryPolicy RetryPolicy       `json:"retryPolicy"`
	Filters     []string          `json:"filters"`
}

// RetryPolicy defines how to handle delivery failures.
type RetryPolicy struct {
	MaxAttempts    int           `json:"maxAttempts"`
	InitialBackoff time.Duration `json:"initialBackoff"`
	MaxBackoff     time.Duration `json:"maxBackoff"`
}

// DeliveryTask represents the payload sent to the delivery worker via NATS.
type DeliveryTask struct {
	TriggerID  string                 `json:"triggerId"`
	Tenant     string                 `json:"tenant"`
	Event      string                 `json:"event"`
	Collection string                 `json:"collection"`
	DocKey     string                 `json:"docKey"`
	LSN        string                 `json:"lsn"`
	Seq        int64                  `json:"seq"`
	Before     map[string]interface{} `json:"before,omitempty"`
	After      map[string]interface{} `json:"after,omitempty"`
	Timestamp  int64                  `json:"ts"`
	URL        string                 `json:"url"`
	Headers    map[string]string      `json:"headers"`
	SecretsRef string                 `json:"secretsRef"`
}
