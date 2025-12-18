package trigger

import (
	"encoding/json"
	"errors"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a wrapper around time.Duration that supports JSON/YAML string marshaling.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)
		return nil
	default:
		return errors.New("invalid duration")
	}
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		// Try decoding as int (nanoseconds)
		var i int64
		if err := value.Decode(&i); err == nil {
			*d = Duration(time.Duration(i))
			return nil
		}
		return err
	}
	tmp, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(tmp)
	return nil
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
	TriggerID   string                 `json:"triggerId"`
	Tenant      string                 `json:"tenant"`
	Event       string                 `json:"event"`
	Collection  string                 `json:"collection"`
	DocKey      string                 `json:"docKey"`
	LSN         string                 `json:"lsn"`
	Seq         int64                  `json:"seq"`
	Before      map[string]interface{} `json:"before,omitempty"`
	After       map[string]interface{} `json:"after,omitempty"`
	Timestamp   int64                  `json:"ts"`
	URL         string                 `json:"url"`
	Headers     map[string]string      `json:"headers"`
	SecretsRef  string                 `json:"secretsRef"`
	RetryPolicy RetryPolicy            `json:"retryPolicy"`
	Timeout     Duration               `json:"timeout"`
}
