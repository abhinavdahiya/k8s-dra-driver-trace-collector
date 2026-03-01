package alloy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client communicates with the Alloy admin HTTP API.
type Client struct {
	baseURL    string // e.g. "http://127.0.0.1:12345"
	httpClient *http.Client
}

// NewClient creates a new Alloy admin API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Reload triggers a configuration reload on the Alloy instance.
// Returns nil on HTTP 200. Returns a descriptive error on HTTP 400
// (includes response body) or on connection failure.
func (c *Client) Reload(ctx context.Context) error {
	url := c.baseURL + "/-/reload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating reload request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("reload failed (HTTP %d): %s", resp.StatusCode, string(body))
}

// Healthy checks whether the Alloy instance reports healthy.
// Returns nil on HTTP 200. Returns an error on non-200 status
// (includes response body).
func (c *Client) Healthy(ctx context.Context) error {
	url := c.baseURL + "/-/healthy"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating healthy request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("alloy unhealthy (HTTP %d): %s", resp.StatusCode, string(body))
}
