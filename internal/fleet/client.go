package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
)

// RequestTimeout bounds one call to a node. A fleet view has to stay snappy:
// a wedged node must show as unreachable rather than hold up every other
// node's row. A variable so tests can shorten it.
var RequestTimeout = 5 * time.Second

// Client calls one daemon's control API. It is the transport half of a node;
// what a caller wants of a node is the Node interface below.
type Client struct {
	BaseURL string
	Token   string
	// HTTP is the client used for calls; nil means one with RequestTimeout.
	HTTP *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: RequestTimeout}
}

// do performs one API call, decoding a JSON reply into out when given.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, payload)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &httpError{status: resp.StatusCode, body: data}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s reply: %w", path, err)
	}
	return nil
}

// httpError carries a non-200 reply so the caller can classify it (401 is a
// token problem, everything else is the daemon refusing) and show the
// daemon's own message.
type httpError struct {
	status int
	body   []byte
}

func (e *httpError) Error() string {
	var decoded struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(e.body, &decoded) == nil {
		if decoded.Error != "" {
			return decoded.Error
		}
		if decoded.Message != "" {
			return decoded.Message
		}
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

// Status reads the daemon's engine state.
func (c *Client) Status(ctx context.Context) (daemon.StatusResponse, error) {
	var out daemon.StatusResponse
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// Metrics reads the daemon's collected engine and system metrics.
func (c *Client) Metrics(ctx context.Context) (metrics.Stats, error) {
	var out metrics.Stats
	err := c.do(ctx, http.MethodGet, "/v1/metrics", nil, &out)
	return out, err
}

// Start asks the daemon to start its engine, returning the resulting status.
func (c *Client) Start(ctx context.Context) (daemon.StatusResponse, error) {
	var out daemon.StatusResponse
	err := c.do(ctx, http.MethodPost, "/v1/start", nil, &out)
	return out, err
}

// Stop asks the daemon to stop its engine, returning the resulting status.
func (c *Client) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	var out daemon.StatusResponse
	err := c.do(ctx, http.MethodPost, "/v1/stop", nil, &out)
	return out, err
}
