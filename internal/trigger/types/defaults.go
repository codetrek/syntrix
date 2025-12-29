package types

import "time"

// Default timeout values for trigger processing.
const (
	// DefaultTaskTimeout is the default timeout for processing a single task.
	DefaultTaskTimeout = 10 * time.Second

	// DefaultHTTPTimeout is the default timeout for HTTP requests to webhooks.
	DefaultHTTPTimeout = 5 * time.Second
)
