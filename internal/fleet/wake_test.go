package fleet

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/remote"
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
	// started records whether a start was accepted.
	started bool
	// pushed is the deploy config the start carried.
	pushed *remote.DeployConfig

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
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.startErr != "" {
			status := f.startStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(daemon.Error{Error: f.startErr})
			return
		}
		var dc remote.DeployConfig
		json.NewDecoder(r.Body).Decode(&dc)
		f.pushed = &dc
		f.started = true
		f.state = string(daemon.StateRunning)
		if dc.ModelID != "" {
			f.model = dc.ModelID
		}
		delay := f.engineDelay
		go func() {
			time.Sleep(delay)
			f.listenAsEngine()
		}()
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
	dc := remote.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}

	choice, err := cfg.Wake(context.Background(), Want{Model: "qwen3-27b"}, dc, statusOf(t, cfg), nil)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}, statusOf(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Node.Name != "right-box" {
		t.Errorf("chose %q, want the node that accepted", choice.Node.Name)
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
		remote.DeployConfig{Runner: "vllm", ModelID: "m"}, statusOf(t, cfg), nil)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}, statusOf(t, cfg), log)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}, statusOf(t, cfg), nil)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}, stale, nil)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "mine"}, statusOf(t, cfg), nil)
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
		remote.DeployConfig{Runner: "llamacpp", ModelID: "qwen3-27b"}, statusOf(t, cfg), nil)
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
