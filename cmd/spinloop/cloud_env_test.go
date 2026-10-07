package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
)

// unsetEnvOnCleanup removes keys that applySpinloopEnv sets via os.Setenv (which,
// unlike t.Setenv, are not auto-restored) once the test finishes.
func unsetEnvOnCleanup(t *testing.T, keys ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, k := range keys {
			os.Unsetenv(k)
		}
	})
}

// applySpinloopEnv establishes the local environment with precedence
// ENV > process environment > .env.
func TestApplySpinloopEnv_Precedence(t *testing.T) {
	dir := t.TempDir()
	dotenv := "SPINLOOP_TEST_GAP=from-dotenv\n" +
		"SPINLOOP_TEST_SHELL=from-dotenv\n" +
		"SPINLOOP_TEST_ENVKW=from-dotenv\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}
	// The shell already exports two of them; the .env must not shadow those.
	t.Setenv("SPINLOOP_TEST_SHELL", "from-shell")
	t.Setenv("SPINLOOP_TEST_ENVKW", "from-shell")
	unsetEnvOnCleanup(t, "SPINLOOP_TEST_GAP") // applySpinloopEnv sets this from the .env

	sel := spinloop.Selection{Env: []spinloop.EnvVar{
		{Key: "SPINLOOP_TEST_ENVKW", Value: "from-env-keyword"},
	}}
	if err := applySpinloopEnv(sel, filepath.Join(dir, "Spinloop")); err != nil {
		t.Fatalf("applySpinloopEnv: %v", err)
	}

	want := map[string]string{
		"SPINLOOP_TEST_GAP":   "from-dotenv",      // .env fills a gap
		"SPINLOOP_TEST_SHELL": "from-shell",       // process environment wins over .env
		"SPINLOOP_TEST_ENVKW": "from-env-keyword", // ENV overrides both
	}
	for k, w := range want {
		if got := os.Getenv(k); got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}

// A .env that cannot be read (here, a directory in its place) surfaces the
// error rather than proceeding on a partial environment.
func TestApplySpinloopEnv_ReadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := applySpinloopEnv(spinloop.Selection{}, filepath.Join(dir, "Spinloop")); err == nil {
		t.Error("applySpinloopEnv should surface a .env read error")
	}
}

// A URL-sourced Spinloop has no local directory to look beside, so its .env
// read is skipped entirely rather than attempted against a mangled path.
func TestApplySpinloopEnv_URLSourceSkipsEnvFile(t *testing.T) {
	sel := spinloop.Selection{Env: []spinloop.EnvVar{
		{Key: "SPINLOOP_TEST_URL_ENVKW", Value: "from-env-keyword"},
	}}
	unsetEnvOnCleanup(t, "SPINLOOP_TEST_URL_ENVKW")
	if err := applySpinloopEnv(sel, "https://example.com/team/Spinloop"); err != nil {
		t.Fatalf("applySpinloopEnv: %v", err)
	}
	if got := os.Getenv("SPINLOOP_TEST_URL_ENVKW"); got != "from-env-keyword" {
		t.Errorf("SPINLOOP_TEST_URL_ENVKW = %q, want %q (ENV instructions still apply)", got, "from-env-keyword")
	}
}

// hitRecorder is an httptest handler that records it was reached and returns a
// benign stop response.
func hitRecorder(name string, hit chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- name:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopping"}`))
	}
}

// A .env beside the Spinloop reaches a control command: its SPINLOOP_CLOUD_STOP_URL
// override wins over the cloud.json value, so the .env server is the one hit.
func TestCloudStop_RespectsDotEnv(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	hit := make(chan string, 1)
	fromConfig := httptest.NewServer(hitRecorder("cloud.json", hit))
	defer fromConfig.Close()
	fromDotEnv := httptest.NewServer(hitRecorder("dotenv", hit))
	defer fromDotEnv.Close()

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Spinloop", []byte("PROVIDER openai-compatible\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cloud.SaveEnvironment("testenv", cloud.Config{StartURL: fromConfig.URL, StopURL: fromConfig.URL, Region: "eu-west-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("SPINLOOP_CLOUD_STOP_URL="+fromDotEnv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "SPINLOOP_CLOUD_STOP_URL")

	// An explicit Spinloop path, exercising resolveCloudConfig's explicit-arg branch.
	if err := cmdCloudStop([]string{"Spinloop", "--env", "testenv"}); err != nil {
		t.Fatalf("cmdCloudStop: %v", err)
	}
	select {
	case name := <-hit:
		if name != "dotenv" {
			t.Errorf("stop reached the %q server, want the .env override to win", name)
		}
	default:
		t.Error("no server was reached")
	}
}

// An ENV instruction overrides the .env end-to-end: with both setting
// SPINLOOP_CLOUD_STOP_URL, the ENV server is the one hit.
func TestCloudStop_EnvKeywordOverridesDotEnv(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	hit := make(chan string, 1)
	fromDotEnv := httptest.NewServer(hitRecorder("dotenv", hit))
	defer fromDotEnv.Close()
	fromEnvKw := httptest.NewServer(hitRecorder("env-keyword", hit))
	defer fromEnvKw.Close()

	t.Chdir(t.TempDir())
	spinloopBody := "PROVIDER openai-compatible\n" +
		"ENV SPINLOOP_CLOUD_STOP_URL=" + fromEnvKw.URL + "\n"
	if err := os.WriteFile("Spinloop", []byte(spinloopBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// The environment still supplies the required StartURL; StopURL is overridden.
	if err := cloud.SaveEnvironment("testenv", cloud.Config{StartURL: fromDotEnv.URL, StopURL: fromDotEnv.URL, Region: "eu-west-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("SPINLOOP_CLOUD_STOP_URL="+fromDotEnv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "SPINLOOP_CLOUD_STOP_URL")

	if err := cmdCloudStop([]string{"--env", "testenv"}); err != nil {
		t.Fatalf("cmdCloudStop: %v", err)
	}
	select {
	case name := <-hit:
		if name != "env-keyword" {
			t.Errorf("stop reached the %q server, want the ENV override to win", name)
		}
	default:
		t.Error("no server was reached")
	}
}

// The local-only guarantee: ENV and .env values shape the deploying process's
// own environment but never enter the payload sent to the instance.
func TestCloudDeploy_DoesNotForwardEnvToInstance(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`))
	}))
	defer server.Close()
	stubDeploySeams(t, server.URL, "undeployed")

	t.Chdir(t.TempDir())
	spinloopBody := "PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\n" +
		"ENV SPINLOOP_SECRET_TOKEN=do-not-leak\n"
	if err := os.WriteFile("Spinloop", []byte(spinloopBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("SPINLOOP_DOTENV_SECRET=also-no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "SPINLOOP_SECRET_TOKEN", "SPINLOOP_DOTENV_SECRET")

	if err := cmdCloudDeploy([]string{"--env", "testenv"}); err != nil {
		t.Fatalf("cmdCloudDeploy: %v", err)
	}
	for _, leak := range []string{"SPINLOOP_SECRET_TOKEN", "do-not-leak", "SPINLOOP_DOTENV_SECRET", "also-no"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("deploy payload leaked %q to the instance:\n%s", leak, body)
		}
	}
}
