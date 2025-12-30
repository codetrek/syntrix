package types

import (
	"context"
	"errors"
	"fmt"

	stypes "github.com/codetrek/syntrix/internal/storage/types"
)

// FatalError represents an error that should not be retried.
type FatalError struct {
	Err error
}

func (e *FatalError) Error() string {
	return fmt.Sprintf("fatal error: %v", e.Err)
}

func (e *FatalError) Unwrap() error {
	return e.Err
}

// IsFatal checks if an error is a FatalError.
func IsFatal(err error) bool {
	var fe *FatalError
	return errors.As(err, &fe)
}

// Trigger represents the configuration for a server-side trigger.
type Trigger struct {
	ID            string            `json:"triggerId" yaml:"triggerId"`
	Version       string            `json:"version" yaml:"version"`
	Tenant        string            `json:"tenant" yaml:"tenant"`
	Collection    string            `json:"collection" yaml:"collection"`
	Events        []string          `json:"events" yaml:"events"` // create, update, delete
	Condition     string            `json:"condition" yaml:"condition"`
	URL           string            `json:"url" yaml:"url"`
	Headers       map[string]string `json:"headers" yaml:"headers"`
	SecretsRef    string            `json:"secretsRef" yaml:"secretsRef"`
	Concurrency   int               `json:"concurrency" yaml:"concurrency"`
	RateLimit     int               `json:"rateLimit" yaml:"rateLimit"`
	IncludeBefore bool              `json:"includeBefore" yaml:"includeBefore"`
	RetryPolicy   RetryPolicy       `json:"retryPolicy" yaml:"retryPolicy"`
	Filters       []string          `json:"filters" yaml:"filters"`
	Timeout       Duration          `json:"timeout" yaml:"timeout"`
}

// RetryPolicy defines how to handle delivery failures.
type RetryPolicy struct {
	MaxAttempts    int      `json:"maxAttempts" yaml:"maxAttempts"`
	InitialBackoff Duration `json:"initialBackoff" yaml:"initialBackoff"`
	MaxBackoff     Duration `json:"maxBackoff" yaml:"maxBackoff"`
}

// DeliveryTask represents the payload sent to the delivery worker via NATS.
type DeliveryTask struct {
	TriggerID      string                 `json:"triggerId"`
	Tenant         string                 `json:"tenant"`
	Event          string                 `json:"event"`
	Collection     string                 `json:"collection"`
	DocumentID     string                 `json:"documentId"`
	LSN            string                 `json:"lsn"`
	Seq            int64                  `json:"seq"`
	Before         map[string]interface{} `json:"before,omitempty"`
	After          map[string]interface{} `json:"after,omitempty"`
	Timestamp      int64                  `json:"ts"`
	URL            string                 `json:"url"`
	Headers        map[string]string      `json:"headers"`
	SecretsRef     string                 `json:"secretsRef"`
	RetryPolicy    RetryPolicy            `json:"retryPolicy"`
	Timeout        Duration               `json:"timeout"`
	PreIssuedToken string                 `json:"preIssuedToken,omitempty"`
	Payload        map[string]interface{} `json:"payload,omitempty"` // Added Payload field for compatibility
	SubjectHashed  bool                   `json:"subjectHashed,omitempty"`
}

// Evaluator evaluates trigger conditions.
type Evaluator interface {
	Evaluate(ctx context.Context, t *Trigger, event *stypes.Event) (bool, error)
}

// EventPublisher publishes delivery tasks.
type EventPublisher interface {
	Publish(ctx context.Context, task *DeliveryTask) error
}
