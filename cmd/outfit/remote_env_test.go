package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// unsetEnvOnCleanup removes keys that applyOutfitEnv sets via os.Setenv (which,
// unlike t.Setenv, are not auto-restored) once the test finishes.
func unsetEnvOnCleanup(t *testing.T, keys ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, k := range keys {
			os.Unsetenv(k)
		}
	})
}

// applyOutfitEnv establishes the local environment with precedence
// ENV > process environment > .env.
func TestApplyOutfitEnv_Precedence(t *testing.T) {
	dir := t.TempDir()
	dotenv := "OUTFIT_TEST_GAP=from-dotenv\n" +
		"OUTFIT_TEST_SHELL=from-dotenv\n" +
		"OUTFIT_TEST_ENVKW=from-dotenv\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}
	// The shell already exports two of them; the .env must not shadow those.
	t.Setenv("OUTFIT_TEST_SHELL", "from-shell")
	t.Setenv("OUTFIT_TEST_ENVKW", "from-shell")
	unsetEnvOnCleanup(t, "OUTFIT_TEST_GAP") // applyOutfitEnv sets this from the .env

	sel := outfit.Selection{Env: []outfit.EnvVar{
		{Key: "OUTFIT_TEST_ENVKW", Value: "from-env-keyword"},
	}}
	if err := applyOutfitEnv(sel, filepath.Join(dir, "Outfit")); err != nil {
		t.Fatalf("applyOutfitEnv: %v", err)
	}

	want := map[string]string{
		"OUTFIT_TEST_GAP":   "from-dotenv",      // .env fills a gap
		"OUTFIT_TEST_SHELL": "from-shell",       // process environment wins over .env
		"OUTFIT_TEST_ENVKW": "from-env-keyword", // ENV overrides both
	}
	for k, w := range want {
		if got := os.Getenv(k); got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}

// A .env that cannot be read (here, a directory in its place) surfaces the
// error rather than proceeding on a partial environment.
func TestApplyOutfitEnv_ReadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := applyOutfitEnv(outfit.Selection{}, filepath.Join(dir, "Outfit")); err == nil {
		t.Error("applyOutfitEnv should surface a .env read error")
	}
}

// A URL-sourced Outfit has no local directory to look beside, so its .env
// read is skipped entirely rather than attempted against a mangled path.
func TestApplyOutfitEnv_URLSourceSkipsEnvFile(t *testing.T) {
	sel := outfit.Selection{Env: []outfit.EnvVar{
		{Key: "OUTFIT_TEST_URL_ENVKW", Value: "from-env-keyword"},
	}}
	unsetEnvOnCleanup(t, "OUTFIT_TEST_URL_ENVKW")
	if err := applyOutfitEnv(sel, "https://example.com/team/Outfit"); err != nil {
		t.Fatalf("applyOutfitEnv: %v", err)
	}
	if got := os.Getenv("OUTFIT_TEST_URL_ENVKW"); got != "from-env-keyword" {
		t.Errorf("OUTFIT_TEST_URL_ENVKW = %q, want %q (ENV instructions still apply)", got, "from-env-keyword")
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

// A .env beside the Outfit reaches a control command: its OUTFIT_REMOTE_STOP_URL
// override wins over the remote.json value, so the .env server is the one hit.
func TestRemoteStop_RespectsDotEnv(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	hit := make(chan string, 1)
	fromConfig := httptest.NewServer(hitRecorder("remote.json", hit))
	defer fromConfig.Close()
	fromDotEnv := httptest.NewServer(hitRecorder("dotenv", hit))
	defer fromDotEnv.Close()

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER openai-compatible\nREMOTE remote.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(remote.Config{StartURL: fromConfig.URL, StopURL: fromConfig.URL, Region: "eu-west-1"})
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("OUTFIT_REMOTE_STOP_URL="+fromDotEnv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "OUTFIT_REMOTE_STOP_URL")

	// An explicit Outfit path, exercising resolveRemoteConfig's explicit-arg branch.
	if err := cmdRemoteStop([]string{"Outfit"}); err != nil {
		t.Fatalf("cmdRemoteStop: %v", err)
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
// OUTFIT_REMOTE_STOP_URL, the ENV server is the one hit.
func TestRemoteStop_EnvKeywordOverridesDotEnv(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	hit := make(chan string, 1)
	fromDotEnv := httptest.NewServer(hitRecorder("dotenv", hit))
	defer fromDotEnv.Close()
	fromEnvKw := httptest.NewServer(hitRecorder("env-keyword", hit))
	defer fromEnvKw.Close()

	t.Chdir(t.TempDir())
	outfitBody := "PROVIDER openai-compatible\nREMOTE remote.json\n" +
		"ENV OUTFIT_REMOTE_STOP_URL=" + fromEnvKw.URL + "\n"
	if err := os.WriteFile("Outfit", []byte(outfitBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// remote.json still supplies the required StartURL; StopURL is overridden.
	cfg, _ := json.Marshal(remote.Config{StartURL: fromDotEnv.URL, StopURL: fromDotEnv.URL, Region: "eu-west-1"})
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("OUTFIT_REMOTE_STOP_URL="+fromDotEnv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "OUTFIT_REMOTE_STOP_URL")

	if err := cmdRemoteStop(nil); err != nil {
		t.Fatalf("cmdRemoteStop: %v", err)
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
func TestRemoteDeploy_DoesNotForwardEnvToInstance(t *testing.T) {
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
	outfitBody := "PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\nREMOTE testenv\n" +
		"ENV OUTFIT_SECRET_TOKEN=do-not-leak\n"
	if err := os.WriteFile("Outfit", []byte(outfitBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("OUTFIT_DOTENV_SECRET=also-no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvOnCleanup(t, "OUTFIT_SECRET_TOKEN", "OUTFIT_DOTENV_SECRET")

	if err := cmdRemoteDeploy(nil); err != nil {
		t.Fatalf("cmdRemoteDeploy: %v", err)
	}
	for _, leak := range []string{"OUTFIT_SECRET_TOKEN", "do-not-leak", "OUTFIT_DOTENV_SECRET", "also-no"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("deploy payload leaked %q to the instance:\n%s", leak, body)
		}
	}
}

// An Outfit named explicitly but carrying no REMOTE is an error naming the
// file, not a silent fall back to the per-user default: naming a path says
// which endpoint you meant, so guessing a different one would be wrong.
func TestRemoteStop_NamedOutfitWithoutREMOTE(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit", []byte("PROVIDER llamacpp\nMODEL gemma\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := cmdRemoteStop([]string{"Outfit"})
	if err == nil {
		t.Fatal("expected an error for an Outfit with no REMOTE")
	}
	if !strings.Contains(err.Error(), "has no REMOTE instruction") {
		t.Errorf("error = %q, want it to name the missing REMOTE", err)
	}
}
