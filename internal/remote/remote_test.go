package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"

	"github.com/spinloop-ai/spinloop/internal/inference"
)

// isolateConfig sandboxes the config file location.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// stubAWSEnv pins the default credential chain to static env credentials so
// signing works offline and no real profile, SSO session or IMDS is consulted.
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

// writeConfig registers the `default` environment, which is where a
// configuration lives now that no path outside the registry is read.
func writeConfig(t *testing.T, cfg Config) {
	t.Helper()
	path := must1(EnvConfigPath("default"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func noEnv(string) string { return "" }

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig_FromFile(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{
		StartURL: "https://start.example/",
		StopURL:  "https://stop.example/",
		Region:   "eu-west-1",
	})
	cfg, err := LoadEnvironment("default", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartURL != "https://start.example/" || cfg.Region != "eu-west-1" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "https://old/", StopURL: "https://old-stop/", Region: "us-east-1"})
	cfg, err := LoadEnvironment("default", envMap(map[string]string{
		"SPINLOOP_REMOTE_START_URL": "https://new/",
		"SPINLOOP_REMOTE_REGION":    "eu-west-2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartURL != "https://new/" {
		t.Errorf("env should override start URL, got %q", cfg.StartURL)
	}
	if cfg.StopURL != "https://old-stop/" {
		t.Errorf("stored stop URL should survive, got %q", cfg.StopURL)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("env should override region, got %q", cfg.Region)
	}
}

func TestLoadConfig_RegionDerivedFromURL(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{
		StartURL: "https://abc123.lambda-url.eu-west-1.on.aws/",
		StopURL:  "https://def456.lambda-url.eu-west-1.on.aws/",
	})
	cfg, err := LoadEnvironment("default", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-1" {
		t.Errorf("region should be derived from the URL, got %q", cfg.Region)
	}
}

func TestLoadConfig_Unconfigured(t *testing.T) {
	isolateConfig(t)
	_, err := LoadEnvironment("default", noEnv)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected a not-configured error, got %v", err)
	}
}

func TestLoadConfig_NoRegion(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "https://start.example/", StopURL: "https://stop.example/"})
	_, err := LoadEnvironment("default", noEnv)
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("expected a region error, got %v", err)
	}
}

func TestRegionFromURL(t *testing.T) {
	cases := map[string]string{
		"https://abc.lambda-url.eu-west-1.on.aws/": "eu-west-1",
		"https://abc.lambda-url.us-east-2.on.aws":  "us-east-2",
		"https://example.com/":                     "",
		"://bad":                                   "",
	}
	for in, want := range cases {
		if got := regionFromURL(in); got != want {
			t.Errorf("regionFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStart_RetriesUntilReady(t *testing.T) {
	stubAWSEnv(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("start should POST, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("request is not SigV4-signed: %q", auth)
		}
		if r.Header.Get("X-Amz-Content-Sha256") == "" {
			t.Error("X-Amz-Content-Sha256 header missing")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"starting","retry_after_seconds":0}`))
			return
		}
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var progress []string
	resp, err := Start(context.Background(), cfg, func(msg string) { progress = append(progress, msg) }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != "ready" || resp.BaseURL != "http://198.51.100.1:8000/v1" || resp.APIKey != "sk-test" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if len(progress) != 1 || !strings.Contains(progress[0], "starting") {
		t.Errorf("unexpected progress lines: %v", progress)
	}
}

// A 503 seeding reply names the seed on every progress line — there is no
// instance to speak of yet — and the loop keeps polling until the weights
// are in and the model is serving.
func TestStart_NamesTheSeedWhileSeeding(t *testing.T) {
	stubAWSEnv(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"seeding","seedId":"llamacpp--org-model--Q4_K_M","retry_after_seconds":0}`))
			return
		}
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var progress []string
	resp, err := Start(context.Background(), cfg, func(msg string) { progress = append(progress, msg) }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != "ready" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	for _, line := range progress {
		if !strings.Contains(line, "seeding the weights") || !strings.Contains(line, "llamacpp--org-model--Q4_K_M") {
			t.Errorf("progress line does not name the seed: %q", line)
		}
	}
}

// A deadline that expires mid-seed is not the generic give-up: the weights
// are still being fetched, and the error carries the seed's follow command
// and the longer-timeout hint.
func TestStart_GiveUpDuringSeedingNamesTheSeed(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"seeding","seedId":"llamacpp--org-model--Q4_K_M","retry_after_seconds":30}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := Start(ctx, cfg, func(string) {}, nil, nil)
	if err == nil {
		t.Fatal("expected an error when the deadline expires")
	}
	for _, want := range []string{"seeding", "llamacpp--org-model--Q4_K_M", "spinloop remote seed status", "--timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("give-up error does not name %q: %v", want, err)
		}
	}
}

// The last reply was seeding and then the connection dropped: the give-up
// that follows the drop still names the seed, so the state a reply carried
// survives a dropped connection.
func TestStart_GiveUpAfterADroppedConnectionNamesTheSeed(t *testing.T) {
	stubAWSEnv(t)
	origWait := startRetryWait
	startRetryWait = time.Second
	t.Cleanup(func() { startRetryWait = origWait })

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"seeding","seedId":"llamacpp--org-model--Q4_K_M","retry_after_seconds":0}`))
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	_, err := Start(ctx, cfg, func(string) {}, nil, nil)
	if err == nil {
		t.Fatal("expected an error once the deadline passed")
	}
	for _, want := range []string{"seeding", "llamacpp--org-model--Q4_K_M", "spinloop remote seed status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("give-up error does not name %q: %v", want, err)
		}
	}
}

// A deadline that expires outside seeding keeps today's give-up message.
func TestStart_GiveUpOutsideSeedingIsGeneric(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"starting","retry_after_seconds":30}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := Start(ctx, cfg, func(string) {}, nil, nil)
	if err == nil {
		t.Fatal("expected an error when the deadline expires")
	}
	if want := "gave up waiting for the endpoint: context deadline exceeded"; err.Error() != want {
		t.Errorf("give-up error = %q, want %q", err.Error(), want)
	}
}

// A seeding reply that carries no seed id names nothing: the progress line
// keeps the plain form, and a deadline that expires on it keeps the generic
// give-up — a follow command for a seed there is none of would be a lie.
func TestStart_SeedingWithoutASeedIdStaysGeneric(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"seeding","retry_after_seconds":30}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var progress []string
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := Start(ctx, cfg, func(msg string) { progress = append(progress, msg) }, nil, nil)
	if err == nil {
		t.Fatal("expected an error when the deadline expires")
	}
	if want := "gave up waiting for the endpoint: context deadline exceeded"; err.Error() != want {
		t.Errorf("give-up error = %q, want the generic message with no seed to name", err.Error())
	}
	for _, line := range progress {
		if strings.Contains(line, "seeding the weights (seed") {
			t.Errorf("progress line names a seed the reply did not carry: %q", line)
		}
	}
}

// onState must see both the raw state of every poll and each attempt as it is
// issued, so a caller can tell a capacity wait apart from a boot rather than
// assume the instance is starting.
func TestStart_ReportsEachPollState(t *testing.T) {
	stubAWSEnv(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"no-capacity","retry_after_seconds":0}`))
			return
		}
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var states []string
	if _, err := Start(context.Background(), cfg, func(string) {}, func(s string) { states = append(states, s) }, nil); err != nil {
		t.Fatal(err)
	}
	want := StateInFlight + ",no-capacity," + StateInFlight + ",ready"
	if got := strings.Join(states, ","); got != want {
		t.Errorf("onState saw %q, want %q", got, want)
	}
}

// A boot following a capacity wait must not leave the observer holding the
// stale no-capacity state: the attempt that finds capacity holds its request
// open without a reply, so Start must report it as in flight for the boot to
// be distinguishable from still waiting on capacity.
func TestStart_ReportsInFlightAfterACapacityWait(t *testing.T) {
	stubAWSEnv(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"no-capacity","retry_after_seconds":0}`))
			return
		}
		// The attempt that finds capacity holds its request open while the
		// instance boots and answers only when ready.
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var states []string
	if _, err := Start(context.Background(), cfg, func(string) {}, func(s string) { states = append(states, s) }, nil); err != nil {
		t.Fatal(err)
	}
	lastNoCapacity, inFlightAfter, readyAfter := -1, false, false
	for i, s := range states {
		switch s {
		case "no-capacity":
			lastNoCapacity = i
		case StateInFlight:
			if i > lastNoCapacity {
				inFlightAfter = true
			}
		case "ready":
			if i > lastNoCapacity {
				readyAfter = true
			}
		}
	}
	if lastNoCapacity == -1 {
		t.Fatalf("onState never saw the no-capacity reply: %v", states)
	}
	if !inFlightAfter {
		t.Errorf("no in-flight report between the no-capacity reply and ready: %v", states)
	}
	if !readyAfter {
		t.Errorf("no ready report after the no-capacity reply: %v", states)
	}
}

func TestStart_RetriesADroppedConnection(t *testing.T) {
	stubAWSEnv(t)
	origWait := startRetryWait
	startRetryWait = 10 * time.Millisecond
	t.Cleanup(func() { startRetryWait = origWait })

	// First call: the server kills the TCP connection mid-request (a network
	// change mid-boot looks like this). Second call: ready. The wake is
	// idempotent server-side, so the client must reattach, not give up.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var progress []string
	resp, err := Start(context.Background(), cfg, func(msg string) { progress = append(progress, msg) }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != "ready" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if len(progress) != 1 || !strings.Contains(progress[0], "connection dropped") {
		t.Errorf("expected a connection-dropped progress line, got %v", progress)
	}
}

func TestStart_DoesNotRetryPastTheDeadline(t *testing.T) {
	stubAWSEnv(t)
	origWait := startRetryWait
	startRetryWait = 10 * time.Millisecond
	t.Cleanup(func() { startRetryWait = origWait })

	// Every call drops: the retry loop must still respect the caller's
	// deadline rather than spinning forever.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Start(ctx, cfg, func(string) {}, nil, nil)
	if err == nil {
		t.Fatal("expected an error once the deadline passed")
	}
}

func TestStart_Failure(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"state":"terminated","message":"cannot start"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Start(context.Background(), cfg, func(string) {}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Errorf("expected the server's message in the error, got %v", err)
	}
}

func TestStart_ContextDeadline(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"starting","retry_after_seconds":60}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Start(ctx, cfg, func(string) {}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "gave up") {
		t.Errorf("expected a gave-up error, got %v", err)
	}
}

func TestStatusAndStop(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"state":"running","healthy":true,"base_url":"http://198.51.100.1:8000/v1"}`))
			return
		}
		w.Write([]byte(`{"state":"stopping"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	status, err := Status(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "running" || status.Healthy == nil || !*status.Healthy {
		t.Errorf("unexpected status: %+v", status)
	}

	stop, err := Stop(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if stop.State != "stopping" {
		t.Errorf("unexpected stop response: %+v", stop)
	}
}

func TestPause(t *testing.T) {
	stubAWSEnv(t)
	var gotAction, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAction = r.URL.Query().Get("action")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopping"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	pause, err := Pause(context.Background(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if pause.State != "stopping" {
		t.Errorf("unexpected pause response: %+v", pause)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("pause should POST, got %s", gotMethod)
	}
	// The pause speaks to the stop Lambda in pause mode: same URL, an action
	// parameter — nothing new to configure.
	if gotAction != "pause" {
		t.Errorf("pause must select the stop Lambda's pause mode, got action=%q", gotAction)
	}
}

// pauseURL keeps the pause mode (action on the stop URL) and, only when asked
// for, marks the stop forced; a URL the parser rejects is handed back as-is,
// so a malformed config fails later where it always failed.
func TestPauseURL(t *testing.T) {
	if got := pauseURL("https://stop.example/", false); got != "https://stop.example/?action=pause" {
		t.Errorf("unforced pause URL = %q, want the pause mode and nothing else", got)
	}
	if got := pauseURL("https://stop.example/", true); got != "https://stop.example/?action=pause&force=true" {
		t.Errorf("forced pause URL = %q, want action and force", got)
	}
	if got := pauseURL("://bad", true); got != "://bad" {
		t.Errorf("unparseable URL should be returned as-is, got %q", got)
	}
}

// A forced pause marks the stop forced on the way over (force=true beside
// action=pause) so the control plane can skip the graceful engine stop; an
// unforced one sends nothing, so an old control plane sees exactly what it
// always saw.
func TestPause_ForceParameter(t *testing.T) {
	stubAWSEnv(t)
	var gotAction, gotForce [2]string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction[calls] = r.URL.Query().Get("action")
		gotForce[calls] = r.URL.Query().Get("force")
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopping"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	if _, err := Pause(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if gotAction[0] != "pause" || gotForce[0] != "true" {
		t.Errorf("forced pause must send action=pause&force=true, got action=%q force=%q", gotAction[0], gotForce[0])
	}
	if _, err := Pause(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if gotAction[1] != "pause" {
		t.Errorf("unforced pause must still select the stop Lambda's pause mode, got action=%q", gotAction[1])
	}
	if gotForce[1] != "" {
		t.Errorf("unforced pause must not send a force parameter, got force=%q", gotForce[1])
	}
}

// A restart is a pause-style stop followed by the wake, in that order: the
// stop keeps the boot disk (action=pause), and the wake blocks until ready.
func TestRestart_StopsThenWakes(t *testing.T) {
	stubAWSEnv(t)
	var order []string
	var stopQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "pause" {
			order = append(order, "stop")
			stopQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"state":"stopping"}`))
			return
		}
		order = append(order, "wake")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	var progress []string
	resp, err := Restart(context.Background(), cfg, false, func(msg string) { progress = append(progress, msg) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != "ready" || resp.BaseURL != "http://198.51.100.1:8000/v1" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if len(order) != 2 || order[0] != "stop" || order[1] != "wake" {
		t.Fatalf("expected the stop before the wake, got %v", order)
	}
	// The stop is pause-style and unforced: the boot disk survives, the engine
	// is stopped politely.
	if !strings.Contains(stopQuery, "action=pause") || strings.Contains(stopQuery, "force=") {
		t.Errorf("unforced restart must stop in pause mode without force, got %q", stopQuery)
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "stopped; waking") {
		t.Errorf("expected a stop-phase progress line, got %v", progress)
	}
}

func TestRestart_ForceMarksTheStop(t *testing.T) {
	stubAWSEnv(t)
	var stopQuery string
	var woke bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "pause" {
			stopQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"state":"stopping"}`))
			return
		}
		woke = true
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	if _, err := Restart(context.Background(), cfg, true, func(string) {}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stopQuery, "action=pause") || !strings.Contains(stopQuery, "force=true") {
		t.Errorf("forced restart must stop in pause mode with force=true, got %q", stopQuery)
	}
	if !woke {
		t.Error("forced restart must still wake the instance")
	}
}

// A failed stop never reaches the wake: the instance may still be running, so
// starting it again is pointless and the error is the stop's.
func TestRestart_StopFailureNeverWakes(t *testing.T) {
	stubAWSEnv(t)
	woke := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "pause" {
			woke = true
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"state":"terminated","message":"cannot stop"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Restart(context.Background(), cfg, false, func(string) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot stop") {
		t.Errorf("expected the stop's error, got %v", err)
	}
	if woke {
		t.Error("a failed stop must not wake the instance")
	}
}

// When the stop took effect but the wake then fails, the error says the
// instance is stopped and names the command that recovers it.
func TestRestart_WakeFailureNamesRecovery(t *testing.T) {
	stubAWSEnv(t)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "pause" {
			calls = append(calls, "stop")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"state":"stopping"}`))
			return
		}
		calls = append(calls, "wake")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"state":"terminated","message":"cannot start"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Restart(context.Background(), cfg, false, func(string) {}, nil)
	if err == nil {
		t.Fatal("expected a wake failure")
	}
	if !strings.Contains(err.Error(), "stopped") ||
		!strings.Contains(err.Error(), "spinloop remote start") {
		t.Errorf("expected the recovery hint in the error, got %v", err)
	}
	if len(calls) != 2 || calls[0] != "stop" || calls[1] != "wake" {
		t.Fatalf("expected the wake to be attempted after the stop, got %v", calls)
	}
}

func TestPause_NonSuccess(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"state":"stopped","message":"cannot stop"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Pause(context.Background(), cfg, false)
	if err == nil || !strings.Contains(err.Error(), "pause") ||
		!strings.Contains(err.Error(), "cannot stop") {
		t.Errorf("expected a pause error with the reply's detail, got %v", err)
	}
}

func TestCall_NonJSONError(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Forbidden"))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Status(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "403") ||
		!strings.Contains(err.Error(), "lambda:InvokeFunctionUrl") {
		t.Errorf("expected a 403 error with the IAM hint, got %v", err)
	}
}

// expiredTokenBody is the shape a Function URL authorizer returns when the
// signed request carries an expired token: valid JSON, so it parses cleanly
// into a Response with an empty state.
const expiredTokenBody = `{"Message":"The security token included in the request is expired"}`

func TestStatus_ExpiredCredentials(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	resp, err := Status(context.Background(), cfg)
	if err == nil {
		t.Fatalf("expected an error for expired credentials, got a response: %+v", resp)
	}
	if !strings.Contains(err.Error(), "expired or invalid") ||
		!strings.Contains(err.Error(), refreshCredsHint) {
		t.Errorf("expected an expired-credentials error telling the user to refresh, got %v", err)
	}
	if strings.Contains(err.Error(), "lambda:InvokeFunctionUrl") {
		t.Errorf("expired credentials should not be reported as a permissions problem: %v", err)
	}
}

func TestStop_ExpiredCredentials(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	if _, err := Stop(context.Background(), cfg); err == nil ||
		!strings.Contains(err.Error(), "expired or invalid") {
		t.Errorf("expected stop to fail with an expired-credentials error, got %v", err)
	}
}

// noExplicitAmbient keeps explicitAmbientCreds false regardless of the
// machine the tests run on: no env credential, no profile, IMDS off.
func noExplicitAmbient(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func TestCredsRefreshHintVariants(t *testing.T) {
	const region = "ap-southeast-2"

	t.Run("no stored key names the ambient credentials", func(t *testing.T) {
		noExplicitAmbient(t)
		storeCredForTest(t, testCred("eu-west-1"))
		if got := credsRefreshHint(region); got != refreshCredsHint {
			t.Fatalf("hint = %q; want the ambient hint", got)
		}
	})

	t.Run("a stored key in use names the store command", func(t *testing.T) {
		noExplicitAmbient(t)
		storeCredForTest(t, testCred(region))
		if got := credsRefreshHint(region); got != storedCredsHint {
			t.Fatalf("hint = %q; want the stored-key hint", got)
		}
	})

	t.Run("explicit ambient credentials win over the stored key", func(t *testing.T) {
		noExplicitAmbient(t)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVENVENVENVENV")
		storeCredForTest(t, testCred(region))
		if got := credsRefreshHint(region); got != refreshCredsHint {
			t.Fatalf("hint = %q; want the ambient hint", got)
		}
	})
}

// A rejected request signed with the stored key must point at the store
// command, not at ambient credentials that play no part.
func TestStatus_ExpiredStoredCredentials(t *testing.T) {
	const region = "eu-west-1"
	noExplicitAmbient(t)
	storeCredForTest(t, testCred(region))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: region}
	_, err := Status(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error for expired credentials")
	}
	if !strings.Contains(err.Error(), "expired or invalid") ||
		!strings.Contains(err.Error(), storedCredsHint) {
		t.Errorf("expected the stored-key refresh hint, got %v", err)
	}
	if strings.Contains(err.Error(), refreshCredsHint) {
		t.Errorf("the ambient hint must not appear for a stored-key rejection: %v", err)
	}
}

// A JSON-parseable 403 that is not a credential problem keeps the IAM hint —
// the parse-success path must classify the same way as the non-JSON one.
func TestStatus_ForbiddenKeepsPermissionHint(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"AccessDeniedException: not authorized"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, Region: "eu-west-1"}
	_, err := Status(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "403") ||
		!strings.Contains(err.Error(), "lambda:InvokeFunctionUrl") {
		t.Errorf("expected a 403 error with the IAM hint, got %v", err)
	}
}

func TestStats_ExpiredCredentials(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{StatsURL: server.URL, Region: "eu-west-1"}
	if _, err := Stats(context.Background(), cfg); err == nil ||
		!strings.Contains(err.Error(), "expired or invalid") {
		t.Errorf("expected stats to fail with an expired-credentials error, got %v", err)
	}
}

func TestCredentialError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"typed api error", &smithy.GenericAPIError{Code: "ExpiredToken", Message: "the token expired"}, true},
		{"typed invalid client", &smithy.GenericAPIError{Code: "InvalidClientTokenId"}, true},
		{"typed permission error", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
		{"string marker", errors.New("get credentials: RequestExpired: signature no longer valid"), true},
		{"security token phrase", errors.New("the security token included in the request is expired"), true},
		{"unrelated error", errors.New("no EC2 IMDS role found"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := credentialError(tc.err); got != tc.want {
				t.Errorf("credentialError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDeploy_Success(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model"}
	resp, err := Deploy(context.Background(), cfg, dc, "203.0.113.0/24", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Deployed {
		t.Errorf("expected a deployed response, got %+v", resp)
	}
	// The deploy body is signed over, so it must actually reach the endpoint.
	if !strings.Contains(string(gotBody), `"runner":"vllm"`) ||
		!strings.Contains(string(gotBody), `"allowedCidr":"203.0.113.0/24"`) {
		t.Errorf("deploy did not send the expected body: %s", gotBody)
	}
	// Omitted entirely when not asked for, so an older control plane sees the
	// body it always saw.
	for _, field := range []string{"reseed", "apiKey"} {
		if strings.Contains(string(gotBody), field) {
			t.Errorf("%s should be omitted when unset: %s", field, gotBody)
		}
	}
}

func TestDeploy_SpinloopVersionReachesTheRequest(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model", SpinloopVersion: "1.26.1"}
	if _, err := Deploy(context.Background(), cfg, dc, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"spinloopVersion":"1.26.1"`) {
		t.Errorf("spinloopVersion did not reach the request body: %s", gotBody)
	}
}

// An unpinned deploy sends exactly the body a control plane predating the pin
// expects — the field is absent, not null or "latest".
func TestDeploy_SpinloopVersionOmittedWhenUnpinned(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model"}
	if _, err := Deploy(context.Background(), cfg, dc, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotBody), "spinloopVersion") {
		t.Errorf("spinloopVersion should be omitted when empty: %s", gotBody)
	}
}

func TestDeploy_InstanceTypeReachesTheRequest(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model", InstanceType: "g6e.2xlarge"}
	if _, err := Deploy(context.Background(), cfg, dc, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"instanceType":"g6e.2xlarge"`) {
		t.Errorf("instanceType did not reach the request body: %s", gotBody)
	}
}

// An untyped deploy sends exactly the body a control plane predating the field
// expects — the key is absent, not null or empty.
func TestDeploy_InstanceTypeOmittedWhenUntyped(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model"}
	if _, err := Deploy(context.Background(), cfg, dc, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotBody), "instanceType") {
		t.Errorf("instanceType should be omitted when empty: %s", gotBody)
	}
}

// TestIsInstanceType pins the shape guard a deploy runs before sending a type
// the control plane would otherwise reject at launch.
func TestIsInstanceType(t *testing.T) {
	for _, ok := range []string{
		"g6e.xlarge", "g6e.2xlarge", "g5.2xlarge", "trn1.2xlarge",
		"inf2.xlarge", "c7g.large", "m5.xlarge", "u7i-6tb.112xlarge",
		"mac2-m2.2xlarge", "p4d.24xlarge", "g6e.metal",
	} {
		if !IsInstanceType(ok) {
			t.Errorf("IsInstanceType(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", "g6exlarge", "G6E.xlarge", "g6e.xlarge.extra", "g6e.", ".xlarge",
		"g6e.xlarge ", " g6e.xlarge", "g6e_xlarge", "g6e..xlarge",
	} {
		if IsInstanceType(bad) {
			t.Errorf("IsInstanceType(%q) = true, want false", bad)
		}
	}
}

func TestDeploy_ReseedReachesTheRequest(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"state":"deployed","deployed":true,"seeding":true}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model"}
	if _, err := Deploy(context.Background(), cfg, dc, "", true, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"reseed":true`) {
		t.Errorf("reseed did not reach the request body: %s", gotBody)
	}
}

// A supplied key rides the signed body beside the other request-scoped fields
// — never on inference.DeployConfig, so it is never persisted or read back.
func TestDeploy_APIKeyReachesTheRequest(t *testing.T) {
	stubAWSEnv(t)
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"deployed":true,"apiKeyAction":"rotated"}`))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	dc := inference.DeployConfig{Runner: "vllm", ModelID: "org/model"}
	resp, err := Deploy(context.Background(), cfg, dc, "", false, "sk-supplied")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"apiKey":"sk-supplied"`) {
		t.Errorf("the key did not reach the request body: %s", gotBody)
	}
	// The action comes back without the value.
	if resp.APIKeyAction != "rotated" {
		t.Errorf("APIKeyAction = %q, want rotated", resp.APIKeyAction)
	}
}

func TestDeploy_ExpiredCredentials(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{DeployURL: server.URL, Region: "eu-west-1"}
	if _, err := Deploy(context.Background(), cfg, inference.DeployConfig{Runner: "vllm"}, "", false, ""); err == nil ||
		!strings.Contains(err.Error(), "expired or invalid") {
		t.Errorf("expected deploy to fail with an expired-credentials error, got %v", err)
	}
}

func TestDeploy_MissingURL(t *testing.T) {
	if _, err := Deploy(context.Background(), Config{}, inference.DeployConfig{}, "", false, ""); err == nil ||
		!strings.Contains(err.Error(), "no deploy_url") {
		t.Errorf("expected a missing-URL error, got %v", err)
	}
}

// A non-retryable start failure (an expired-credentials 403) returns at once via
// the default case, classified, rather than looping until the deadline.
func TestStart_ExpiredCredentials(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(expiredTokenBody))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, Region: "eu-west-1"}
	if _, err := Start(context.Background(), cfg, func(string) {}, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "expired or invalid") {
		t.Errorf("expected start to fail with an expired-credentials error, got %v", err)
	}
}

// An environment resolves by name, from the registry, with the overrides
// applied on top of whatever the file holds.
func TestLoadEnvironment(t *testing.T) {
	isolateConfig(t)
	if err := SaveEnvironment("prod", Config{
		StartURL:    "https://start.example/",
		StopURL:     "https://stop.example/",
		Region:      "eu-west-1",
		Environment: "prod",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadEnvironment("prod", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartURL != "https://start.example/" || cfg.Region != "eu-west-1" {
		t.Errorf("unexpected config: %+v", cfg)
	}

	cfg, err = LoadEnvironment("prod", envMap(map[string]string{"SPINLOOP_REMOTE_REGION": "eu-west-2"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("env should override the file's region, got %q", cfg.Region)
	}
}

// A name with no file and no overrides fails naming the registry path to
// create, rather than a path the user never chose.
func TestLoadEnvironment_Missing(t *testing.T) {
	isolateConfig(t)
	_, err := LoadEnvironment("nosuchenv", noEnv)
	if err == nil {
		t.Fatal("expected a not-configured error")
	}
	if !strings.Contains(err.Error(), "remotes/nosuchenv/remote.json") {
		t.Errorf("the failure should name the environment's registry path, got %v", err)
	}
}

func TestStats_NoStatsURL(t *testing.T) {
	_, err := Stats(context.Background(), Config{Region: "us-east-1"})
	if err == nil || !strings.Contains(err.Error(), "no stats_url") {
		t.Errorf("expected no-stats-url error, got %v", err)
	}
}

func TestStats_Success(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("stats should GET, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("request is not SigV4-signed: %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"environment": "dev",
			"state": "running",
			"instanceId": "i-123456",
			"instanceType": "g6e.xlarge",
			"runner": "llamacpp",
			"modelId": "unsloth/Qwen3.6-27B",
			"uptimeSeconds": 7200,
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
			"memory": {"total": 17179869184, "used": 4294967296},
			"retainUntil": "2026-01-02T04:00:00Z"
		}`))
	}))
	defer server.Close()

	cfg := Config{StatsURL: server.URL, Region: "us-east-1"}
	resp, err := Stats(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Environment != "dev" || resp.State != "running" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp.RetainUntil != "2026-01-02T04:00:00Z" {
		t.Errorf("retainUntil not decoded: %q", resp.RetainUntil)
	}
	if resp.Tokens == nil || resp.Tokens.Requests == nil || *resp.Tokens.Requests != 342 {
		t.Errorf("unexpected tokens: %+v", resp.Tokens)
	}
	if len(resp.GPUs) != 1 || resp.GPUs[0].Utilization != 85 {
		t.Errorf("unexpected GPU stats: %+v", resp.GPUs)
	}
	if resp.CPU == nil || resp.CPU.Utilization != 23.5 {
		t.Errorf("unexpected CPU: %+v", resp.CPU)
	}
	if resp.Memory == nil || resp.Memory.Used != 4294967296 {
		t.Errorf("unexpected memory: %+v", resp.Memory)
	}
}

func TestStats_Stopped(t *testing.T) {
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

	cfg := Config{StatsURL: server.URL, Region: "us-east-1"}
	resp, err := Stats(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != "stopped" || resp.Tokens != nil || len(resp.GPUs) != 0 {
		t.Errorf("stopped instance should have no metrics: %+v", resp)
	}
	// A reply without the field leaves it empty, not a zero time.
	if resp.RetainUntil != "" {
		t.Errorf("no retainUntil in the reply should decode empty, got %q", resp.RetainUntil)
	}
}

func TestStats_WithEnvironment(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := r.URL.Query().Get("env")
		if env != "staging" {
			t.Errorf("expected env=staging query param, got %q", env)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"environment":"staging","state":"running"}`))
	}))
	defer server.Close()

	cfg := Config{StatsURL: server.URL, Environment: "staging", Region: "us-east-1"}
	resp, err := Stats(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Environment != "staging" {
		t.Errorf("unexpected environment: %+v", resp)
	}
}

func TestStats_ErrorResponse(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"environment":"dev","state":"unknown","errors":["SSM timeout"]}`))
	}))
	defer server.Close()

	cfg := Config{StatsURL: server.URL, Region: "us-east-1"}
	_, err := Stats(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "SSM timeout") {
		t.Errorf("expected error with message, got %v", err)
	}
}

func TestEnv_ReturnsKeyAndBaseURL(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("env should GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"base_url":"http://198.51.100.1:8000/v1","api_key":"sk-remote"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL, Region: "eu-west-1"}
	resp, err := Env(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.BaseURL != "http://198.51.100.1:8000/v1" {
		t.Errorf("base_url = %q, want http://198.51.100.1:8000/v1", resp.BaseURL)
	}
	if resp.APIKey != "sk-remote" {
		t.Errorf("api_key = %q, want sk-remote", resp.APIKey)
	}
}

func TestEnv_NoEnvURL(t *testing.T) {
	stubAWSEnv(t)
	cfg := Config{StartURL: "https://start.example/", StopURL: "https://stop.example/", Region: "eu-west-1"}
	_, err := Env(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "env_url") {
		t.Errorf("expected env_url error, got %v", err)
	}
}

func TestLoadConfig_EnvURLOverride(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "https://start/", StopURL: "https://stop/", EnvURL: "https://old-env/", Region: "eu-west-1"})
	cfg, err := LoadEnvironment("default", envMap(map[string]string{
		"SPINLOOP_REMOTE_ENV_URL": "https://new-env/",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnvURL != "https://new-env/" {
		t.Errorf("env should override env URL, got %q", cfg.EnvURL)
	}
}

func TestProbeReachability_Success(t *testing.T) {
	// Listen on a local address to simulate a reachable endpoint.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	// Accept and close connections so the probe doesn't hang.
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
	}()
	defer l.Close()

	if err := ProbeReachability(fmt.Sprintf("http://127.0.0.1:%d/v1", port)); err != nil {
		t.Errorf("probe should succeed on a listening port, got %v", err)
	}
}

func TestProbeReachability_Refused(t *testing.T) {
	origTimeout := ProbeTimeout
	ProbeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { ProbeTimeout = origTimeout })

	// Pick a port that nothing is listening on.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // nothing listening now

	if err := ProbeReachability(fmt.Sprintf("http://127.0.0.1:%d/v1", port)); err == nil {
		t.Error("probe should fail on a closed port")
	}
}

func TestProbeReachability_BadURL(t *testing.T) {
	err := ProbeReachability("://not-a-url")
	if err == nil {
		t.Error("probe should fail on an unparseable URL")
	}
}

func TestKeep(t *testing.T) {
	stubAWSEnv(t)
	var gotCmd, gotRetainUntil, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCmd = r.URL.Query().Get("cmd")
		gotRetainUntil = r.URL.Query().Get("retainUntil")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"environment":"test","retainUntil":"2025-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, UpdateURL: server.URL, Region: "eu-west-1"}
	retainUntil := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	resp, err := Keep(context.Background(), cfg, retainUntil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RetainUntil != "2025-01-01T00:00:00Z" {
		t.Errorf("unexpected retainUntil: %q", resp.RetainUntil)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("keep should POST, got %s", gotMethod)
	}
	if gotCmd != "set-keep" {
		t.Errorf("keep should send cmd=set-keep, got %q", gotCmd)
	}
	if gotRetainUntil != "2025-01-01T00:00:00Z" {
		t.Errorf("keep should send retainUntil as ISO-8601, got %q", gotRetainUntil)
	}
}

func TestKeep_NoUpdateURL(t *testing.T) {
	stubAWSEnv(t)
	cfg := Config{StartURL: "https://start/", StopURL: "https://stop/", Region: "eu-west-1"}
	_, err := Keep(context.Background(), cfg, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no update_url") {
		t.Errorf("expected a no-update-url error, got %v", err)
	}
}

func TestKeep_NonSuccess(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"no running instance"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, UpdateURL: server.URL, Region: "eu-west-1"}
	_, err := Keep(context.Background(), cfg, time.Now())
	if err == nil || !strings.Contains(err.Error(), "keep") ||
		!strings.Contains(err.Error(), "no running instance") {
		t.Errorf("expected a keep error with the reply's detail, got %v", err)
	}
}

func TestStart_RetainUntil(t *testing.T) {
	stubAWSEnv(t)
	var gotRetainUntil string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRetainUntil = r.URL.Query().Get("retainUntil")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","retainUntil":"2025-01-01T04:00:00Z"}`))
	}))
	defer server.Close()

	retainUntil := time.Date(2025, 1, 1, 4, 0, 0, 0, time.UTC)
	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	resp, err := Start(context.Background(), cfg, func(string) {}, nil, &retainUntil)
	if err != nil {
		t.Fatal(err)
	}
	if gotRetainUntil != "2025-01-01T04:00:00Z" {
		t.Errorf("start should send retainUntil as ISO-8601, got %q", gotRetainUntil)
	}
	if resp.RetainUntil != "2025-01-01T04:00:00Z" {
		t.Errorf("unexpected retainUntil in response: %q", resp.RetainUntil)
	}
}

func TestStart_NoRetainUntil(t *testing.T) {
	stubAWSEnv(t)
	var gotRetainUntil string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRetainUntil = r.URL.Query().Get("retainUntil")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready"}`))
	}))
	defer server.Close()

	cfg := Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"}
	_, err := Start(context.Background(), cfg, func(string) {}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotRetainUntil != "" {
		t.Errorf("start without retainUntil should not send the parameter, got %q", gotRetainUntil)
	}
}
