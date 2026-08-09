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

// registerEnv writes an environment's remote.json into the registry.
func registerEnv(t *testing.T, name string, cfg remote.Config) {
	t.Helper()
	if err := os.MkdirAll(must1(remote.EnvDir(name)), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(remote.EnvConfigPath(name)), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stateServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"running","healthy":true}`))
	}))
}

// A bare REMOTE name resolves through the registry.
func TestRemote_EnvNameResolves(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()
	registerEnv(t, "prodenv", remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER openai-compatible\nREMOTE prodenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("status via REMOTE name: %v", err)
		}
	})
	if !strings.Contains(out, "state: running") {
		t.Errorf("REMOTE name should resolve via the registry, got:\n%s", out)
	}
}

// A path-form REMOTE still resolves beside the Outfit (back-compat).
func TestRemote_PathFormStillWorks(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER openai-compatible\nREMOTE ./remote.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})
	if err := os.WriteFile("remote.json", data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("status via REMOTE path: %v", err)
		}
	})
	if !strings.Contains(out, "state: running") {
		t.Errorf("REMOTE path should resolve beside the Outfit, got:\n%s", out)
	}
}

// With no Outfit in play, the default environment is used.
func TestRemote_DefaultEnvironment(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()
	registerEnv(t, "default", remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})

	t.Chdir(t.TempDir()) // no ./Outfit here
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("status via default env: %v", err)
		}
	})
	if !strings.Contains(out, "state: running") {
		t.Errorf("no-Outfit should use the default environment, got:\n%s", out)
	}
}

// A pre-existing ~/.config/outfit/remote.json is read as the default env.
func TestRemote_LegacyFileReadThrough(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()

	data, _ := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})
	if err := os.MkdirAll(filepath.Dir(must1(remote.ConfigPath())), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(remote.ConfigPath()), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Errorf("status via legacy file: %v", err)
		}
	})
	if !strings.Contains(out, "state: running") {
		t.Errorf("legacy remote.json should be read as default, got:\n%s", out)
	}
}

func TestRemoteList(t *testing.T) {
	isolateConfig(t)

	// Empty registry says so.
	out := captureStdout(t, func() {
		if err := cmdRemoteList(nil); err != nil {
			t.Errorf("ls empty: %v", err)
		}
	})
	if !strings.Contains(out, "No remote environments") {
		t.Errorf("empty ls should say so, got:\n%s", out)
	}

	registerEnv(t, "prod", remote.Config{StartURL: "https://s", StopURL: "https://x", Region: "eu-west-1", BaseURL: "http://1.2.3.4:8000/v1"})
	if err := os.MkdirAll(must1(remote.EnvDir("broken")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(remote.EnvConfigPath("broken")), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	out = captureStdout(t, func() {
		if err := cmdRemoteList(nil); err != nil {
			t.Errorf("ls: %v", err)
		}
	})
	for _, want := range []string{"prod", "http://1.2.3.4:8000/v1", "eu-west-1", "broken", "missing or unreadable"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}
