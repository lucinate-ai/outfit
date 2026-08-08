package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// scrapeClient is a package variable so tests can substitute it. The timeout
// mirrors the Lambda's curl --max-time 5.
var scrapeClient = &http.Client{Timeout: 5 * time.Second}

// ScrapeTarget names where a running engine's Prometheus metrics live: the
// engine's own base URL, the metric dialect it speaks, and the API key it
// gates /metrics behind (empty for an ungated engine).
type ScrapeTarget struct {
	BaseURL string
	Engine  string
	APIKey  string
}

// ScrapeTokenStats fetches the engine's /metrics and parses its token and
// request counters. An unreachable endpoint, a non-200 reply, or output with
// no recognisable metrics all return an error — the caller omits engine stats
// and carries on, per the engine-metrics spec.
func ScrapeTokenStats(ctx context.Context, target ScrapeTarget) (*TokenStats, error) {
	u, err := url.Parse(strings.TrimSuffix(target.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("engine base URL %q: %w", target.BaseURL, err)
	}
	// BASEURL conventionally ends in /v1 (the OpenAI-style API root), but
	// /metrics is served at the server root.
	u.Path = strings.TrimSuffix(u.Path, "/v1") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	resp, err := scrapeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("engine metrics returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	tokens := ParseTokenStats(string(body), target.Engine)
	if tokens == nil {
		return nil, fmt.Errorf("no %s metrics in scrape", target.Engine)
	}
	return tokens, nil
}
