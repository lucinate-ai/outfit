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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go"

	"github.com/lucinate-ai/outfit/internal/metrics"
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
	StatsURL  string `json:"stats_url"`
	// EnvURL is the Lambda that returns environment variables for a running
	// endpoint without starting it. Optional — configs predating the env Lambda
	// still work for start/stop/deploy.
	EnvURL string `json:"env_url"`
	Region string `json:"region"`
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
func ConfigPath() (string, error) {
	home, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "remote.json"), nil
}

// LoadConfig reads the per-user config file and applies environment overrides
// (OUTFIT_REMOTE_START_URL, OUTFIT_REMOTE_STOP_URL, OUTFIT_REMOTE_REGION; the
// region also falls back to AWS_REGION and then to the region embedded in the
// Function URL host). A missing file is fine — env vars alone can carry the
// config. getenv is injectable for tests.
func LoadConfig(getenv func(string) string) (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	return finishConfig(cfg, getenv, path)
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
	if v := getenv("OUTFIT_REMOTE_STATS_URL"); v != "" {
		cfg.StatsURL = v
	}
	if v := getenv("OUTFIT_REMOTE_ENV_URL"); v != "" {
		cfg.EnvURL = v
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
	// Status-specific fields: the on-instance daemon's activity record,
	// relayed by the status branch of the start Lambda. camelCase to match the
	// daemon's own names, since these are copied through untouched — this
	// struct is already mixed (see modelId, contextSize below). Absent when
	// the instance is not running, when its daemon could not be reached, or
	// when no engine has yet done any work.
	LastActiveAt string `json:"lastActiveAt"`
	IdleSeconds  int    `json:"idleSeconds"`
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

// Deploy creates (or updates) cfg.Environment on the control plane and sets
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
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = forbiddenHint(detail)
		}
		return nil, fmt.Errorf("deploy failed (HTTP %d)%s: %s", resp.StatusCode, hint, detail)
	}
	return resp, nil
}

// startRetryWait is how long Start waits before retrying a dropped
// connection. A variable so tests can shorten it.
var startRetryWait = 5 * time.Second

// Start boots the instance and blocks until the model is serving, retrying
// while the endpoint reports it is still starting. progress is called with a
// status line before each wait. onState, when non-nil, is called with the raw
// state of every poll that returns a response, so a caller can describe what is
// happening (booting versus waiting for capacity) rather than assume a boot is
// underway.
//
// A start holds one long-lived request while the instance boots, so a network
// blip mid-wait (switching networks, a dropped VPN) surfaces as a transport
// error even though the boot continues server-side. Those are retried within
// the caller's deadline: the wake is idempotent — a repeated call reattaches
// to the same booting instance — so retrying never launches a second one.
func Start(ctx context.Context, cfg Config, progress func(string), onState func(string)) (*Response, error) {
	for {
		resp, err := call(ctx, cfg, http.MethodPost, cfg.StartURL, nil)
		if err != nil {
			var urlErr *url.Error
			if ctx.Err() == nil && errors.As(err, &urlErr) {
				progress(fmt.Sprintf("connection dropped (%v); retrying in %s", urlErr.Unwrap(), startRetryWait))
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("gave up waiting for the endpoint: %w", ctx.Err())
				case <-time.After(startRetryWait):
				}
				continue
			}
			return nil, err
		}
		if onState != nil {
			onState(resp.State)
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
			hint := ""
			if resp.StatusCode == http.StatusForbidden {
				hint = forbiddenHint(resp.Message)
			}
			return nil, fmt.Errorf("start failed (HTTP %d, state %q)%s: %s",
				resp.StatusCode, resp.State, hint, resp.Message)
		}
	}
}

// Status reports the instance state and endpoint health without side effects.
func Status(ctx context.Context, cfg Config) (*Response, error) {
	resp, err := call(ctx, cfg, http.MethodGet, cfg.StartURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, controlReplyError("status", resp)
	}
	return resp, nil
}

// Stop stops the instance immediately rather than waiting for the idle timer.
func Stop(ctx context.Context, cfg Config) (*Response, error) {
	resp, err := call(ctx, cfg, http.MethodPost, cfg.StopURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, controlReplyError("stop", resp)
	}
	return resp, nil
}

// Env returns the environment variables for an endpoint (base URL and API key)
// without starting the instance. The API key is stored in Secrets Manager and
// the EIP is allocated at deploy, so both are available regardless of instance
// state.
func Env(ctx context.Context, cfg Config) (*Response, error) {
	if cfg.EnvURL == "" {
		return nil, fmt.Errorf(
			"no env_url configured: the remote deployment needs to be updated for env support")
	}
	resp, err := call(ctx, cfg, http.MethodGet, cfg.EnvURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("env failed (HTTP %d): %s", resp.StatusCode, resp.Message)
	}
	return resp, nil
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
			hint = forbiddenHint(string(respBody))
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
		if credentialError(err) {
			return fmt.Errorf(
				"AWS credentials are expired or invalid: %w (%s)", err, refreshCredsHint)
		}
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

// refreshCredsHint is the fix appended when a request is rejected because the
// caller's AWS credentials are expired or invalid, rather than lacking
// permission — outfit stores no credentials of its own to refresh.
const refreshCredsHint = "refresh your env credentials, profile, or SSO session"

// credentialErrorCodes are the SDK/smithy error codes that mean the caller's
// credentials are expired or otherwise invalid. The same tokens appear in the
// body of an authorizer 403 on a Function URL, where the rejection arrives as
// an HTTP reply rather than a typed error.
var credentialErrorCodes = []string{
	"ExpiredToken",
	"ExpiredTokenException",
	"InvalidClientTokenId",
	"RequestExpired",
	"UnrecognizedClientException",
}

// credentialError reports whether err is an AWS expired- or invalid-credential
// failure — distinct from lacking permission. It matches a smithy API error
// code first, then falls back to the message text, since SSO and some
// credential-provider failures surface as plain errors, not typed ones.
func credentialError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		for _, code := range credentialErrorCodes {
			if apiErr.ErrorCode() == code {
				return true
			}
		}
	}
	return expiredCredsMarker(err.Error())
}

// expiredCredsMarker matches the stable tokens AWS uses for an expired or
// invalid credential, in an error string or the body of an authorizer 403.
func expiredCredsMarker(s string) bool {
	for _, code := range credentialErrorCodes {
		if strings.Contains(s, code) {
			return true
		}
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "security token") && strings.Contains(lower, "expired")
}

// forbiddenHint builds the guidance appended to an HTTP 403 from a control
// endpoint: a rejection carrying an expired/invalid-credential marker tells the
// user to refresh their credentials; anything else keeps the IAM-permission
// hint, since a resolvable credential that lacks lambda:InvokeFunctionUrl fails
// the same way.
func forbiddenHint(detail string) string {
	if expiredCredsMarker(detail) {
		return fmt.Sprintf(" (AWS credentials are expired or invalid — %s)", refreshCredsHint)
	}
	return " (do your AWS credentials grant lambda:InvokeFunctionUrl?)"
}

// controlReplyError turns a non-success control reply into an error, reading the
// reply's own detail (error or message) and, for a 403, classifying whether the
// credentials are expired/invalid or merely lack permission. Callers that treat
// some non-200 statuses as expected (Start's 503 "still starting") must handle
// those before falling through to this.
func controlReplyError(method string, resp *Response) error {
	detail := resp.Error
	if detail == "" {
		detail = resp.Message
	}
	hint := ""
	if resp.StatusCode == http.StatusForbidden {
		hint = forbiddenHint(detail)
	}
	return fmt.Errorf("%s returned HTTP %d%s: %s", method, resp.StatusCode, hint, truncate(detail, 200))
}

// StatsResponse is the JSON reply from the stats Lambda.
type StatsResponse struct {
	StatusCode int `json:"-"`
	// Message carries a rejection reason on a non-success reply — including the
	// authorizer's own text on a 403 — so an expired-credential rejection can be
	// classified even though the stats fields are empty.
	Message       string      `json:"message"`
	Environment   string      `json:"environment"`
	State         string      `json:"state"`
	InstanceID    string      `json:"instanceId"`
	InstanceType  string      `json:"instanceType"`
	Runner        string      `json:"runner"`
	ModelID       string      `json:"modelId"`
	UptimeSeconds int         `json:"uptimeSeconds"`
	Tokens        *TokenStats `json:"tokens"`
	GPUs          []GpuStat   `json:"gpus"`
	CPU           *CpuStat    `json:"cpu"`
	Memory        *MemoryStat `json:"memory"`
	Errors        []string    `json:"errors"`
	// LastActiveAt and IdleSeconds relay the on-instance daemon's answer to
	// "has this engine been working?", verbatim. Empty when the daemon was
	// unreachable, when no engine has run, or when the control plane predates
	// this — in every case the formatters simply omit the line.
	LastActiveAt string `json:"lastActiveAt"`
	IdleSeconds  int    `json:"idleSeconds"`
}

// The stat sub-types are aliases into internal/metrics, their canonical home
// since collection moved in-process: the Lambda's reply and the local
// collector speak the same dialect, so the formatters render either.
type (
	// TokenStats holds per-runner token/request counters from /metrics.
	TokenStats = metrics.TokenStats
	// GpuStat holds per-GPU metrics from nvidia-smi.
	GpuStat = metrics.GpuStat
	// CpuStat holds CPU utilization from vmstat.
	CpuStat = metrics.CpuStat
	// MemoryStat holds system memory from free.
	MemoryStat = metrics.MemoryStat
)

// Stats queries the stats Lambda for instance metrics: token usage, GPU, CPU,
// and RAM utilization. Returns an error if the stats URL is not configured,
// indicating the control plane was deployed before stats support was added.
func Stats(ctx context.Context, cfg Config) (*StatsResponse, error) {
	if cfg.StatsURL == "" {
		return nil, fmt.Errorf(
			"no stats_url configured: the control plane needs re-deploying with `pnpm deploy` (or set OUTFIT_REMOTE_STATS_URL)")
	}
	out, err := callStats(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if out.StatusCode != http.StatusOK {
		detail := strings.Join(out.Errors, "; ")
		if detail == "" {
			detail = out.Message
		}
		hint := ""
		if out.StatusCode == http.StatusForbidden {
			hint = forbiddenHint(detail)
		}
		return nil, fmt.Errorf("stats failed (HTTP %d)%s: %s", out.StatusCode, hint, detail)
	}
	return out, nil
}

// callStats signs and sends a request to the stats Lambda, parsing the
// stats-specific response shape.
func callStats(ctx context.Context, cfg Config) (*StatsResponse, error) {
	rawURL := cfg.StatsURL
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	body := []byte{}
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
	out := &StatsResponse{StatusCode: resp.StatusCode}
	if err := json.Unmarshal(respBody, out); err != nil {
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = forbiddenHint(string(respBody))
		}
		return nil, fmt.Errorf("stats returned HTTP %d%s: %s",
			resp.StatusCode, hint, truncate(string(respBody), 200))
	}
	return out, nil
}

// ProbeTimeout is the maximum time to wait for a TCP connection when probing
// the endpoint's reachability. A variable so tests can shorten it.
var ProbeTimeout = 5 * time.Second

// ProbeReachability performs a TCP dial to the host and port derived from a
// base URL (e.g. "http://198.51.100.1:8000/v1" -> "198.51.100.1:8000"). It
// returns nil if the connection succeeds within probeTimeout, or an error if
// it cannot connect.
func ProbeReachability(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}
