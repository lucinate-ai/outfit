//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// stubEngineDaemon points llamaServerBinary at a long-running script that
// records its argv and exits cleanly on TERM, restoring the original after.
func stubEngineDaemon(t *testing.T, argsFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "llama-server")
	// The argv is written to a sibling file and renamed into place. A plain
	// redirection creates and truncates the file before printf writes to it, so
	// a test polling for the file could read it empty; a rename makes it appear
	// complete.
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + ".tmp && mv " + argsFile + ".tmp " + argsFile +
		"\ntrap 'exit 0' TERM\nwhile true; do sleep 0.05; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := llamaServerBinary
	llamaServerBinary = script
	t.Cleanup(func() { llamaServerBinary = orig })
}

// apiAddrFromStderr redirects os.Stderr to a file and returns a poller that
// waits for the control API's listen address to appear in spinloop's log.
//
// It reads stderr, not stdout: the address is a log record now, because the
// daemon is a service and its startup belongs in the log. The logger is built
// from os.Stderr inside the command, which is what lets this redirect capture
// it — see commandLogger.
func apiAddrFromStderr(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		f.Close()
	})
	re := regexp.MustCompile(`api=([^\s]+)`)
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
		t.Fatalf("control API address never logged; stderr so far:\n%s", data)
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

// waitForFile polls until path holds something and returns its contents.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	return waitForFileContaining(t, path)
}

// waitForFileContaining polls until path holds something and every want is in
// it, and returns the contents. A file existing is not the same as its having
// been written: a shell redirection creates and truncates a file before it
// writes, and the daemon opens the engine log before its first line, so a read
// that only waits for the file can land in that gap and see it empty or
// partial. Waiting for the text the caller goes on to assert on removes the
// gap. On timeout it fails with what the file last held.
func waitForFileContaining(t *testing.T, path string, wants ...string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			last = string(data)
			if last != "" && containsAll(last, wants) {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never held %q; it last held:\n%s", path, wants, last)
	return ""
}

func containsAll(s string, wants []string) bool {
	for _, w := range wants {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}

// interruptSelf delivers the signal serve's daemon modes shut down on.
func interruptSelf(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
}

func TestCmdServe_PlainServeHasNoMetricsFlag(t *testing.T) {
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", spinloopPath}); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "--metrics") {
		t.Errorf("plain serve grew --metrics:\n%s", out)
	}
}

func TestCmdServe_DaemonFlagRemoved(t *testing.T) {
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	captureStderr(t, func() {
		if err := cmdServe([]string{"-d", spinloopPath}); err == nil ||
			!strings.Contains(err.Error(), "unknown") {
			t.Errorf("serve -d = %v, want unknown-flag error", err)
		}
	})
}

// TestCmdServe_HasNoLoopbackFlag pins the shorthand's scope: the spec gives
// --loopback to the daemon alone, so serve's API must still keep --api-addr
// rather than growing a second bind source.
func TestCmdServe_HasNoLoopbackFlag(t *testing.T) {
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	captureStderr(t, func() {
		if err := cmdServe([]string{"-a", "--loopback", spinloopPath}); err == nil ||
			!strings.Contains(err.Error(), "unknown") {
			t.Errorf("serve --loopback = %v, want unknown-flag error", err)
		}
		if err := cmdServe([]string{"-a", "-l", spinloopPath}); err == nil ||
			!strings.Contains(err.Error(), "unknown") {
			t.Errorf("serve -l = %v, want unknown-flag error", err)
		}
	})
}

func TestCmdServe_APIDryRunAddsMetricsFlag(t *testing.T) {
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"-a", "--dry-run", spinloopPath}); err != nil {
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

func TestDaemonAPIAddr(t *testing.T) {
	tests := []struct {
		name     string
		apiAddr  string
		explicit bool
		loopback bool
		want     string
		wantErr  bool
	}{
		{name: "neither", apiAddr: daemon.DefaultAPIAddr, want: daemon.DefaultAPIAddr},
		{name: "typed address alone", apiAddr: "10.0.0.5:9999", explicit: true, want: "10.0.0.5:9999"},
		{name: "loopback replaces the default", apiAddr: daemon.DefaultAPIAddr, loopback: true, want: daemon.LoopbackAPIAddr},
		{name: "loopback and a typed address conflict", apiAddr: "10.0.0.5:9999", explicit: true, loopback: true, wantErr: true},
		{name: "loopback and the repeated default conflict", apiAddr: daemon.DefaultAPIAddr, explicit: true, loopback: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := daemonAPIAddr(tt.apiAddr, tt.explicit, tt.loopback)
			if (err != nil) != tt.wantErr {
				t.Fatalf("daemonAPIAddr(%q, %v, %v) error = %v, wantErr %v", tt.apiAddr, tt.explicit, tt.loopback, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("daemonAPIAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCmdDaemon_LoopbackConflictsWithExplicitAddr covers the rule through the
// real flag parsing, so the -l spelling and an --api-addr typed to the
// default's own value are both counted as explicit. The conflict is detected
// by fs.Visit, which is order-independent — the last case types the address
// first, pinning that a sequential rewrite can't make the rule depend on
// flag position.
func TestCmdDaemon_LoopbackConflictsWithExplicitAddr(t *testing.T) {
	for _, args := range [][]string{
		{"--loopback", "--api-addr", "127.0.0.1:0"},
		{"--loopback", "--api-addr", daemon.DefaultAPIAddr},
		{"-l", "--api-addr", daemon.DefaultAPIAddr},
		{"--api-addr", "127.0.0.1:0", "--loopback"},
	} {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv(daemon.TokenEnvVar, "")
		t.Chdir(t.TempDir())
		err := cmdDaemon(args)
		if err == nil || !strings.Contains(err.Error(), "--loopback") || !strings.Contains(err.Error(), "--api-addr") {
			t.Fatalf("cmdDaemon(%v) = %v, want a conflict naming both flags", args, err)
		}
	}
}

// TestCmdDaemon_LoopbackBindsLoopback checks the shorthand end to end: the
// daemon binds daemon.LoopbackAPIAddr and answers unauthenticated, because a
// loopback listen needs no token. The port is fixed, so the test declines
// rather than fights one — a developer in this repo often has a real daemon
// on it, and the rest of the suite never binds a fixed port for the same
// reason.
func TestCmdDaemon_LoopbackBindsLoopback(t *testing.T) {
	probe, err := net.DialTimeout("tcp", daemon.LoopbackAPIAddr, 500*time.Millisecond)
	if err == nil {
		probe.Close()
		t.Skipf("%s is taken", daemon.LoopbackAPIAddr)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "")
	t.Chdir(t.TempDir())

	waitAddr := apiAddrFromStderr(t)
	done := make(chan error, 1)
	go func() { done <- cmdDaemon([]string{"--loopback"}) }()
	addr := waitAddr()
	if addr != daemon.LoopbackAPIAddr {
		t.Fatalf("daemon --loopback bound %s, want %s", addr, daemon.LoopbackAPIAddr)
	}
	// No token was configured anywhere: an unauthenticated status answers.
	if code, body := apiDo(t, "GET", "http://"+addr+"/v1/status", "", ""); code != 200 || body["state"] != "idle" {
		t.Fatalf("unauthenticated status = %d %v, want 200 idle", code, body)
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

// TestCmdDaemon_LogsVersionAtStartup pins the version on the daemon's startup
// record: it is the build-time string the CLI hands the daemon, the same one
// /v1/status reports, so a log line and a status reply cannot name two builds
// of the process that started.
func TestCmdDaemon_LogsVersionAtStartup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	t.Chdir(t.TempDir())

	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdDaemon([]string{"--api-addr", "127.0.0.1:0"}) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if startupRecordHasVersion(string(data), version) {
			break
		}
		time.Sleep(20 * time.Millisecond)
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
	data, _ := os.ReadFile(path)
	if !startupRecordHasVersion(string(data), version) {
		t.Fatalf("startup record never logged its version; stderr so far:\n%s", data)
	}
}

// startupRecordHasVersion reports whether the daemon ready record names the
// version, checked within one line so two records cannot satisfy it together.
func startupRecordHasVersion(stderr, version string) bool {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "daemon ready") && strings.Contains(line, "version="+version) {
			return true
		}
	}
	return false
}

// TestCmdDaemon_LifecycleFromItsAPI covers the daemon as a worker: its token
// comes from a file rather than a Spinloop's .env, an adjacent Spinloop is not a
// source, and everything it runs arrives over the API. What it used to do —
// serve the Spinloop it was started beside — is gone, so a bare start with
// nothing stored says so.
func TestCmdDaemon_LifecycleFromItsAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineDaemon(t, argsFile)

	// A Spinloop sits right here and must be ignored entirely.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Spinloop"), "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")
	t.Chdir(dir)

	tokenFile := filepath.Join(t.TempDir(), "token")
	mustWrite(t, tokenFile, "sekrit\n") // trailing newline must not be part of it

	waitAddr := apiAddrFromStderr(t)
	done := make(chan error, 1)
	go func() {
		done <- cmdDaemon([]string{"--api-addr", "127.0.0.1:0", "--api-token-file", tokenFile})
	}()
	base := "http://" + waitAddr()

	// The file's token gates the API, whitespace trimmed.
	if code, _ := apiDo(t, "GET", base+"/v1/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("tokenless status = %d, want 401", code)
	}
	code, body := apiDo(t, "GET", base+"/v1/status", "sekrit", "")
	if code != 200 || body["state"] != "idle" {
		t.Fatalf("boot status = %d %v, want idle", code, body)
	}

	// The adjacent Spinloop is not a source: a bare start has nothing to serve.
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", ""); code != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "nothing to serve") {
		t.Fatalf("bare start beside a Spinloop = %d %v, want a refusal", code, body)
	}

	// What it runs arrives over the API.
	cfg := `{"runner":"llamacpp","modelId":"org/model","quant":"Q4_K_M","contextSize":4096}`
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", cfg); code != 200 ||
		body["state"] != "running" || body["runner"] != "llamacpp" {
		t.Fatalf("start with a config = %d %v", code, body)
	}
	if _, body := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); body["logPath"] == nil {
		t.Fatal("status has no logPath")
	}

	// The engine was started with its metrics endpoint on.
	if args := waitForFileContaining(t, argsFile, "--metrics"); !strings.Contains(args, "--metrics") {
		t.Errorf("engine argv missing --metrics:\n%s", args)
	}

	// Start while running: 409.
	if code, body := apiDo(t, "POST", base+"/v1/start", "sekrit", ""); code != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("start while running = %d %v", code, body)
	}

	// Stop over the API, idempotently; the daemon itself keeps answering, and
	// a bare restart serves the config it was given.
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
	t.Chdir(t.TempDir()) // no Spinloop anywhere

	waitAddr := apiAddrFromStderr(t)
	done := make(chan error, 1)
	go func() { done <- cmdDaemon([]string{"--api-addr", "127.0.0.1:0"}) }()
	base := "http://" + waitAddr()

	// No Spinloop, nothing pushed: idle, and a bare start says why it cannot.
	if code, body := apiDo(t, "GET", base+"/v1/status", "tok", ""); code != 200 || body["state"] != "idle" {
		t.Fatalf("status = %d %v", code, body)
	}
	if code, body := apiDo(t, "POST", base+"/v1/start", "tok", ""); code != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "nothing to serve") {
		t.Fatalf("idle start = %d %v", code, body)
	}

	// An unservable runner is rejected, on push and on a start body alike.
	if code, body := apiDo(t, "PUT", base+"/v1/deploy-config", "tok",
		`{"runner":"ollama","modelId":"org/model"}`); code != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "ollama") {
		t.Fatalf("ollama push = %d %v", code, body)
	}
	if code, _ := apiDo(t, "POST", base+"/v1/start", "tok",
		`{"runner":"ollama","modelId":"org/model"}`); code != http.StatusBadRequest {
		t.Fatalf("ollama start body = %d, want 400", code)
	}

	// A start carrying its config pushes and starts in one call.
	dc := `{"runner":"llamacpp","modelId":"org/model","quant":"Q4_K_M","contextSize":16384,"servedModelName":"friendly","serveArgs":["--ngl","99"]}`
	if code, body := apiDo(t, "POST", base+"/v1/start", "tok", dc); code != 200 || body["state"] != "running" ||
		body["model"] != "org/model" {
		t.Fatalf("start with body = %d %v", code, body)
	}
	wantArgs := []string{"org/model:Q4_K_M", "friendly", "16384", "--ngl", "--metrics"}
	args := waitForFileContaining(t, argsFile, wantArgs...)
	for _, want := range wantArgs {
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
	stored, err := os.ReadFile(filepath.Join(configHome, "spinloop", "daemon", "deploy-config.json"))
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

// stubEngineView points llamaServerBinary at a script that records its argv,
// writes the named line to its own stdout as it starts, then either runs the
// remainder of body or sleeps until signalled, and restores the binary after.
func stubEngineView(t *testing.T, argsFile, startupLine, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "llama-server")
	src := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\necho " + startupLine + "\n"
	if body != "" {
		src += body + "\n"
	} else {
		src += "trap 'exit 0' TERM\nwhile true; do sleep 0.05; done\n"
	}
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := llamaServerBinary
	llamaServerBinary = script
	t.Cleanup(func() { llamaServerBinary = orig })
}

// fakeTerminal flips the gate serve checks for a terminal, so the view path
// runs in the suite without one.
func fakeTerminal(t *testing.T) {
	t.Helper()
	orig := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = orig })
}

// fakeViewProgram runs the view on a program with injected input and output,
// so the suite executes it without a terminal: input disabled, the key rules
// being the model tests' — and output discarded, the frame being the render
// tests'.
func fakeViewProgram(t *testing.T) {
	t.Helper()
	orig := newServeProgram
	newServeProgram = func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		return tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(io.Discard))
	}
	t.Cleanup(func() { newServeProgram = orig })
}

// TestCmdServe_ViewRunCapturesEngineOutput covers the view run end to end:
// the engine's output lands in the state-dir engine log rather than on serve's
// stdio, the engine starts with its metrics endpoint on, and the command
// serve prints goes to stderr — stdout is the view's screen.
func TestCmdServe_ViewRunCapturesEngineOutput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'engine up'", "sleep 0.3\necho 'engine down'\nexit 0")
	fakeTerminal(t)
	fakeViewProgram(t)
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	done := make(chan error, 1)
	var stderr, stdout string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			go func() { done <- cmdServe([]string{spinloopPath}) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("serve exited with %v after a clean engine exit", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("serve did not exit after the engine did")
			}
		})
	})
	// The printed command goes to stderr, and stays off the view's screen.
	if !strings.Contains(stderr, "llama-server") {
		t.Errorf("the printed command must go to stderr under the view:\n%s", stderr)
	}
	if strings.Contains(stdout, "llama-server") {
		t.Errorf("the printed command must stay off the view's screen:\n%s", stdout)
	}
	stateDir, err := daemon.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	log := waitForFileContaining(t, filepath.Join(stateDir, "engine.log"), "engine up", "engine down")
	for _, want := range []string{"engine up", "engine down"} {
		if !strings.Contains(log, want) {
			t.Errorf("the engine log is missing %q:\n%s", want, log)
		}
	}
	args := waitForFileContaining(t, argsFile, "--metrics")
	if !strings.Contains(args, "--metrics") {
		t.Errorf("the view run must switch the metrics endpoint on:\n%s", args)
	}
}

// TestCmdServe_ViewRunExitsWithEngineStatus covers the exit-status rule: an
// engine that fails on its own takes serve down with it, whatever closed the
// view.
func TestCmdServe_ViewRunExitsWithEngineStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'engine up'", "exit 3")
	fakeTerminal(t)
	fakeViewProgram(t)
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	done := make(chan error, 1)
	captureStdout(t, func() {
		captureStderr(t, func() {
			go func() { done <- cmdServe([]string{spinloopPath}) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("serve must report the engine's failure status")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("serve did not exit after the engine did")
			}
		})
	})
}

// TestCmdServe_ViewQuitStopsTheEngine covers the quit key end to end: q goes
// through the view's own stop — the engine goes down through the supervisor,
// and serve exits cleanly after it is actually stopped. The Spinloop names no
// MODEL, so the supervised run serves under its alias.
func TestCmdServe_ViewQuitStopsTheEngine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'engine up'",
		"trap 'echo \"engine stopped on TERM\"; exit 0' TERM\nwhile true; do sleep 0.05; done\n")
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), samplePreset)
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")

	pr, pw := io.Pipe()
	origProgram := newServeProgram
	newServeProgram = func(m tea.Model, _ ...tea.ProgramOption) *tea.Program {
		return tea.NewProgram(m, tea.WithInput(pr), tea.WithOutput(io.Discard))
	}
	t.Cleanup(func() {
		newServeProgram = origProgram
		pw.Close()
	})
	fakeTerminal(t)

	go func() {
		time.Sleep(2 * time.Second)
		fmt.Fprint(pw, "q")
	}()

	done := make(chan error, 1)
	captureStdout(t, func() {
		captureStderr(t, func() {
			go func() { done <- cmdServe([]string{spinloopPath}) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("serve must exit cleanly after q, got %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("serve did not exit after q")
			}
		})
	})
	stateDir, err := daemon.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	log := waitForFileContaining(t, filepath.Join(stateDir, "engine.log"), "engine stopped on TERM")
	if !strings.Contains(log, "engine stopped on TERM") {
		t.Errorf("the engine must be stopped through the supervisor on q:\n%s", log)
	}
}

// TestCmdServe_DryRunOnTerminalNeverOpensTheView pins the gate's dry-run
// half: on a terminal, --dry-run must still print the command and start
// nothing — no view, no engine, no engine log.
func TestCmdServe_DryRunOnTerminalNeverOpensTheView(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'engine up'", "exit 0")
	fakeTerminal(t)
	viewOpened := false
	origProgram := newServeProgram
	newServeProgram = func(m tea.Model, _ ...tea.ProgramOption) *tea.Program {
		viewOpened = true
		return tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(io.Discard))
	}
	t.Cleanup(func() { newServeProgram = origProgram })

	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	out := captureStdout(t, func() {
		captureStderr(t, func() {
			if err := cmdServe([]string{"--dry-run", spinloopPath}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
		})
	})
	if viewOpened {
		t.Error("--dry-run must not open the view, even on a terminal")
	}
	if !strings.Contains(out, "llama-server") {
		t.Errorf("the dry run must still print the command on stdout:\n%s", out)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Error("the dry run must not start the engine")
	}
	stateDir, err := daemon.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "engine.log")); !os.IsNotExist(err) {
		t.Error("the dry run must not open an engine log")
	}
}

// TestCmdServe_ViewRunMissingBinaryFailsAroundNoView covers the view's
// startup order: the engine starts before the view opens, so a missing
// binary fails with its install hint — and no view, no engine log.
func TestCmdServe_ViewRunMissingBinaryFailsAroundNoView(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	viewOpened := false
	origProgram := newServeProgram
	newServeProgram = func(m tea.Model, _ ...tea.ProgramOption) *tea.Program {
		viewOpened = true
		return tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(io.Discard))
	}
	t.Cleanup(func() { newServeProgram = origProgram })
	origBin := llamaServerBinary
	llamaServerBinary = filepath.Join(t.TempDir(), "no-such-llama-server")
	t.Cleanup(func() { llamaServerBinary = origBin })
	fakeTerminal(t)

	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	err := cmdServe([]string{spinloopPath})
	if err == nil {
		t.Fatal("a missing engine binary must fail the serve")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "install llama.cpp") {
		t.Errorf("the failure must carry the install hint: %v", err)
	}
	if viewOpened {
		t.Error("a missing binary must fail around no view")
	}
	stateDir, serr := daemon.StateDir()
	if serr != nil {
		t.Fatal(serr)
	}
	log, _ := os.ReadFile(filepath.Join(stateDir, "engine.log"))
	if len(log) != 0 {
		t.Errorf("a missing binary must not write an engine log: %q", log)
	}
}

// TestCmdServe_SupervisedRunRefusesAnInsecureListen covers the control API's
// listen guard on the supervised path: a non-loopback address without a token
// is refused before the engine starts — nothing listens, nothing runs.
func TestCmdServe_SupervisedRunRefusesAnInsecureListen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'engine up'", "exit 0")

	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	err := cmdServe([]string{"-a", "--api-addr", "0.0.0.0:0", spinloopPath})
	if err == nil {
		t.Fatal("a non-loopback listen without a token must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to serve the control API on non-loopback") {
		t.Errorf("the refusal must name what it refuses and the fix: %v", err)
	}
	if _, statErr := os.Stat(argsFile); !os.IsNotExist(statErr) {
		t.Error("the engine must not start when the listen is refused")
	}
}

// TestCmdServe_ViewRunAPILogServesTheCapture covers serve --api under the
// view: the control API listens beside the run, and its log endpoint serves
// the captured engine log rather than reporting the log missing.
func TestCmdServe_ViewRunAPILogServesTheCapture(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineView(t, argsFile, "'boot line'", "")
	fakeTerminal(t)
	fakeViewProgram(t)
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	waitAddr := apiAddrFromStderr(t)
	done := make(chan error, 1)
	go func() {
		var serveErr error
		captureStdout(t, func() {
			serveErr = cmdServe([]string{"-a", "--api-addr", "127.0.0.1:0", spinloopPath})
		})
		done <- serveErr
	}()
	base := "http://" + waitAddr()

	// The engine's output reaches the log the API serves, not its stdio.
	deadline := time.Now().Add(10 * time.Second)
	var code int
	var body map[string]any
	for {
		code, body = apiDo(t, "GET", base+"/v1/logs", "tok", "")
		content, _ := body["content"].(string)
		if code == 200 && strings.Contains(content, "boot line") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log endpoint never carried the engine's output: %d %v", code, body)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop over the API; the stop on request exits serve as success.
	if code, body := apiDo(t, "POST", base+"/v1/stop", "tok", ""); code != 200 || body["state"] != "stopped" {
		t.Fatalf("stop = %d %v", code, body)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve exited with %v after an API stop", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not exit after the foreground engine stopped")
	}
}

// TestCmdServe_ForegroundAPIStopExitsServe covers the non-terminal --api run
// against the same shared construction: the engine runs in the foreground
// with stdio forwarded, the API listens beside it, and a stop over the API
// exits serve as success.
// TestCmdServe_OffTerminalCapturesNothing covers the other side of the gate:
// off the terminal the run forwards the engine's output to serve's stdio and
// writes no engine log file — the capture is the view's own.
func TestCmdServe_OffTerminalCapturesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateDir, err := daemon.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	stubLlamaServer(t, filepath.Join(t.TempDir(), "args"))
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	captureStdout(t, func() {
		captureStderr(t, func() {
			if err := cmdServe([]string{spinloopPath}); err != nil {
				t.Error(err)
			}
		})
	})
	if _, err := os.Stat(filepath.Join(stateDir, "engine.log")); !os.IsNotExist(err) {
		t.Errorf("a run off the terminal must write no engine log (stat = %v)", err)
	}
}

func TestCmdServe_ForegroundAPIStopExitsServe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "tok")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubEngineDaemon(t, argsFile)
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nMODEL org/model:Q4_K_M\n")

	waitAddr := apiAddrFromStderr(t)
	done := make(chan error, 1)
	go func() { done <- cmdServe([]string{"-a", "--api-addr", "127.0.0.1:0", spinloopPath}) }()
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

func TestCmdServe_VllmDryRun(t *testing.T) {
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath,
		"PROVIDER vllm\nMODEL org/model\nALIAS friendly\nCONTEXT 16k\nBASEURL http://0.0.0.0:8000/v1\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", spinloopPath}); err != nil {
			t.Error(err)
		}
	})
	// The model rides positionally after `serve`, before any flags.
	if !strings.Contains(out, "vllm serve org/model") {
		t.Errorf("model is not vllm serve's positional argument:\n%s", out)
	}
	for _, want := range []string{"--served-model-name friendly", "--max-model-len 16000",
		"--host 0.0.0.0", "--port 8000"} {
		if !strings.Contains(out, want) {
			t.Errorf("vllm argv missing %q:\n%s", want, out)
		}
	}
}

// TestCmdServe_VllmParallelDoesNotScaleContext checks vLLM's PARALLEL maps to
// --max-num-seqs, a concurrency cap independent of context: unlike
// llama.cpp, --max-model-len (from CONTEXT) is never scaled by it.
func TestCmdServe_VllmParallelDoesNotScaleContext(t *testing.T) {
	dir := t.TempDir()
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER vllm\nMODEL org/model\nCONTEXT 128k\nPARALLEL 4\n")
	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", spinloopPath}); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"--max-model-len 128000", "--max-num-seqs 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("vllm argv missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--max-model-len 512000") {
		t.Errorf("vllm's context must not be scaled by PARALLEL:\n%s", out)
	}
}

func TestArgvFromDeployConfigVllm(t *testing.T) {
	engine, err := engineFor("vllm")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := argvFromDeployConfig(engine, inference.DeployConfig{
		Runner:          "vllm",
		ModelID:         "/opt/llm/model",
		ContextSize:     32768,
		ServedModelName: "friendly",
		ServeArgs:       []string{"--gpu-memory-utilization", "0.92"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	if !strings.HasPrefix(got, "vllm serve /opt/llm/model ") {
		t.Errorf("model is not the positional argument: %s", got)
	}
	for _, want := range []string{"--served-model-name friendly", "--max-model-len 32768",
		"--gpu-memory-utilization 0.92"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q: %s", want, got)
		}
	}
}

// TestArgvFromDeployConfigLlamacppScalesContext checks a pushed deploy config
// (the daemon/fleet path) scales ctx-size by parallel exactly as a local
// `spinloop serve` would — the whole point of putting this math in the shared
// *ServeParams functions.
func TestArgvFromDeployConfigLlamacppScalesContext(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := argvFromDeployConfig(engine, inference.DeployConfig{
		Runner:      "llamacpp",
		ModelID:     "/opt/llm/model.gguf",
		ContextSize: 128000,
		Parallel:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{"--ctx-size 256000", "--parallel 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q: %s", want, got)
		}
	}
}

// TestArgvFromDeployConfigParallelWithoutContext covers a state only the fleet
// path can reach: waking a node does not require a context size (the engine's
// own default stands), so a config may carry a slot count and no context at
// all. The slot count must still be applied, and nothing may invent a
// ctx-size out of the zero — a scaled `0 * n` would cap the engine at nothing.
func TestArgvFromDeployConfigParallelWithoutContext(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := argvFromDeployConfig(engine, inference.DeployConfig{
		Runner:   "llamacpp",
		ModelID:  "/opt/llm/model.gguf",
		Parallel: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	if !strings.Contains(got, "--parallel 2") {
		t.Errorf("argv missing --parallel 2: %s", got)
	}
	if strings.Contains(got, "--ctx-size") {
		t.Errorf("no stored context size should mean no ctx-size flag at all: %s", got)
	}
}

// TestArgvFromDeployConfigVllmParallel checks vLLM's PARALLEL maps to
// --max-num-seqs with no effect on --max-model-len, from a pushed deploy
// config just as from a local Spinloop.
func TestArgvFromDeployConfigVllmParallel(t *testing.T) {
	engine, err := engineFor("vllm")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := argvFromDeployConfig(engine, inference.DeployConfig{
		Runner:      "vllm",
		ModelID:     "/opt/llm/model",
		ContextSize: 32768,
		Parallel:    4,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{"--max-model-len 32768", "--max-num-seqs 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q: %s", want, got)
		}
	}
}

func TestScrapeTargetForReadsAPIKeyFile(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "api-key")
	mustWrite(t, keyFile, "sk-from-file\n")
	target := scrapeTargetFor(engine, "", []string{"llama-server", "--api-key-file", keyFile})
	if target.APIKey != "sk-from-file" {
		t.Errorf("APIKey = %q, want the file's trimmed contents", target.APIKey)
	}
	if target.BaseURL != engine.defaultBaseURL || target.Engine != "llamacpp" {
		t.Errorf("target = %+v", target)
	}
	// A literal --api-key still wins the usual way.
	target = scrapeTargetFor(engine, "http://127.0.0.1:9000/v1", []string{"--api-key", "sk-lit"})
	if target.APIKey != "sk-lit" || target.BaseURL != "http://127.0.0.1:9000/v1" {
		t.Errorf("target = %+v", target)
	}
}

func TestCmdServe_ForegroundWithoutAPIListensNowhere(t *testing.T) {
	// This probes a fixed port, so it can only tell "serve opened a listener"
	// from "serve did not" when that port starts out free. A developer running
	// `spinloop daemon` on this machine — which examples/fleet-local encourages
	// — would otherwise see this fail as though serve had leaked a listener.
	probe := fmt.Sprintf("127.0.0.1:%d", daemon.DefaultAPIPort)
	requireFreePort(t, probe)

	// A plain foreground serve must not open the control API. The engine
	// exits immediately; if an API listener had started it would outlive it.
	spinloopPath := writePresetSpinloop(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	argsFile := filepath.Join(t.TempDir(), "args")
	stubLlamaServer(t, argsFile)
	captureStdout(t, func() {
		if err := cmdServe([]string{spinloopPath}); err != nil {
			t.Error(err)
		}
	})
	if _, err := http.Get("http://" + probe + "/v1/status"); err == nil {
		t.Fatal("plain serve left a control API listening")
	}
}

// requireFreePort skips the test when something is already listening on addr,
// naming what it found so the skip is not mistaken for a passing assertion.
// It must be given the same host:port the assertion probes: binding the
// wildcard address can succeed while a specific one is taken, which would let
// the guard pass and the assertion still fail.
func requireFreePort(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("something is already listening on %s (%v): "+
			"this test can only prove serve opened no listener when the port starts free", addr, err)
	}
	ln.Close()
}

// TestScrapeTargetForHonoursTheEngineBind pins the regression that left every
// cloud llama.cpp deployment without token stats: the engine was started with
// the deploy config's --port 8000 while the scraper used llama.cpp's built-in
// 8080, so every scrape was refused and the activity record never moved.
func TestScrapeTargetForHonoursTheEngineBind(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}

	// The cloud's shape: a deploy config, so no Spinloop BASEURL, and the bind
	// stated on the command line.
	cloud := []string{"llama-server", "--host", "0.0.0.0", "--port", "8000", "--metrics"}
	target := scrapeTargetFor(engine, "", cloud)
	if target.BaseURL != "http://127.0.0.1:8000" {
		t.Errorf("BaseURL = %q, want the engine's actual bind on loopback", target.BaseURL)
	}
	// A wildcard bind names every interface; it is not something to dial.
	if strings.Contains(target.BaseURL, "0.0.0.0") {
		t.Errorf("BaseURL = %q, want the wildcard rewritten to loopback", target.BaseURL)
	}

	// The bind wins over a Spinloop BASEURL too: the scrape is local, and the
	// port the process bound is the truth about where to find it.
	target = scrapeTargetFor(engine, "http://example.com:9999/v1", cloud)
	if target.BaseURL != "http://127.0.0.1:8000" {
		t.Errorf("BaseURL = %q, want the engine's bind to win over BASEURL", target.BaseURL)
	}

	// With no bind stated, the previous precedence is untouched.
	if got := scrapeTargetFor(engine, "http://127.0.0.1:9000/v1", []string{"llama-server"}); got.BaseURL != "http://127.0.0.1:9000/v1" {
		t.Errorf("BaseURL = %q, want the Spinloop's BASEURL when no bind is given", got.BaseURL)
	}
	if got := scrapeTargetFor(engine, "", []string{"llama-server"}); got.BaseURL != engine.defaultBaseURL {
		t.Errorf("BaseURL = %q, want the engine default when nothing else says", got.BaseURL)
	}

	// A port alone is enough — host defaults to loopback.
	if got := scrapeTargetFor(engine, "", []string{"llama-server", "--port", "1234"}); got.BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("BaseURL = %q, want loopback on the stated port", got.BaseURL)
	}
	// A host alone keeps the engine's own default port out of it entirely.
	if got := scrapeTargetFor(engine, "", []string{"llama-server", "--host", "127.0.0.5"}); got.BaseURL != "http://127.0.0.5" {
		t.Errorf("BaseURL = %q, want the stated host", got.BaseURL)
	}

	// vLLM takes the same flags and must behave the same way.
	vllm, err := engineFor("vllm")
	if err != nil {
		t.Fatal(err)
	}
	if got := scrapeTargetFor(vllm, "", []string{"vllm", "--host", "0.0.0.0", "--port", "7000"}); got.BaseURL != "http://127.0.0.1:7000" {
		t.Errorf("vLLM BaseURL = %q, want its stated bind", got.BaseURL)
	}
}

// TestScrapeTargetForDialectlessEngineStillResolvesAnAddress covers mtplx: it
// has no /metrics to scrape, so the target's dialect is empty, but the address
// is still resolved from the bind, BASEURL, or default — the readiness check
// needs that address to probe /health even where there is nothing to scrape.
func TestScrapeTargetForDialectlessEngineStillResolvesAnAddress(t *testing.T) {
	engine, err := engineFor("mtplx")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		baseURL string
		argv    []string
		want    string
	}{
		{"stated bind wins", "", []string{"mtplx", "serve", "--host", "0.0.0.0", "--port", "9100"}, "http://127.0.0.1:9100"},
		{"BASEURL when no bind", "http://127.0.0.1:9000/v1", []string{"mtplx", "serve"}, "http://127.0.0.1:9000/v1"},
		{"engine default when nothing else", "", []string{"mtplx", "serve"}, "http://127.0.0.1:8000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scrapeTargetFor(engine, tc.baseURL, tc.argv)
			if got.BaseURL != tc.want {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, tc.want)
			}
			if got.Engine != "" {
				t.Errorf("Engine = %q, want empty (mtplx has no metrics dialect)", got.Engine)
			}
		})
	}
}

// The endpoint a node advertises to a router is derived from the same command
// line the metrics scrape reads, so the two cannot disagree about one engine.
func TestEngineEndpointFor(t *testing.T) {
	llama, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	vllm, err := engineFor("vllm")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		engine  serveEngine
		baseURL string
		argv    []string
		want    *daemon.EngineEndpoint
	}{
		{
			name:   "the engine's own bind wins",
			engine: llama,
			argv:   []string{"llama-server", "--host", "0.0.0.0", "--port", "9001"},
			want:   &daemon.EngineEndpoint{Port: 9001},
		},
		{
			name:    "the Spinloop's base url when the command says nothing",
			engine:  llama,
			baseURL: "http://127.0.0.1:9000/v1",
			argv:    []string{"llama-server"},
			want:    &daemon.EngineEndpoint{Port: 9000, LoopbackOnly: true},
		},
		{
			name:   "llama.cpp binds loopback by default",
			engine: llama,
			argv:   []string{"llama-server"},
			want:   &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true},
		},
		{
			name:   "vLLM binds every interface by default",
			engine: vllm,
			argv:   []string{"vllm", "serve", "m"},
			want:   &daemon.EngineEndpoint{Port: 8000},
		},
		{
			name:   "a gated engine says so and nothing more",
			engine: llama,
			argv:   []string{"llama-server", "--api-key", "sk-secret", "--port", "8080"},
			want:   &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true, RequiresKey: true},
		},
		{
			name:    "a real path prefix is reported, /v1 is not",
			engine:  llama,
			baseURL: "http://127.0.0.1:9000/openai",
			argv:    []string{"llama-server"},
			want:    &daemon.EngineEndpoint{Port: 9000, Path: "/openai", LoopbackOnly: true},
		},
		{
			name:   "no port anywhere yields no endpoint",
			engine: serveEngine{},
			argv:   []string{"omlx-cli", "serve"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := engineEndpointFor(tc.engine, tc.baseURL, tc.argv)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("endpoint = %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatal("no endpoint derived")
			}
			if *got != *tc.want {
				t.Errorf("endpoint = %+v, want %+v", *got, *tc.want)
			}
		})
	}
}

// The reported port is the engine's, never the control API's — nothing in the
// reply implies one from the other.
func TestEngineEndpointIsNotTheAPIPort(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	ep := engineEndpointFor(engine, "", []string{"llama-server", "--port", "8080"})
	if ep == nil {
		t.Fatal("no endpoint derived")
	}
	if ep.Port == daemon.DefaultAPIPort {
		t.Errorf("engine port %d is the control API's default port", ep.Port)
	}
}

// The key reaches the scrape, which needs it, and never the endpoint, which
// does not.
func TestEngineEndpointNeverCarriesTheKey(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "api-key")
	mustWrite(t, keyFile, "sk-from-file\n")
	argv := []string{"llama-server", "--api-key-file", keyFile}

	if target := scrapeTargetFor(engine, "", argv); target.APIKey != "sk-from-file" {
		t.Errorf("the scrape lost the key it needs: %+v", target)
	}
	ep := engineEndpointFor(engine, "", argv)
	if ep == nil || !ep.RequiresKey {
		t.Fatalf("endpoint should report a key is required: %+v", ep)
	}
	if encoded, err := json.Marshal(ep); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(encoded), "sk-from-file") {
		t.Errorf("endpoint leaked the key: %s", encoded)
	}
}

// A fleet node is a machine the operator owns, so the preset's bind is theirs
// to choose. The cloud assigns its own, and dropping it there is right; doing
// the same for a node left every woken engine on llama.cpp's loopback default
// however the preset was written, so no other machine in the fleet could reach
// it.
func TestNodeDeployConfigKeepsThePresetBind(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), `
[*]
host  = 0.0.0.0
port  = 9090
ngl   = 99

[qwen]
hf       = org/model:Q4_K_M
ctx-size = 4096
`)
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER llamacpp\nALIAS qwen\nPRESET ./preset.ini\n")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}

	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(node.ServeArgs, " ")
	for _, want := range []string{"--host 0.0.0.0", "--port 9090"} {
		if !strings.Contains(args, want) {
			t.Errorf("a node's serve args should keep %q, got: %s", want, args)
		}
	}
	// Everything the daemon supplies from the config itself still goes, so the
	// engine is not told twice.
	for _, unwanted := range []string{"--hf-repo", "--ctx-size", "--alias"} {
		if strings.Contains(args, unwanted) {
			t.Errorf("a node's serve args should not repeat %q, got: %s", unwanted, args)
		}
	}

	// The cloud assigns its own bind, so it is still dropped there.
	sel.Context = "4096" // the cloud requires one
	cloud, err := deployConfigFor(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	cloudArgs := strings.Join(cloud.ServeArgs, " ")
	for _, unwanted := range []string{"--host", "--port"} {
		if strings.Contains(cloudArgs, unwanted) {
			t.Errorf("the cloud sets its own bind, so %q should be dropped, got: %s", unwanted, cloudArgs)
		}
	}
}

// The Spinloop's own BASEURL travels where the preset's bind does: a woken
// node with no preset must not land on the engine's default bind, which for
// llama.cpp is loopback — reachable from nobody but the node itself.
func TestNodeDeployConfigCarriesTheSpinloopBind(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER llamacpp\nMODEL org/model:Q4_K_M\nCONTEXT 4096\nBASEURL http://0.0.0.0:8080/v1\n", "")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}

	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(node.ServeArgs, " ")
	for _, want := range []string{"--host 0.0.0.0", "--port 8080"} {
		if !strings.Contains(args, want) {
			t.Errorf("a node's serve args should carry the Spinloop's bind %q, got: %s", want, args)
		}
	}

	// The cloud assigns its own bind, so the Spinloop's is not carried there.
	cloud, err := deployConfigFor(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	cloudArgs := strings.Join(cloud.ServeArgs, " ")
	for _, unwanted := range []string{"--host", "--port"} {
		if strings.Contains(cloudArgs, unwanted) {
			t.Errorf("the cloud sets its own bind, so %q should not be carried, got: %s", unwanted, cloudArgs)
		}
	}
}

// Where the Spinloop states a bind and the preset states one too, the
// Spinloop's wins — the same precedence a local serve gives its own BASEURL
// over the preset — and the engine is told once, not twice.
func TestNodeDeployConfigBindBeatsThePreset(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER llamacpp\nALIAS qwen\nPRESET ./preset.ini\nBASEURL http://0.0.0.0:8080/v1\n",
		`[*]
host = 127.0.0.1
port = 9090

[qwen]
hf       = org/model:Q4_K_M
ctx-size = 4096
`)
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}

	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(node.ServeArgs, " ")
	for _, want := range []string{"--host 0.0.0.0", "--port 8080"} {
		if !strings.Contains(args, want) {
			t.Errorf("the Spinloop's bind should win, got: %s", args)
		}
	}
	for _, unwanted := range []string{"127.0.0.1", "9090"} {
		if strings.Contains(args, unwanted) {
			t.Errorf("the preset's bind should be overridden, got: %s", args)
		}
	}
}

// mtplxNodePreset is an MTPLX-vocabulary preset: long-form keys, the model under
// `model`, the window under `context-window`, the cap under
// `max-active-requests`, a served name under `model-id`, and a scheduling mode
// the engine — not spinloop — owns.
const mtplxNodePreset = `
[*]
host = 0.0.0.0
port = 8000

[qwen]
model               = Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed
context-window      = 32768
scheduler-mode      = ar_batch
max-active-requests = 4
model-id            = preset-alias
`

// A fleet node runs mtplx, so the node path accepts it and reads the model,
// window, and slot count back out of an MTPLX preset — each in its own
// vocabulary — while the Spinloop's ALIAS supplies the served name and the
// operator's own settings (the bind, the scheduling mode) survive.
func TestNodeDeployConfigMtplxFromPreset(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), mtplxNodePreset)
	spinloopPath := filepath.Join(dir, "Spinloop")
	mustWrite(t, spinloopPath, "PROVIDER mtplx\nALIAS qwen\nPRESET ./preset.ini\n")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}

	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	if node.Runner != "mtplx" {
		t.Errorf("runner = %q, want mtplx", node.Runner)
	}
	if node.ModelID != "Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed" || node.Quant != "" {
		t.Errorf("model = %q quant = %q, want the preset's model with no quant", node.ModelID, node.Quant)
	}
	if node.ContextSize != 32768 {
		t.Errorf("contextSize = %d, want the preset's context-window", node.ContextSize)
	}
	if node.Parallel != 4 {
		t.Errorf("parallel = %d, want the preset's max-active-requests", node.Parallel)
	}
	// The served name comes from the Spinloop's ALIAS, not the preset's model-id.
	if node.ServedModelName != "qwen" {
		t.Errorf("servedModelName = %q, want the ALIAS, not the preset's model-id", node.ServedModelName)
	}

	args := strings.Join(node.ServeArgs, " ")
	// The daemon supplies these from the config, so the preset's raw values go.
	for _, unwanted := range []string{"--model ", "--model-id", "--context-window", "--max-active-requests"} {
		if strings.Contains(args, unwanted) {
			t.Errorf("a node's serve args should not repeat %q, got: %s", unwanted, args)
		}
	}
	// The operator's own settings survive: the bind, and the scheduling mode,
	// which spinloop does not compute.
	for _, want := range []string{"--host 0.0.0.0", "--port 8000", "--scheduler-mode ar_batch"} {
		if !strings.Contains(args, want) {
			t.Errorf("a node's serve args should keep %q, got: %s", want, args)
		}
	}
}

// A node wake needs no preset: the Spinloop's own MODEL, CONTEXT, and PARALLEL
// drive the config, exactly as they do a local `serve`.
func TestNodeDeployConfigMtplxWithoutPreset(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER mtplx\nMODEL Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed\nALIAS qwen\nCONTEXT 128k\nPARALLEL 2\n", "")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	if node.Runner != "mtplx" || node.ModelID != "Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed" {
		t.Errorf("runner = %q model = %q, want mtplx and the stated model", node.Runner, node.ModelID)
	}
	if node.ContextSize != 128000 {
		t.Errorf("contextSize = %d, want the stated 128k", node.ContextSize)
	}
	if node.Parallel != 2 {
		t.Errorf("parallel = %d, want the stated 2", node.Parallel)
	}
}

// A node has the file its Spinloop points at, so a local model path is carried
// as the model to load rather than refused.
func TestNodeDeployConfigMtplxLocalModelPath(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER mtplx\nMODEL ./models/qwen-mtplx\nCONTEXT 32k\n", "")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatalf("a node wake with a local model path should succeed: %v", err)
	}
	if node.ModelID != "./models/qwen-mtplx" {
		t.Errorf("modelId = %q, want the local path carried as the model", node.ModelID)
	}
}

// Moving the local-path check to the weights-seeding target unblocks llamacpp
// (and vllm) node wakes with local weights too, which the old unconditional
// check blocked.
func TestNodeDeployConfigLlamacppLocalModelPathNowSucceeds(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER llamacpp\nMODEL ./models/qwen.gguf\nCONTEXT 32k\n", "")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	node, err := deployConfigForNode(sel, spinloopPath)
	if err != nil {
		t.Fatalf("a node wake with a local model path should succeed: %v", err)
	}
	if node.ModelID != "./models/qwen.gguf" {
		t.Errorf("modelId = %q, want the local path", node.ModelID)
	}

	// The cloud still refuses the same file: it downloads from Hugging Face.
	sel.Context = "32k"
	if _, err := deployConfigFor(sel, spinloopPath); err == nil ||
		!strings.Contains(err.Error(), "cannot deploy the local model file") {
		t.Errorf("the cloud should still refuse a local model file, got %v", err)
	}
}

// MTPLX is Apple-Silicon-only and has no machine image, so it never becomes a
// cloud runner: the cloud path refuses it with its existing message.
func TestDeployConfigFor_CloudStillRefusesMtplx(t *testing.T) {
	spinloopPath := writeDeploySpinloop(t,
		"PROVIDER mtplx\nMODEL Youssofal/Qwen3.8-27B\nCONTEXT 32k\n", "")
	sel, _, err := readSpinloop("test", spinloopPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deployConfigFor(sel, spinloopPath); err == nil ||
		!strings.Contains(err.Error(), "cannot be deployed") {
		t.Errorf("the cloud should refuse mtplx, got %v", err)
	}
}

// The bind reaching the engine is only half of it: the daemon must then report
// that engine as reachable, or routing refuses a node that is in fact fine.
func TestNodeBindReachesTheReportedEndpoint(t *testing.T) {
	engine, err := engineFor("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"llama-server", "--hf-repo", "org/model", "--host", "0.0.0.0", "--port", "9090"}

	ep := engineEndpointFor(engine, "", argv)
	if ep == nil {
		t.Fatal("no endpoint derived")
	}
	if ep.Port != 9090 {
		t.Errorf("reported port = %d, want the preset's 9090", ep.Port)
	}
	if ep.LoopbackOnly {
		t.Error("an engine bound to 0.0.0.0 was reported as loopback-only, so routing would refuse a reachable node")
	}
	// And the scrape follows the same bind, so the two cannot disagree.
	if target := scrapeTargetFor(engine, "", argv); !strings.Contains(target.BaseURL, "9090") {
		t.Errorf("scrape target = %q, want the engine's own port", target.BaseURL)
	}
}

// The daemon reads no Spinloop, so its token no longer arrives from an adjacent
// `.env`. Three sources replace it, and two at once is a conflict rather than
// a silent precedence.
func TestDaemonToken(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	mustWrite(t, file, "  from-file\n")

	t.Setenv(daemon.TokenEnvVar, "from-env")
	if got, err := daemonToken("", file); err != nil || got != "from-file" {
		t.Errorf("file = %q, %v; want the trimmed file contents", got, err)
	}
	if got, err := daemonToken("from-flag", ""); err != nil || got != "from-flag" {
		t.Errorf("flag = %q, %v", got, err)
	}
	if got, err := daemonToken("", ""); err != nil || got != "from-env" {
		t.Errorf("env = %q, %v", got, err)
	}

	t.Setenv(daemon.TokenEnvVar, "")
	if got, err := daemonToken("", ""); err != nil || got != "" {
		t.Errorf("nothing = %q, %v; want empty, which loopback permits", got, err)
	}

	// A conflict names both rather than choosing.
	_, err := daemonToken("from-flag", file)
	if err == nil {
		t.Fatal("two sources should be a conflict")
	}
	for _, want := range []string{"--api-token", "--api-token-file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}

	// An unreadable or empty file fails rather than listening with no token.
	if _, err := daemonToken("", filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing token file should fail")
	}
	empty := filepath.Join(dir, "empty")
	mustWrite(t, empty, "\n")
	if _, err := daemonToken("", empty); err == nil {
		t.Error("an empty token file should fail")
	}
}

// The engine's key reaches it by path or not at all. An engine with no
// key-file option is refused, because the alternative is a literal argument
// every local user can read.
func TestEngineKeyArgs(t *testing.T) {
	args, err := engineKeyArgs(&inference.DeployConfig{Runner: "llamacpp"}, "/state/key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "--api-key-file /state/key" {
		t.Errorf("args = %v", args)
	}

	if _, err := engineKeyArgs(&inference.DeployConfig{Runner: "vllm"}, "/state/key"); err == nil {
		t.Error("an engine with no key-file option should be refused")
	} else if !strings.Contains(err.Error(), "command line") {
		t.Errorf("the refusal should say why, got: %v", err)
	}
}
