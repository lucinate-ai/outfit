//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// stubEngineDaemon points llamaServerBinary at a long-running script that
// records its argv and exits cleanly on TERM, restoring the original after.
func stubEngineDaemon(t *testing.T, argsFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "llama-server")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ntrap 'exit 0' TERM\nwhile true; do sleep 0.05; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := llamaServerBinary
	llamaServerBinary = script
	t.Cleanup(func() { llamaServerBinary = orig })
}

// apiAddrFromStdout redirects os.Stdout to a file and returns a poller that
// waits for the control API's printed listen address.
func apiAddrFromStdout(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})
	re := regexp.MustCompile(`control API on ([^\s]+)`)
	return func() string {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(path)
			if m := re.FindStringSubmatch(string(data)); m != nil {
				return m[1]
			}
			time.Sleep(20 * time.Millisecond)
		}
		data, _ := os.ReadFile(path)
		t.Fatalf("control API address never printed; stdout so far:\n%s", data)
		return ""
	}
}

// apiDo makes one control-API request and decodes the JSON reply.
func apiDo(t *testing.T, method, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

// waitForFile polls until the stub engine has written path.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// interruptSelf delivers the signal serve's daemon modes shut down on.
func interruptSelf(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
}

func TestCmdServe_PlainServeHasNoMetricsFlag(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "--metrics") {
		t.Errorf("plain serve grew --metrics:\n%s", out)
	}
}

func TestCmdServe_DaemonFlagRemoved(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	captureStderr(t, func() {
		if err := cmdServe([]string{"-d", outfitPath}); err == nil ||
			!strings.Contains(err.Error(), "not defined") {
			t.Errorf("serve -d = %v, want unknown-flag error", err)
		}
	})
}

func TestCmdServe_APIDryRunAddsMetricsFlag(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"-a", "--dry-run", outfitPath}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "--metrics") {
		t.Errorf("api dry run missing --metrics:\n%s", out)
	}
}

func TestCmdDaemon_RefusesTokenlessNonLoopback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "")
	t.Chdir(t.TempDir())
	err := cmdDaemon(nil)
	if err == nil || !strings.Contains(err.Error(), daemon.TokenEnvVar) {
		t.Fatalf("tokenless daemon on %s = %v, want refusal naming %s",
			daemon.DefaultAPIAddr, err, daemon.TokenEnvVar)
	}
}

func TestCmdDaemon_ServesOutfitOnRequestOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineDaemon(t, argsFile)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")
	// The .env beside the Outfit carries the API token — the daemon must load
	// it before reading the token.
	mustWrite(t, filepath.Join(dir, ".env"), "OUTFIT_API_TOKEN=sekrit\n")

	waitAddr := apiAddrFromStdout(t)
	done := make(chan error, 1)
	go func() { done <- cmdDaemon([]string{"--api-addr", "127.0.0.1:0", filepath.Join(dir, "Outfit")}) }()
	base := "http://" + waitAddr()

	// The .env token gates the API.
	if code, _ := apiDo(t, "GET", base+"/v1/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("tokenless status = %d, want 401", code)
	}

	// No auto-start: the daemon idles beside a perfectly good Outfit.
	code, body := apiDo(t, "GET", base+"/v1/status", "sekrit", "")
	if code != 200 || body["state"] != "idle" {
		t.Fatalf("boot status = %d %v, want idle", code, body)
	}

	// A bare start serves the Outfit.
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", ""); code != 200 || body["state"] != "running" ||
		body["runner"] != "llamacpp" {
		t.Fatalf("start = %d %v", code, body)
	}
	if _, body := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); body["logPath"] == nil {
		t.Fatal("status has no logPath")
	}

	// The engine was started with its metrics endpoint on.
	if args := waitForFile(t, argsFile); !strings.Contains(args, "--metrics") {
		t.Errorf("engine argv missing --metrics:\n%s", args)
	}

	// Start while running: 409.
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", ""); code != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("start while running = %d %v", code, body)
	}

	// Stop over the API, idempotently; the daemon itself keeps answering.
	if code, body := apiDo(t, "POST", base+"/v1/stop", "sekrit", ""); code != 200 || body["state"] != "stopped" {
		t.Fatalf("stop = %d %v", code, body)
	}
	if code, body := apiDo(t, "POST", base+"/v1/stop", "sekrit", ""); code != 200 || body["state"] != "stopped" {
		t.Fatalf("second stop = %d %v", code, body)
	}
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", ""); code != 200 || body["state"] != "running" {
		t.Fatalf("restart = %d %v", code, body)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit on SIGINT")
	}
}

func TestCmdDaemon_StartCarriesDeployConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineDaemon(t, argsFile)
	t.Chdir(t.TempDir()) // no Outfit anywhere

	waitAddr := apiAddrFromStdout(t)
	done := make(chan error, 1)
	go func() { done <- cmdDaemon([]string{"--api-addr", "127.0.0.1:0"}) }()
	base := "http://" + waitAddr()

	// No Outfit, nothing pushed: idle, and a bare start says why it cannot.
	if code, body := apiDo(t, "GET", base+"/v1/status", "tok", ""); code != 200 || body["state"] != "idle" {
		t.Fatalf("status = %d %v", code, body)
	}
	if code, body := apiDo(t, "POST", base+"/v1/start", "tok", ""); code != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "nothing to serve") {
		t.Fatalf("idle start = %d %v", code, body)
	}

	// An unservable runner is rejected, on push and on a start body alike.
	if code, body := apiDo(t, "PUT", base+"/v1/deploy-config", "tok",
		`{"runner":"vllm","modelId":"org/model"}`); code != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "vllm") {
		t.Fatalf("vllm push = %d %v", code, body)
	}
	if code, _ := apiDo(t, "POST", base+"/v1/start", "tok",
		`{"runner":"vllm","modelId":"org/model"}`); code != http.StatusBadRequest {
		t.Fatalf("vllm start body = %d, want 400", code)
	}

	// A start carrying its config pushes and starts in one call.
	dc := `{"runner":"llamacpp","modelId":"org/model","quant":"Q4_K_M","contextSize":16384,"servedModelName":"friendly","serveArgs":["--ngl","99"]}`
	if code, body := apiDo(t, "POST", base+"/v1/start", "tok", dc); code != 200 || body["state"] != "running" ||
		body["model"] != "org/model" {
		t.Fatalf("start with body = %d %v", code, body)
	}
	args := waitForFile(t, argsFile)
	for _, want := range []string{"org/model:Q4_K_M", "friendly", "16384", "--ngl", "--metrics"} {
		if !strings.Contains(args, want) {
			t.Errorf("engine argv missing %q:\n%s", want, args)
		}
	}

	// A start body while running is a 409 that stores nothing.
	other := `{"runner":"llamacpp","modelId":"org/other","serveArgs":[]}`
	if code, body := apiDo(t, "POST", base+"/v1/start", "tok", other); code != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("start body while running = %d %v", code, body)
	}
	stored, err := os.ReadFile(filepath.Join(configHome, "outfit", "daemon", "deploy-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "org/other") || !strings.Contains(string(stored), "org/model") {
		t.Errorf("409 start body was stored:\n%s", stored)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit on SIGINT")
	}
}

func TestCmdServe_ForegroundAPIStopExitsServe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineDaemon(t, argsFile)
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	waitAddr := apiAddrFromStdout(t)
	done := make(chan error, 1)
	go func() { done <- cmdServe([]string{"-a", "--api-addr", "127.0.0.1:0", outfitPath}) }()
	base := "http://" + waitAddr()

	code, body := apiDo(t, "GET", base+"/v1/status", "tok", "")
	if code != 200 || body["state"] != "running" {
		t.Fatalf("status = %d %v", code, body)
	}
	// The engine is foreground-managed: start cannot replace it.
	if code, _ := apiDo(t, "POST", base+"/v1/start", "tok", ""); code != http.StatusConflict {
		t.Fatalf("start over foreground = %d, want 409", code)
	}
	// Stop terminates the engine and serve exits cleanly.
	if code, body := apiDo(t, "POST", base+"/v1/stop", "tok", ""); code != 200 || body["state"] != "stopped" {
		t.Fatalf("stop = %d %v", code, body)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve exited with %v after API stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after the foreground engine stopped")
	}
}

func TestCmdServe_ForegroundWithoutAPIListensNowhere(t *testing.T) {
	// A plain foreground serve must not open the control API. The engine
	// exits immediately; if an API listener had started it would outlive it.
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubLlamaServer(t, argsFile)
	captureStdout(t, func() {
		if err := cmdServe([]string{outfitPath}); err != nil {
			t.Error(err)
		}
	})
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1%s/v1/status", daemon.DefaultAPIAddr)); err == nil {
		t.Fatal("plain serve left a control API listening")
	}
}
