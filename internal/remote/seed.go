package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Seeds are account-wide rather than per-environment: one model seeded once
// serves every environment that names it. So none of these calls carries an
// environment, and none of them uses the shared Response struct — the seed
// Lambda's replies have their own shapes.

// SeedRequest asks for a model's weights to be fetched into S3. Deliberately no
// weights prefix: the Lambda derives the S3 layout from runner+model+quant, the
// same way a deploy does, so a caller never encodes it.
type SeedRequest struct {
	Runner  string `json:"runner"`
	ModelID string `json:"modelId"`
	Quant   string `json:"quant,omitempty"`
	// Revision pins the commit or branch to fetch. Empty takes the repository's
	// default branch; either way the resolved commit is recorded in the manifest.
	Revision string `json:"revision,omitempty"`
	// Force seeds weights that are already stored, replacing them in place.
	Force bool `json:"force,omitempty"`
}

// SeedStarted is the reply to a start. Started distinguishes a fresh seed from
// one that was joined, so a repeated invocation is unambiguous.
type SeedStarted struct {
	SeedID        string `json:"seedId"`
	InstanceID    string `json:"instanceId"`
	Started       bool   `json:"started"`
	Joined        bool   `json:"joined"`
	AlreadySeeded bool   `json:"alreadySeeded"`
	ModelID       string `json:"modelId"`
	WeightsPrefix string `json:"weightsPrefix"`
	Message       string `json:"message"`
	Error         string `json:"error"`
}

// SeedStatus is one seed's state, joined from its own progress records and
// whether its instance still exists.
type SeedStatus struct {
	SeedID     string  `json:"seedId"`
	State      string  `json:"state"`
	ModelID    string  `json:"modelId"`
	Revision   string  `json:"revision"`
	InstanceID string  `json:"instanceId"`
	Progress   float64 `json:"progressPercent"`
	BytesDone  int64   `json:"bytesDone"`
	BytesTotal int64   `json:"bytesTotal"`
	FilesDone  int     `json:"filesDone"`
	FilesTotal int     `json:"filesTotal"`
	// CurrentFile is what it was transferring when it last reported.
	CurrentFile     string `json:"currentFile"`
	Message         string `json:"message"`
	Err             string `json:"error"`
	LastReportAt    string `json:"lastReportAt"`
	StartedAt       string `json:"startedAt"`
	DurationSeconds int    `json:"durationSeconds"`
}

// SeedSummary is one row of the in-flight listing.
type SeedSummary struct {
	SeedID     string  `json:"seedId"`
	InstanceID string  `json:"instanceId"`
	ModelID    string  `json:"modelId"`
	State      string  `json:"state"`
	Progress   float64 `json:"progressPercent"`
	StartedAt  string  `json:"startedAt"`
	AgeSeconds int     `json:"ageSeconds"`
}

type seedList struct {
	Seeds []SeedSummary `json:"seeds"`
	Count int           `json:"count"`
	Error string        `json:"error"`
}

// SeedStopped is the reply to a stop. Stopped is false when nothing was
// running, which is not an error — stopping twice must be safe.
type SeedStopped struct {
	SeedID      string   `json:"seedId"`
	Stopped     bool     `json:"stopped"`
	InstanceIDs []string `json:"instanceIds"`
	Message     string   `json:"message"`
	Error       string   `json:"error"`
}

// seedURL returns the configured seed endpoint, or an error naming the value to
// add. Optional in the same way as env_url: a configuration written before the
// seed endpoint existed keeps working for every other subcommand.
func seedURL(cfg Config) (string, error) {
	if cfg.SeedURL == "" {
		return "", fmt.Errorf(
			"no seed_url configured: add the remote/ deployment's SeedUrl output to the remote config (or set OUTFIT_REMOTE_SEED_URL)")
	}
	return cfg.SeedURL, nil
}

// seedCall signs and sends one seed request, decoding into out. id is appended
// as ?id= when set.
func seedCall(ctx context.Context, cfg Config, method, id string, body []byte, out any) (int, error) {
	rawURL, err := seedURL(cfg)
	if err != nil {
		return 0, err
	}
	if id != "" {
		u, err := url.Parse(rawURL)
		if err != nil {
			return 0, err
		}
		q := u.Query()
		q.Set("id", id)
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}
	status, respBody, err := send(ctx, cfg, method, rawURL, body)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		hint := ""
		if status == http.StatusForbidden {
			hint = forbiddenHint(string(respBody))
		}
		return status, fmt.Errorf("seed %s returned HTTP %d%s: %s",
			method, status, hint, truncate(string(respBody), 200))
	}
	return status, nil
}

// SeedStart requests a seed. Requesting one that is already in flight joins it
// rather than starting a second, and the reply says which happened.
func SeedStart(ctx context.Context, cfg Config, req SeedRequest) (*SeedStarted, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := &SeedStarted{}
	status, err := seedCall(ctx, cfg, http.MethodPost, "", body, out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%s", out.Error)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("starting the seed failed (HTTP %d): %s", status, firstNonEmpty(out.Error, out.Message))
	}
	return out, nil
}

// SeedGet reports one seed's state. It answers for a seed whose instance is
// gone, which is when a failed seed most needs explaining.
func SeedGet(ctx context.Context, cfg Config, seedID string) (*SeedStatus, error) {
	out := &SeedStatus{}
	status, err := seedCall(ctx, cfg, http.MethodGet, seedID, nil, out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("no seed %q is known", seedID)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("reading the seed failed (HTTP %d): %s", status, out.Err)
	}
	return out, nil
}

// SeedList returns the seeds currently in flight across the account.
func SeedList(ctx context.Context, cfg Config) ([]SeedSummary, error) {
	out := &seedList{}
	status, err := seedCall(ctx, cfg, http.MethodGet, "", nil, out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing seeds failed (HTTP %d): %s", status, out.Error)
	}
	return out.Seeds, nil
}

// SeedStop stops a seed. Stopping one that is not running is reported, not an
// error.
func SeedStop(ctx context.Context, cfg Config, seedID string) (*SeedStopped, error) {
	out := &SeedStopped{}
	status, err := seedCall(ctx, cfg, http.MethodDelete, seedID, nil, out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("stopping the seed failed (HTTP %d): %s", status, firstNonEmpty(out.Error, out.Message))
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
