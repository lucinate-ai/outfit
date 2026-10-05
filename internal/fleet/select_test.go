package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// runningNode is a node result for a machine serving model, last active
// idleSeconds ago. A negative idle means it has never done any work.
func runningNode(name, model string, idleSeconds int) NodeResult {
	s := daemon.StatusResponse{
		State:  string(daemon.StateRunning),
		Model:  model,
		Engine: &daemon.EngineEndpoint{Port: 8080},
	}
	if idleSeconds >= 0 {
		s.LastActiveAt = "2026-08-12T10:00:00Z"
		s.IdleSeconds = idleSeconds
	}
	return NodeResult{Name: name, Outcome: OutcomeOK, Status: s}
}

func idleNode(name string) NodeResult {
	return NodeResult{
		Name:    name,
		Outcome: OutcomeOK,
		Status:  daemon.StatusResponse{State: string(daemon.StateIdle)},
	}
}

func downNode(name string, outcome Outcome) NodeResult {
	return NodeResult{Name: name, Outcome: outcome, Err: errors.New("connection refused")}
}

// testConfig builds a Config over the named hosts without touching disk.
func testConfig(t *testing.T, names ...string) *Config {
	t.Helper()
	cfg := &Config{Path: "fleet.yaml", Dir: t.TempDir()}
	for _, n := range names {
		cfg.Nodes = append(cfg.Nodes, NodeConfig{Name: n, Host: n + ".local", Kind: KindDaemon})
	}
	return cfg
}

func TestRankPrefersIdleThenActive(t *testing.T) {
	cfg := testConfig(t, "a", "b", "c")
	results := []NodeResult{
		runningNode("a", "qwen", 5),
		runningNode("b", "qwen", 3600),
		runningNode("c", "qwen", 60),
	}
	want := Want{Model: "qwen"}

	got, err := cfg.choose(results, want)
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "b" {
		t.Errorf("prefer idle chose %q, want the longest-idle b", got.Node.Name)
	}
	if !strings.Contains(got.Reason, "prefer idle") {
		t.Errorf("reason should name the preference, got %q", got.Reason)
	}

	want.Prefer = PreferActive
	got, err = cfg.choose(results, want)
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "a" {
		t.Errorf("prefer active chose %q, want the most recently active a", got.Node.Name)
	}
}

// A node mid-request is the least idle of all, so the default preference is
// exactly what keeps a second agent off it.
func TestBusyEngineIsLastResortUnderIdle(t *testing.T) {
	cfg := testConfig(t, "busy", "quiet")
	results := []NodeResult{
		runningNode("busy", "qwen", 0),
		runningNode("quiet", "qwen", 900),
	}
	got, err := cfg.choose(results, Want{Model: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "quiet" {
		t.Errorf("chose %q, want the quiet node", got.Node.Name)
	}
}

// A node that has never done work is the most idle there is, and the least
// active.
func TestNeverActiveRanksAtBothEnds(t *testing.T) {
	cfg := testConfig(t, "fresh", "used")
	results := []NodeResult{
		runningNode("fresh", "qwen", -1),
		runningNode("used", "qwen", 30),
	}
	if got, err := cfg.choose(results, Want{Model: "qwen"}); err != nil {
		t.Fatal(err)
	} else if got.Node.Name != "fresh" {
		t.Errorf("prefer idle chose %q, want fresh", got.Node.Name)
	}
	if got, err := cfg.choose(results, Want{Model: "qwen", Prefer: PreferActive}); err != nil {
		t.Fatal(err)
	} else if got.Node.Name != "used" {
		t.Errorf("prefer active chose %q, want used", got.Node.Name)
	}
}

func TestTiesBreakByFleetFileOrder(t *testing.T) {
	cfg := testConfig(t, "first", "second")
	results := []NodeResult{
		runningNode("first", "qwen", 100),
		runningNode("second", "qwen", 100),
	}
	for i := 0; i < 5; i++ {
		got, err := cfg.choose(results, Want{Model: "qwen"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Node.Name != "first" {
			t.Fatalf("tie chose %q, want the first in file order", got.Node.Name)
		}
	}
}

func TestUnreachableNodesAreSkipped(t *testing.T) {
	cfg := testConfig(t, "down", "up")
	results := []NodeResult{
		downNode("down", OutcomeUnreachable),
		runningNode("up", "qwen", 10),
	}
	got, err := cfg.choose(results, Want{Model: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "up" {
		t.Errorf("chose %q, want the reachable node", got.Node.Name)
	}
}

// A Spinloop's ALIAS is the other name a node might be serving under.
func TestAliasMatchesWhatTheNodeReports(t *testing.T) {
	cfg := testConfig(t, "a")
	results := []NodeResult{runningNode("a", "qwen3", 10)}
	if _, err := cfg.choose(results, Want{Model: "unsloth/Qwen3:Q4", Alias: "qwen3"}); err != nil {
		t.Errorf("alias should match what the node reports: %v", err)
	}
}

// A Spinloop naming no model wants any running engine.
func TestNoModelMatchesAnyRunningNode(t *testing.T) {
	cfg := testConfig(t, "a")
	if _, err := cfg.choose([]NodeResult{runningNode("a", "whatever", 1)}, Want{}); err != nil {
		t.Errorf("a launch naming no model should take any running node: %v", err)
	}
}

func TestNoneServingCarriesTheFleetState(t *testing.T) {
	cfg := testConfig(t, "idle", "other", "down")
	results := []NodeResult{
		idleNode("idle"),
		runningNode("other", "different-model", 10),
		downNode("down", OutcomeUnauthorized),
	}
	_, err := cfg.choose(results, Want{Model: "qwen"})
	var none *ErrNoneServing
	if !errors.As(err, &none) {
		t.Fatalf("err = %v, want ErrNoneServing", err)
	}
	msg := none.Error()
	for _, want := range []string{"qwen", "fleet.yaml", "idle", "different-model", "unauthorized"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should mention %q, got:\n%s", want, msg)
		}
	}
}

// A whole fleet that cannot be reached still says what happened to each node
// rather than one generic error.
func TestWholeFleetDownNamesEveryNode(t *testing.T) {
	cfg := testConfig(t, "a", "b")
	results := []NodeResult{downNode("a", OutcomeUnreachable), downNode("b", OutcomeConfigError)}
	_, err := cfg.choose(results, Want{Model: "qwen"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"a", "b", "unreachable", "config-error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got:\n%s", want, err)
		}
	}
}

// A pinned node running something else is not restarted: someone may be using
// it.
func TestPinnedNodeServingSomethingElseFails(t *testing.T) {
	cfg := testConfig(t, "gpu")
	results := []NodeResult{runningNode("gpu", "other-model", 5)}
	_, err := cfg.choose(results, Want{Model: "qwen", Node: "gpu"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"gpu", "other-model", "qwen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}
}

func TestPinnedNodeUnreachableFails(t *testing.T) {
	cfg := testConfig(t, "gpu")
	results := []NodeResult{downNode("gpu", OutcomeUnreachable)}
	_, err := cfg.choose(results, Want{Model: "qwen", Node: "gpu"})
	if err == nil || !strings.Contains(err.Error(), "gpu") {
		t.Fatalf("err = %v, want one naming the pinned node", err)
	}
}

func TestPreferencePrecedence(t *testing.T) {
	file := &Config{Prefer: PreferActive}
	empty := &Config{}

	cases := []struct {
		name string
		cfg  *Config
		flag string
		want Prefer
	}{
		{"flag beats the file", file, "idle", PreferIdle},
		{"the file beats the default", file, "", PreferActive},
		{"the default is idle", empty, "", PreferIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.Preference(tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("preference = %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := empty.Preference("sideways"); err == nil {
		t.Error("an unknown preference should fail")
	} else {
		for _, want := range []string{"idle", "active"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should name %q, got %q", want, err)
			}
		}
	}
}

func TestEngineBaseURL(t *testing.T) {
	cfg := &Config{Path: "fleet.yaml"}
	reported := daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080}}

	cases := []struct {
		name   string
		node   NodeConfig
		status daemon.StatusResponse
		want   string
	}{
		{
			name:   "the node's host with the engine's port",
			node:   NodeConfig{Name: "gpu", Host: "gpu-box"},
			status: reported,
			want:   "http://gpu-box:8080/v1",
		},
		{
			name:   "a reported path prefix is kept",
			node:   NodeConfig{Name: "gpu", Host: "gpu-box"},
			status: daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080, Path: "/openai"}},
			want:   "http://gpu-box:8080/openai",
		},
		{
			name:   "an override replaces what the daemon reports",
			node:   NodeConfig{Name: "gpu", Host: "gpu-box", Engine: &EngineOverride{Host: "proxy", Port: 9443, Path: "/openai"}},
			status: reported,
			want:   "http://proxy:9443/openai",
		},
		{
			name:   "a partial override falls back per field",
			node:   NodeConfig{Name: "gpu", Host: "gpu-box", Engine: &EngineOverride{Port: 18080}},
			status: reported,
			want:   "http://gpu-box:18080/v1",
		},
		{
			name:   "an override may name a whole origin",
			node:   NodeConfig{Name: "gpu", Host: "gpu-box", Engine: &EngineOverride{Host: "https://engine.example"}},
			status: reported,
			want:   "https://engine.example:8080/v1",
		},
		{
			name: "a reported engine host is used in place of the fleet file's",
			node: NodeConfig{Name: "env", Kind: "cloud"},
			status: daemon.StatusResponse{
				Engine: &daemon.EngineEndpoint{Host: "1.2.3.4", Port: 8000, Path: "/v1"},
			},
			want: "http://1.2.3.4:8000/v1",
		},
		{
			name: "an override still beats a reported engine host",
			node: NodeConfig{Name: "env", Kind: "cloud", Engine: &EngineOverride{Host: "proxy"}},
			status: daemon.StatusResponse{
				Engine: &daemon.EngineEndpoint{Host: "1.2.3.4", Port: 8000, Path: "/v1"},
			},
			want: "http://proxy:8000/v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cfg.EngineBaseURL(tc.node, tc.status)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("base URL = %q, want %q", got, tc.want)
			}
		})
	}
}

// A loopback-bound engine on a cloud node is refused with both remedies,
// rather than handed over as an address that cannot connect.
func TestLoopbackEngineIsRefused(t *testing.T) {
	cfg := &Config{Path: "fleet.yaml"}
	status := daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080, LoopbackOnly: true}}

	_, err := cfg.EngineBaseURL(NodeConfig{Name: "gpu", Host: "gpu-box"}, status)
	if err == nil {
		t.Fatal("a loopback engine on a cloud node should be refused")
	}
	for _, want := range []string{"gpu", "loopback", "--host", "fleet.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}

	// Reached over loopback it is fine.
	if _, err := cfg.EngineBaseURL(NodeConfig{Name: "local", Host: "127.0.0.1"}, status); err != nil {
		t.Errorf("a loopback engine on a loopback node is reachable: %v", err)
	}
	// And declaring an override is taking responsibility for reachability.
	node := NodeConfig{Name: "gpu", Host: "gpu-box", Engine: &EngineOverride{Port: 18080}}
	if _, err := cfg.EngineBaseURL(node, status); err != nil {
		t.Errorf("an engine override should silence the loopback refusal: %v", err)
	}
}

// A daemon too old to report an endpoint is named, with the way through.
func TestNoReportedEndpointIsNamed(t *testing.T) {
	cfg := &Config{Path: "fleet.yaml"}
	_, err := cfg.EngineBaseURL(NodeConfig{Name: "old", Host: "old-box"}, daemon.StatusResponse{})
	if err == nil {
		t.Fatal("a node reporting no endpoint should fail")
	}
	for _, want := range []string{"old", "upgrade", "fleet.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}

	// Unless the fleet file supplies the port itself.
	node := NodeConfig{Name: "old", Host: "old-box", Engine: &EngineOverride{Port: 8080}}
	if got, err := cfg.EngineBaseURL(node, daemon.StatusResponse{}); err != nil {
		t.Errorf("an override should cover an old daemon: %v", err)
	} else if got != "http://old-box:8080/v1" {
		t.Errorf("base URL = %q", got)
	}
}

func TestEngineKeyResolution(t *testing.T) {
	cfg := &Config{Path: "fleet.yaml", Dir: t.TempDir()}
	gated := daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080, RequiresKey: true}}
	open := daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080}}

	// An ungated engine needs nothing.
	if key, err := cfg.engineKeyFor(NodeConfig{Name: "a"}, open); err != nil || key != "" {
		t.Errorf("key = %q, %v; want empty and no error", key, err)
	}

	// A gated engine whose node names no variable fails, naming both.
	_, err := cfg.engineKeyFor(NodeConfig{Name: "gated"}, gated)
	if err == nil {
		t.Fatal("a gated engine with no key reference should fail")
	}
	for _, want := range []string{"gated", "engineTokenEnv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}

	t.Setenv("NODE_ENGINE_KEY", "sk-node")
	node := NodeConfig{Name: "gated", EngineTokenEnv: "NODE_ENGINE_KEY"}
	if key, err := cfg.engineKeyFor(node, gated); err != nil || key != "sk-node" {
		t.Errorf("key = %q, %v; want sk-node", key, err)
	}
}

// A remote's engine is always gated by its key — the control plane reports the
// instance, never the gate — so its key is looked up whatever the status says,
// from the node's own reference or the fleet's, and a remote with no resolvable
// key fails before a launch depends on it.
func TestCloudEngineKeyResolution(t *testing.T) {
	cfg := &Config{Path: "fleet.yaml", Dir: t.TempDir(), APIKeyEnv: "FLEET_KEY"}
	t.Setenv("FLEET_KEY", "sk-fleet")
	t.Setenv("NODE_ENGINE_KEY", "sk-node")
	// The control plane reports no engine gate at all.
	empty := daemon.StatusResponse{}

	// The fleet's key, by default.
	remote := NodeConfig{Name: "cloud", Kind: KindCloud}
	if key, err := cfg.engineKeyFor(remote, empty); err != nil || key != "sk-fleet" {
		t.Errorf("key = %q, %v; want sk-fleet", key, err)
	}

	// The node's own reference overrides it.
	own := NodeConfig{Name: "cloud", Kind: KindCloud, EngineTokenEnv: "NODE_ENGINE_KEY"}
	if key, err := cfg.engineKeyFor(own, empty); err != nil || key != "sk-node" {
		t.Errorf("key = %q, %v; want sk-node", key, err)
	}

	// No key named anywhere fails, naming the node and both places to fix it.
	cfg.APIKeyEnv = ""
	_, err := cfg.engineKeyFor(NodeConfig{Name: "cloud", Kind: KindCloud}, empty)
	if err == nil {
		t.Fatal("a remote with no key named should fail")
	}
	for _, want := range []string{"cloud", "engineTokenEnv", "apiKeyEnv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}

	// A daemon is untouched by the fleet's key: an ungated engine needs none.
	daemonNode := NodeConfig{Name: "box", Kind: KindDaemon, Host: "box.local"}
	if key, err := cfg.engineKeyFor(daemonNode, daemon.StatusResponse{Engine: &daemon.EngineEndpoint{Port: 8080}}); err != nil || key != "" {
		t.Errorf("daemon key = %q, %v; want empty and no error", key, err)
	}
}

// A node woken from a Spinloop reports the deploy config's model id, which is
// not the ALIAS the client asked for — a Spinloop may take its model from a
// preset and state no MODEL at all. Unless that id counts as a match, a second
// launch fails to recognise the node the first one started and has nothing
// left to wake.
func TestWokenNodeIsRecognisedOnASecondLaunch(t *testing.T) {
	cfg := testConfig(t, "local")
	// What the node reports after being woken: the resolved repo, not the
	// friendly name the Spinloop used.
	woken := []NodeResult{runningNode("local", "Zambizi/slim-gemma-4-12b-gguf", 5)}

	// The client knows only its ALIAS and the id it would have pushed.
	want := Want{
		Alias:   "gemma-4-12b-it",
		ModelID: "Zambizi/slim-gemma-4-12b-gguf",
	}
	got, err := cfg.choose(woken, want)
	if err != nil {
		t.Fatalf("a second launch should reuse the woken node: %v", err)
	}
	if got.Node.Name != "local" {
		t.Errorf("chose %q", got.Node.Name)
	}

	// Without the id it is genuinely unrecognisable, which is the bug.
	if _, err := cfg.choose(woken, Want{Alias: "gemma-4-12b-it"}); err == nil {
		t.Error("the alias alone should not match the reported repo id")
	}

	// And a node serving something else still does not match.
	other := []NodeResult{runningNode("local", "someone/else", 5)}
	if _, err := cfg.choose(other, want); err == nil {
		t.Error("an unrelated model should not match")
	}
}

// notReadyNode is a node whose engine process is up but has not answered its
// health check — llama.cpp fetching or loading weights, which is minutes on a
// large model and the whole time its port refuses connections.
func notReadyNode(name, model string) NodeResult {
	r := runningNode(name, model, -1)
	r.Status.Ready = daemon.ReadyNo
	return r
}

func TestNotReadyNodeIsNotSelected(t *testing.T) {
	cfg := testConfig(t, "loading", "up")
	results := []NodeResult{
		notReadyNode("loading", "qwen"),
		runningNode("up", "qwen", 10),
	}
	got, err := cfg.choose(results, Want{Model: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "up" {
		t.Errorf("chose %q, want the node whose engine has answered", got.Node.Name)
	}
}

// The only node running the model has not answered yet: routing there would
// fail the request, so nothing is serving it.
func TestNotReadyIsTheSameAsNothingServing(t *testing.T) {
	cfg := testConfig(t, "loading")
	results := []NodeResult{notReadyNode("loading", "qwen")}
	_, err := cfg.choose(results, Want{Model: "qwen"})
	var none *ErrNoneServing
	if !errors.As(err, &none) {
		t.Fatalf("err = %v, want ErrNoneServing", err)
	}
	if !strings.Contains(none.Error(), "not ready") {
		t.Errorf("the fleet state should mark the node not ready, got:\n%s", none.Error())
	}
}

// A readiness reading that never landed — an older daemon, or a runner with no
// known health check — is unknown, not false, so it must not exclude a node.
func TestUnknownReadinessStillSelects(t *testing.T) {
	cfg := testConfig(t, "old")
	results := []NodeResult{runningNode("old", "qwen", 10)}
	if results[0].Status.Ready != "" {
		t.Fatal("this fixture should report no readiness reading")
	}
	if _, err := cfg.choose(results, Want{Model: "qwen"}); err != nil {
		t.Errorf("a node reporting no readiness should still be selected: %v", err)
	}
}

func TestPinnedNodeStillStartingSaysSo(t *testing.T) {
	cfg := testConfig(t, "gpu")
	results := []NodeResult{notReadyNode("gpu", "qwen")}
	_, err := cfg.choose(results, Want{Model: "qwen", Node: "gpu"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"gpu", "qwen", "has not answered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q, got: %v", want, err)
		}
	}
}

// Loading is what lets a caller tell "nothing is serving this" from "something
// is about to", so it names the still-starting nodes and nothing else.
func TestLoadingNamesTheStartingNodes(t *testing.T) {
	cfg := testConfig(t, "loading", "up", "idle", "other")
	results := []NodeResult{
		notReadyNode("loading", "qwen"),
		runningNode("up", "qwen", 10),
		idleNode("idle"),
		notReadyNode("other", "different-model"),
	}
	got := cfg.Loading(results, Want{Model: "qwen"})
	if len(got) != 1 || got[0].Name != "loading" {
		t.Fatalf("Loading() = %v, want just the node starting qwen", got)
	}
}

func TestEndpointBaseURL(t *testing.T) {
	cases := []struct {
		target string
		want   string
	}{
		{"http://gw:4000", "http://gw:4000/v1"},
		{"http://gw:4000/", "http://gw:4000/v1"},
		{"http://gw:4000/custom", "http://gw:4000/custom"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		if got := EndpointBaseURL(c.target); got != c.want {
			t.Errorf("EndpointBaseURL(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}
