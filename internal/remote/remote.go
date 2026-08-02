// Package remote controls the scale-to-zero GPU inference instance defined by
// this repository's remote/ subproject, by calling its Lambdas through their
// Function URLs. The URLs use IAM auth, so every request is SigV4-signed
// (service "lambda") with the caller's AWS credentials.
package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// httpClient is a package variable so tests can substitute it. The long
// timeout matters: a start call blocks while the instance boots and loads the
// model into VRAM, which takes minutes.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Config holds the connection details for the remote instance's control
// Lambdas: deploying remote/ prints it as the OutfitRemoteConfig output, ready
// to paste into the config file.
type Config struct {
	StartURL  string `json:"start_url"`
	StopURL   string `json:"stop_url"`
	DeployURL string `json:"deploy_url"`
	Region    string `json:"region"`
	// BaseURL is the endpoint's own address (the environment's stable Elastic
	// IP). It belongs to the deployment rather than to the Outfit, so it is
	// written here and `apply` reads it back for an Outfit that states no
	// BASEURL. Like DeployURL it is optional: the control calls do not need it —
	// start and status report the address themselves — so configs without it
	// still work.
	BaseURL string `json:"base_url"`
	// Environment names which environment's instance the shared lifecycle
	// Lambdas act on. The control URLs are shared across environments, so this
	// travels with every control call; the Lambdas reject a call without one.
	Environment string `json:"environment"`
}

// ConfigPath returns the path of the legacy per-user remote config file,
// alongside outfit's own config in the same directory. The environments
// registry (see environments.go) supersedes it; it is still read as the
// fallback for the default environment.
func ConfigPath() string {
	return filepath.Join(configHome(), "remote.json")
}

// LoadConfig reads the per-user config file and applies environment overrides
// (OUTFIT_REMOTE_START_URL, OUTFIT_REMOTE_STOP_URL, OUTFIT_REMOTE_REGION; the
// region also falls back to AWS_REGION and then to the region embedded in the
// Function URL host). A missing file is fine — env vars alone can carry the
// config. getenv is injectable for tests.
func LoadConfig(getenv func(string) string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(ConfigPath())
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", ConfigPath(), err)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	return finishConfig(cfg, getenv, ConfigPath())
}

// LoadConfigFile reads the remote config from an explicit file — typically
// one named by an Outfit's REMOTE instruction — then applies the same
// environment overrides as LoadConfig. Unlike LoadConfig, the file must
// exist: it was asked for by name.
func LoadConfigFile(path string, getenv func(string) string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf(
				"remote config %s does not exist: run `outfit remote deploy` to create and register the environment",
				path)
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return finishConfig(cfg, getenv, path)
}

// finishConfig applies env overrides and validates. source names the config
// file for error messages.
func finishConfig(cfg Config, getenv func(string) string, source string) (Config, error) {
	if v := getenv("OUTFIT_REMOTE_START_URL"); v != "" {
		cfg.StartURL = v
	}
	if v := getenv("OUTFIT_REMOTE_STOP_URL"); v != "" {
		cfg.StopURL = v
	}
	if v := getenv("OUTFIT_REMOTE_DEPLOY_URL"); v != "" {
		cfg.DeployURL = v
	}
	if v := getenv("OUTFIT_REMOTE_REGION"); v != "" {
		cfg.Region = v
	}
	if cfg.StartURL == "" || cfg.StopURL == "" {
		return Config{}, fmt.Errorf(
			"remote is not configured: paste the OutfitRemoteConfig output of the remote/ deployment into %s",
			source)
	}
	if cfg.Region == "" {
		cfg.Region = getenv("AWS_REGION")
	}
	if cfg.Region == "" {
		cfg.Region = regionFromURL(cfg.StartURL)
	}
	if cfg.Region == "" {
		return Config{}, fmt.Errorf(
			"cannot determine the AWS region: set \"region\" in %s or OUTFIT_REMOTE_REGION",
			source)
	}
	return cfg, nil
}

// regionFromURL extracts the region from a Lambda Function URL host
// (<id>.lambda-url.<region>.on.aws).
func regionFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(u.Hostname(), ".")
	for i, part := range parts {
		if part == "lambda-url" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// Response is the control Lambdas' JSON reply.
type Response struct {
	StatusCode        int    `json:"-"`
	State             string `json:"state"`
	Healthy           *bool  `json:"healthy"`
	BaseURL           string `json:"base_url"`
	APIKey            string `json:"api_key"`
	Environment       string `json:"environment"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
	// Deploy-specific fields.
	Deployed       bool   `json:"deployed"`
	Seeding        bool   `json:"seeding"`
	SeedInstanceID string `json:"seedInstanceId"`
	Runner         string `json:"runner"`
	ModelID        string `json:"modelId"`
	ContextSize    int    `json:"contextSize"`
	WeightsPrefix  string `json:"weightsPrefix"`
	Error          string `json:"error"`
}

// DeployConfig is what the deploy Lambda accepts: the runner-neutral
// description of WHAT to serve, derived from an Outfit. Deliberately no
// weights prefix — the Lambda derives the S3 layout itself, and seeds the
// weights when they are not there yet, so this stays a statement of intent.
type DeployConfig struct {
	Runner          string   `json:"runner"`
	ModelID         string   `json:"modelId"`
	Quant           string   `json:"quant"`
	ContextSize     int      `json:"contextSize"`
	ServedModelName string   `json:"servedModelName"`
	ServeArgs       []string `json:"serveArgs"`
}

// Deploy creates (or updates) cfg.Environment on the shared layer and sets
// what its next wake will serve. The Lambda validates the config, provisions
// the environment's own resources if absent, seeds the weights into S3 if they
// are absent, and stores the config; deploying does not start the instance.
// allowedCidr scopes who may reach this environment's instance; it is required
// the first time and optional afterwards (empty leaves ingress alone).
func Deploy(ctx context.Context, cfg Config, dc DeployConfig, allowedCidr string) (*Response, error) {
	if cfg.DeployURL == "" {
		return nil, fmt.Errorf(
			"no deploy_url configured: add the remote/ deployment's DeployUrl output to the remote config (or set OUTFIT_REMOTE_DEPLOY_URL)")
	}
	body, err := json.Marshal(struct {
		DeployConfig
		AllowedCidr string `json:"allowedCidr,omitempty"`
	}{dc, allowedCidr})
	if err != nil {
		return nil, err
	}
	resp, err := call(ctx, cfg, http.MethodPost, cfg.DeployURL, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		detail := resp.Error
		if detail == "" {
			detail = resp.Message
		}
		return nil, fmt.Errorf("deploy failed (HTTP %d): %s", resp.StatusCode, detail)
	}
	return resp, nil
}

// Start boots the instance and blocks until vLLM is serving, retrying while
// the endpoint reports it is still starting. progress is called with a status
// line before each wait.
func Start(ctx context.Context, cfg Config, progress func(string)) (*Response, error) {
	for {
		resp, err := call(ctx, cfg, http.MethodPost, cfg.StartURL, nil)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusOK && resp.State == "ready":
			return resp, nil
		case resp.StatusCode == http.StatusServiceUnavailable:
			wait := resp.RetryAfterSeconds
			if wait <= 0 {
				wait = 1
			}
			progress(fmt.Sprintf("instance %s; retrying in %ds", resp.State, wait))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("gave up waiting for the endpoint: %w", ctx.Err())
			case <-time.After(time.Duration(wait) * time.Second):
			}
		default:
			return nil, fmt.Errorf("start failed (HTTP %d, state %q): %s",
				resp.StatusCode, resp.State, resp.Message)
		}
	}
}

// Status reports the instance state and endpoint health without side effects.
func Status(ctx context.Context, cfg Config) (*Response, error) {
	return call(ctx, cfg, http.MethodGet, cfg.StartURL, nil)
}

// Stop stops the instance immediately rather than waiting for the idle timer.
func Stop(ctx context.Context, cfg Config) (*Response, error) {
	return call(ctx, cfg, http.MethodPost, cfg.StopURL, nil)
}

// call signs and sends one request. body is nil for the bodyless calls
// (start/stop/status); deploy passes JSON, which must be hashed into the
// signature rather than sent unsigned. The environment travels as a query
// parameter on every call — the Lambdas are shared across environments and
// require it.
func call(ctx context.Context, cfg Config, method, rawURL string, body []byte) (*Response, error) {
	if cfg.Environment != "" {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("env", cfg.Environment)
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		// Set explicitly: with a bytes.Reader net/http would infer it, but the
		// signature covers Content-Length, so leaving it to chance risks a
		// mismatch between what is signed and what is sent.
		req.ContentLength = int64(len(body))
	}
	if err := sign(ctx, req, cfg.Region, body); err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out := &Response{StatusCode: resp.StatusCode}
	if err := json.Unmarshal(respBody, out); err != nil {
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = " (do your AWS credentials grant lambda:InvokeFunctionUrl?)"
		}
		return nil, fmt.Errorf("%s returned HTTP %d%s: %s",
			method, resp.StatusCode, hint, truncate(string(respBody), 200))
	}
	return out, nil
}

// sign SigV4-signs the request with the default AWS credential chain
// (environment, shared config/credentials, SSO). Function URL IAM auth
// requires the payload hash to be sent and signed via X-Amz-Content-Sha256.
func sign(ctx context.Context, req *http.Request, region string, body []byte) error {
	awsCfg, err := LoadAWSConfig(ctx, region)
	if err != nil {
		return err
	}
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf(
			"resolving AWS credentials: %w (configure env credentials, a profile or an SSO session)", err)
	}
	// sha256 of the exact bytes sent (of the empty string for a bodyless call).
	hash := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(hash[:])
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	return v4.NewSigner().SignHTTP(ctx, creds, req, payloadHash, "lambda", region, time.Now())
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
