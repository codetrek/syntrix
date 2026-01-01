package csp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/codetrek/syntrix/internal/storage"
)

// CSPClient provides HTTP-based access to a remote CSP service.
// Use this in distributed mode where CSP runs as a separate service.
type CSPClient struct {
	baseURL string
	client  *http.Client
}

// NewClient creates a new CSPClient with the given CSP service URL.
func NewClient(baseURL string) *CSPClient {
	return &CSPClient{
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

// SetHTTPClient sets a custom HTTP client (useful for testing).
func (c *CSPClient) SetHTTPClient(client *http.Client) {
	c.client = client
}

// Watch returns a channel of events by connecting to the remote CSP service.
// Note: resumeToken and opts are not currently supported over HTTP protocol;
// the server manages state internally. These parameters are accepted for
// interface compatibility but are ignored.
func (c *CSPClient) Watch(ctx context.Context, tenant, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	reqBody, err := json.Marshal(map[string]string{
		"collection": collection,
		"tenant":     tenant,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/internal/v1/watch", c.baseURL), bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to CSP service: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("csp watch failed with status: %d", resp.StatusCode)
	}

	out := make(chan storage.Event)

	go func() {
		defer close(out)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		for {
			var evt storage.Event
			if err := decoder.Decode(&evt); err != nil {
				log.Printf("[Error][CSP CSPClient] Watch decode event failed: %v\n", err)
				return
			}
			select {
			case out <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// Ensure CSPClient implements Service interface at compile time.
var _ Service = (*CSPClient)(nil)
