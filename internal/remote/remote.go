// Package remote controls the scale-to-zero GPU inference instance defined in
// the cloud-vm-llm repo, by calling its start/stop Lambdas through their
// Function URLs. The URLs use IAM auth, so every request is SigV4-signed
// (service "lambda") with the caller's AWS credentials.
package remote

import (
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
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// httpClient is a package variable so tests can substitute it. The long
// timeout matters: a start call blocks while the instance boots and loads the
// model into VRAM, which takes minutes.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Config holds the connection details for the remote instance's control
// Lambdas: the cloud-vm-llm stack prints it as the OutfitRemoteConfig output,
// ready to paste into the config file.
type Config struct {
	StartURL string `json:"start_url"`
	StopURL  string `json:"stop_url"`
	Region   string `json:"region"`
}

// ConfigPath returns the path of the remote config file, alongside outfit's
// own config in the same directory.
func ConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "outfit", "remote.json")
}

// LoadConfig reads the stored config and applies environment overrides
// (OUTFIT_REMOTE_START_URL, OUTFIT_REMOTE_STOP_URL, OUTFIT_REMOTE_REGION; the
// region also falls back to AWS_REGION and then to the region embedded in the
// Function URL host). getenv is injectable for tests.
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
	if v := getenv("OUTFIT_REMOTE_START_URL"); v != "" {
		cfg.StartURL = v
	}
	if v := getenv("OUTFIT_REMOTE_STOP_URL"); v != "" {
		cfg.StopURL = v
	}
	if v := getenv("OUTFIT_REMOTE_REGION"); v != "" {
		cfg.Region = v
	}
	if cfg.StartURL == "" || cfg.StopURL == "" {
		return Config{}, fmt.Errorf(
			"remote is not configured: paste the OutfitRemoteConfig output of the cloud-vm-llm stack into %s",
			ConfigPath())
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
			ConfigPath())
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
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
}

// Start boots the instance and blocks until vLLM is serving, retrying while
// the endpoint reports it is still starting. progress is called with a status
// line before each wait.
func Start(ctx context.Context, cfg Config, progress func(string)) (*Response, error) {
	for {
		resp, err := call(ctx, cfg, http.MethodPost, cfg.StartURL)
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
	return call(ctx, cfg, http.MethodGet, cfg.StartURL)
}

// Stop stops the instance immediately rather than waiting for the idle timer.
func Stop(ctx context.Context, cfg Config) (*Response, error) {
	return call(ctx, cfg, http.MethodPost, cfg.StopURL)
}

func call(ctx context.Context, cfg Config, method, rawURL string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := sign(ctx, req, cfg.Region); err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out := &Response{StatusCode: resp.StatusCode}
	if err := json.Unmarshal(body, out); err != nil {
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = " (do your AWS credentials grant lambda:InvokeFunctionUrl?)"
		}
		return nil, fmt.Errorf("%s returned HTTP %d%s: %s",
			method, resp.StatusCode, hint, truncate(string(body), 200))
	}
	return out, nil
}

// sign SigV4-signs the request with the default AWS credential chain
// (environment, shared config/credentials, SSO). Function URL IAM auth
// requires the payload hash to be sent and signed via X-Amz-Content-Sha256.
func sign(ctx context.Context, req *http.Request, region string) error {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return err
	}
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf(
			"resolving AWS credentials: %w (configure env credentials, a profile or an SSO session)", err)
	}
	hash := sha256.Sum256(nil) // requests carry no body
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
