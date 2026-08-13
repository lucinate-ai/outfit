package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
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

// Logs reads a slice of the node's engine log. offset is where to read from —
// daemon.TailLog for the end, or the NextOffset of a previous reply to receive
// only what has been appended since. limit bounds the read; zero takes the
// daemon's default. A daemon predating the endpoint answers 404, which classify
// turns into a "needs upgrading" outcome rather than a generic failure.
func (c *Client) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	q := url.Values{}
	// The endpoint reads the tail when no offset is given; sending the
	// sentinel would be rejected as a negative offset.
	if offset != daemon.TailLog {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/logs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out daemon.LogsResponse
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Start asks the daemon to start its engine, returning the resulting status.
func (c *Client) Start(ctx context.Context) (daemon.StatusResponse, error) {
	return c.StartWith(ctx, nil, "")
}

// StartWith asks the daemon to start its engine on a deploy config it carries,
// so a caller can say what to run and run it in one call. A nil config starts
// from what the node already has. The daemon validates the config against what
// it can serve and stores it only if the start is accepted.
func (c *Client) StartWith(ctx context.Context, dc *remote.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	var out daemon.StatusResponse
	var body any
	if dc != nil {
		// The key travels with the config, in the one request that runs it:
		// the caller decides what the engine is gated with, and therefore
		// knows what to hand the agent.
		body = daemon.StartRequest{DeployConfig: *dc, EngineAPIKey: engineKey}
	}
	err := c.do(ctx, http.MethodPost, "/v1/start", body, &out)
	return out, err
}

// Stop asks the daemon to stop its engine, returning the resulting status.
func (c *Client) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	var out daemon.StatusResponse
	err := c.do(ctx, http.MethodPost, "/v1/stop", nil, &out)
	return out, err
}
