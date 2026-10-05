package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// registerRemoteEnv points the environment registry (SPINLOOP_CONFIG_DIR) at
// a temp config directory and writes one environment's remote.json, whose
// control plane — start, stop and stats alike — is the server url given, and
// stubs the AWS credential chain so a signed control call reaches it.
// Reproduced from internal/fleet's own helper of the same name because it
// lives in a different package.
func registerRemoteEnv(t *testing.T, name, url string) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATESTTESTTESTTEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	home := t.TempDir()
	t.Setenv("SPINLOOP_CONFIG_DIR", home)
	envDir := filepath.Join(home, "remotes", name)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"start_url":%q,"stop_url":%q,"stats_url":%q,"region":"us-east-1","environment":%q}`,
		url, url, url+"/stats", name)
	if err := os.WriteFile(filepath.Join(envDir, "remote.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeNode is one machine: its daemon's control API and, once running, its
// engine's real HTTP endpoint on the port the daemon reports — so the
// gateway's proxy path is exercised end to end against a listener, not a
// stub. The engine's port is reserved at construction and occupied only when
// the engine is meant to be up, which is what readiness means here.
type fakeNode struct {
	mu sync.Mutex
	// state and what the daemon reports serving.
	state      string
	model      string
	servedName string
	// ready is what the daemon reports for `ready`: daemon.ReadyYes,
	// daemon.ReadyNo, or empty for a daemon whose reading has not landed —
	// an older build, or a runner with no known health-check convention.
	ready string
	// engineAuth, when set, is what the engine requires as its key.
	engineAuth string
	// startErr and startStatus are a refused start's reply.
	startErr    string
	startStatus int
	// engineDelay is how long after a start the engine listens.
	engineDelay time.Duration
	// noEngine keeps the engine down even after an accepted start:
	// readiness can only come from the daemon's own reading.
	noEngine bool
	// loopbackOnly is what the daemon reports for its engine's bind.
	loopbackOnly bool
	// started counts accepted starts; pushed is the last config and key.
	started   int
	pushed    *inference.DeployConfig
	pushedKey string
	// engineGotAuth is the last authorisation the engine itself saw.
	engineGotAuth string
	// engineGotBody is the last request body the engine itself read.
	engineGotBody string
	// statusHits counts status calls, so a burst's fan-out is countable.
	statusHits int

	daemonSrv  *httptest.Server
	engine     *http.Server
	engineLn   net.Listener
	enginePort int
}

func newFakeNode(t *testing.T, state, model string) *fakeNode {
	t.Helper()
	f := &fakeNode{state: state, model: model}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.enginePort = ln.Addr().(*net.TCPAddr).Port
	ln.Close() // free it until the engine is meant to be up
	f.engine = &http.Server{Handler: f.engineHandler()}
	// A node already running has its engine up: its port answers.
	if state == string(daemon.StateRunning) {
		f.upAsEngine()
	}
	f.daemonSrv = httptest.NewServer(f.daemonMux())
	t.Cleanup(func() {
		f.daemonSrv.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.engineLn != nil {
			f.engineLn.Close()
		}
	})
	return f
}

// engineHandler is the engine itself: it checks its key, streams when asked,
// and fails when told to.
func (f *fakeNode) engineHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.engineGotAuth = r.Header.Get("Authorization")
		auth := f.engineAuth
		f.mu.Unlock()
		if auth != "" && f.engineGotAuth != "Bearer "+auth {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"bad key"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.engineGotBody = string(body)
		f.mu.Unlock()
		switch {
		case strings.Contains(string(body), `"stream":true`):
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			for _, chunk := range []string{"data: one\n\n", "data: two\n\n", "data: [DONE]\n\n"} {
				io.WriteString(w, chunk)
				fl.Flush()
			}
		case strings.Contains(string(body), `"fail":true`):
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"the engine is on fire"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
		}
	})
}

func (f *fakeNode) daemonMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.statusHits++
		resp := daemon.StatusResponse{State: f.state, Model: f.model, ServedName: f.servedName}
		if f.state == string(daemon.StateRunning) {
			resp.Engine = &daemon.EngineEndpoint{
				Port:         f.enginePort,
				LoopbackOnly: f.loopbackOnly,
				RequiresKey:  f.engineAuth != "",
			}
			resp.Ready = f.ready
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if f.started > 0 && f.state == string(daemon.StateRunning) && f.startErr == "" {
			// The daemon's own conflict: another start while one runs.
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(daemon.Error{Error: "an engine is already running"})
			f.mu.Unlock()
			return
		}
		if f.startErr != "" {
			status := f.startStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(daemon.Error{Error: f.startErr})
			f.mu.Unlock()
			return
		}
		var req daemon.StartRequest
		json.NewDecoder(r.Body).Decode(&req)
		dc := req.DeployConfig
		f.pushed = &dc
		f.pushedKey = req.EngineAPIKey
		f.started++
		f.state = string(daemon.StateRunning)
		if dc.ModelID != "" {
			f.model = dc.ModelID
		}
		f.servedName = dc.ServedModelName
		delay := f.engineDelay
		noEngine := f.noEngine
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(daemon.StatusResponse{State: f.state, Model: f.model})
		f.mu.Unlock()
		if !noEngine {
			go func() {
				time.Sleep(delay)
				f.upAsEngine()
			}()
		}
	})
	return mux
}

// upAsEngine occupies the reserved port, which readiness probes and the proxy
// both dial.
func (f *fakeNode) upAsEngine() {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.enginePort)))
	if err != nil {
		return
	}
	f.mu.Lock()
	f.engineLn = ln
	f.mu.Unlock()
	go f.engine.Serve(ln)
}

// nodeConfig is the fleet-file entry pointing at this fake.
func (f *fakeNode) nodeConfig(name string) fleet.NodeConfig {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(f.daemonSrv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return fleet.NodeConfig{Name: name, Host: host, Port: p, Kind: fleet.KindDaemon}
}

// fleetOf builds a Config over the fakes, in the order given.
func fleetOf(t *testing.T, names []string, nodes ...*fakeNode) *fleet.Config {
	t.Helper()
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir()}
	for i, n := range nodes {
		cfg.Nodes = append(cfg.Nodes, n.nodeConfig(names[i]))
	}
	return cfg
}

// cfgForOf is a per-node source resolver from a table: what each node's own
// Spinloop source would resolve to, or its refusal.
func cfgForOf(t *testing.T, table map[string]inference.DeployConfig, refused map[string]string) fleet.ConfigFor {
	return func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
		if msg, ok := refused[entry.Name]; ok {
			return inference.DeployConfig{}, fmt.Errorf("%s", msg)
		}
		dc, ok := table[entry.Name]
		if !ok {
			return inference.DeployConfig{}, fmt.Errorf("node %q names no Spinloop source: no `file` field, no alias, no subdirectory", entry.Name)
		}
		return dc, nil
	}
}

// intPtr is a pointer to an int, for a declared limit.
func intPtr(n int) *int { return &n }

// post sends one completion request to a handler and returns the reply.
func post(t *testing.T, h http.Handler, token string, body string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://gw/v1/chat/completions", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	data, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(data)
}

// --- the surface ----------------------------------------------------------

func TestListenRefusesTokenlessNonLoopback(t *testing.T) {
	_, err := Listen("0.0.0.0:0", "")
	if err == nil {
		t.Fatal("a tokenless non-loopback listen should be refused")
	}
	for _, want := range []string{"--api-token-file", "SPINLOOP_API_TOKEN", "--api-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
	ln, err := Listen("127.0.0.1:0", "")
	if err != nil {
		t.Fatalf("a tokenless loopback listen is allowed: %v", err)
	}
	ln.Close()
}

func TestCallerAuthentication(t *testing.T) {
	cfg := fleetOf(t, []string{"box"}, newFakeNode(t, string(daemon.StateIdle), ""))
	h := New(cfg, "secret", Options{})

	// Missing and wrong tokens are 401, and no node is contacted.
	for _, tok := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodGet, "http://gw/health", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: HTTP %d, want 401", tok, rec.Code)
		}
	}

	// The right one is through.
	req := httptest.NewRequest(http.MethodGet, "http://gw/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("right token: HTTP %d, want 200", rec.Code)
	}
}

func TestTokenlessLoopbackServesWithoutAuth(t *testing.T) {
	cfg := fleetOf(t, []string{"box"}, newFakeNode(t, string(daemon.StateIdle), ""))
	h := New(cfg, "", Options{})
	req := httptest.NewRequest(http.MethodGet, "http://gw/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d, want 200 with no token", rec.Code)
	}
}

func TestHealthTouchesNoNode(t *testing.T) {
	// A node on a dead port: unreachable, but health must not care.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "down", Host: "127.0.0.1", Port: port, Kind: fleet.KindDaemon},
	}}
	h := New(cfg, "", Options{})
	req := httptest.NewRequest(http.MethodGet, "http://gw/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health with every node down: HTTP %d, want 200", rec.Code)
	}
}

func TestUnknownPathNamesTheSurface(t *testing.T) {
	cfg := fleetOf(t, []string{"box"}, newFakeNode(t, string(daemon.StateIdle), ""))
	h := New(cfg, "", Options{})
	req := httptest.NewRequest(http.MethodGet, "http://gw/v1/embeddings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("HTTP %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"/v1/models", "/v1/chat/completions", "/v1/completions", "/v1/fleet", "/health"} {
		if !strings.Contains(body, want) {
			t.Errorf("404 should name %s, got: %s", want, body)
		}
	}
}

// --- models ----------------------------------------------------------------

// modelsList makes one models request and returns the ids it lists.
func modelsList(t *testing.T, h http.Handler) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gw/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d, want 200", rec.Code)
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&list)
	var ids []string
	for _, m := range list.Data {
		ids = append(ids, m["id"].(string))
	}
	return ids
}

func TestModelsListsWhatIsRunning(t *testing.T) {
	aliased := newFakeNode(t, string(daemon.StateRunning), "org/model")
	aliased.servedName = "the-alias"
	plain := newFakeNode(t, string(daemon.StateRunning), "org/other")
	stopped := newFakeNode(t, string(daemon.StateStopped), "org/stale")
	cfg := fleetOf(t, []string{"aliased", "plain", "stopped"}, aliased, plain, stopped)
	h := New(cfg, "", Options{})

	ids := modelsList(t, h)
	if !slices.Contains(ids, "the-alias") || !slices.Contains(ids, "org/other") {
		t.Errorf("models should list the running names, got %v", ids)
	}
	if slices.Contains(ids, "org/stale") {
		t.Error("a stopped node with no resolved source contributes nothing")
	}
}

func TestModelsEmptyWhenNothingIsReachable(t *testing.T) {
	cfg := fleetOf(t, []string{"box"}, newFakeNode(t, string(daemon.StateIdle), ""))
	h := New(cfg, "", Options{})
	if ids := modelsList(t, h); len(ids) != 0 {
		t.Errorf("nothing reachable is an empty list, not %v", ids)
	}
}

// With waking allowed, a node that is not running contributes the model its
// own source describes — the served name when the source gives one — so a
// client sees the models it can ask for, not only the ones answering now.
func TestModelsListsWhatAWakeCanStart(t *testing.T) {
	live := newFakeNode(t, string(daemon.StateRunning), "org/live")
	cold := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"live", "cold"}, live, cold)
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{
			"live": {Runner: "llamacpp", ModelID: "org/live"},
			"cold": {Runner: "llamacpp", ModelID: "org/cold", ServedModelName: "cold"},
		},
		nil)})

	ids := modelsList(t, h)
	if !slices.Contains(ids, "org/live") || !slices.Contains(ids, "cold") {
		t.Errorf("models should list what runs and what a wake can start, got %v", ids)
	}
	if slices.Contains(ids, "org/cold") {
		t.Error("the source's served name, not its model id, is what a request names")
	}
}

// A running engine is never displaced to make room, so a running node lists
// what it reports and nothing its source describes.
func TestRunningNodeListsOnlyWhatItReports(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/live")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/other"}},
		nil)})
	if ids := modelsList(t, h); len(ids) != 1 || ids[0] != "org/live" {
		t.Errorf("a running node lists what it reports, got %v", ids)
	}
}

// With the fleet's wake off, nothing is started, so nothing is listed beyond
// what answers now.
func TestModelsListsOnlyWhatRunsWhenWakeIsOff(t *testing.T) {
	cold := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"cold"}, cold)
	cfg.WakePolicy = fleet.WakeOff
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{"cold": {Runner: "llamacpp", ModelID: "org/cold"}},
		nil)})
	if ids := modelsList(t, h); len(ids) != 0 {
		t.Errorf("with wake off a stopped node's model is not listed, got %v", ids)
	}
}

// A deployed-but-stopped remote environment's model comes from its own
// stats reply — the environment's stored deploy config, read directly by
// the stats Lambda — not from its status reply, which carries no deploy
// facts while the environment is stopped, and not from a Spinloop source.
// So it is wakeable and listed the same as a daemon node's, under the same
// served-name-first naming: the stats reply carries the served name beside
// the model id, so a stopped environment lists the same name it would report
// while running rather than the bare model id.
func TestModelsListsADeployedRemoteEnvironment(t *testing.T) {
	url, _ := remoteControlServer(t)
	registerRemoteEnv(t, "env", url)
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "env", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})
	m := h.wakeableModels(context.Background())
	if got := m["env"]; got != "deployed" {
		t.Errorf("wakeableModels()[env] = %q, want the served name from its stats reply", got)
	}
}

// An undeployed remote environment's stats read fails outright — the stats
// Lambda has no deploy config to read — so it has nothing to be woken with
// and is not listed.
func TestModelsLeavesOutAnUndeployedRemoteEnvironment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"undeployed"}`))
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"cannot read deploy config: run spinloop remote deploy first"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	registerRemoteEnv(t, "env", srv.URL)

	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "env", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})
	if m := h.wakeableModels(context.Background()); len(m) != 0 {
		t.Errorf("an undeployed remote environment has nothing to start it with, got %v", m)
	}
}

// A remote node with its own wake disabled is not listed, even though it is
// deployed, and even under a fleet that wakes.
func TestModelsLeavesOutARemoteEnvironmentWithWakeDisabled(t *testing.T) {
	url, _ := remoteControlServer(t)
	registerRemoteEnv(t, "env", url)
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "env", Kind: fleet.KindRemote, WakePolicy: fleet.WakeOff},
	}}
	h := New(cfg, "", Options{})
	if m := h.wakeableModels(context.Background()); len(m) != 0 {
		t.Errorf("a node with its own wake disabled should not be listed, got %v", m)
	}
}

// A remote node whose environment is not registered fails before any network
// call, naming the node the way a daemon node with no resolvable Spinloop
// source would.
func TestRemoteConfigForUnregisteredEnvironment(t *testing.T) {
	t.Setenv("SPINLOOP_CONFIG_DIR", t.TempDir())
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "env", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})
	cfgFor := h.remoteConfigFor(context.Background())
	if _, err := cfgFor(fleet.NodeConfig{Name: "env", Kind: fleet.KindRemote}); err == nil ||
		!strings.Contains(err.Error(), "env") {
		t.Errorf("an unregistered environment should fail naming it, got %v", err)
	}
}

// Two sources describing one model list it once.
func TestModelsListsASharedSourceModelOnce(t *testing.T) {
	a := newFakeNode(t, string(daemon.StateStopped), "")
	b := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"a", "b"}, a, b)
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{
			"a": {Runner: "llamacpp", ModelID: "org/same"},
			"b": {Runner: "llamacpp", ModelID: "org/same"},
		},
		nil)})
	ids := modelsList(t, h)
	if len(ids) != 1 || ids[0] != "org/same" {
		t.Errorf("a model two sources describe is listed once, got %v", ids)
	}
}

// A burst of models requests resolves each node's source once; a request
// after the reading ages resolves it again.
func TestModelsResolvesEachSourceOnce(t *testing.T) {
	cold := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"cold"}, cold)
	now := time.Now()
	calls := 0
	h := New(cfg, "", Options{
		Now: func() time.Time { return now },
		ConfigFor: func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
			calls++
			return inference.DeployConfig{Runner: "llamacpp", ModelID: "org/cold"}, nil
		},
	})
	for range 3 {
		modelsList(t, h)
	}
	if calls != 1 {
		t.Fatalf("a burst of three resolved the source %d times, want once", calls)
	}
	now = now.Add(sourcesTTL + time.Second)
	modelsList(t, h)
	if calls != 2 {
		t.Fatalf("the aged reading should have resolved the source once more, got %d total", calls)
	}
}

// --- topology -----------------------------------------------------------------

// topologyFetch makes one topology request and returns the decoded reply,
// failing the test when the reply is not a 200 topology.
func topologyFetch(t *testing.T, h http.Handler, token string) *Topology {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gw/v1/fleet", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d, want 200: %s", rec.Code, rec.Body.String())
	}
	topo := new(Topology)
	if err := json.NewDecoder(rec.Body).Decode(topo); err != nil {
		t.Fatalf("the reply is not a topology: %v", err)
	}
	return topo
}

// The reply joins the reading with the file's claims: tags and settings as
// declared, serving facts as reported, in the file's order.
func TestTopologyReportsTheFleet(t *testing.T) {
	live := newFakeNode(t, string(daemon.StateRunning), "org/live")
	live.servedName = "the-alias"
	live.ready = daemon.ReadyYes
	cold := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"gpu-a", "cpu-a"}, live, cold)
	cfg.Nodes[0].Tags = map[string]string{"gpu": "a100", "region": "eu"}
	cfg.Nodes[1].Tags = map[string]string{"cpu": "big"}
	cfg.Prefer = fleet.PreferActive
	cfg.Concurrency = &fleet.Concurrency{Total: intPtr(4), Tags: map[string]int{"gpu=a100": 2}}
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{"cpu-a": {Runner: "llamacpp", ModelID: "org/cold"}},
		nil)})

	topo := topologyFetch(t, h, "")
	if !topo.Wake {
		t.Error("a fleet that declares no wake policy wakes; the reply should say so")
	}
	if topo.Prefer != "active" {
		t.Errorf("prefer should be the file's, got %q", topo.Prefer)
	}
	if topo.Concurrency == nil || topo.Concurrency.Total == nil || *topo.Concurrency.Total != 4 {
		t.Errorf("the fleet-wide limit should be the file's, got %+v", topo.Concurrency)
	}
	if got := topo.Concurrency.Tags["gpu=a100"]; got != 2 {
		t.Errorf("the tag limit should be the file's, got %d", got)
	}
	if len(topo.Nodes) != 2 || topo.Nodes[0].Name != "gpu-a" || topo.Nodes[1].Name != "cpu-a" {
		t.Fatalf("nodes should be in the file's order, got %+v", topo.Nodes)
	}
	gpu := topo.Nodes[0]
	if gpu.Kind != fleet.KindDaemon || gpu.Tags["gpu"] != "a100" || gpu.Tags["region"] != "eu" {
		t.Errorf("the file's claims should ride on the node, got %+v", gpu)
	}
	if gpu.State != string(daemon.StateRunning) || gpu.Model != "org/live" ||
		gpu.ServedName != "the-alias" || gpu.Ready != daemon.ReadyYes {
		t.Errorf("a running node reports what it serves, got %+v", gpu)
	}
	if gpu.WakeableModel != "" {
		t.Errorf("a running engine is never displaced, so it names no wakeable model, got %q", gpu.WakeableModel)
	}
	cpu := topo.Nodes[1]
	if cpu.State != string(daemon.StateStopped) || cpu.WakeableModel != "org/cold" {
		t.Errorf("a stopped node names what a request would start it with, got %+v", cpu)
	}
}

// A node that does not answer is reported in its place, the way the fleet's
// own views report it; the rest of the reply stands.
func TestTopologyReportsADeadNodeInPlace(t *testing.T) {
	live := newFakeNode(t, string(daemon.StateRunning), "org/live")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := fleetOf(t, []string{"gpu-a", "down"}, live)
	cfg.Nodes = append(cfg.Nodes, fleet.NodeConfig{
		Name: "down", Host: "127.0.0.1", Port: port, Kind: fleet.KindDaemon,
	})
	h := New(cfg, "", Options{})

	topo := topologyFetch(t, h, "")
	if len(topo.Nodes) != 2 {
		t.Fatalf("both nodes should be reported, got %+v", topo.Nodes)
	}
	if topo.Nodes[0].State != string(daemon.StateRunning) {
		t.Errorf("the live node should report as it is, got %+v", topo.Nodes[0])
	}
	dead := topo.Nodes[1]
	if dead.State != string(fleet.OutcomeUnreachable) || dead.Detail == "" {
		t.Errorf("a dead node is reported with its outcome and its detail, got %+v", dead)
	}
	if dead.Model != "" || dead.WakeableModel != "" {
		t.Errorf("a node that does not answer reports no serving facts, got %+v", dead)
	}
}

// Settings the file does not declare are absent from the reply, not filled
// with defaults a consumer could mistake for a declaration.
func TestTopologySettingsAbsentWhereUndeclared(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{})

	req := httptest.NewRequest(http.MethodGet, "http://gw/v1/fleet", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["prefer"]; ok {
		t.Error("an undeclared preference should be absent, not default-filled")
	}
	if _, ok := raw["concurrency"]; ok {
		t.Error("an undeclared concurrency section should be absent")
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(raw["nodes"], &nodes); err != nil {
		t.Fatalf("nodes should be an array: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("one node should be reported, got %d", len(nodes))
	}
	var first map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0], &first); err != nil {
		t.Fatalf("the node should be an object: %v", err)
	}
	if _, ok := first["tags"]; ok {
		t.Error("a node the file gives no tags should carry none")
	}
}

// With the fleet's wake off, no node is started on a request, so no node
// names a wakeable model — and the reply says the fleet does not wake.
func TestTopologyNamesNoWakeableModelWhenWakeIsOff(t *testing.T) {
	cold := newFakeNode(t, string(daemon.StateStopped), "")
	cfg := fleetOf(t, []string{"cold"}, cold)
	cfg.WakePolicy = fleet.WakeOff
	h := New(cfg, "", Options{ConfigFor: cfgForOf(t,
		map[string]inference.DeployConfig{"cold": {Runner: "llamacpp", ModelID: "org/cold"}},
		nil)})

	topo := topologyFetch(t, h, "")
	if topo.Wake {
		t.Error("the reply should say the fleet does not wake")
	}
	if got := topo.Nodes[0].WakeableModel; got != "" {
		t.Errorf("nothing is started when wake is off, got %q", got)
	}
}

// The topology rides the gateway's own authentication: the token through,
// its absence refused as on every other path.
func TestTopologyBehindTheCallerToken(t *testing.T) {
	cfg := fleetOf(t, []string{"box"}, newFakeNode(t, string(daemon.StateIdle), ""))
	h := New(cfg, "secret", Options{})

	req := httptest.NewRequest(http.MethodGet, "http://gw/v1/fleet", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: HTTP %d, want 401", rec.Code)
	}
	if topo := topologyFetch(t, h, "secret"); len(topo.Nodes) != 1 {
		t.Fatalf("the token gets the topology, got %+v", topo)
	}
}

// --- routing ----------------------------------------------------------------

func TestRequestGoesToTheNodeServingItsModel(t *testing.T) {
	t.Setenv("RIGHT_ENGINE_KEY", "engine-key")
	right := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	wrong := newFakeNode(t, string(daemon.StateRunning), "org/other")
	cfg := fleetOf(t, []string{"right", "wrong"}, right, wrong)
	cfg.Nodes[0].EngineTokenEnv = "RIGHT_ENGINE_KEY"
	right.engineAuth = "engine-key"
	h := New(cfg, "caller-token", Options{})

	resp, body := post(t, h, "caller-token", `{"model":"org/wanted","messages":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "hello") {
		t.Errorf("the engine's reply did not reach the caller: %s", body)
	}
	right.mu.Lock()
	defer right.mu.Unlock()
	// The engine got its own key, not the caller's token.
	if right.engineGotAuth != "Bearer engine-key" {
		t.Errorf("engine saw %q, want the fleet's key", right.engineGotAuth)
	}
	if strings.Contains(right.engineGotAuth, "caller-token") {
		t.Error("the caller's token travelled past the gateway")
	}
}

func TestRequestNamesNoModel(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{})
	resp, body := post(t, h, "", `{"messages":[]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("HTTP %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "no model") {
		t.Errorf("the refusal should say the request names no model: %s", body)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.statusHits != 0 {
		t.Error("a refused request contacted a node")
	}
}

func TestStreamedReplyPassesThrough(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{})
	resp, body := post(t, h, "", `{"model":"org/wanted","stream":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("content type = %q, want the engine's stream type", resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"data: one", "data: two", "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream lost %q: %s", want, body)
		}
	}
}

func TestEngineRefusalIsTheCallersError(t *testing.T) {
	t.Setenv("KEY", "k")
	failing := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	other := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"failing", "other"}, failing, other)
	h := New(cfg, "", Options{})

	resp, body := post(t, h, "", `{"model":"org/wanted","fail":true}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("the engine's 500 is the caller's: HTTP %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "on fire") {
		t.Errorf("the engine's own refusal should reach the caller: %s", body)
	}
	other.mu.Lock()
	defer other.mu.Unlock()
	// The other node was never tried: its engine saw no request at all.
	if other.engineGotAuth != "" {
		t.Error("a failed request was retried at another node")
	}
}

func TestEngineDownFailsNamingTheNode(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{})
	node.mu.Lock()
	node.engineLn.Close() // the engine dies while the daemon still reports it
	node.mu.Unlock()

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("HTTP %d, want 502: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "box") {
		t.Errorf("the failure should name the node: %s", body)
	}
}

// A node reached by a non-loopback name with a loopback-bound engine, without
// an override, is not a candidate; the mark carries the daemon's explanation,
// which names the bind and the fix.
func TestReachableMarksLoopbackBoundEngines(t *testing.T) {
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "bound", Host: "remote-box", Kind: fleet.KindDaemon, Port: 14242},
		{Name: "overridden", Host: "remote-box", Kind: fleet.KindDaemon, Port: 14243,
			Engine: &fleet.EngineOverride{Host: "proxy.local", Port: 9000, Path: "/v1"}},
	}}
	h := New(cfg, "", Options{})
	results := []fleet.NodeResult{
		{Name: "bound", Outcome: fleet.OutcomeOK, Status: daemon.StatusResponse{
			State: string(daemon.StateRunning), Model: "m",
			Engine: &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true},
		}},
		{Name: "overridden", Outcome: fleet.OutcomeOK, Status: daemon.StatusResponse{
			State: string(daemon.StateRunning), Model: "m",
			Engine: &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true},
		}},
	}
	marked := h.reachable(results)
	if marked[0].OK() {
		t.Fatal("a loopback-bound engine without an override is not a candidate")
	}
	if !strings.Contains(marked[0].Err.Error(), "loopback") {
		t.Errorf("the mark should carry the explanation naming the bind: %v", marked[0].Err)
	}
	if !marked[1].OK() {
		t.Errorf("an override takes responsibility for reachability: %v", marked[1].Err)
	}
}

// With nothing else matching, the failure the gateway gives names the bind
// and the fix rather than a bare "no node serving".
func TestLoopbackBoundEngineFailsNamingTheFix(t *testing.T) {
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "bound", Host: "remote-box", Kind: fleet.KindDaemon, Port: 14242},
	}}
	h := New(cfg, "", Options{})
	results := []fleet.NodeResult{
		{Name: "bound", Outcome: fleet.OutcomeOK, Status: daemon.StatusResponse{
			State: string(daemon.StateRunning), Model: "org/wanted",
			Engine: &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true},
		}},
	}
	_, err := cfg.Choose(h.reachable(results), fleet.Want{Model: "org/wanted"})
	if err == nil {
		t.Fatal("nothing reachable serves the model, so the request must fail")
	}
	for _, want := range []string{"bound", "loopback"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure should name %q: %v", want, err)
		}
	}
}

// A remote environment's engine address arrives as its status's reported host —
// the control plane's published endpoint — so it is a candidate, not marked
// unreachable the way a loopback-bound engine is. Without this the gateway
// could list a remote node's model and then refuse to route a request to it.
func TestReachableRoutesToARemoteNode(t *testing.T) {
	t.Setenv("TEST_ENGINE_KEY", "secret")
	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), APIKeyEnv: "TEST_ENGINE_KEY", Nodes: []fleet.NodeConfig{
		{Name: "env", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})
	results := []fleet.NodeResult{
		{Name: "env", Outcome: fleet.OutcomeOK, Status: daemon.StatusResponse{
			State:      string(daemon.StateRunning),
			Model:      "org/wanted",
			ServedName: "wanted",
			Engine:     &daemon.EngineEndpoint{Host: "1.2.3.4", Port: 8000, Path: "/v1"},
		}},
	}
	choice, err := cfg.Choose(h.reachable(results), fleet.Want{Model: "wanted"})
	if err != nil {
		t.Fatalf("a request for the remote node's model should route to it: %v", err)
	}
	if choice.Node.Name != "env" {
		t.Errorf("choice = %q, want env", choice.Node.Name)
	}
	if choice.BaseURL != "http://1.2.3.4:8000/v1" {
		t.Errorf("base URL = %q, want the control plane's published address", choice.BaseURL)
	}
	if choice.APIKey != "secret" {
		t.Errorf("API key = %q, want the resolved engine key", choice.APIKey)
	}
}

func TestBurstFansOutOnce(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	now := time.Now()
	h := New(cfg, "", Options{Now: func() time.Time { return now }})

	for i := range 3 {
		resp, _ := post(t, h, "", `{"model":"org/wanted"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: HTTP %d", i, resp.StatusCode)
		}
	}
	node.mu.Lock()
	hits := node.statusHits
	node.mu.Unlock()
	if hits != 1 {
		t.Fatalf("a burst of three fanned out %d times, want once", hits)
	}

	// A request after the reading ages fans out again.
	now = now.Add(3 * time.Second)
	resp, _ := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stale-reading request: HTTP %d", resp.StatusCode)
	}
	node.mu.Lock()
	hits = node.statusHits
	node.mu.Unlock()
	if hits != 2 {
		t.Fatalf("the aged reading should have fanned out once more, got %d total", hits)
	}
}

// --- waking -----------------------------------------------------------------

func TestColdRequestWakesANode(t *testing.T) {
	t.Setenv("BOX_ENGINE_KEY", "engine-key")
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.engineDelay = 100 * time.Millisecond
	cfg := fleetOf(t, []string{"box"}, node)
	cfg.Nodes[0].EngineTokenEnv = "BOX_ENGINE_KEY"
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted", ServedModelName: "org/wanted"}},
			nil),
	})

	start := time.Now()
	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("answered before the engine was up")
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started != 1 {
		t.Fatalf("node started %d times, want once", node.started)
	}
	// Started with its own source's config, gated with the fleet's key.
	if node.pushed == nil || node.pushed.ModelID != "org/wanted" {
		t.Errorf("the node's own config did not reach it: %+v", node.pushed)
	}
	if node.pushedKey != "engine-key" {
		t.Errorf("the engine was gated with %q, want the fleet's key", node.pushedKey)
	}
}

func TestConcurrentColdRequestsShareOneWake(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.engineDelay = 150 * time.Millisecond
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted"}},
			nil),
	})

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := post(t, h, "", `{"model":"org/wanted"}`)
			codes[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d: HTTP %d, want 200 from the shared wake", i, code)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started != 1 {
		t.Fatalf("the node was started %d times, want once", node.started)
	}
}

func TestWakeTimeoutFailsTheRequestAndLeavesTheEngine(t *testing.T) {
	old := fleet.WakeTimeout
	fleet.WakeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { fleet.WakeTimeout = old })

	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.noEngine = true // the engine never answers
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted"}},
			nil),
	})

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "box") {
		t.Errorf("the timeout should name the node: %s", body)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started != 1 || node.state != string(daemon.StateRunning) {
		t.Error("the started engine is left running on timeout, not stopped")
	}
}

func TestWakeOffRefusesNamingTheNodeAndCommand(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	cfg.WakePolicy = fleet.WakeOff
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted"}},
			nil),
	})

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503", resp.StatusCode)
	}
	for _, want := range []string{"box", "spinloop fleet start box"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal should name %q: %s", want, body)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started != 0 {
		t.Error("wake off starts nothing")
	}
}

func TestWakeOffWithNoMatchingSource(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	cfg.WakePolicy = fleet.WakeOff
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/something-else"}},
			nil),
	})
	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503", resp.StatusCode)
	}
	if !strings.Contains(body, "no node's source describes") {
		t.Errorf("the refusal should say nothing describes the model: %s", body)
	}
}

func TestNothingCanServeNamesEveryRefusal(t *testing.T) {
	a := newFakeNode(t, string(daemon.StateIdle), "")
	b := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"a", "b"}, a, b)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"a": {Runner: "llamacpp", ModelID: "org/other"}},
			map[string]string{"b": "node \"b\" names no Spinloop source: no `file` field, no alias, no subdirectory"},
		),
	})

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503", resp.StatusCode)
	}
	for _, want := range []string{"a", "b", "org/other", "Spinloop source"} {
		if !strings.Contains(body, want) {
			t.Errorf("the failure should name %q: %s", want, body)
		}
	}
	a.mu.Lock()
	b.mu.Lock()
	defer func() { a.mu.Unlock(); b.mu.Unlock() }()
	if a.started != 0 || b.started != 0 {
		t.Error("nothing is started when nothing can serve")
	}
}

// --- waking a remote node -----------------------------------------------------

// remoteControlServer serves a deployed-but-stopped environment's status
// until its instance is booted (POST), after which it reports running with
// an engine that actually answers an OpenAI-compatible completion — so a
// request proxied to it end to end gets a real reply, the same way it does
// against a daemon's engine.
//
// Its GET / (status) reply matches the real control plane: deploy facts
// (runner, modelId, servedName) ride along only once the environment is
// running, not while it is stopped — a stopped environment's status names
// only its state and base_url, the same gap that "gateway waking an
// undeployed node" is really about. GET /stats always carries runner and
// modelId (never servedName — the stats reply has none), since the stats
// Lambda reads the deploy config directly rather than relaying it alongside
// instance state; that is what the gateway now resolves a stopped remote
// node's wakeable model from.
func remoteControlServer(t *testing.T) (url string, started func() bool) {
	t.Helper()
	var (
		mu      sync.Mutex
		engine  net.Listener
		wasSent bool
	)
	engineHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	})
	statusBody := func() string {
		mu.Lock()
		defer mu.Unlock()
		if engine == nil {
			return `{"state":"stopped"}`
		}
		return fmt.Sprintf(
			`{"state":"running","runner":"llamacpp","modelId":"org/deployed","servedName":"deployed","base_url":"http://%s/v1"}`,
			engine.Addr())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(statusBody()))
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"state":"stopped","runner":"llamacpp","modelId":"org/deployed","servedName":"deployed"}`)
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		wasSent = true
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			mu.Unlock()
			t.Fatal(err)
		}
		engine = ln
		mu.Unlock()
		go (&http.Server{Handler: engineHandler}).Serve(ln)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"state":"ready","healthy":true,"runner":"llamacpp","modelId":"org/deployed","servedName":"deployed","base_url":"http://%s/v1"}`, ln.Addr())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		mu.Lock()
		defer mu.Unlock()
		if engine != nil {
			engine.Close()
		}
	})
	return srv.URL, func() bool { mu.Lock(); defer mu.Unlock(); return wasSent }
}

// A request for a model only a deployed-but-stopped remote node serves is
// held and answered once that node's instance boots — the gateway sources
// its wakeable model from its own last status, not a Spinloop source, and
// StartWith boots it without pushing any config.
func TestColdRequestWakesADeployedRemoteNode(t *testing.T) {
	shortWake := func(t *testing.T) {
		old := fleet.WakeTimeout
		fleet.WakeTimeout = 3 * time.Second
		t.Cleanup(func() { fleet.WakeTimeout = old })
	}
	shortWake(t)
	url, started := remoteControlServer(t)
	registerRemoteEnv(t, "cloud", url)

	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "cloud", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})

	resp, body := post(t, h, "", `{"model":"org/deployed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if !started() {
		t.Error("the environment's instance was never started")
	}
}

// A request for a deployed-but-stopped remote node's served name — the
// alias a caller knew it by while it was running, rather than its bare model
// id — still matches: the stats reply the wake path reads carries the served
// name from the deploy config, the same field the status reply carries while
// running, so a caller need not learn a second name once the node stops.
func TestColdRequestWakesADeployedRemoteNodeByItsServedName(t *testing.T) {
	shortWake := func(t *testing.T) {
		old := fleet.WakeTimeout
		fleet.WakeTimeout = 3 * time.Second
		t.Cleanup(func() { fleet.WakeTimeout = old })
	}
	shortWake(t)
	url, started := remoteControlServer(t)
	registerRemoteEnv(t, "cloud", url)

	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "cloud", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})

	resp, body := post(t, h, "", `{"model":"deployed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if !started() {
		t.Error("the environment's instance was never started")
	}
}

// An undeployed remote node's stats read fails outright — nothing deployed
// to read — so it does not match any request and the failure says so,
// naming the deploy path, without holding the request for the wake timeout.
func TestWakeRefusesAnUndeployedRemoteNode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"undeployed"}`))
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"cannot read deploy config: run spinloop remote deploy first"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	registerRemoteEnv(t, "cloud", srv.URL)

	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "cloud", Kind: fleet.KindRemote},
	}}
	h := New(cfg, "", Options{})

	resp, body := post(t, h, "", `{"model":"org/deployed"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "spinloop remote deploy") {
		t.Errorf("the failure should name the deploy path: %s", body)
	}
}

// A remote node whose own wake is disabled is not started even though its
// last status matches the request, and the failure says waking is off for
// it rather than that nothing can serve the model at all.
func TestWakeDisabledForAMatchingRemoteNodeNamesIt(t *testing.T) {
	url, started := remoteControlServer(t)
	registerRemoteEnv(t, "cloud", url)

	cfg := &fleet.Config{Path: "fleet.yaml", Dir: t.TempDir(), Nodes: []fleet.NodeConfig{
		{Name: "cloud", Kind: fleet.KindRemote, WakePolicy: fleet.WakeOff},
	}}
	h := New(cfg, "", Options{})

	resp, body := post(t, h, "", `{"model":"org/deployed"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "cloud") {
		t.Errorf("the failure should name the node: %s", body)
	}
	if started() {
		t.Error("a node with its own wake disabled must not be started")
	}
}

// --- logging ------------------------------------------------------------------

func TestRoutedRequestLeavesOneLogLine(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{Log: log})

	resp, _ := post(t, h, "", `{"model":"org/wanted","messages":[{"content":"a secret prompt"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("one routed request leaves one log line, got %d: %q", len(lines), buf.String())
	}
	for _, want := range []string{"org/wanted", "box"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the log line should name %q: %s", want, lines[0])
		}
	}
	if strings.Contains(buf.String(), "secret prompt") {
		t.Error("the log carries the request body")
	}
}

// --- a node that is running but has not answered yet -----------------------

// startingNode is a machine whose daemon reports running with its engine's
// port closed and readiness not-ready: llama.cpp fetching or loading weights.
// It is the state that made a request fail with a dial error before the
// gateway consulted readiness at all.
func startingNode(t *testing.T, model string) *fakeNode {
	t.Helper()
	f := newFakeNode(t, string(daemon.StateIdle), model)
	f.mu.Lock()
	f.state = string(daemon.StateRunning)
	f.ready = daemon.ReadyNo
	f.mu.Unlock()
	return f
}

// answersIn flips the node to ready and opens its engine port after d.
func (f *fakeNode) answersIn(d time.Duration) {
	go func() {
		time.Sleep(d)
		f.upAsEngine()
		f.mu.Lock()
		f.ready = daemon.ReadyYes
		f.mu.Unlock()
	}()
}

func TestRequestWaitsForAStartingEngine(t *testing.T) {
	node := startingNode(t, "org/wanted")
	node.answersIn(150 * time.Millisecond)
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted"}},
			nil),
	})

	start := time.Now()
	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("answered before the engine was up")
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started != 0 {
		t.Errorf("the node was started %d times; it was already running", node.started)
	}
}

// The node loading the model is the one to wait for, not a reason to start a
// second engine somewhere else — which on a fleet of cloud nodes costs money
// for a model that is already minutes from being served.
func TestAStartingEngineIsWaitedForRatherThanWakingAnother(t *testing.T) {
	starting := startingNode(t, "org/wanted")
	starting.answersIn(100 * time.Millisecond)
	spare := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"loading", "spare"}, starting, spare)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t, map[string]inference.DeployConfig{
			"loading": {Runner: "llamacpp", ModelID: "org/wanted"},
			"spare":   {Runner: "llamacpp", ModelID: "org/wanted"},
		}, nil),
	})

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	spare.mu.Lock()
	defer spare.mu.Unlock()
	if spare.started != 0 {
		t.Errorf("the spare node was started %d times, want none", spare.started)
	}
}

// An engine that never answers fails the request with a message naming the
// node, not the dial error the caller used to get on every attempt.
func TestStartingEngineThatNeverAnswersFailsNamingTheNode(t *testing.T) {
	old := fleet.WakeTimeout
	fleet.WakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fleet.WakeTimeout = old })

	node := startingNode(t, "org/wanted")
	cfg := fleetOf(t, []string{"box"}, node)
	h := New(cfg, "", Options{
		ConfigFor: cfgForOf(t,
			map[string]inference.DeployConfig{"box": {Runner: "llamacpp", ModelID: "org/wanted"}},
			nil),
	})

	resp, body := post(t, h, "", `{"model":"org/wanted"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{"box", "still be loading"} {
		if !strings.Contains(body, want) {
			t.Errorf("body should mention %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "connect: connection refused") {
		t.Errorf("the caller should not be given a dial error: %s", body)
	}
}

// paddedRequest is a valid completion request of exactly size bytes.
func paddedRequest(size int) string {
	const head = `{"model":"org/wanted","messages":[{"role":"user","content":"`
	const tail = `"}]}`
	return head + strings.Repeat("a", size-len(head)-len(tail)) + tail
}

func TestRequestAtTheLimitIsForwardedInFull(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	h := New(fleetOf(t, []string{"box"}, node), "", Options{MaxRequestBytes: 4096})
	req := paddedRequest(4096)

	resp, body := post(t, h, "", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.engineGotBody != req {
		t.Errorf("the engine read %d bytes, want the full %d", len(node.engineGotBody), len(req))
	}
}

func TestRequestOverTheLimitIsRefusedWith413(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	h := New(fleetOf(t, []string{"box"}, node), "", Options{MaxRequestBytes: 4096})

	resp, body := post(t, h, "", paddedRequest(4097))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("HTTP %d, want 413; body %s", resp.StatusCode, body)
	}
	for _, want := range []string{"4096", "--max-request-bytes"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal should name %q: %s", want, body)
		}
	}
	if strings.Contains(body, "not a JSON body") {
		t.Errorf("an over-limit body must not read as malformed JSON: %s", body)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.statusHits != 0 || node.engineGotBody != "" {
		t.Error("an over-limit request reached a node")
	}
}

func TestDefaultRequestLimitAdmitsWhatOneMiBRefused(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	h := New(fleetOf(t, []string{"box"}, node), "", Options{})

	resp, body := post(t, h, "", paddedRequest(2<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, body %s", resp.StatusCode, body)
	}
}

// failingBody yields some bytes and then a read error, the way a client that
// drops its connection part-way through a body does.
type failingBody struct{ sent bool }

func (b *failingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, fmt.Errorf("connection reset")
	}
	b.sent = true
	return copy(p, `{"model":"org/wanted","messages":[`), nil
}

func TestPartiallyReadRequestIsNeverForwarded(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	h := New(fleetOf(t, []string{"box"}, node), "", Options{})
	req := httptest.NewRequest(http.MethodPost, "http://gw/v1/chat/completions", &failingBody{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "reading the request") {
		t.Errorf("the refusal should say the read failed: %s", rec.Body.String())
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.statusHits != 0 || node.engineGotBody != "" {
		t.Error("a partly read request reached a node")
	}
}

func TestMalformedJSONStillReadsAsMalformed(t *testing.T) {
	node := newFakeNode(t, string(daemon.StateRunning), "org/wanted")
	h := New(fleetOf(t, []string{"box"}, node), "", Options{})
	resp, body := post(t, h, "", `{"model":`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "not a JSON body") {
		t.Fatalf("HTTP %d, body %s; want 400 naming malformed JSON", resp.StatusCode, body)
	}
}
