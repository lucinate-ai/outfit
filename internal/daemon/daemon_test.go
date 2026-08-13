//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// stubEngine writes an executable shell script and returns its path.
func stubEngine(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitForState polls until the supervisor reaches want or the deadline hits.
func waitForState(t *testing.T, s *Supervisor, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := s.Status(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _, _ := s.Status()
	t.Fatalf("state = %s, want %s", got, want)
}

func TestSupervisorLifecycle(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "engine.log")
	s := NewSupervisor(logPath)
	engine := stubEngine(t, `echo booted
trap 'exit 0' TERM
while true; do sleep 0.05; done`)

	if state, _, _ := s.Status(); state != StateIdle {
		t.Fatalf("initial state = %s, want idle", state)
	}
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	if state, name, _ := s.Status(); state != StateRunning || name != engine {
		t.Fatalf("after start: state=%s name=%s", state, name)
	}

	// Wait for the engine to have actually booted (and installed its TERM
	// trap) before poking it further.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, _ := os.ReadFile(logPath); strings.Contains(string(data), "booted") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine never wrote its boot line")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// One engine per supervisor: a second start fails naming the first.
	err := s.Start([]string{engine})
	if err == nil || !strings.Contains(err.Error(), engine) {
		t.Fatalf("second start error = %v, want one naming %s", err, engine)
	}

	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := s.Status(); state != StateStopped {
		t.Fatalf("after stop: state = %s, want stopped", state)
	}
	// Stop with nothing running is a no-op.
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "booted") {
		t.Errorf("log file missing engine output: %q", data)
	}
}

func TestSupervisorCrashIsReportedNotRestarted(t *testing.T) {
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	engine := stubEngine(t, "exit 3")
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateCrashed)
	// Stay crashed: nothing restarts it.
	time.Sleep(50 * time.Millisecond)
	if state, _, _ := s.Status(); state != StateCrashed {
		t.Fatalf("state = %s, want crashed to persist", state)
	}
	if err := s.Wait(); err == nil {
		t.Error("Wait returned nil for a crashed engine")
	}
}

func TestSupervisorCleanExitIsStopped(t *testing.T) {
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	if err := s.Start([]string{stubEngine(t, "exit 0")}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateStopped)
	if err := s.Wait(); err != nil {
		t.Errorf("Wait = %v for a clean exit", err)
	}
}

func TestSupervisorStopEscalatesToKill(t *testing.T) {
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	s.Grace = 100 * time.Millisecond
	engine := stubEngine(t, `trap '' TERM
while true; do sleep 0.05; done`)
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return: SIGKILL escalation failed")
	}
	if state, _, _ := s.Status(); state != StateStopped {
		t.Fatalf("state = %s, want stopped after kill", state)
	}
}

// testDaemon builds a Daemon whose BuildArgv serves the stub engine, with the
// runner recorded from the deploy config when one is stored.
func testDaemon(t *testing.T, engineScript string) *Daemon {
	t.Helper()
	dir := t.TempDir()
	engine := stubEngine(t, engineScript)
	d := &Daemon{
		Sup: NewSupervisor(filepath.Join(dir, "engine.log")),
		Dir: dir,
		BuildArgv: func(dc *remote.DeployConfig) ([]string, error) {
			if dc == nil {
				return nil, fmt.Errorf("nothing to serve: no Outfit and no stored deploy config")
			}
			return append([]string{engine}, dc.ServeArgs...), nil
		},
		ValidateConfig: func(dc remote.DeployConfig) error {
			if dc.Runner != "llamacpp" {
				return fmt.Errorf("runner %q cannot be served locally", dc.Runner)
			}
			return nil
		},
	}
	return d
}

func TestDaemonDeployConfigPersistence(t *testing.T) {
	d := testDaemon(t, "exit 0")

	// Nothing pushed yet: stored config is absent, start has nothing to serve.
	if dc, err := d.StoredConfig(); err != nil || dc != nil {
		t.Fatalf("StoredConfig = %v, %v; want nil, nil", dc, err)
	}
	if err := d.StartEngine(); err == nil || !strings.Contains(err.Error(), "nothing to serve") {
		t.Fatalf("idle start error = %v", err)
	}

	// An unservable runner is rejected and not stored.
	if err := d.Push(remote.DeployConfig{Runner: "vllm"}); err == nil ||
		!strings.Contains(err.Error(), "vllm") {
		t.Fatalf("push of unservable runner = %v", err)
	}
	if dc, _ := d.StoredConfig(); dc != nil {
		t.Fatal("rejected config was stored")
	}

	dc := remote.DeployConfig{Runner: "llamacpp", ModelID: "org/model", ServeArgs: []string{}}
	if err := d.Push(dc); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(d.Dir, "deploy-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("deploy-config.json mode = %v, want 0600", fi.Mode().Perm())
	}

	// A fresh Daemon over the same dir — the restart case — serves it.
	d2 := testDaemon(t, "exit 0")
	d2.Dir = d.Dir
	got, err := d2.StoredConfig()
	if err != nil || got == nil || got.ModelID != "org/model" {
		t.Fatalf("restarted StoredConfig = %+v, %v", got, err)
	}
}

func TestDaemonAPI(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.Collector = &metrics.Collector{GOOS: "linux", Run: func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "free" {
			return "Mem: 1000 400 600 0 0 600\n", nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}}
	srv := httptest.NewServer(d.Handler("sekrit"))
	defer srv.Close()
	client := srv.Client()

	do := func(method, path, token string, body string) (*http.Response, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var decoded map[string]any
		json.NewDecoder(resp.Body).Decode(&decoded)
		return resp, decoded
	}

	// Auth: missing and wrong tokens are 401; nothing happens.
	if resp, _ := do("GET", "/v1/status", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: HTTP %d, want 401", resp.StatusCode)
	}
	if resp, _ := do("POST", "/v1/start", "wrong", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: HTTP %d, want 401", resp.StatusCode)
	}
	if state, _, _ := d.Sup.Status(); state != StateIdle {
		t.Fatal("unauthorised request changed engine state")
	}

	// Status while idle.
	if resp, body := do("GET", "/v1/status", "sekrit", ""); resp.StatusCode != 200 || body["state"] != "idle" {
		t.Fatalf("status = %d %v", resp.StatusCode, body)
	}

	// Start with nothing to serve is a 400 with the reason.
	if resp, body := do("POST", "/v1/start", "sekrit", ""); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "nothing to serve") {
		t.Fatalf("idle start = %d %v", resp.StatusCode, body)
	}

	// A malformed start body is a 400.
	if resp, _ := do("POST", "/v1/start", "sekrit", "{not json"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad start body = %d, want 400", resp.StatusCode)
	}
	// An unservable start body is a 400 and stores nothing.
	if resp, _ := do("POST", "/v1/start", "sekrit", `{"runner":"vllm","modelId":"m"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unservable start body = %d, want 400", resp.StatusCode)
	}
	if dc, _ := d.StoredConfig(); dc != nil {
		t.Fatal("rejected start body was stored")
	}

	// A start carrying its config pushes and starts in one call.
	dc := `{"runner":"llamacpp","modelId":"org/model","serveArgs":[]}`
	if resp, body := do("POST", "/v1/start", "sekrit", dc); resp.StatusCode != 200 || body["state"] != "running" {
		t.Fatalf("start with body = %d %v", resp.StatusCode, body)
	}
	if stored, _ := d.StoredConfig(); stored == nil || stored.ModelID != "org/model" {
		t.Fatalf("start body not persisted: %+v", stored)
	}

	// A start body while running is a 409 that stores nothing.
	if resp, body := do("POST", "/v1/start", "sekrit",
		`{"runner":"llamacpp","modelId":"org/other","serveArgs":[]}`); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("start body while running = %d %v", resp.StatusCode, body)
	}
	if stored, _ := d.StoredConfig(); stored == nil || stored.ModelID != "org/model" {
		t.Fatalf("409 start body was stored: %+v", stored)
	}

	// Start while running is a 409 naming the engine.
	if resp, body := do("POST", "/v1/start", "sekrit", ""); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("start while running = %d %v", resp.StatusCode, body)
	}

	// Push while running: stored, engine untouched.
	if resp, body := do("PUT", "/v1/deploy-config", "sekrit", dc); resp.StatusCode != 200 ||
		!strings.Contains(body["message"].(string), "next start") {
		t.Fatalf("push while running = %d %v", resp.StatusCode, body)
	}
	if state, _, _ := d.Sup.Status(); state != StateRunning {
		t.Fatal("push disturbed the running engine")
	}

	// Metrics while running: state, runner, and the collector's memory stat.
	if resp, body := do("GET", "/v1/metrics", "sekrit", ""); resp.StatusCode != 200 ||
		body["state"] != "running" || body["runner"] != "llamacpp" || body["memory"] == nil {
		t.Fatalf("metrics = %d %v", resp.StatusCode, body)
	}

	// The activity pair crosses the wire, not just the Go call: this is the
	// shape the stats Lambda curls, so a field that never serialised would
	// leave `outfit remote metrics` silently blank.
	_, metricsBody := do("GET", "/v1/metrics", "sekrit", "")
	_, statusBody := do("GET", "/v1/status", "sekrit", "")
	if metricsBody["lastActiveAt"] == nil {
		t.Errorf("metrics carried no lastActiveAt over HTTP: %v", metricsBody)
	}
	// One record, so the two endpoints cannot answer differently.
	if metricsBody["lastActiveAt"] != statusBody["lastActiveAt"] {
		t.Errorf("metrics %v and status %v disagree on lastActiveAt",
			metricsBody["lastActiveAt"], statusBody["lastActiveAt"])
	}

	// Stop, and stop again: idempotent.
	if resp, body := do("POST", "/v1/stop", "sekrit", ""); resp.StatusCode != 200 || body["state"] != "stopped" {
		t.Fatalf("stop = %d %v", resp.StatusCode, body)
	}
	if resp, body := do("POST", "/v1/stop", "sekrit", ""); resp.StatusCode != 200 || body["state"] != "stopped" {
		t.Fatalf("second stop = %d %v", resp.StatusCode, body)
	}

	// A crashed engine restarts only on explicit start.
	crash := testDaemon(t, "exit 3")
	crash.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "org/model"})
	if err := crash.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, crash.Sup, StateCrashed)
	if got := crash.Status(); got.State != "crashed" {
		t.Fatalf("status after crash = %+v", got)
	}
}

func TestDaemonMetricsIdle(t *testing.T) {
	d := testDaemon(t, "exit 0")
	stats := d.Metrics(context.Background())
	if stats.State != "idle" || stats.CPU != nil || stats.Memory != nil {
		t.Errorf("idle metrics = %+v, want bare state", stats)
	}
}

func TestListenGuard(t *testing.T) {
	// Tokenless non-loopback is refused.
	for _, addr := range []string{":0", "0.0.0.0:0", "[::]:0"} {
		if l, err := Listen(addr, ""); err == nil {
			l.Close()
			t.Errorf("tokenless Listen(%q) succeeded, want refusal", addr)
		} else if !strings.Contains(err.Error(), TokenEnvVar) {
			t.Errorf("refusal for %q does not name %s: %v", addr, TokenEnvVar, err)
		}
	}
	// Tokenless loopback is fine.
	for _, addr := range []string{"127.0.0.1:0", "localhost:0"} {
		l, err := Listen(addr, "")
		if err != nil {
			t.Errorf("tokenless Listen(%q) = %v, want success", addr, err)
			continue
		}
		l.Close()
	}
	// With a token, non-loopback is fine.
	l, err := Listen("127.0.0.1:0", "tok")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

// The engine endpoint is what a router needs and cannot guess: the engine's
// own port, not the control API's. It appears only while an engine runs, and
// never carries the key itself — saying a key is required is a fact a caller
// needs, handing the key over is not.
func TestStatusReportsEngineEndpoint(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.SetEngineEndpoint(&EngineEndpoint{Port: 8080, LoopbackOnly: true, RequiresKey: true})

	// Idle: an address for a process that does not exist is worse than none.
	if got := d.Status(); got.Engine != nil {
		t.Errorf("idle daemon reported an engine endpoint: %+v", got.Engine)
	}

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	defer d.Sup.Stop()
	waitForState(t, d.Sup, StateRunning)

	got := d.Status()
	if got.Engine == nil {
		t.Fatal("running engine reported no endpoint")
	}
	if got.Engine.Port != 8080 {
		t.Errorf("port = %d, want the engine's 8080", got.Engine.Port)
	}
	if !got.Engine.LoopbackOnly || !got.Engine.RequiresKey {
		t.Errorf("endpoint lost its flags: %+v", got.Engine)
	}

	// The serialised reply must not carry a key under any name.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "sekrit") {
		t.Errorf("status leaked a key: %s", encoded)
	}
	var decoded struct {
		Engine map[string]any `json:"engine"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for field := range decoded.Engine {
		switch field {
		case "port", "path", "loopbackOnly", "requiresKey":
		default:
			t.Errorf("engine endpoint serialised unexpected field %q", field)
		}
	}

	// Stopping takes the address away again.
	d.Sup.Stop()
	waitForState(t, d.Sup, StateStopped)
	if got := d.Status(); got.Engine != nil {
		t.Errorf("stopped daemon reported an engine endpoint: %+v", got.Engine)
	}
}

// A daemon that cannot work out where its engine serves reports nothing rather
// than a guess: the fleet file's per-node override is the way through.
func TestStatusOmitsUnknownEngineEndpoint(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	defer d.Sup.Stop()
	waitForState(t, d.Sup, StateRunning)
	if got := d.Status(); got.Engine != nil {
		t.Errorf("endpoint reported without one being set: %+v", got.Engine)
	}
}

// The caller supplies the key an engine is gated with, and it reaches the
// engine as a path — never as an argument, where every local user could read
// it out of the process list.
func TestEngineKeyReachesTheEngineAsAFile(t *testing.T) {
	// The engine records the arguments it was actually given, which is the
	// only place the question can be answered: StartEngine appends the key
	// arguments after BuildArgv has returned.
	argsFile := filepath.Join(t.TempDir(), "args")
	d := testDaemon(t, `echo "$@" > `+argsFile+`
trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.EngineKeyArgs = func(_ *remote.DeployConfig, path string) ([]string, error) {
		return []string{"--api-key-file", path}, nil
	}

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEngineKey("sk-supplied"); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	defer d.Sup.Stop()
	waitForState(t, d.Sup, StateRunning)

	var joined string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(argsFile); err == nil && len(data) > 0 {
			joined = string(data)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if joined == "" {
		t.Fatal("the engine recorded no arguments")
	}
	if !strings.Contains(joined, "--api-key-file") {
		t.Errorf("engine was not gated: %s", joined)
	}
	if strings.Contains(joined, "sk-supplied") {
		t.Errorf("the key is on the command line, where any local user can read it: %s", joined)
	}

	// The file itself is private and holds exactly the key.
	path := d.storedEngineKeyPath()
	if path == "" {
		t.Fatal("no key file was written")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "sk-supplied" {
		t.Errorf("key file holds %q", data)
	}
}

// A key replaces its predecessor rather than accumulating, and clearing it
// leaves nothing behind.
func TestEngineKeyIsReplacedAndCleared(t *testing.T) {
	d := testDaemon(t, "exit 0")
	if err := d.SetEngineKey("first"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEngineKey("second"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(d.storedEngineKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Errorf("key file holds %q, want the newer key", data)
	}
	if err := d.SetEngineKey(""); err != nil {
		t.Fatal(err)
	}
	if p := d.storedEngineKeyPath(); p != "" {
		t.Errorf("clearing left a key file at %s", p)
	}
	// Clearing twice is not an error.
	if err := d.SetEngineKey(""); err != nil {
		t.Errorf("clearing an absent key: %v", err)
	}
}

// An engine with no way to read a key from a file is refused rather than
// gated with a literal argument.
func TestUngatableEngineIsRefused(t *testing.T) {
	d := testDaemon(t, "exit 0")
	d.EngineKeyArgs = func(*remote.DeployConfig, string) ([]string, error) {
		return nil, fmt.Errorf("vllm cannot be gated with an API key")
	}
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEngineKey("sk"); err != nil {
		t.Fatal(err)
	}
	err := d.StartEngine()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "cannot be gated") {
		t.Errorf("error = %v", err)
	}
	if state, _, _ := d.Sup.Status(); state == StateRunning {
		t.Error("the engine started despite the refusal")
	}
}

// The key is the one field of a start request that must not come back out, on
// any path — success, conflict, or a rejected config.
func TestStartRequestKeyNeverComesBack(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.EngineKeyArgs = func(_ *remote.DeployConfig, path string) ([]string, error) {
		return []string{"--api-key-file", path}, nil
	}
	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()
	defer d.Sup.Stop()

	const key = "sk-must-not-leak"
	post := func(body string) (int, string) {
		t.Helper()
		resp, err := srv.Client().Post(srv.URL+"/v1/start", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}

	// A rejected config: the runner cannot be served here.
	code, body := post(`{"runner":"nope","modelId":"m","engineApiKey":"` + key + `"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("rejected config = %d %s", code, body)
	}
	if strings.Contains(body, key) {
		t.Errorf("the key came back in a rejection: %s", body)
	}
	// ...and nothing was stored, neither config nor credential.
	if p := d.storedEngineKeyPath(); p != "" {
		t.Error("a refused start stored the key")
	}

	// A successful start.
	code, body = post(`{"runner":"llamacpp","modelId":"m","engineApiKey":"` + key + `"}`)
	if code != 200 {
		t.Fatalf("start = %d %s", code, body)
	}
	if strings.Contains(body, key) {
		t.Errorf("the key came back in the reply: %s", body)
	}
	waitForState(t, d.Sup, StateRunning)

	// A conflict, carrying a different key: the running engine is untouched
	// and the stored key is not replaced.
	code, body = post(`{"runner":"llamacpp","modelId":"m","engineApiKey":"sk-second"}`)
	if code != http.StatusConflict {
		t.Fatalf("start while running = %d %s", code, body)
	}
	if strings.Contains(body, "sk-second") {
		t.Errorf("the key came back in a conflict: %s", body)
	}
	stored, err := os.ReadFile(d.storedEngineKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != key {
		t.Errorf("a refused start replaced the stored key with %q", stored)
	}

	// Status says a key is needed and never what it is.
	resp, err := srv.Client().Get(srv.URL + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(out), key) {
		t.Errorf("status disclosed the key: %s", out)
	}
}

// A config arriving without a key opens the engine: the key travels with the
// config it accompanied, so a new instruction does not inherit an old
// credential. A start carrying neither reuses what was stored.
func TestKeyTravelsWithItsConfig(t *testing.T) {
	d := testDaemon(t, "exit 0")

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEngineKey("sk-first"); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()

	// A new config with no key clears it.
	resp, err := srv.Client().Post(srv.URL+"/v1/start", "application/json",
		strings.NewReader(`{"runner":"llamacpp","modelId":"m2"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if p := d.storedEngineKeyPath(); p != "" {
		t.Error("a config carrying no key left the previous key in place")
	}
}

// Whether a key is required is the daemon's own fact. The endpoint is derived
// while the command is being built, before the key arguments are appended, so
// reading it back out of that argv would report a gated engine as open — and a
// client picking up an already-running node has nothing else to go on.
func TestStatusReportsAGatedEngineEvenWhenDerivedEarly(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	// Exactly what the CLI does: the endpoint is set from the argv before the
	// key exists on it.
	d.SetEngineEndpoint(&EngineEndpoint{Port: 8080})
	d.EngineKeyArgs = func(_ *remote.DeployConfig, path string) ([]string, error) {
		return []string{"--api-key-file", path}, nil
	}
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEngineKey("sk"); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	defer d.Sup.Stop()
	waitForState(t, d.Sup, StateRunning)

	got := d.Status()
	if got.Engine == nil || !got.Engine.RequiresKey {
		t.Fatalf("a gated engine reported %+v, want requiresKey", got.Engine)
	}
	// Clearing the key opens it again.
	d.Sup.Stop()
	waitForState(t, d.Sup, StateStopped)
	if err := d.SetEngineKey(""); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	if got := d.Status(); got.Engine == nil || got.Engine.RequiresKey {
		t.Errorf("an ungated engine reported %+v", got.Engine)
	}
}
