package query

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
)

// Client is a remote client for the Query Service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Query Service Client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

func (c *Client) GetDocument(ctx context.Context, path string) (model.Document, error) {
	reqBody := map[string]string{"path": path}
	resp, err := c.post(ctx, "/internal/v1/document/get", reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, model.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var doc model.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (c *Client) CreateDocument(ctx context.Context, doc model.Document) error {
	resp, err := c.post(ctx, "/internal/v1/document/create", doc)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	reqBody := map[string]interface{}{
		"data": data,
		"pred": pred,
	}
	resp, err := c.post(ctx, "/internal/v1/document/replace", reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var doc model.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (c *Client) PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	resp, err := c.post(ctx, "/internal/v1/document/patch", map[string]interface{}{
		"data": data,
		"pred": pred,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, model.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var doc model.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (c *Client) DeleteDocument(ctx context.Context, path string, pred model.Filters) error {
	reqBody := map[string]interface{}{"path": path, "pred": pred}
	resp, err := c.post(ctx, "/internal/v1/document/delete", reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return model.ErrNotFound
	}
	if resp.StatusCode == http.StatusPreconditionFailed {
		return model.ErrPreconditionFailed
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error) {
	resp, err := c.post(ctx, "/internal/v1/query/execute", q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var docs []model.Document
	if err := json.NewDecoder(resp.Body).Decode(&docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (c *Client) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	reqBody := map[string]string{"collection": collection}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/internal/v1/watch", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	out := make(chan storage.Event)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var evt storage.Event
			if err := json.Unmarshal(line, &evt); err != nil {
				// Log error?
				continue
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

func (c *Client) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	resp, err := c.post(ctx, "/internal/replication/v1/pull", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result storage.ReplicationPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	resp, err := c.post(ctx, "/internal/replication/v1/push", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result storage.ReplicationPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) post(ctx context.Context, endpoint string, body interface{}) (*http.Response, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}
