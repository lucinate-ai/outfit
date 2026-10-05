package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// fakeNode is one machine's daemon plus, optionally, its engine's listener.
// It is a real HTTP server so the client's own request path is exercised.
type fakeNode struct {
	mu sync.Mutex
	// state is what /v1/status reports.
	state string
	model string
	// startErr, when set, is the error /v1/start answers with.
	startErr string
	// startStatus is the HTTP status for a refused start.
	startStatus int
	// engineDelay is how long after starting before the engine listens.
	engineDelay time.Duration
	// ready, when set (daemon.ReadyYes or daemon.ReadyNo), is what
	// /v1/status reports for `ready`; empty reports no reading at all.
	ready string
	// noEngine keeps the engine's listener down even after an accepted start:
	// readiness can only come from the daemon's own reading.
	noEngine bool
	// started records whether a start was accepted.
	started bool
	// startCalls counts every /v1/start request received, accepted or
	// refused — unlike started, which only says whether the last one was.
	startCalls int
	// pushed is the deploy config the start carried.
	pushed *inference.DeployConfig
	// pushedKey is the engine key the start carried.
	pushedKey string

	srv        *httptest.Server
	engineLn   net.Listener
	enginePort int
}

func newFakeNode(t *testing.T, state, model string) *fakeNode {
	t.Helper()
	f := &fakeNode{state: state, model: model}

	// A listener the "engine" occupies only once it is ready.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.enginePort = ln.Addr().(*net.TCPAddr).Port
	// Close it again: readiness means something answers on that port, so the
	// port stays free until the engine is meant to be up.
	ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		resp := daemon.StatusResponse{State: f.state, Model: f.model}
		if f.state == string(daemon.StateRunning) {
			resp.Engine = &daemon.EngineEndpoint{Port: f.enginePort}
			if f.ready != "" {
				resp.Ready = f.ready
			}
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.startCalls++
		if f.startErr != "" {
			status := f.startStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(daemon.Error{Error: f.startErr})
			return
		}
		var req daemon.StartRequest
		json.NewDecoder(r.Body).Decode(&req)
		dc := req.DeployConfig
		f.pushed = &dc
		f.pushedKey = req.EngineAPIKey
		f.started = true
		f.state = string(daemon.StateRunning)
		if dc.ModelID != "" {
			f.model = dc.ModelID
		}
		if !f.noEngine {
			delay := f.engineDelay
			go func() {
				time.Sleep(delay)
				f.listenAsEngine()
			}()
		}
		json.NewEncoder(w).Encode(daemon.StatusResponse{State: f.state, Model: f.model})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		f.srv.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.engineLn != nil {
			f.engineLn.Close()
		}
	})
	return f
}

// listenAsEngine occupies the engine's port, which is what readiness probes.
func (f *fakeNode) listenAsEngine() {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.enginePort)))
	if err != nil {
		return
	}
	f.mu.Lock()
	f.engineLn = ln
	f.mu.Unlock()
}

// nodeConfig is the fleet-file entry pointing at this fake.
func (f *fakeNode) nodeConfig(name string) NodeConfig {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(f.srv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return NodeConfig{Name: name, Host: host, Port: p, Kind: KindDaemon}
}

// fleetOf builds a Config over the fakes, in the order given.
func fleetOf(t *testing.T, names []string, nodes ...*fakeNode) *Config {
	t.Helper()
	cfg := &Config{Path: "fleet.yaml", Dir: t.TempDir()}
	for i, n := range nodes {
		cfg.Nodes = append(cfg.Nodes, n.nodeConfig(names[i]))
	}
	return cfg
}

// statusOf fans out over the fleet, as a launch does before waking anything.
func statusOf(t *testing.T, cfg *Config) []NodeResult {
	t.Helper()
	return cfg.FanOut(context.Background(), StatusCall)
}

func shortWake(t *testing.T) {
	t.Helper()
	oldTimeout, oldPoll := WakeTimeout, wakePoll
	WakeTimeout, wakePoll = 3*time.Second, 10*time.Millisecond
	t.Cleanup(func() { WakeTimeout, wakePoll = oldTimeout, oldPoll })
}

func TestWakeStartsAnIdleNode(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	dc := inference.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}

	choice, err := cfg.Wake(context.Background(), Want{Model: "qwen3-27b"}, ConstantConfig(dc, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "box" || !choice.Woken {
		t.Errorf("choice = %+v, want the woken box", choice)
	}
	if !strings.Contains(choice.BaseURL, strconv.Itoa(node.enginePort)) {
		t.Errorf("base URL %q should point at the engine's port", choice.BaseURL)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.pushed == nil || node.pushed.ModelID != "qwen3-27b" {
		t.Errorf("the deploy config did not reach the node: %+v", node.pushed)
	}
}

// A node that cannot serve the model is passed over for one that can.
func TestWakeSkipsANodeThatRefusesTheConfig(t *testing.T) {
	shortWake(t)
	refuses := newFakeNode(t, string(daemon.StateIdle), "")
	refuses.startErr = "runner \"llamacpp\" cannot be served locally"
	accepts := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"wrong-box", "right-box"}, refuses, accepts)

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "right-box" {
		t.Errorf("chose %q, want the node that accepted", choice.Node.Name)
	}
}

// A node whose own wake is off is never started, even though its config
// matches and the fleet otherwise wakes: another candidate is tried instead.
func TestWakeSkipsANodeWithItsOwnWakeDisabled(t *testing.T) {
	shortWake(t)
	disabled := newFakeNode(t, string(daemon.StateIdle), "")
	enabled := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"disabled-box", "enabled-box"}, disabled, enabled)
	cfg.Nodes[0].WakePolicy = WakeOff

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "enabled-box" {
		t.Errorf("chose %q, want the node whose own wake is not disabled", choice.Node.Name)
	}
	disabled.mu.Lock()
	defer disabled.mu.Unlock()
	if disabled.started {
		t.Error("a node with its own wake disabled must not be started")
	}
}

// A node with nothing to try but a disabled node reports why, naming the
// node and that its own waking is disabled — not a generic "refuses the
// config" reason.
func TestWakeDisabledNodeNamesItselfInTheRefusal(t *testing.T) {
	shortWake(t)
	disabled := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"disabled-box"}, disabled)
	cfg.Nodes[0].WakePolicy = WakeOff

	_, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a failure: the only candidate's own wake is disabled")
	}
	for _, want := range []string{"disabled-box", "waking is disabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got:\n%s", want, err)
		}
	}
}

// A node that opts its own wake on is started even though the fleet as a
// whole does not wake.
func TestWakeStartsANodeThatOptsInUnderAFleetThatDoesNotWake(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"opted-in-box"}, node)
	cfg.WakePolicy = WakeOff
	cfg.Nodes[0].WakePolicy = WakeOn

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "opted-in-box" {
		t.Errorf("chose %q, want the node that opted its own wake on", choice.Node.Name)
	}
}

func TestWakeReportsEveryRefusal(t *testing.T) {
	shortWake(t)
	a := newFakeNode(t, string(daemon.StateIdle), "")
	a.startErr = "cannot serve vllm"
	b := newFakeNode(t, string(daemon.StateIdle), "")
	b.startErr = "no weights for that model"
	cfg := fleetOf(t, []string{"a", "b"}, a, b)

	_, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "vllm", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a failure when every node refuses")
	}
	for _, want := range []string{"a", "b", "cannot serve vllm", "no weights"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got:\n%s", want, err)
		}
	}
}

// Readiness is the engine answering, not the daemon saying running.
func TestWakeWaitsForTheEngineToAnswer(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.engineDelay = 150 * time.Millisecond
	cfg := fleetOf(t, []string{"slow"}, node)

	var progress []string
	log := func(format string, args ...any) { progress = append(progress, format) }

	start := time.Now()
	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), log)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("returned before the engine was listening")
	}
	if !engineAnswers(context.Background(), choice.BaseURL) {
		t.Error("returned an endpoint that does not answer")
	}
	if len(progress) == 0 {
		t.Error("waiting should report progress rather than sit silent")
	}
}

// A node that never comes up fails naming itself, and its engine is left
// running rather than stopped: it is probably still loading.
func TestWakeTimesOutWithoutStopping(t *testing.T) {
	shortWake(t)
	WakeTimeout = 200 * time.Millisecond
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.engineDelay = time.Hour // never, for this test's purposes
	cfg := fleetOf(t, []string{"stuck"}, node)

	_, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "stuck") {
		t.Errorf("message should name the node, got: %v", err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if !node.started || node.state != string(daemon.StateRunning) {
		t.Error("the started engine should be left running on timeout")
	}
}

// Two Wake calls racing to wake the same node coalesce into one actual
// start. This fixture's own /v1/start does not itself reject a concurrent
// call the way a real daemon's supervisor mutex does — unlike a daemon
// node, a cloud environment's control plane has no such guard at all — so
// without wakeSingleflight both calls would reach StartWith and each start
// their own engine (or, for a cloud node, each launch their own instance).
func TestWakeCoalescesConcurrentCallsForTheSameNode(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.engineDelay = 100 * time.Millisecond
	cfg := fleetOf(t, []string{"box"}, node)
	cfgFor := ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil)
	results := statusOf(t, cfg)

	var wg sync.WaitGroup
	choices := make([]*Choice, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			choices[i], errs[i] = cfg.Wake(context.Background(), Want{Model: "m"}, cfgFor, results, nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if choices[0].Node.Name != "box" || choices[1].Node.Name != "box" {
		t.Errorf("both calls should land on the same node, got %q and %q", choices[0].Node.Name, choices[1].Node.Name)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.startCalls != 1 {
		t.Errorf("the node's /v1/start was called %d times, want exactly 1 — the second wake should have joined the first rather than starting its own", node.startCalls)
	}
}

// Losing the race to another client is another route to the same place.
func TestWakeLosingTheRaceUsesTheNode(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	// The node refuses the start as already-running, and reports itself
	// serving what we wanted — exactly what a client that lost a race sees.
	node.startErr = "an engine is already running"
	node.startStatus = http.StatusConflict
	node.state = string(daemon.StateRunning)
	node.model = "qwen3-27b"
	node.listenAsEngine()
	cfg := fleetOf(t, []string{"contested"}, node)

	// The fan-out is taken while the node still looked idle.
	stale := []NodeResult{{
		Name:    "contested",
		Outcome: OutcomeOK,
		Status:  daemon.StatusResponse{State: string(daemon.StateIdle)},
	}}
	choice, err := cfg.Wake(context.Background(), Want{Model: "qwen3-27b"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}, nil), stale, nil)
	if err != nil {
		t.Fatalf("losing the race should not fail the launch: %v", err)
	}
	if choice.Node.Name != "contested" {
		t.Errorf("chose %q", choice.Node.Name)
	}
}

// A running engine is never displaced to make room, so it is not a candidate.
func TestWakeNeverDisplacesARunningEngine(t *testing.T) {
	shortWake(t)
	busy := newFakeNode(t, string(daemon.StateRunning), "someone-elses-model")
	cfg := fleetOf(t, []string{"busy"}, busy)

	_, err := cfg.Wake(context.Background(), Want{Model: "mine"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "mine"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a failure rather than a restart")
	}
	busy.mu.Lock()
	defer busy.mu.Unlock()
	if busy.started {
		t.Error("a running engine was restarted")
	}
	if busy.model != "someone-elses-model" {
		t.Errorf("the running engine was displaced: now serving %q", busy.model)
	}
}

// A node whose stored config already names the model has the weights, so it is
// tried first.
func TestWakePrefersANodeThatAlreadyHasTheModel(t *testing.T) {
	shortWake(t)
	cold := newFakeNode(t, string(daemon.StateIdle), "")
	warm := newFakeNode(t, string(daemon.StateStopped), "qwen3-27b")
	cfg := fleetOf(t, []string{"cold", "warm"}, cold, warm)

	choice, err := cfg.Wake(context.Background(), Want{Model: "qwen3-27b"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "warm" {
		t.Errorf("chose %q, want the node that already has the weights", choice.Node.Name)
	}
	cold.mu.Lock()
	defer cold.mu.Unlock()
	if cold.started {
		t.Error("the cold node should not have been started")
	}
}

// The client gates the engine it starts, with the key its own fleet entry
// names — so the value it hands the agent is the value the engine checks,
// rather than something it has to look up and hope matches.
func TestWakeGatesTheEngineWithTheClientsKey(t *testing.T) {
	shortWake(t)
	t.Setenv("BOX_ENGINE_KEY", "sk-from-the-client")
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	cfg.Nodes[0].EngineTokenEnv = "BOX_ENGINE_KEY"

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.pushedKey != "sk-from-the-client" {
		t.Errorf("the node was started with key %q, want the client's", node.pushedKey)
	}
	if choice.APIKey != "sk-from-the-client" {
		t.Errorf("the agent would be given %q, want the key the engine was gated with", choice.APIKey)
	}
}

// A node naming no key wakes an ungated engine, which is right for one reached
// over loopback.
func TestWakeWithoutAKeyIsUngated(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.APIKey != "" {
		t.Errorf("APIKey = %q, want none", choice.APIKey)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.pushedKey != "" {
		t.Errorf("the node was gated with %q despite no key being named", node.pushedKey)
	}
}

// The wake path stays daemon-only: a remote is never woken — what it serves is
// set by `spinloop cloud deploy`. Wake boots its instance and waits for its
// engine to answer the same way it does for a daemon node, without pushing
// the candidate resolver's config onto it — the environment already knows
// what it serves.
func TestWakeStartsADeployedCloudNode(t *testing.T) {
	shortWake(t)
	stubAWSCreds(t)

	var (
		mu      sync.Mutex
		engine  net.Listener
		started bool
	)
	statusBody := func() string {
		mu.Lock()
		defer mu.Unlock()
		if engine == nil {
			// The control plane carries no deploy facts on a stopped
			// environment's status reply — only a running one's does — so
			// this deliberately reports none, the way the real one does.
			return `{"state":"stopped"}`
		}
		return fmt.Sprintf(
			`{"state":"running","runner":"llamacpp","modelId":"org/m","servedName":"m","base_url":"http://%s/v1"}`,
			engine.Addr())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(statusBody()))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		started = true
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			mu.Unlock()
			t.Fatal(err)
		}
		engine = ln
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// cloud.Start only accepts HTTP 200 with state "ready" as done; the
		// engine's own running state comes from the status polls waitReady
		// makes afterwards, not from this reply.
		fmt.Fprintf(w, `{"state":"ready","healthy":true,"runner":"llamacpp","modelId":"org/m","servedName":"m","base_url":"http://%s/v1"}`, ln.Addr())
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
	registerCloudEnv(t, "cloud", srv.URL, srv.URL)

	path := writeFleet(t, "nodes:\n  - name: cloud\n    kind: cloud\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	results := statusOf(t, cfg)
	if !results[0].OK() || results[0].Status.State != "stopped" {
		t.Fatalf("initial status = %+v", results[0])
	}

	cfgFor := ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "org/m", ServedModelName: "m"}, nil)
	choice, err := cfg.Wake(context.Background(), Want{Model: "org/m"}, cfgFor, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "cloud" {
		t.Errorf("chose %q, want cloud", choice.Node.Name)
	}
	mu.Lock()
	defer mu.Unlock()
	if !started {
		t.Error("the environment's instance was never started")
	}
}

// A cloud node's wake waits for the control plane's own health check to
// say ready, not just for its port to accept a connection: this fake
// reports running-but-unhealthy for a stretch after boot, the way an engine
// that has opened its port but is still loading weights does, before
// flipping healthy. The bug this guards against would have returned as
// soon as the port opened.
func TestWakeWaitsForARemoteEngineToBecomeHealthy(t *testing.T) {
	shortWake(t)
	stubAWSCreds(t)
	const unhealthyFor = 150 * time.Millisecond

	var (
		mu        sync.Mutex
		engine    net.Listener
		healthyAt time.Time
	)
	statusBody := func() string {
		mu.Lock()
		defer mu.Unlock()
		if engine == nil {
			return `{"state":"stopped"}`
		}
		healthy := time.Now().After(healthyAt)
		return fmt.Sprintf(
			`{"state":"running","healthy":%t,"runner":"llamacpp","modelId":"org/m","servedName":"m","base_url":"http://%s/v1"}`,
			healthy, engine.Addr())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(statusBody()))
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			mu.Unlock()
			t.Fatal(err)
		}
		engine = ln
		healthyAt = time.Now().Add(unhealthyFor)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"state":"ready","healthy":true,"runner":"llamacpp","modelId":"org/m","servedName":"m","base_url":"http://%s/v1"}`, ln.Addr())
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
	registerCloudEnv(t, "cloud", srv.URL, srv.URL)

	path := writeFleet(t, "nodes:\n  - name: cloud\n    kind: cloud\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	results := statusOf(t, cfg)

	cfgFor := ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "org/m", ServedModelName: "m"}, nil)
	start := time.Now()
	choice, err := cfg.Wake(context.Background(), Want{Model: "org/m"}, cfgFor, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < unhealthyFor {
		t.Error("wake returned before the control plane reported the engine healthy — an open TCP port alone must not be trusted")
	}
	if choice.Node.Name != "cloud" {
		t.Errorf("chose %q, want cloud", choice.Node.Name)
	}
}

// engineUpAfter makes the "engine" of a node started by someone else come
// listening after d: the node already reports running, so the engine's delay
// is not the start's.
func (f *fakeNode) engineUpAfter(d time.Duration) {
	go func() {
		time.Sleep(d)
		f.listenAsEngine()
	}()
}

// The per-candidate resolver lets different nodes be started with different
// configs — the gateway shape, where each node's own Spinloop source decides
// what it would run.
func TestWakeTakesPerCandidateConfigs(t *testing.T) {
	shortWake(t)
	a := newFakeNode(t, string(daemon.StateIdle), "")
	b := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"a", "b"}, a, b)

	cfgFor := func(entry NodeConfig) (inference.DeployConfig, error) {
		if entry.Name == "a" {
			return inference.DeployConfig{}, fmt.Errorf("node %q names no Spinloop source", entry.Name)
		}
		return inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil
	}
	choice, err := cfg.Wake(context.Background(), Want{Model: "m"}, cfgFor, statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "b" {
		t.Errorf("chose %q, want the node whose source resolved", choice.Node.Name)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pushed == nil || b.pushed.ModelID != "m" {
		t.Errorf("the node's own config did not reach it: %+v", b.pushed)
	}
}

// A node woken by someone else is not taken on state alone: its engine may
// still be loading, so the same readiness wait applies to a node we did not
// start ourselves.
func TestWakeWaitsForARacedNodeToAnswer(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateRunning), "qwen3-27b")
	node.startErr = "an engine is already running"
	node.startStatus = http.StatusConflict
	node.engineUpAfter(150 * time.Millisecond)
	cfg := fleetOf(t, []string{"contested"}, node)

	stale := []NodeResult{{
		Name:    "contested",
		Outcome: OutcomeOK,
		Status:  daemon.StatusResponse{State: string(daemon.StateIdle)},
	}}
	start := time.Now()
	choice, err := cfg.Wake(context.Background(), Want{Model: "qwen3-27b"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}, nil), stale, nil)
	if err != nil {
		t.Fatalf("losing the race should not fail the launch: %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("returned before the raced node's engine was listening")
	}
	if choice.Node.Name != "contested" {
		t.Errorf("chose %q", choice.Node.Name)
	}
}

// A daemon that reports its own readiness reading is trusted without a probe:
// it checked from the same machine the engine runs on.
func TestWakeTrustsTheDaemonReadinessReading(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.ready = daemon.ReadyYes
	node.noEngine = true // the probe could never succeed; only the reading could
	cfg := fleetOf(t, []string{"box"}, node)

	choice, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err != nil {
		t.Fatalf("the daemon's own readiness reading should have been enough: %v", err)
	}
	if choice.Node.Name != "box" {
		t.Errorf("chose %q", choice.Node.Name)
	}
}

// An explicit "not ready" reading is trusted too, and is not second-guessed
// by a successful TCP probe: a runner's port can accept a connection well
// before it can answer a request — llama.cpp and vLLM both open it early
// and answer their own health check with a 503 while still loading — so an
// engine that says it is not ready must not be waved through because
// something merely answers on its port.
func TestWakeDoesNotTrustTheProbeOverAnExplicitNotReadyReading(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	node.ready = daemon.ReadyNo
	// engineDelay defaults to 0, so the "engine" starts listening almost
	// immediately — the TCP probe alone would say ready straight away. The
	// bug this guards against is exactly that probe overriding a reading
	// that already says no.
	cfg := fleetOf(t, []string{"box"}, node)

	_, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a timeout: the reading never says ready")
	}
	if !strings.Contains(err.Error(), "box") {
		t.Errorf("message should name the node, got: %v", err)
	}
}

// A variable that resolves to nothing fails before any engine is started.
func TestWakeFailsOnAnUnresolvableKey(t *testing.T) {
	shortWake(t)
	node := newFakeNode(t, string(daemon.StateIdle), "")
	cfg := fleetOf(t, []string{"box"}, node)
	cfg.Nodes[0].EngineTokenEnv = "NOWHERE_ENGINE_KEY"

	_, err := cfg.Wake(context.Background(), Want{Model: "m"},
		ConstantConfig(inference.DeployConfig{Runner: "llamacpp", ModelID: "m"}, nil), statusOf(t, cfg), nil)
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"NOWHERE_ENGINE_KEY", "box"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.started {
		t.Error("an engine was started despite the key failing to resolve")
	}
}
