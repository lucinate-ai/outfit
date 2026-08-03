package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestRemoteStats_Running(t *testing.T) {
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
	// WriteRemoteConfig requires StartURL/StopURL for validation; stats reuses the same URL.
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStats(nil); err != nil {
			t.Errorf("cmdRemoteStats: %v", err)
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
			t.Errorf("stats output missing %q:\n%s", want, out)
		}
	}
}

func TestRemoteStats_Stopped(t *testing.T) {
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
		if err := cmdRemoteStats(nil); err != nil {
			t.Errorf("cmdRemoteStats: %v", err)
		}
	})
	if !strings.Contains(out, "state:        stopped") {
		t.Errorf("stats output missing stopped state:\n%s", out)
	}
	if strings.Contains(out, "prompt tokens") {
		t.Errorf("stopped instance should not show token metrics:\n%s", out)
	}
}

func TestRemoteStats_WithErrors(t *testing.T) {
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
		if err := cmdRemoteStats(nil); err != nil {
			t.Errorf("cmdRemoteStats: %v", err)
		}
	})
	for _, want := range []string{"metric collection errors", "nvidia-smi failed", "vmstat timeout"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
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
