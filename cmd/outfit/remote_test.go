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
	data, err := json.Marshal(remote.Config{StartURL: serverURL, StopURL: serverURL, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
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
	for _, sub := range []string{"start", "stop", "status"} {
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
		if err := cmdRemoteStart(nil); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	if !strings.Contains(out, "export OPENAI_BASE_URL=http://198.51.100.1:8000/v1") ||
		!strings.Contains(out, "export OPENAI_API_KEY=sk-test") {
		t.Errorf("start should print the endpoint exports, got:\n%s", out)
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
