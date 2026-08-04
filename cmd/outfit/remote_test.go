package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// stubAWSEnv pins the default credential chain to static env credentials so
// SigV4 signing works offline without touching a real profile or IMDS.
func stubAWSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATESTTESTTESTTEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// writeRemoteConfig stores a remote config pointing at the test server.
func writeRemoteConfig(t *testing.T, serverURL string) {
	t.Helper()
	path := remote.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(remote.Config{
		StartURL: serverURL,
		StopURL:  serverURL,
		EnvURL:   serverURL,
		Region:   "eu-west-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteEnvName covers the harness-provider-name contract: a bare REMOTE is
// its own name, a path form takes the name from its config's environment field,
// and an empty value or an absent/environment-less config yields "" so the caller
// keeps the PROVIDER value.
func TestRemoteEnvName(t *testing.T) {
	dir := t.TempDir()
	withEnv := filepath.Join(dir, "with-env.json")
	if err := os.WriteFile(withEnv, []byte(`{"environment":"dev-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withoutEnv := filepath.Join(dir, "without-env.json")
	if err := os.WriteFile(withoutEnv, []byte(`{"base_url":"http://x/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, value, want string
	}{
		{"empty is no name", "", ""},
		{"bare name is itself", "dev-1", "dev-1"},
		{"path uses environment field", withEnv, "dev-1"},
		{"path without environment is no name", withoutEnv, ""},
		{"absent path is tolerated", filepath.Join(dir, "missing.json"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := remoteEnvName(tc.value, dir)
			if err != nil {
				t.Fatalf("remoteEnvName(%q): %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("remoteEnvName(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestRemoteEnvName_Malformed checks that a path-form REMOTE naming a malformed
// config surfaces a parse error rather than silently yielding no name.
func TestRemoteEnvName_Malformed(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := remoteEnvName(bad, dir); err == nil {
		t.Error("expected a parse error for a malformed remote config")
	}
}

func TestRemoteDispatch(t *testing.T) {
	if err := run([]string{"remote"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("bare remote should error with usage, got %v", err)
	}
	if err := run([]string{"remote", "bogus"}); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown subcommand should error, got %v", err)
	}
}

func TestRemote_Unconfigured(t *testing.T) {
	isolateConfig(t)
	for _, sub := range []string{"start", "stop", "status"} { // deploy needs an Outfit, covered separately
		if err := run([]string{"remote", sub}); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Errorf("remote %s without config should explain setup, got %v", sub, err)
		}
	}
}

func TestRemoteStart_PrintsExports(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStart([]string{"--env"}); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	if !strings.Contains(out, "export OPENAI_BASE_URL=http://198.51.100.1:8000/v1") ||
		!strings.Contains(out, "export OPENAI_API_KEY=sk-test") {
		t.Errorf("start should print the endpoint exports, got:\n%s", out)
	}
}

// Start without --env prints nothing to stdout (progress goes to stderr).
func TestRemoteStart_NoExportsWithoutFlag(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStart(nil); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	if strings.Contains(out, "export OPENAI_") {
		t.Errorf("start without --env should not print exports, got:\n%s", out)
	}
}

// Start with -env after a positional argument still parses the flag.
// Regression test: Go's flag package stops at the first non-flag argument,
// so `outfit remote start path -env` would silently ignore -env without
// sortFlagsBeforeArgs.
func TestRemoteStart_FlagAfterPositional(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	outfitFile := "PROVIDER openai-compatible\nREMOTE remote.json\n"
	if err := os.WriteFile(filepath.Join(dir, "Outfit"), []byte(outfitFile), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "remote.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdRemoteStart([]string{dir, "-e"}); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	if !strings.Contains(out, "export OPENAI_BASE_URL=http://198.51.100.1:8000/v1") ||
		!strings.Contains(out, "export OPENAI_API_KEY=sk-test") {
		t.Errorf("start with -e after positional should print exports, got:\n%s", out)
	}
}

// Remote env command prints exports for a running endpoint.
func TestRemoteEnv_PrintsExports(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("env should GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"base_url":"http://198.51.100.1:8000/v1","api_key":"sk-remote"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteEnv(nil); err != nil {
			t.Errorf("cmdRemoteEnv: %v", err)
		}
	})
	if !strings.Contains(out, "export OPENAI_BASE_URL=http://198.51.100.1:8000/v1") ||
		!strings.Contains(out, "export OPENAI_API_KEY=sk-remote") {
		t.Errorf("env should print the endpoint exports, got:\n%s", out)
	}
}

func TestRemoteStatus_PrintsState(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"running","healthy":true,"base_url":"http://198.51.100.1:8000/v1"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("cmdRemoteStatus: %v", err)
		}
	})
	for _, want := range []string{"state: running", "healthy: true", "base_url: http://198.51.100.1:8000/v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestRemoteStop_PrintsState(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("stop should POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopping"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStop(nil); err != nil {
			t.Errorf("cmdRemoteStop: %v", err)
		}
	})
	if !strings.Contains(out, "state: stopping") {
		t.Errorf("stop should print the state, got:\n%s", out)
	}
}

func TestRemote_OutfitDiscovery(t *testing.T) {
	isolateConfig(t) // no per-user config exists, so success proves discovery
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"running","healthy":true,"base_url":"http://198.51.100.1:8000/v1"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	outfitFile := "PROVIDER openai-compatible\nREMOTE remote.json\n"
	if err := os.WriteFile("Outfit", []byte(outfitFile), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("cmdRemoteStatus: %v", err)
		}
	})
	if !strings.Contains(out, "state: running") {
		t.Errorf("status via Outfit REMOTE should work, got:\n%s", out)
	}
}

func TestRemote_ExplicitOutfitNeedsRemote(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER ollama\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdRemoteStatus([]string{"Outfit"})
	if err == nil || !strings.Contains(err.Error(), "no REMOTE") {
		t.Errorf("explicit Outfit without REMOTE should error, got %v", err)
	}
}

func TestRemote_OutfitWithoutRemoteFallsBack(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopped","healthy":false,"base_url":"http://198.51.100.1:8000/v1"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER ollama\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("cmdRemoteStatus: %v", err)
		}
	})
	if !strings.Contains(out, "state: stopped") {
		t.Errorf("an Outfit without REMOTE should fall back to the user config, got:\n%s", out)
	}
}

func TestRemote_IgnoresLowercaseOutfitFile(t *testing.T) {
	// On case-insensitive filesystems a stat of "Outfit" matches a file named
	// "outfit" (e.g. the built binary in this repo's root); discovery must not
	// try to parse it.
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopped","healthy":false,"base_url":"http://198.51.100.1:8000/v1"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	t.Chdir(t.TempDir())
	if err := os.WriteFile("outfit", []byte{0xcf, 0xfa, 0xed, 0xfe}, 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("cmdRemoteStatus: %v", err)
		}
	})
	if !strings.Contains(out, "state: stopped") {
		t.Errorf("a lowercase outfit file should not shadow discovery, got:\n%s", out)
	}
}

func TestRemoteMetrics_Running(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceId": "i-abc123",
			"instanceType": "g6e.xlarge",
			"runner": "llamacpp",
			"modelId": "unsloth/Qwen3.6-27B",
			"uptimeSeconds": 3725,
			"tokens": {
				"running": 1,
				"promptTokens": 50000,
				"generationTokens": 120000,
				"requests": 342
			},
			"gpus": [{
				"index": 0,
				"name": "NVIDIA L40S",
				"utilization": 85,
				"memoryUsed": 32212254720,
				"memoryTotal": 48130938880,
				"temperature": 72
			}],
			"cpu": {"utilization": 23.5},
			"memory": {"total": 17179869184, "used": 4294967296}
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=table"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	for _, want := range []string{
		"environment:  dev",
		"state:        running",
		"instance:     i-abc123",
		"instanceType: g6e.xlarge",
		"runner:       llamacpp",
		"model:        unsloth/Qwen3.6-27B",
		"uptime:       1h 2m 5s",
		"running:          1",
		"prompt tokens:    50000",
		"generation tokens: 120000",
		"requests:         342",
		"GPU 0: NVIDIA L40S",
		"util=85%",
		"CPU: 24% util",
		"RAM: 4.0 GB/16.0 GB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output missing %q:\n%s", want, out)
		}
	}
}

func TestRemoteMetrics_Stopped(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "stopped",
			"runner": "llamacpp",
			"modelId": "unsloth/Qwen3.6-27B"
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=table"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	if !strings.Contains(out, "state:        stopped") {
		t.Errorf("metrics output missing stopped state:\n%s", out)
	}
	if strings.Contains(out, "prompt tokens") {
		t.Errorf("stopped instance should not show token metrics:\n%s", out)
	}
}

func TestRemoteMetrics_WithErrors(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"uptimeSeconds": 60,
			"errors": ["nvidia-smi failed", "vmstat timeout"]
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	errOut := captureStderr(t, func() {
		if err := cmdRemoteMetrics(nil); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	for _, want := range []string{"metric collection errors", "nvidia-smi failed", "vmstat timeout"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestRemoteMetrics_DefaultFormat(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceId": "i-abc123",
			"instanceType": "g6e.xlarge",
			"uptimeSeconds": 100
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics(nil); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	if !strings.Contains(out, "dev") || !strings.Contains(out, "running") {
		t.Errorf("default format should be bar, got:\n%s", out)
	}
}

func TestRemoteMetrics_JsonFormat(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceId": "i-abc123",
			"instanceType": "g6e.xlarge",
			"runner": "llamacpp",
			"modelId": "test/model",
			"uptimeSeconds": 300
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=json"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, out)
	}
	if result["environment"] != "dev" {
		t.Errorf("expected environment=dev, got %v", result["environment"])
	}
	if result["state"] != "running" {
		t.Errorf("expected state=running, got %v", result["state"])
	}
	if result["instanceId"] != "i-abc123" {
		t.Errorf("expected instanceId=i-abc123, got %v", result["instanceId"])
	}
}

func TestRemoteMetrics_JsonFormatWithCost(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceId": "i-abc123",
			"instanceType": "g6e.xlarge",
			"uptimeSeconds": 300
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=json", "--cost"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, out)
	}
	if result["environment"] != "dev" {
		t.Errorf("expected environment=dev, got %v", result["environment"])
	}
}

func TestRemoteMetrics_InvalidFormat(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	writeRemoteConfig(t, "http://localhost:0")

	err := cmdRemoteMetrics([]string{"--format=csv"})
	if err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("expected format error, got %v", err)
	}
}

func TestRemoteMetrics_BarFormat(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceType": "g5.2xlarge",
			"modelId": "llama-3.1-8b",
			"uptimeSeconds": 300,
			"gpus": [
				{"index": 0, "name": "A10G", "utilization": 85, "memoryUsed": 16106127360, "memoryTotal": 24297466368, "temperature": 65}
			],
			"cpu": {"utilization": 45.5},
			"memory": {"total": 33145275904, "used": 12884901888},
			"tokens": {"running": 2, "promptTokens": 1500, "generationTokens": 8200, "requests": 120}
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		err := cmdRemoteMetrics([]string{"--format=bar"})
		if err != nil {
			t.Errorf("bar format failed: %v", err)
		}
	})

	if !strings.Contains(out, "dev") || !strings.Contains(out, "running") {
		t.Errorf("bar output missing header:\n%s", out)
	}
	if !strings.Contains(out, "CPU") {
		t.Errorf("bar output missing CPU bar:\n%s", out)
	}
	if !strings.Contains(out, "RAM") {
		t.Errorf("bar output missing RAM bar:\n%s", out)
	}
	if !strings.Contains(out, "GPU util") {
		t.Errorf("bar output missing GPU util bar:\n%s", out)
	}
	if !strings.Contains(out, "GPU mem") {
		t.Errorf("bar output missing GPU mem bar:\n%s", out)
	}
	if !strings.Contains(out, "85%") {
		t.Errorf("bar output missing GPU utilization percentage:\n%s", out)
	}
	if !strings.Contains(out, "running:") {
		t.Errorf("bar output missing token stats:\n%s", out)
	}
}

func TestRemoteMetrics_BarFormatStopped(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "stopped",
			"instanceType": "g5.2xlarge"
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		err := cmdRemoteMetrics([]string{"--format=bar"})
		if err != nil {
			t.Errorf("bar format failed: %v", err)
		}
	})

	if !strings.Contains(out, "stopped") {
		t.Errorf("bar output should show stopped state:\n%s", out)
	}
	if strings.Contains(out, "CPU") {
		t.Errorf("bar output should not show bars for stopped state:\n%s", out)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0s"},
		{45, "45s"},
		{125, "2m 5s"},
		{3725, "1h 2m 5s"},
		{86400, "24h 0m 0s"},
	}
	for _, tc := range tests {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1 KB"},
		{1048576, "1 MB"},
		{4294967296, "4.0 GB"},
		{32212254720, "30.0 GB"},
	}
	for _, tc := range tests {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSortFlagsBeforeArgs(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{}, []string{}},
		{[]string{"-env"}, []string{"-env"}},
		{[]string{"path"}, []string{"path"}},
		{[]string{"path", "-env"}, []string{"-env", "path"}},
		{[]string{"-env", "path"}, []string{"-env", "path"}},
		{[]string{"path", "-e"}, []string{"-e", "path"}},
	}
	for _, tc := range tests {
		got := sortFlagsBeforeArgs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("sortFlagsBeforeArgs(%v) = %v (len %d), want %v (len %d)", tc.in, got, len(got), tc.want, len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("sortFlagsBeforeArgs(%v) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestRemoteMetrics_WatchMode(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		// Fail on 3rd call to stop the loop.
		if callCount >= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "server error"}`))
			return
		}
		w.Write([]byte(`{"environment": "dev", "state": "running", "uptimeSeconds": 100}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	// Short interval so the test completes quickly.
	oldInterval := metricsWatchInterval
	metricsWatchInterval = 50 * time.Millisecond
	defer func() { metricsWatchInterval = oldInterval }()

	out := captureStdout(t, func() {
		err := cmdRemoteMetrics([]string{"--watch", "--format=table"})
		// Expects error from the 3rd call.
		if err == nil {
			t.Error("watch should exit with error when server fails")
		}
	})

	if callCount < 3 {
		t.Errorf("watch should have polled at least 3 times, got %d calls", callCount)
	}
	// Should see the metrics output multiple times (watch polls repeatedly).
	count := strings.Count(out, "environment:  dev")
	if count < 2 {
		t.Errorf("watch should have produced at least 2 outputs, got %d:\n%s", count, out)
	}
}

func TestRemoteMetrics_WatchShortFlag(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount >= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "server error"}`))
			return
		}
		w.Write([]byte(`{"environment": "dev", "state": "stopped"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	oldInterval := metricsWatchInterval
	metricsWatchInterval = 10 * time.Millisecond
	defer func() { metricsWatchInterval = oldInterval }()

	out := captureStdout(t, func() {
		err := cmdRemoteMetrics([]string{"-w", "--format=table"})
		if err == nil {
			t.Error("-w should exit with error when server fails")
		}
	})
	if !strings.Contains(out, "environment:  dev") {
		t.Errorf("-w flag should work like --watch:\n%s", out)
	}
	if callCount < 2 {
		t.Errorf("-w should have polled at least twice, got %d calls", callCount)
	}
}

func TestRemoteMetrics_MultiGPU(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"gpus": [
				{"index": 0, "name": "GPU A", "utilization": 50, "memoryUsed": 1073741824, "memoryTotal": 2147483648, "temperature": 60},
				{"index": 1, "name": "GPU B", "utilization": 70, "memoryUsed": 2147483648, "memoryTotal": 2147483648, "temperature": 75}
			]
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=table"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	for _, want := range []string{"GPU 0: GPU A", "GPU 1: GPU B", "avg util:", "total mem:"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-GPU output missing %q:\n%s", want, out)
		}
	}
}

func TestRemoteMetrics_JsonStopped(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"environment": "dev", "state": "stopped"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=json"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, out)
	}
	if result["state"] != "stopped" {
		t.Errorf("expected state=stopped, got %v", result["state"])
	}
}

func TestRemoteMetrics_JsonWithErrors(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"uptimeSeconds": 100,
			"errors": ["nvidia-smi failed"]
		}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=json"}); err != nil {
			t.Errorf("cmdRemoteMetrics: %v", err)
		}
	})
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, out)
	}
	errors, ok := result["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Errorf("expected errors in JSON output:\n%s", out)
	}
}

// The start probe runs after the endpoint is ready. When the probe connects,
// no warning is printed.
func TestRemoteStart_ProbeSucceedsNoWarning(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	// Create a TCP listener to simulate a reachable endpoint.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	defer l.Close()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"state":"ready","base_url":"%s","api_key":"sk-test"}`, baseURL)))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	stderr := captureStderr(t, func() {
		if err := cmdRemoteStart(nil); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	if strings.Contains(stderr, "not reachable") {
		t.Errorf("should not warn when probe succeeds, got:\n%s", stderr)
	}
}

// When the probe fails, start warns but still exits 0.
func TestRemoteStart_ProbeFailsWarns(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	origDetect := detectPublicCIDRFn
	detectPublicCIDRFn = func(context.Context) (string, error) { return "203.0.113.5/32", nil }
	t.Cleanup(func() { detectPublicCIDRFn = origDetect })

	origProbe := remote.ProbeTimeout
	remote.ProbeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { remote.ProbeTimeout = origProbe })

	baseURL := "http://192.0.2.1:8000/v1" // unreachable
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"state":"ready","base_url":"%s","api_key":"sk-test"}`, baseURL)))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	errOut := captureStderr(t, func() {
		err := cmdRemoteStart(nil)
		if err != nil {
			t.Fatalf("start should exit 0 after a probe warning, got %v", err)
		}
	})

	if !strings.Contains(errOut, "not reachable from this network") {
		t.Errorf("expected a reachability warning, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "203.0.113.5/32") {
		t.Errorf("expected the detected CIDR in the hint, got:\n%s", errOut)
	}
}

// When the probe fails and IP detection also fails, the hint uses a placeholder.
func TestRemoteStart_ProbeFailsIPDetectFails(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	origDetect := detectPublicCIDRFn
	detectPublicCIDRFn = func(context.Context) (string, error) { return "", fmt.Errorf("network error") }
	t.Cleanup(func() { detectPublicCIDRFn = origDetect })

	origProbe := remote.ProbeTimeout
	remote.ProbeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { remote.ProbeTimeout = origProbe })

	baseURL := "http://192.0.2.1:8000/v1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"state":"ready","base_url":"%s","api_key":"sk-test"}`, baseURL)))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	errOut := captureStderr(t, func() {
		err := cmdRemoteStart(nil)
		if err != nil {
			t.Fatalf("start should exit 0 even when probe and IP detection both fail, got %v", err)
		}
	})

	if !strings.Contains(errOut, "not reachable from this network") {
		t.Errorf("expected a reachability warning, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "<your-ip>/32") {
		t.Errorf("expected the placeholder CIDR, got:\n%s", errOut)
	}
}
