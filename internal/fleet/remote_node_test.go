package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
	"github.com/spinloop-ai/spinloop/internal/remote"
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

// Fleet operations on remote environments go through the same signed control
// calls as `spinloop remote`, so they inherit the stored control-plane
// credential: here the only credential the process can resolve is the one
// stored for the region (the ambient chain is empty), and the status call
// still signs and gets its answer. The file store behind SPINLOOP_REMOTE_KEYSTORE
// keeps the entry in a temp directory, never in the machine's keystore.
func TestRemoteNodeSignsWithStoredCredential(t *testing.T) {
	region := "ap-southeast-2"
	srv := remoteControlServer(t, `{"state":"stopped"}`, http.StatusOK)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SPINLOOP_CONFIG_DIR", "")
	t.Setenv("SPINLOOP_REMOTE_KEYSTORE", "file")
	// No ambient credential at all: env, profile, config files, IMDS.
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	cred := remote.StoredCredential{
		AccessKeyID:     "AKIATESTTESTTESTTEST",
		SecretAccessKey: "test-secret",
		Account:         "0",
		User:            "cloud-vm-llm-remote-cli",
		Region:          region,
		StoredAt:        time.Now().UTC(),
	}
	if err := remote.StoreCredential(cred); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	t.Cleanup(func() { remote.DeleteStoredCredential(region) })

	node, err := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: region})
	if err != nil {
		t.Fatal(err)
	}
	status, err := node.Status(context.Background())
	if err != nil {
		t.Fatalf("status with only a stored credential available: %v", err)
	}
	if status.State != "stopped" {
		t.Fatalf("status = %+v, want stopped", status)
	}
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
	requests := 3
	tokens := &metrics.TokenStats{Running: 2, PromptTokens: 5, GenerationTokens: 7, Requests: &requests}
	cpuPct := 30.0
	history := []metrics.HistorySample{{Time: 1, CPU: &cpuPct, GPUs: []metrics.HistoryGPU{{Index: 0, Util: 61, Mem: &cpuPct}}}}
	got := statsFromRemote(remote.StatsResponse{
		State: "running", Runner: "llamacpp", ModelID: "org/m", UptimeSeconds: 10,
		Tokens: tokens, LastActiveAt: "2026-01-02T00:00:00Z", IdleSeconds: 5, Version: "1.2.3",
		History:     history,
		RetainUntil: "2026-01-02T04:00:00Z",
	})
	if got.State != "running" || got.Runner != "llamacpp" || got.ModelID != "org/m" || got.UptimeSeconds != 10 {
		t.Errorf("statsFromRemote = %+v", got)
	}
	if got.Tokens == nil || got.Tokens.Running != 2 || got.Tokens.Requests == nil || *got.Tokens.Requests != 3 {
		t.Errorf("token stats not carried over: %+v", got.Tokens)
	}
	if got.IdleSeconds != 5 || got.LastActiveAt == "" {
		t.Errorf("activity not carried over: %+v", got)
	}
	// The daemon's retained readings relay through unaltered: the fleet does
	// not own them, so it copies them rather than rebuilding them.
	if len(got.History) != 1 || got.History[0].Time != 1 || got.History[0].CPU == nil ||
		*got.History[0].CPU != 30 || len(got.History[0].GPUs) != 1 || got.History[0].GPUs[0].Util != 61 {
		t.Errorf("history not carried over: %+v", got.History)
	}
	// And a reply without them stays without them.
	if got := statsFromRemote(remote.StatsResponse{State: "running"}); got.History != nil {
		t.Errorf("an absent history became present: %+v", got.History)
	}
	if got.RetainUntil != "2026-01-02T04:00:00Z" {
		t.Errorf("retainUntil not carried over: %q", got.RetainUntil)
	}
	// An environment with no live retention carries nothing: the field is
	// absent, not a zero time a formatter would have to special-case.
	if got := statsFromRemote(remote.StatsResponse{State: "running"}); got.RetainUntil != "" {
		t.Errorf("no retention should map to an empty retainUntil, got %q", got.RetainUntil)
	}
}

func TestLogsFromRemote(t *testing.T) {
	if got := logsFromRemote(nil, true); !got.Missing {
		t.Errorf("no fresh events on a from-the-beginning read should be reported missing, got %+v", got)
	}
	// No fresh events on a later poll — the common steady state once a follow
	// is caught up — must not be reported missing: the log is not gone, it is
	// just quiet right now.
	if got := logsFromRemote(nil, false); got.Missing {
		t.Errorf("no fresh events on a later poll should not be reported missing, got %+v", got)
	}

	t1 := time.UnixMilli(1000)
	t2 := time.UnixMilli(2000)
	got := logsFromRemote([]remote.LogEvent{
		{Message: "loading model", Timestamp: t1}, {Message: "server ready", Timestamp: t2},
	}, false)
	want := "loading model\nserver ready\n"
	if got.Content != want {
		t.Errorf("content = %q, want %q", got.Content, want)
	}
	if got.NextOffset != t2.UnixMilli() {
		t.Errorf("nextOffset = %d, want the newest event's millisecond %d", got.NextOffset, t2.UnixMilli())
	}
	if got.Size != int64(len(want)) {
		t.Errorf("size = %d, want %d", got.Size, len(want))
	}
}

// A remote node's log follow uses the exact same cursor as `spinloop remote
// logs -f` (remote.FollowCursor, seeded with remote.FollowOverlap) — not a
// lookalike reimplementation — so a second poll bounds its query behind the
// newest event already shown by the same overlap window, and does not show
// that event again. The bug this guards is the tail being replayed in full
// on every poll regardless of what was already shown.
func TestRemoteNodeLogsSharesTheFollowCursorWithRemoteLogsCommand(t *testing.T) {
	stubAWSCreds(t)
	eventMs := time.Date(2026, 8, 9, 11, 30, 0, 0, time.UTC).UnixMilli()

	var mu sync.Mutex
	served := map[string]bool{}
	var starts []*int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in struct {
			LogGroupName string `json:"logGroupName"`
			StartTime    *int64 `json:"startTime"`
		}
		json.Unmarshal(body, &in)

		mu.Lock()
		starts = append(starts, in.StartTime)
		first := !served[in.LogGroupName]
		served[in.LogGroupName] = true
		mu.Unlock()

		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if first {
			// Each group ships the same one event, the first time it is asked —
			// the fixture's stand-in for "the engine's log group has this".
			fmt.Fprintf(w, `{"events":[{"eventId":"e1","timestamp":%d,"logStreamName":"env-1/i-1","message":"hello"}]}`, eventMs)
			return
		}
		w.Write([]byte(`{"events":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_CLOUDWATCH_LOGS", srv.URL)
	cfg := remote.Config{StartURL: "http://x", StopURL: "http://x", Environment: "env-1", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}

	resp1, err := node.Logs(context.Background(), daemon.TailLog, 0)
	if err != nil {
		t.Fatalf("tail poll: %v", err)
	}
	if !strings.Contains(resp1.Content, "hello") {
		t.Fatalf("the tail poll should show the event, got %+v", resp1)
	}
	for _, s := range starts {
		if s != nil {
			t.Errorf("the tail poll (offset TailLog) sent a start bound: %v", *s)
		}
	}

	starts = nil
	resp2, err := node.Logs(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("follow-up poll: %v", err)
	}
	if resp2.Content != "" || resp2.Missing {
		t.Errorf("the follow-up poll should show nothing new and not report missing, got %+v", resp2)
	}
	wantStart := eventMs - remote.FollowOverlap.Milliseconds()
	for _, s := range starts {
		if s == nil {
			t.Fatal("the follow-up poll sent no start bound")
		}
		if *s != wantStart {
			t.Errorf("start bound = %d, want the shared overlap behind the last event (%d)", *s, wantStart)
		}
	}
}

// StartWith boots a deployed-but-stopped environment exactly as Start does,
// ignoring the config and key it is handed: what a remote environment
// serves, and the key that gates it, are fixed by `spinloop remote deploy`,
// not by a wake call.
func TestRemoteNodeStartWithBootsADeployedEnvironment(t *testing.T) {
	stubAWSCreds(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopped"}`))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	node, err := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := node.StartWith(context.Background(),
		&inference.DeployConfig{Runner: "llamacpp", ModelID: "org/other"}, "some-key")
	if err != nil {
		t.Fatalf("StartWith should boot a deployed environment: %v", err)
	}
	if status.State != "ready" {
		t.Errorf("status = %+v, want ready", status)
	}
}

// StartWith does not itself tell a deployed environment from an undeployed
// one by reading status: the control plane's status reply carries no deploy
// facts for a stopped environment either way (only a running one's does), so
// a status read cannot distinguish them. That check belongs to the caller —
// the gateway's own candidate matching confirms deployment from a stats read
// before a candidate is ever chosen — so StartWith just boots, whatever a
// status read would have said.
func TestRemoteNodeStartWithDoesNotItselfCheckDeployment(t *testing.T) {
	stubAWSCreds(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"undeployed"}`))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	node, err := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.StartWith(context.Background(), &inference.DeployConfig{Runner: "llamacpp"}, ""); err != nil {
		t.Fatalf("StartWith should not refuse on a status read alone: %v", err)
	}
}

func TestRemoteNodeStatusOverTheControlPlane(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t,
		`{"state":"running","healthy":true,"runner":"llamacpp","modelId":"org/m","servedName":"m",`+
			`"base_url":"http://1.2.3.4:8000/v1","lastActiveAt":"2026-01-02T00:00:00Z","idleSeconds":30}`, http.StatusOK)
	node, err := NewRemoteNode("env", remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	r := StatusCall(context.Background(), node)
	if !r.OK() || r.Status.State != "running" || r.Status.IdleSeconds != 30 {
		t.Errorf("status result = %+v", r)
	}
	// What the engine is serving rides the status, so a router can match a
	// request to this node: the model id and the served name beside it.
	if r.Status.Runner != "llamacpp" || r.Status.Model != "org/m" || r.Status.ServedName != "m" {
		t.Errorf("serving facts not mapped: %+v", r.Status)
	}
	// And where the engine answers: the control plane's published address, so
	// the router can reach the node rather than only name it.
	if r.Status.Engine == nil || r.Status.Engine.Host != "1.2.3.4" || r.Status.Engine.Port != 8000 {
		t.Errorf("engine address not mapped: %+v", r.Status.Engine)
	}
	if r.Name != "env" {
		t.Errorf("name = %q", r.Name)
	}
}

// A remote node's keep pins the instance over its control plane: the deadline
// is computed from the duration and sent as an absolute time, and the value
// returned to a caller is the control plane's own deadline, not the caller's
// clock plus the duration.
func TestRemoteNodeKeepOverTheControlPlane(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t,
		`{"retainUntil":"2026-01-02T04:00:00Z"}`, http.StatusOK)
	cfg := remote.Config{StartURL: srv.URL, StopURL: srv.URL, UpdateURL: srv.URL, Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := node.(Keeper).Keep(context.Background(), 4*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-01-02T04:00:00Z" {
		t.Errorf("keep returned %q, want the control plane's deadline", got)
	}
}

// When the control plane's reply omits the deadline — one that predates keep
// echoing the value back — the keep still succeeds and returns the requested
// deadline, so a caller always has something to show.
func TestRemoteNodeKeepFallsBackToTheRequestedDeadline(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t, `{}`, http.StatusOK)
	cfg := remote.Config{StartURL: srv.URL, StopURL: srv.URL, UpdateURL: srv.URL, Region: "us-east-1"}
	node, _ := NewRemoteNode("env", cfg)
	before := time.Now()
	got, err := node.(Keeper).Keep(context.Background(), 4*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	deadline, perr := time.Parse(time.RFC3339, got)
	if perr != nil {
		t.Fatalf("fallback deadline is not RFC 3339: %q", got)
	}
	want := before.Add(4 * time.Hour)
	if d := deadline.Sub(want); d < -5*time.Second || d > 5*time.Second {
		t.Errorf("fallback deadline = %s, want the requested now+4h (%s)", deadline, want)
	}
}

// A keep on an environment whose config has no update URL is a configuration
// error before any call, naming the fix — it is not a call that fails part-way.
func TestRemoteNodeKeepWithoutAnUpdateURL(t *testing.T) {
	stubAWSCreds(t)
	cfg := remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "us-east-1"}
	node, _ := NewRemoteNode("env", cfg)
	if _, err := node.(Keeper).Keep(context.Background(), time.Hour); err == nil ||
		!strings.Contains(err.Error(), "no update_url") {
		t.Errorf("expected a no-update-url error, got %v", err)
	}
}

// The keep capability is exactly one of the remote node's: a local daemon node
// has no retention tag to set, so it does not implement Keeper. A caller (the
// dashboard) relies on this boundary to decide whether to offer a keep at all.
func TestKeeperIsRemoteOnly(t *testing.T) {
	rn, _ := NewRemoteNode("env", remote.Config{StartURL: "http://x", StopURL: "http://x", Region: "r"})
	if _, ok := rn.(Keeper); !ok {
		t.Error("a remote node should implement Keeper")
	}
	dn := &daemonNode{name: "dev", client: &Client{BaseURL: "http://127.0.0.1:1", Token: "t"}}
	if _, ok := any(dn).(Keeper); ok {
		t.Error("a local daemon node should not implement Keeper")
	}
}

// statusFromRemote carries the serving facts when the daemon reports them, and
// leaves them empty when it does not — a running-but-unreachable daemon, or an
// engine that is not running a model, must not be invented into serving one.
func TestStatusFromRemoteServingFacts(t *testing.T) {
	with := statusFromRemote(remote.Response{
		State:      "running",
		Runner:     "llamacpp",
		ModelID:    "org/m",
		ServedName: "m",
	})
	if with.Runner != "llamacpp" || with.Model != "org/m" || with.ServedName != "m" {
		t.Errorf("serving facts should map across, got %+v", with)
	}
	without := statusFromRemote(remote.Response{State: "running"})
	if without.Runner != "" || without.Model != "" || without.ServedName != "" {
		t.Errorf("an absent serving fact must stay empty, got %+v", without)
	}
}

// statusFromRemote maps the control plane's own health check onto Ready the
// way a local daemon's reading is reported, so a router waiting for a
// remote engine to answer trusts it instead of a raw TCP probe — which can
// succeed well before the model has finished loading.
func TestStatusFromRemoteMapsHealthyOntoReady(t *testing.T) {
	if got := statusFromRemote(remote.Response{State: "running", Healthy: boolPtr(true)}); got.Ready != "ready" {
		t.Errorf("healthy=true should map to Ready=%q, got %q", "ready", got.Ready)
	}
	if got := statusFromRemote(remote.Response{State: "running", Healthy: boolPtr(false)}); got.Ready != "not-ready" {
		t.Errorf("healthy=false should map to Ready=%q, got %q", "not-ready", got.Ready)
	}
	// No healthy reading at all — an older control plane, or the branch
	// where the SSM agent is not yet reachable — leaves Ready empty rather
	// than claiming either answer.
	if got := statusFromRemote(remote.Response{State: "running"}); got.Ready != "" {
		t.Errorf("an absent healthy reading should leave Ready empty, got %q", got.Ready)
	}
}

// statusFromRemote carries a running environment's engine address — the
// control plane's published base url — as the engine's host, so routing can
// reach it the way it reaches any node. A stopped or undeployed environment
// reports none, so its status carries no engine address.
func TestStatusFromRemoteCarriesTheEngineAddress(t *testing.T) {
	got := statusFromRemote(remote.Response{
		State:   "running",
		BaseURL: "http://1.2.3.4:8000/v1",
	})
	if got.Engine == nil {
		t.Fatal("a running environment's engine address was not carried")
	}
	if got.Engine.Host != "1.2.3.4" || got.Engine.Port != 8000 || got.Engine.Path != "/v1" {
		t.Errorf("engine endpoint = %+v, want host 1.2.3.4 port 8000 path /v1", got.Engine)
	}
	// No base url — a stopped or undeployed environment — means no address.
	if got := statusFromRemote(remote.Response{State: "stopped"}); got.Engine != nil {
		t.Errorf("a stopped environment should carry no engine address: %+v", got.Engine)
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
	if stats.Tokens == nil || stats.Tokens.Running != 1 || stats.Tokens.Requests == nil || *stats.Tokens.Requests != 2 {
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
func (n *failingNode) StartWith(context.Context, *inference.DeployConfig, string) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *failingNode) Stop(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *failingNode) Logs(context.Context, int64, int) (daemon.LogsResponse, error) {
	return daemon.LogsResponse{}, nil
}

// StartWithProgress carries the boot up to the caller as phases: the attempt
// going out, then the instance coming up once a reply reports it.
func TestRemoteNodeStartWithProgressReportsTheBoot(t *testing.T) {
	stubAWSCreds(t)
	var (
		mu       sync.Mutex
		phases   []StartPhase
		attempts int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first { // the boot needs a moment; the endpoint says so
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"starting","retryAfterSeconds":1}`))
			return
		}
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := remote.Config{StartURL: srv.URL + "/start", StopURL: srv.URL + "/start", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	starter, ok := node.(ProgressStarter)
	if !ok {
		t.Fatal("the remote node does not carry progress")
	}
	resp, err := starter.StartWithProgress(context.Background(), func(p StartPhase) {
		mu.Lock()
		phases = append(phases, p)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("StartWithProgress: %v", err)
	}
	if resp.State != "ready" {
		t.Errorf("state = %q", resp.State)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Errorf("start attempts = %d (want 2)", attempts)
	}
	// The attempt goes out, and the reply reporting the instance coming up
	// replaces it. The second attempt is a poll of that same boot, so it does
	// not take the phase back to an attempt.
	want := []StartPhaseKind{PhaseAttempting, PhaseBooting}
	got := make([]StartPhaseKind, len(phases))
	for i, p := range phases {
		got[i] = p.Kind
	}
	if !slices.Equal(got, want) {
		t.Errorf("phases = %v (want %v)", got, want)
	}
	if last := phases[len(phases)-1]; last.Detail != "starting" {
		t.Errorf("the boot carries detail %q, want the control plane's state", last.Detail)
	}
}

// The defect this covers: a start refused for capacity, then granted. The
// refusal describes the attempt it refused, and is accurate only until the next
// attempt is issued; the attempt that obtains capacity then holds a single
// request open for the duration of the boot and reports nothing further. A
// caller that draws the start's latest situation in a fixed place (the
// dashboard tile) would show a capacity wait for the remainder of the start,
// alongside its own refreshes reporting the instance running. The last phase a
// start reports must therefore not be the capacity wait.
func TestRemoteNodeStartWithProgressRetiresACapacityWait(t *testing.T) {
	stubAWSCreds(t)
	var (
		mu       sync.Mutex
		phases   []StartPhase
		attempts int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first { // no GPU to be had in any zone yet
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"no-capacity","retryAfterSeconds":1}`))
			return
		}
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := remote.Config{StartURL: srv.URL + "/start", StopURL: srv.URL + "/start", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	starter := node.(ProgressStarter)
	if _, err := starter.StartWithProgress(context.Background(), func(p StartPhase) {
		mu.Lock()
		phases = append(phases, p)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("StartWithProgress: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(phases) == 0 {
		t.Fatal("the start reported nothing at all")
	}
	// The wait was reported when it was true, with the refusal's own delay...
	waited := slices.IndexFunc(phases, func(p StartPhase) bool {
		return p.Kind == PhaseWaitingCapacity && !p.RetryAt.IsZero()
	})
	if waited < 0 {
		t.Errorf("phases = %+v (want the capacity refusal reported with its due time)", phases)
	}
	// ...and retired by the attempt that superseded it.
	if last := phases[len(phases)-1]; last.Kind == PhaseWaitingCapacity {
		t.Errorf("the start's last phase is the retired capacity wait: %+v (all: %+v)", last, phases)
	}
}

// A nil progress callback is valid. remote.Start invokes progress on every
// retry path, so StartWithProgress must substitute a no-op rather than depend
// on which paths a given start takes; this exercises a start that retries.
func TestRemoteNodeStartWithProgressAcceptsNoReporter(t *testing.T) {
	stubAWSCreds(t)
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"no-capacity","retryAfterSeconds":1}`))
			return
		}
		w.Write([]byte(`{"state":"ready","healthy":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := remote.Config{StartURL: srv.URL + "/start", StopURL: srv.URL + "/start", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.(ProgressStarter).StartWithProgress(context.Background(), nil); err != nil {
		t.Fatalf("StartWithProgress with no reporter: %v", err)
	}
}

// A start the control plane refuses, and a stop it refuses, both surface as
// errors the node's caller can show on its row.
func TestRemoteNodeRejectedOperationsSurfaceAsErrors(t *testing.T) {
	stubAWSCreds(t)
	srv := remoteControlServer(t, `{"error":"boom"}`, http.StatusInternalServerError)
	cfg := remote.Config{StartURL: srv.URL, StopURL: srv.URL, Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	starter, ok := node.(ProgressStarter)
	if !ok {
		t.Fatal("the remote node does not carry progress")
	}
	if _, err := starter.StartWithProgress(ctx, nil); err == nil {
		t.Error("a rejected start did not fail")
	}
	if _, err := node.Stop(ctx); err == nil {
		t.Error("a rejected stop did not fail")
	}
}

// A failing log read surfaces as an error, not as an empty tail: the caller
// renders the detail rather than reporting "no logs" when the read itself
// went wrong.
func TestRemoteNodeLogsErrorsSurface(t *testing.T) {
	stubAWSCreds(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest) // a client error: the SDK will not retry it
		w.Write([]byte(`{"message":"boom"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_CLOUDWATCH_LOGS", srv.URL)
	cfg := remote.Config{StartURL: "http://x", StopURL: "http://x", Environment: "env-1", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Logs(context.Background(), 0, 100); err == nil {
		t.Error("a failing log read surfaced as success")
	}
}

// A log store that answers with an empty tail is a readable state — the
// engine has not logged here — so the node reports missing, not an error.
func TestRemoteNodeLogsEmptyTailIsMissingNotFailure(t *testing.T) {
	stubAWSCreds(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Write([]byte(`{"events":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_CLOUDWATCH_LOGS", srv.URL)
	cfg := remote.Config{StartURL: "http://x", StopURL: "http://x", Environment: "env-1", Region: "us-east-1"}
	node, err := NewRemoteNode("env", cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := node.Logs(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("an empty tail is not a failure: %v", err)
	}
	if !got.Missing {
		t.Errorf("an empty tail should be reported missing: %+v", got)
	}
}
