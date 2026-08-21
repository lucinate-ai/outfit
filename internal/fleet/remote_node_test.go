package fleet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// stubAWSCreds pins the AWS credential chain to static environment credentials
// and disables the EC2 metadata lookup, so a signed control call can run against
// an httptest server with no network or real account.
func stubAWSCreds(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATESTTESTTESTTEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func boolPtr(b bool) *bool { return &b }

// remoteControlServer serves the shape a remote control endpoint answers.
func remoteControlServer(t *testing.T, body string, statusCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewRemoteNodeRequiresACompleteConfig(t *testing.T) {
	if _, err := NewRemoteNode("env", remote.Config{StopURL: "http://x", Region: "r"}); err == nil {
		t.Error("missing start_url should be a configuration error")
	}
	if _, err := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x"}); err == nil {
		t.Error("missing region should be a configuration error")
	}
	if _, err := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "r"}); err != nil {
		t.Errorf("a complete config should build a node: %v", err)
	}
}

func TestStatusFromRemote(t *testing.T) {
	got := statusFromRemote(remote.Response{
		State: "running", Healthy: boolPtr(true), BaseURL: "http://1.2.3.4:8000/v1",
		LastActiveAt: "2026-01-02T00:00:00Z", IdleSeconds: 30,
	})
	if got.State != "running" || got.IdleSeconds != 30 || got.LastActiveAt != "2026-01-02T00:00:00Z" {
		t.Errorf("statusFromRemote = %+v", got)
	}
	// A stopped endpoint reports no activity: nothing to measure from.
	if got := statusFromRemote(remote.Response{State: "stopped"}); got.State != "stopped" || got.LastActiveAt != "" {
		t.Errorf("stopped status = %+v", got)
	}
}

func TestStatsFromRemote(t *testing.T) {
	tokens := &metrics.TokenStats{Running: 2, PromptTokens: 5, GenerationTokens: 7, Requests: 3}
	got := statsFromRemote(remote.StatsResponse{
		State: "running", Runner: "llamacpp", ModelID: "org/m", UptimeSeconds: 10,
		Tokens: tokens, LastActiveAt: "2026-01-02T00:00:00Z", IdleSeconds: 5, Version: "1.2.3",
	})
	if got.State != "running" || got.Runner != "llamacpp" || got.ModelID != "org/m" || got.UptimeSeconds != 10 {
		t.Errorf("statsFromRemote = %+v", got)
	}
	if got.Tokens == nil || got.Tokens.Running != 2 || got.Tokens.Requests != 3 {
		t.Errorf("token stats not carried over: %+v", got.Tokens)
	}
	if got.IdleSeconds != 5 || got.LastActiveAt == "" {
		t.Errorf("activity not carried over: %+v", got)
	}
}

func TestLogsFromRemote(t *testing.T) {
	if got := logsFromRemote(remote.LogResult{}); !got.Missing {
		t.Errorf("an empty tail should be reported as a missing log, got %+v", got)
	}
	got := logsFromRemote(remote.LogResult{Events: []remote.LogEvent{
		{Message: "loading model"}, {Message: "server ready"},
	}})
	want := "loading model\nserver ready"
	if got.Content != want {
		t.Errorf("content = %q, want %q", got.Content, want)
	}
	if got.NextOffset != int64(len(want)) || got.Size != int64(len(want)) {
		t.Errorf("offset/size = %d/%d, want %d", got.NextOffset, got.Size, len(want))
	}
}

func TestRemoteNodeStartWithIsRefused(t *testing.T) {
	node, _ := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "r"})
	_, err := node.StartWith(context.Background(), &remote.DeployConfig{Runner: "llamacpp"}, "")
	if err == nil || !strings.Contains(err.Error(), "outfit remote deploy") {
		t.Errorf("StartWith should refuse, naming the deploy path; got %v", err)
	}
}

func TestRemoteNodeStatusOverTheControlPlane(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t,
		`{"state":"running","healthy":true,"lastActiveAt":"2026-01-02T00:00:00Z","idleSeconds":30}`, http.StatusOK)
	node, err := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	r := StatusCall(context.Background(), node)
	if !r.OK() || r.Status.State != "running" || r.Status.IdleSeconds != 30 {
		t.Errorf("status result = %+v", r)
	}
	if r.Name != "env" {
		t.Errorf("name = %q", r.Name)
	}
}

// A remote node drives start, stop and metrics over its control plane exactly
// like a node would, mapping each reply onto the node's types.
func TestRemoteNodeStartStopMetricsOverTheControlPlane(t *testing.T) {
	stubAWSCreds(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopped","healthy":false}`))
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"running","runner":"llamacpp","modelId":"org/m","uptimeSeconds":9,"tokens":{"running":1,"requests":2},"version":"1.0.0"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := remote.Config{StartURL: srv.URL + "/start", StopURL: srv.URL + "/stop", StatsURL: srv.URL + "/stats", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	started, err := node.Start(ctx)
	if err != nil || started.State != "ready" {
		t.Errorf("Start = %+v, %v", started, err)
	}
	stats, err := node.Metrics(ctx)
	if err != nil || stats.State != "running" || stats.ModelID != "org/m" {
		t.Errorf("Metrics = %+v, %v", stats, err)
	}
	if stats.Tokens == nil || stats.Tokens.Running != 1 || stats.Tokens.Requests != 2 {
		t.Errorf("metrics tokens not mapped: %+v", stats.Tokens)
	}
	stopped, err := node.Stop(ctx)
	if err != nil || stopped.State != "stopped" {
		t.Errorf("Stop = %+v, %v", stopped, err)
	}
}

// A node whose config has no stats endpoint reports that, rather than a silent
// empty result: a control plane predating stats is a readable state.
func TestRemoteNodeMetricsWithoutStatsURL(t *testing.T) {
	node, err := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "r"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Metrics(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stats_url") {
		t.Errorf("metrics for a config with no stats_url should name the gap, got %v", err)
	}
}

// A node for an environment with no name cannot be pointed at a log stream, so a
// log read fails before it touches CloudWatch.
func TestRemoteNodeLogsWithoutAnEnvironmentFails(t *testing.T) {
	node, _ := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "r"})
	_, err := node.Logs(context.Background(), daemon.TailLog, 100)
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Errorf("a log read for an environment with no name should fail, got %v", err)
	}
}

func TestRemoteNodeRejectedCallIsAOutcomeNotAFailure(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t, `{"error":"boom"}`, http.StatusInternalServerError)
	node, _ := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"})
	r := StatusCall(context.Background(), node)
	// A rejected call is a typed outcome carrying the reason, not an error that
	// would blank the rest of the node set.
	if r.OK() {
		t.Fatal("a 500 control reply should not be OK")
	}
	if !strings.Contains(r.Detail(), "boom") {
		t.Errorf("detail should carry the reason, got %q", r.Detail())
	}
}

// A mixed set — a local daemon node and a remote environment — is observed
// through the one fan-out, in order, and one failing member does not stop the
// rest.
func TestFanOutNodesOverAMixedSet(t *testing.T) {
	stubAWSCreds(t)

	up := stubDaemon(t, "", "running")
	cfg := fleetFor(t, up, "")
	entry, _ := cfg.Node("box")
	dnode, err := cfg.NewNode(entry)
	if err != nil {
		t.Fatal(err)
	}

	remoteUp := remoteControlServer(t, `{"state":"running","healthy":true}`, http.StatusOK)
	rnode, _ := NewRemoteNode("env", remote.Config{StartURL: remoteUp.URL, StopURL: remoteUp.URL, Region: "us-east-1"})

	results := FanOutNodes(context.Background(), StatusCall, []Node{dnode, rnode})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Order is the order given, so the two kinds sit side by side.
	if results[0].Name != "box" || results[1].Name != "env" {
		t.Fatalf("order = %q, %q; want box then env", results[0].Name, results[1].Name)
	}
	if !results[0].OK() || results[0].Status.State != "running" {
		t.Errorf("daemon node result = %+v", results[0])
	}
	if !results[1].OK() || results[1].Status.State != "running" {
		t.Errorf("remote node result = %+v", results[1])
	}
}

func TestFanOutNodesKeepsAGoodNodeWhenAnotherFails(t *testing.T) {
	stubAWSCreds(t)
	good := remoteControlServer(t, `{"state":"running"}`, http.StatusOK)
	rnode, _ := NewRemoteNode("env", remote.Config{StartURL: good.URL, StopURL: good.URL, Region: "us-east-1"})

	results := FanOutNodes(context.Background(), StatusCall, []Node{&failingNode{name: "bad"}, rnode})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].OK() {
		t.Error("the failing node should not be OK")
	}
	if !results[1].OK() {
		t.Errorf("a good node in the set should still be observed: %+v", results[1])
	}
}

// failingNode is a node whose status call always errors, for fan-out tests.
type failingNode struct{ name string }

func (n *failingNode) Name() string { return n.name }
func (n *failingNode) Status(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, fmt.Errorf("no such host: %s", n.name)
}
func (n *failingNode) Metrics(context.Context) (metrics.Stats, error) {
	return metrics.Stats{}, nil
}
func (n *failingNode) Start(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *failingNode) StartWith(context.Context, *remote.DeployConfig, string) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *failingNode) Stop(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *failingNode) Logs(context.Context, int64, int) (daemon.LogsResponse, error) {
	return daemon.LogsResponse{}, nil
}
