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
	"regexp"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go"

	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// httpClient is a package variable so tests can substitute it. The long
// timeout matters: a start call blocks while the instance boots and loads the
// model into VRAM, which takes minutes.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Config holds the connection details for the remote instance's control
// Lambdas: deploying remote/ prints it as the SpinloopRemoteConfig output, ready
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
	// SeedURL is the Lambda that starts, reports on, lists and stops model
	// weight seeds. Optional in the same way as EnvURL: a config written before
	// the seed Lambda existed keeps working for every other subcommand, and
	// only the seed subcommand names the value to add.
	SeedURL string `json:"seed_url"`
	// UpdateURL is the Lambda for arbitrary post-provision instance commands
	// (currently: set-keep). Optional — configs predating the update Lambda
	// still work for start/stop/deploy; Keep will fail with a clear message.
	UpdateURL string `json:"update_url"`
	Region    string `json:"region"`
	// BaseURL is the endpoint's own address (the environment's stable Elastic
	// IP). It belongs to the deployment rather than to the Spinloop, so it is
	// written here and `apply` reads it back for a Spinloop that states no
	// BASEURL. Like DeployURL it is optional: the control calls do not need it —
	// start and status report the address themselves — so configs without it
	// still work.
	BaseURL string `json:"base_url"`
	// Environment names which environment's instance the shared lifecycle
	// Lambdas act on. The control URLs are shared across environments, so this
	// travels with every control call; the Lambdas reject a call without one.
	Environment string `json:"environment"`
}

// LoadEnvironment reads a named environment's configuration: the remote.json
// in its registry directory, with the SPINLOOP_REMOTE_* overrides applied on
// top. It is the only way an environment is resolved — there is no path
// outside the registry, and no name that resolves by a different rule.
//
// A name with no file is not a failure on its own: the overrides may carry a
// complete configuration, which is how the remote commands run on a machine
// with nothing on disk. In that case the name is the environment identifier
// the control calls carry, since a configuration assembled from variables has
// no file to take one from. Where the overrides are incomplete too,
// finishConfig fails naming the registry path to create.
func LoadEnvironment(name string, getenv func(string) string) (Config, error) {
	path, err := EnvConfigPath(name)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// No file: the overrides may still configure it, and the name is what
		// tells the shared Lambdas which instance they are acting on.
		cfg.Environment = name
	default:
		return Config{}, err
	}
	return finishConfig(cfg, getenv, path)
}

// finishConfig applies env overrides and validates. source names the config
// file for error messages.
func finishConfig(cfg Config, getenv func(string) string, source string) (Config, error) {
	if v := getenv("SPINLOOP_REMOTE_START_URL"); v != "" {
		cfg.StartURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_STOP_URL"); v != "" {
		cfg.StopURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_DEPLOY_URL"); v != "" {
		cfg.DeployURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_STATS_URL"); v != "" {
		cfg.StatsURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_ENV_URL"); v != "" {
		cfg.EnvURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_SEED_URL"); v != "" {
		cfg.SeedURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_UPDATE_URL"); v != "" {
		cfg.UpdateURL = v
	}
	if v := getenv("SPINLOOP_REMOTE_REGION"); v != "" {
		cfg.Region = v
	}
	if cfg.StartURL == "" || cfg.StopURL == "" {
		return Config{}, fmt.Errorf(
			"remote is not configured: paste the SpinloopRemoteConfig output of the remote/ deployment into %s",
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
			"cannot determine the AWS region: set \"region\" in %s or SPINLOOP_REMOTE_REGION",
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
	// The on-instance daemon's activity record, relayed by the status branch of
	// the start Lambda: when the engine last did work, and how long ago.
	// camelCase to match the daemon's own names — this struct is already mixed
	// (see modelId, contextSize below). Absent when the instance is not
	// running, when its daemon could not be reached, or when no engine has yet
	// done any work.
	LastActiveAt string `json:"lastActiveAt"`
	IdleSeconds  int    `json:"idleSeconds"`
	// Deploy-specific fields. Runner, ModelID and ServedName are also relayed
	// by the status reply, which reads them from the environment's deploy
	// config — the same source the stats reply reads.
	Deployed bool `json:"deployed"`
	Seeding  bool `json:"seeding"`
	// SeedID identifies the seed a deploy started, so it can be followed with
	// `spinloop remote seed status`. The instance id it replaces was an
	// implementation detail that changes if the seed is relaunched.
	SeedID string `json:"seedId"`
	// ServedName is the name the engine answers to beside the model id — the
	// served name the deploy gave it — relayed by the status reply from the
	// environment's deploy config, so a caller may know the engine by either
	// name.
	ServedName    string `json:"servedName"`
	Runner        string `json:"runner"`
	ModelID       string `json:"modelId"`
	ContextSize   int    `json:"contextSize"`
	WeightsPrefix string `json:"weightsPrefix"`
	// APIKeyAction is what the deploy did to the environment's key secret,
	// present only when the deploy supplied a key: "created" (a new secret)
	// or "rotated" (an existing secret set to a new value, invalidating the
	// old key). It carries the action, never the value — a deploy never
	// returns the key itself.
	APIKeyAction string `json:"apiKeyAction"`
	// RetainUntil is the instance's retention deadline, returned when the
	// Retain-Until tag is present (set-keep or start --keep). camelCase to
	// match the Lambda's JSON.
	RetainUntil string `json:"retainUntil"`
	Error       string `json:"error"`
}

// instanceTypePattern is the shape of an EC2 instance type: a lowercase family
// and size separated by a single dot. The family may be hyphenated, as in
// u7i-6tb and mac2-m2; the size is lowercase alphanumerics (xlarge, 112xlarge,
// metal). Deliberately permissive to every current EC2 family while rejecting
// obvious junk before it reaches a RunInstances call.
var instanceTypePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*\.[a-z0-9]+$`)

// IsInstanceType reports whether value is shaped like an EC2 instance type.
// It is the guard a deploy (flag or fleet file) runs before sending a type the
// control plane would otherwise reject at launch; a name that fails it is
// reported by its caller, which has the value to name.
func IsInstanceType(value string) bool {
	return instanceTypePattern.MatchString(value)
}

// Deploy creates (or updates) cfg.Environment on the control plane and sets
// what its next wake will serve. The Lambda validates the config, provisions
// the environment's own resources if absent, seeds the weights into S3 if they
// are absent, and stores the config; deploying does not start the instance.
// allowedCidr scopes who may reach this environment's instance; it is required
// the first time and optional afterwards (empty leaves ingress alone).
// reseed asks the control plane to fetch the weights even when they are
// already in S3. Both are properties of this request, not of what the
// environment serves, so they ride beside each other rather than on
// DeployConfig — which is persisted verbatim, and would re-seed on every wake
// that read it back.
//
// apiKey is an externally provided key to store as the environment's key,
// riding the same way: a property of this request, never persisted, and
// omitted entirely when empty, so a control plane that predates it sees the
// body it always saw. The reply's APIKeyAction says what happened to the
// secret — the action, never the value.
func Deploy(ctx context.Context, cfg Config, dc inference.DeployConfig, allowedCidr string, reseed bool, apiKey string) (*Response, error) {
	if cfg.DeployURL == "" {
		return nil, fmt.Errorf(
			"no deploy_url configured: add the remote/ deployment's DeployUrl output to the remote config (or set SPINLOOP_REMOTE_DEPLOY_URL)")
	}
	body, err := json.Marshal(struct {
		inference.DeployConfig
		AllowedCidr string `json:"allowedCidr,omitempty"`
		Reseed      bool   `json:"reseed,omitempty"`
		APIKey      string `json:"apiKey,omitempty"`
	}{dc, allowedCidr, reseed, apiKey})
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
			hint = forbiddenHint(cfg.Region, detail)
		}
		return nil, fmt.Errorf("deploy failed (HTTP %d)%s: %s", resp.StatusCode, hint, detail)
	}
	return resp, nil
}

// startRetryWait is how long Start waits before retrying a dropped
// connection. A variable so tests can shorten it.
var startRetryWait = 5 * time.Second

// StateInFlight is the state Start reports to its onState observer when a new
// attempt is issued and no response has come back yet. It is a client-side
// report of the client's own situation — no Lambda reply ever carries it. An
// in-flight attempt supersedes an earlier no-capacity report: that report
// described the previous attempt, and a refusal of the new one comes back
// within seconds of trying each zone, while a successful one holds its request
// while the instance boots. An observer reading it should describe a start as
// underway, not a capacity wait.
const StateInFlight = "in-flight"

// stateSeeding is the control plane's word for "the weights are still being
// fetched" — no instance has been launched yet, so there is no boot to wait
// on, and the reply's seedId is what an operator follows to see how far the
// fetch has got.
const stateSeeding = "seeding"

// giveUpWaiting is Start's error when the caller's deadline expires mid-wait.
// When the last reply was the seeding state, the useful next steps are
// following the seed and resuming the wait, so the error carries both.
func giveUpWaiting(ctx context.Context, state, seedID string) error {
	if state == stateSeeding && seedID != "" {
		return fmt.Errorf("gave up waiting for the endpoint: the weights are still seeding (seed %s) — follow it with `spinloop remote seed status %s`, and re-run start with a longer --timeout: %w",
			seedID, seedID, ctx.Err())
	}
	return fmt.Errorf("gave up waiting for the endpoint: %w", ctx.Err())
}

// Start boots the instance and blocks until the model is serving, retrying
// while the endpoint reports it is still starting. progress is called with a
// status line before each wait. onState, when non-nil, is called with the raw
// state of every poll that returns a response, and with StateInFlight when a
// new attempt is issued and its response has not come back, so a caller can
// describe what is happening (booting versus waiting for capacity) rather than
// assume a boot is underway.
//
// A start holds one long-lived request while the instance boots, so a network
// blip mid-wait (switching networks, a dropped VPN) surfaces as a transport
// error even though the boot continues server-side. Those are retried within
// the caller's deadline: the wake is idempotent — a repeated call reattaches
// to the same booting instance — so retrying never launches a second one.
//
// When retainUntil is non-nil, the instance's Retain-Until tag is set so the
// idle sweep does not terminate it before the stated deadline; the parameter
// rides on every retry, since every retry is the same start.
func Start(ctx context.Context, cfg Config, progress func(string), onState func(string), retainUntil *time.Time) (*Response, error) {
	startURL := cfg.StartURL
	if retainUntil != nil {
		u, err := url.Parse(startURL)
		if err == nil {
			q := u.Query()
			q.Set("retainUntil", retainUntil.UTC().Format(time.RFC3339))
			u.RawQuery = q.Encode()
			startURL = u.String()
		}
	}
	// The last reply's state and seed id, for the give-up error: a deadline
	// that expires mid-seed is not the same situation as one that expires
	// mid-boot, and the operator should be told which.
	lastState := ""
	lastSeedID := ""
	for {
		// Supersedes whatever the previous attempt reported — including a
		// no-capacity reply: this attempt has not refused anything yet, and a
		// refusal arrives long before a boot would, so the observer should not
		// keep reading the older attempt's verdict while this one is in flight.
		if onState != nil {
			onState(StateInFlight)
		}
		resp, err := call(ctx, cfg, http.MethodPost, startURL, nil)
		if err != nil {
			var urlErr *url.Error
			if ctx.Err() == nil && errors.As(err, &urlErr) {
				progress(fmt.Sprintf("connection dropped (%v); retrying in %s", urlErr.Unwrap(), startRetryWait))
				select {
				case <-ctx.Done():
					return nil, giveUpWaiting(ctx, lastState, lastSeedID)
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
			lastState, lastSeedID = resp.State, resp.SeedID
			wait := resp.RetryAfterSeconds
			if wait <= 0 {
				wait = 1
			}
			// "instance <state>" would be a lie while the weights seed: there
			// is no instance yet, and the seed is what the operator follows.
			if resp.State == stateSeeding && resp.SeedID != "" {
				progress(fmt.Sprintf("seeding the weights (seed %s); retrying in %ds", resp.SeedID, wait))
			} else {
				progress(fmt.Sprintf("instance %s; retrying in %ds", resp.State, wait))
			}
			select {
			case <-ctx.Done():
				return nil, giveUpWaiting(ctx, lastState, lastSeedID)
			case <-time.After(time.Duration(wait) * time.Second):
			}
		default:
			hint := ""
			if resp.StatusCode == http.StatusForbidden {
				hint = forbiddenHint(cfg.Region, resp.Message)
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
		return nil, controlReplyError(cfg.Region, "status", resp)
	}
	return resp, nil
}

// Stop stops the instance immediately rather than waiting for the idle timer:
// it terminates it, discarding the boot disk and the weights on it, so the
// next start is a full launch.
func Stop(ctx context.Context, cfg Config) (*Response, error) {
	resp, err := call(ctx, cfg, http.MethodPost, cfg.StopURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, controlReplyError(cfg.Region, "stop", resp)
	}
	return resp, nil
}

// Pause stops the instance without terminating it: the boot disk and its
// weights survive, so a later Start re-wakes it instead of launching fresh.
// The instance is terminated by the control plane's sweep once it has been
// stopped beyond the retention window. When force is set, the stop is marked
// forced on the way over: the control plane takes the box down without first
// asking the engine to shut down, which is what a wedged engine or daemon
// needs.
func Pause(ctx context.Context, cfg Config, force bool) (*Response, error) {
	resp, err := call(ctx, cfg, http.MethodPost, pauseURL(cfg.StopURL, force), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, controlReplyError(cfg.Region, "pause", resp)
	}
	return resp, nil
}

// Restart stops the instance in the pause manner — without terminating it, so
// the boot disk and its weights survive, the re-wake is fast, and the
// environment's address does not change — and then wakes it up, reusing
// Start's retry and deadline behaviour until the model serves again. force
// marks the stop as forced: the engine is not asked to shut down first. When
// the wake fails after the stop takes effect, the error says the instance is
// stopped and that Start will bring it back — the very state a manual pause
// leaves behind.
func Restart(ctx context.Context, cfg Config, force bool, progress func(string), onState func(string)) (*Response, error) {
	if _, err := Pause(ctx, cfg, force); err != nil {
		return nil, err
	}
	progress("stopped; waking it")
	resp, err := Start(ctx, cfg, progress, onState, nil)
	if err != nil {
		return nil, fmt.Errorf("%w — the instance is stopped; `spinloop remote start` will bring it back", err)
	}
	return resp, nil
}

// pauseURL points the stop Lambda at its pause mode: the same Function URL
// with an action parameter, so both modes share the one configured endpoint
// and old configs need no new entry. force additionally marks the stop as
// forced — the same parameter the terminate mode reads — so a control plane
// that predates it simply ignores it and makes the graceful stop.
func pauseURL(stopURL string, force bool) string {
	u, err := url.Parse(stopURL)
	if err != nil {
		return stopURL
	}
	q := u.Query()
	q.Set("action", "pause")
	if force {
		q.Set("force", "true")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Keep sets the Retain-Until tag on the environment's instance, preventing the
// idle sweep from terminating it before the stated deadline. A manual stop or
// pause still takes effect: the tag guards against accidental death. The CLI
// computes the deadline from a duration and passes the absolute time here.
func Keep(ctx context.Context, cfg Config, retainUntil time.Time) (*Response, error) {
	if cfg.UpdateURL == "" {
		return nil, fmt.Errorf(
			"no update_url configured: the remote deployment needs to be updated for keep support")
	}
	u, err := url.Parse(cfg.UpdateURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("cmd", "set-keep")
	q.Set("retainUntil", retainUntil.UTC().Format(time.RFC3339))
	u.RawQuery = q.Encode()
	resp, err := call(ctx, cfg, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, controlReplyError(cfg.Region, "keep", resp)
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
	status, respBody, err := send(ctx, cfg, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	out := &Response{StatusCode: status}
	if err := json.Unmarshal(respBody, out); err != nil {
		hint := ""
		if status == http.StatusForbidden {
			hint = forbiddenHint(cfg.Region, string(respBody))
		}
		return nil, fmt.Errorf("%s returned HTTP %d%s: %s",
			method, status, hint, truncate(string(respBody), 200))
	}
	return out, nil
}

// send signs and performs one request, returning the status and raw body. It
// is the transport half of call, split out because the seed Lambda's replies
// have their own shapes: seeds are account-wide, so they share the signing but
// not the Response struct or the environment query parameter.
func send(
	ctx context.Context, cfg Config, method, rawURL string, body []byte,
) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		// Set explicitly: with a bytes.Reader net/http would infer it, but the
		// signature covers Content-Length, so leaving it to chance risks a
		// mismatch between what is signed and what is sent.
		req.ContentLength = int64(len(body))
	}
	if err := sign(ctx, req, cfg.Region, body); err != nil {
		return 0, nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	checkControlPlaneVersion(resp.Header)
	return resp.StatusCode, respBody, nil
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
				"AWS credentials are expired or invalid: %w (%s)", err, credsRefreshHint(region))
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
// caller's ambient AWS credentials are expired or invalid, rather than lacking
// permission.
const refreshCredsHint = "refresh your env credentials, profile, or SSO session"

// storedCredsHint is the refresh guidance when the stored control-plane key
// was the credential in use: refreshing the ambient credentials would not
// change what signs the request, so the fix is to store a new key.
const storedCredsHint = "run `spinloop remote auth --store` to create a new stored key"

// credsRefreshHint picks the refresh guidance for a rejected request by the
// source of the credential that signed it: explicit ambient credentials win
// over the stored key in resolution, so they name themselves; a stored key in
// use names `spinloop remote auth --store`; anything else is ambient.
func credsRefreshHint(region string) string {
	if _, ok := LookupStoredCredential(region); ok && !explicitAmbientCreds() {
		return storedCredsHint
	}
	return refreshCredsHint
}

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
// user to refresh the credential that signed the request (source-aware — the
// stored key, if that is what signed it); anything else keeps the
// IAM-permission hint, since a resolvable credential that lacks
// lambda:InvokeFunctionUrl fails the same way.
func forbiddenHint(region, detail string) string {
	if expiredCredsMarker(detail) {
		return fmt.Sprintf(" (AWS credentials are expired or invalid — %s)", credsRefreshHint(region))
	}
	return " (do your AWS credentials grant lambda:InvokeFunctionUrl?)"
}

// controlReplyError turns a non-success control reply into an error, reading the
// reply's own detail (error or message) and, for a 403, classifying whether the
// credentials are expired/invalid or merely lack permission. Callers that treat
// some non-200 statuses as expected (Start's 503 "still starting") must handle
// those before falling through to this.
func controlReplyError(region, method string, resp *Response) error {
	detail := resp.Error
	if detail == "" {
		detail = resp.Message
	}
	hint := ""
	if resp.StatusCode == http.StatusForbidden {
		hint = forbiddenHint(region, detail)
	}
	return fmt.Errorf("%s returned HTTP %d%s: %s", method, resp.StatusCode, hint, truncate(detail, 200))
}

// StatsResponse is the JSON reply from the stats Lambda.
type StatsResponse struct {
	StatusCode int `json:"-"`
	// Message carries a rejection reason on a non-success reply — including the
	// authorizer's own text on a 403 — so an expired-credential rejection can be
	// classified even though the stats fields are empty.
	Message      string `json:"message"`
	Environment  string `json:"environment"`
	State        string `json:"state"`
	InstanceID   string `json:"instanceId"`
	InstanceType string `json:"instanceType"`
	Runner       string `json:"runner"`
	ModelID      string `json:"modelId"`
	// ServedName is the name the engine answers to beside the model id,
	// relayed from the environment's deploy config — the same field the
	// status reply's Response.ServedName carries, so a stopped environment's
	// wakeable name matches what it reported while running.
	ServedName    string      `json:"servedName"`
	UptimeSeconds int         `json:"uptimeSeconds"`
	Tokens        *TokenStats `json:"tokens"`
	GPUs          []GpuStat   `json:"gpus"`
	CPU           *CpuStat    `json:"cpu"`
	Memory        *MemoryStat `json:"memory"`
	// History relays the daemon's retained system readings verbatim — the data
	// the bar format draws. Nil for a daemon that has never run an engine, or
	// a daemon that predates the field; the drawing falls back to the gauge
	// for a series with no readings, per series.
	History []metrics.HistorySample `json:"history,omitempty"`
	Errors  []string                `json:"errors"`
	// LastActiveAt and IdleSeconds relay the on-instance daemon's answer to
	// "has this engine been working?", verbatim. Empty when the daemon was
	// unreachable, when no engine has run, or when the control plane predates
	// this — in every case the formatters simply omit the line.
	LastActiveAt string `json:"lastActiveAt"`
	IdleSeconds  int    `json:"idleSeconds"`
	// Version is the spinloop binary's build-time version string, relayed from
	// the daemon's /v1/status by the stats Lambda. Empty when the daemon was
	// unreachable or the control plane predates this.
	Version string `json:"version"`
	// RetainUntil is the instance's retention deadline, RFC 3339, read from the
	// Retain-Until tag by the stats Lambda. Empty when the instance has no tag
	// or its deadline has already passed — the reply carries it only while it
	// still keeps the instance alive — and for control planes that predate keep.
	RetainUntil string `json:"retainUntil"`
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
			"no stats_url configured: the control plane needs re-deploying with `pnpm run deploy` (or set SPINLOOP_REMOTE_STATS_URL)")
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
			hint = forbiddenHint(cfg.Region, detail)
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
	checkControlPlaneVersion(resp.Header)
	out := &StatsResponse{StatusCode: resp.StatusCode}
	if err := json.Unmarshal(respBody, out); err != nil {
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = forbiddenHint(cfg.Region, string(respBody))
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
