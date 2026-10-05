package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/config"
)

// registerEnv writes an environment's cloud.json into the registry.
func registerEnv(t *testing.T, name string, cfg cloud.Config) {
	t.Helper()
	if err := os.MkdirAll(must1(cloud.EnvDir(name)), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(cloud.EnvConfigPath(name)), data, 0o600); err != nil {
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

// A bare environment name on --env resolves through the registry.
func TestCloud_EnvNameResolves(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()
	registerEnv(t, "prodenv", cloud.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Spinloop", []byte("PROVIDER openai-compatible\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdStatus([]string{"--env", "prodenv"}); err != nil {
			t.Errorf("status via --env name: %v", err)
		}
	})
	if !strings.Contains(out, "running") {
		t.Errorf("--env name should resolve via the registry, got:\n%s", out)
	}
}

// With no Spinloop in play, the default environment is used.
func TestCloud_DefaultEnvironment(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()
	registerEnv(t, "default", cloud.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})

	t.Chdir(t.TempDir()) // no ./Spinloop here
	out := captureStdout(t, func() {
		if err := cmdStatus([]string{"--env", "default"}); err != nil {
			t.Errorf("status via default env: %v", err)
		}
	})
	if !strings.Contains(out, "running") {
		t.Errorf("no-Spinloop should use the default environment, got:\n%s", out)
	}
}

// A file at the superseded path configures nothing: no path outside the
// registry is read for any name.
func TestCloud_SupersededFileIsNotRead(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := stateServer(t)
	defer server.Close()

	data, _ := json.Marshal(cloud.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})
	home := must1(config.Dir())
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cloud.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	err := cmdMetrics([]string{"--env", "default"})
	if err == nil {
		t.Fatal("the superseded file must not configure an environment")
	}
	if !strings.Contains(err.Error(), "clouds/default/cloud.json") {
		t.Errorf("the failure should name the registry path, got %v", err)
	}
}

func TestCloudList(t *testing.T) {
	isolateConfig(t)

	// Empty registry says so.
	out := captureStdout(t, func() {
		if err := cmdCloudList(nil); err != nil {
			t.Errorf("ls empty: %v", err)
		}
	})
	if !strings.Contains(out, "No cloud environments") {
		t.Errorf("empty ls should say so, got:\n%s", out)
	}

	registerEnv(t, "prod", cloud.Config{StartURL: "https://s", StopURL: "https://x", Region: "eu-west-1", BaseURL: "http://1.2.3.4:8000/v1"})
	if err := os.MkdirAll(must1(cloud.EnvDir("broken")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(cloud.EnvConfigPath("broken")), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	out = captureStdout(t, func() {
		if err := cmdCloudList(nil); err != nil {
			t.Errorf("ls: %v", err)
		}
	})
	for _, want := range []string{"prod", "http://1.2.3.4:8000/v1", "eu-west-1", "broken", "missing or unreadable"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}
