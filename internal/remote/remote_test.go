package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func writeConfig(t *testing.T, cfg Config) {
	t.Helper()
	path := ConfigPath()
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

func TestConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got, want := ConfigPath(), "/tmp/xdg/outfit/remote.json"; got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{
		StartURL: "https://start.example/",
		StopURL:  "https://stop.example/",
		Region:   "eu-west-1",
	})
	cfg, err := LoadConfig(noEnv)
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
	cfg, err := LoadConfig(envMap(map[string]string{
		"OUTFIT_REMOTE_START_URL": "https://new/",
		"OUTFIT_REMOTE_REGION":    "eu-west-2",
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
	cfg, err := LoadConfig(noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-1" {
		t.Errorf("region should be derived from the URL, got %q", cfg.Region)
	}
}

func TestLoadConfig_Unconfigured(t *testing.T) {
	isolateConfig(t)
	_, err := LoadConfig(noEnv)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected a not-configured error, got %v", err)
	}
}

func TestLoadConfig_NoRegion(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "https://start.example/", StopURL: "https://stop.example/"})
	_, err := LoadConfig(noEnv)
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
	resp, err := Start(context.Background(), cfg, func(msg string) { progress = append(progress, msg) })
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
	resp, err := Start(context.Background(), cfg, func(msg string) { progress = append(progress, msg) })
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
	_, err := Start(ctx, cfg, func(string) {})
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
	_, err := Start(context.Background(), cfg, func(string) {})
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
	_, err := Start(ctx, cfg, func(string) {})
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

func TestLoadConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	content := `{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"eu-west-1"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFile(path, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartURL != "https://start.example/" || cfg.Region != "eu-west-1" {
		t.Errorf("unexpected config: %+v", cfg)
	}

	cfg, err = LoadConfigFile(path, envMap(map[string]string{"OUTFIT_REMOTE_REGION": "eu-west-2"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("env should override the file's region, got %q", cfg.Region)
	}
}

func TestLoadConfigFile_Missing(t *testing.T) {
	_, err := LoadConfigFile(filepath.Join(t.TempDir(), "remote.json"), noEnv)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected a does-not-exist error, got %v", err)
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
			"memory": {"total": 17179869184, "used": 4294967296}
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
	if resp.Tokens == nil || resp.Tokens.Requests != 342 {
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
