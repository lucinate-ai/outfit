package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/outfit"
)

// routableNode is a daemon answering status for one machine, with a listener
// standing in for its engine so a routed launch has something that answers.
type routableNode struct {
	srv        *httptest.Server
	enginePort int
	engineLn   net.Listener
	started    bool
}

// newRoutableNode serves `model` when running is true, with a live engine.
func newRoutableNode(t *testing.T, model string, running bool, idleSeconds int) *routableNode {
	t.Helper()
	n := &routableNode{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n.engineLn = ln
	n.enginePort = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ln.Close() })

	state := string(daemon.StateIdle)
	if running {
		state = string(daemon.StateRunning)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		resp := daemon.StatusResponse{State: state, Model: model}
		if state == string(daemon.StateRunning) {
			resp.Engine = &daemon.EngineEndpoint{Port: n.enginePort}
			resp.LastActiveAt = "2026-08-12T10:00:00Z"
			resp.IdleSeconds = idleSeconds
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
		n.started = true
		state = string(daemon.StateRunning)
		json.NewEncoder(w).Encode(daemon.StatusResponse{State: state, Model: model})
	})
	n.srv = httptest.NewServer(mux)
	t.Cleanup(n.srv.Close)
	return n
}

// entry renders this node as a fleet.yaml entry.
func (n *routableNode) entry(name string) string {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(n.srv.URL, "http://"))
	return "  - name: " + name + "\n    host: " + host + "\n    port: " + port + "\n"
}

// fleetFileIn puts a fleet.yaml in dir and returns its path. (fleet_test.go's
// writeFleetFile chdirs into a temp dir, which routing tests do not want.)
func fleetFileIn(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "fleet.yaml")
	mustWrite(t, path, body)
	return path
}

// routedOutfit writes an Outfit naming a fleet, and returns its directory.
func routedOutfit(t *testing.T, model, fleetPath string) string {
	t.Helper()
	dir := t.TempDir()
	body := "PROVIDER llamacpp\nMODEL " + model + "\n"
	if fleetPath != "" {
		body += "FLEET " + fleetPath + "\n"
	}
	mustWrite(t, filepath.Join(dir, "Outfit"), body)
	return dir
}

func TestRouteChoosesARunningNode(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	var choice *struct{}
	_ = choice
	stderr := captureStderr(t, func() {
		c, err := routeThroughFleet(sel, path, routeOptions{})
		if err != nil {
			t.Fatalf("routing failed: %v", err)
		}
		if c == nil {
			t.Fatal("an Outfit naming a FLEET should route")
		}
		want := "http://127.0.0.1:" + strconv.Itoa(node.enginePort) + "/v1"
		if c.BaseURL != want {
			t.Errorf("base URL = %q, want %q", c.BaseURL, want)
		}
		if c.Node.Name != "gpu-box" {
			t.Errorf("chose %q", c.Node.Name)
		}
	})
	// The choice is announced before anything launches.
	for _, want := range []string{"gpu-box", "prefer idle"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the route should be announced with %q, got:\n%s", want, stderr)
		}
	}
}

// An Outfit naming no FLEET, with no --fleet, contacts nothing.
func TestNoFleetDoesNotRoute(t *testing.T) {
	outfitDir := routedOutfit(t, "qwen3-27b", "")
	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := routeThroughFleet(sel, path, routeOptions{})
	if err != nil || choice != nil {
		t.Errorf("choice = %+v, err = %v; want no routing at all", choice, err)
	}
}

// The flag overrides the Outfit's own FLEET.
func TestFleetFlagOverridesTheInstruction(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	flagFleet := fleetFileIn(t, dir, "nodes:\n"+node.entry("from-flag"))
	outfitDir := routedOutfit(t, "qwen3-27b", filepath.Join(dir, "nonexistent.yaml"))

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	captureStderr(t, func() {
		c, err := routeThroughFleet(sel, path, routeOptions{fleetPath: flagFleet})
		if err != nil {
			t.Fatalf("routing failed: %v", err)
		}
		if c.Node.Name != "from-flag" {
			t.Errorf("chose %q, want the flag's fleet", c.Node.Name)
		}
	})
}

// A pinned BASEURL is the explicit answer, so nothing is selected.
func TestPinnedBaseURLSkipsRouting(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "Outfit"),
		"PROVIDER llamacpp\nMODEL qwen3-27b\nBASEURL http://pinned:9999/v1\nFLEET "+fleetPath+"\n")

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	var choice any
	stderr := captureStderr(t, func() {
		c, err := routeThroughFleet(sel, path, routeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		choice = c
		if c != nil {
			t.Errorf("a pinned BASEURL should not be routed, got %+v", c)
		}
	})
	_ = choice
	if !strings.Contains(stderr, "Not routing") || !strings.Contains(stderr, "pinned") {
		t.Errorf("outfit should say it is not routing, got:\n%s", stderr)
	}
}

// --no-wake refuses to start anything and shows the fleet.
func TestNoWakeFailsWithTheNodeTable(t *testing.T) {
	node := newRoutableNode(t, "", false, 0)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("idle-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	captureStderr(t, func() {
		_, err := routeThroughFleet(sel, path, routeOptions{noWake: true})
		if err == nil {
			t.Fatal("--no-wake with nothing serving should fail")
		}
		for _, want := range []string{"idle-box", "qwen3-27b", "outfit fleet start"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message should mention %q, got:\n%s", want, err)
			}
		}
	})
	if node.started {
		t.Error("--no-wake started an engine")
	}
}

// A FLEET naming a URL is the gateway shape: it parses, and says plainly that
// it is not implemented rather than being treated as a filename.
func TestFleetURLIsRefusedAsUnimplemented(t *testing.T) {
	outfitDir := routedOutfit(t, "qwen3-27b", "http://gateway.internal:4000")
	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = routeThroughFleet(sel, path, routeOptions{})
	if err == nil {
		t.Fatal("a gateway URL should fail for now")
	}
	if !strings.Contains(err.Error(), "not implemented yet") {
		t.Errorf("error should say it is not implemented, got: %v", err)
	}
}

func TestUnknownPreferenceIsRefused(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	captureStderr(t, func() {
		if _, err := routeThroughFleet(sel, path, routeOptions{prefer: "sideways"}); err == nil {
			t.Fatal("an unknown preference should fail")
		} else if !strings.Contains(err.Error(), "idle") || !strings.Contains(err.Error(), "active") {
			t.Errorf("error should name both values, got: %v", err)
		}
	})
}

// A routed launch points the agent at the chosen node, and an explicit setting
// in the environment still wins.
func TestRoutedLaunchEnvironment(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	sel, path, err := readOutfit("test", outfitDir)
	if err != nil {
		t.Fatal(err)
	}
	var baseURL string
	captureStderr(t, func() {
		c, err := routeThroughFleet(sel, path, routeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		baseURL = c.BaseURL
	})

	t.Setenv("OPENAI_BASE_URL", "")
	env := setEnvIfBlank(os.Environ(), "OPENAI_BASE_URL", baseURL)
	if got, _ := envValue(env, "OPENAI_BASE_URL"); got != baseURL {
		t.Errorf("OPENAI_BASE_URL = %q, want the chosen node %q", got, baseURL)
	}

	// An exported value is a deliberate choice and is left alone.
	exported := append(os.Environ(), "OPENAI_BASE_URL=http://exported/v1")
	exported = setEnvIfBlank(exported, "OPENAI_BASE_URL", baseURL)
	if got, _ := envValue(exported, "OPENAI_BASE_URL"); got != "http://exported/v1" {
		t.Errorf("an exported base URL should win, got %q", got)
	}
}

// A failed route must leave the harness config exactly as it was.
func TestFailedRouteLeavesTheConfigUntouched(t *testing.T) {
	home := isolateConfig(t)
	node := newRoutableNode(t, "", false, 0)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("idle-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	h, _ := harness.Lookup("opencode")
	var err error
	captureStderr(t, func() {
		captureStdout(t, func() {
			_, _, _, _, err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: outfitDir}, "", h, nil,
				routeOptions{noWake: true})
		})
	})
	if err == nil {
		t.Fatal("a launch with nothing to route to should fail")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(statErr) {
		t.Errorf("the config was written by a launch that then failed (stat: %v)", statErr)
	}
}

// The chosen node's address is what the apply writes, in the slot a REMOTE
// endpoint's address goes to.
func TestRoutedApplyWritesTheChosenBaseURL(t *testing.T) {
	isolateConfig(t)
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	h, _ := harness.Lookup("opencode")
	var sel outfit.Selection
	var err error
	captureStderr(t, func() {
		captureStdout(t, func() {
			sel, _, _, _, err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: outfitDir}, "", h, nil, routeOptions{})
		})
	})
	if err != nil {
		t.Fatalf("routed apply failed: %v", err)
	}
	want := "http://127.0.0.1:" + strconv.Itoa(node.enginePort) + "/v1"
	if sel.BaseURL != want {
		t.Errorf("applied base URL = %q, want the chosen node's %q", sel.BaseURL, want)
	}
}

// `fleet route` explains the choice a launch would make.
func TestCmdFleetRouteExplainsTheChoice(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 42)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "prefer: active\nnodes:\n"+node.entry("gpu-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	out := captureStdout(t, func() {
		if err := cmdFleetRoute([]string{filepath.Join(outfitDir, "Outfit")}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"gpu-box", "Prefer: active", strconv.Itoa(node.enginePort), "42s ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should mention %q, got:\n%s", want, out)
		}
	}
}

// The flag lets the two preferences be compared on a live fleet without
// editing the file.
func TestCmdFleetRoutePreferenceFlagBeatsTheFile(t *testing.T) {
	recent := newRoutableNode(t, "qwen3-27b", true, 5)
	stale := newRoutableNode(t, "qwen3-27b", true, 5000)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir,
		"prefer: idle\nnodes:\n"+recent.entry("recent")+stale.entry("stale"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)
	outfitFile := filepath.Join(outfitDir, "Outfit")

	// The file says idle, so the long-idle node wins.
	out := captureStdout(t, func() {
		if err := cmdFleetRoute([]string{outfitFile}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Would use stale") {
		t.Errorf("the file's preference should choose stale, got:\n%s", out)
	}

	// The flag overrides it, and says so.
	out = captureStdout(t, func() {
		if err := cmdFleetRoute([]string{"--prefer", "active", outfitFile}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Would use recent") || !strings.Contains(out, "Prefer: active") {
		t.Errorf("the flag should choose recent and name itself, got:\n%s", out)
	}

	// ...and the file is untouched.
	body, err := os.ReadFile(fleetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "prefer: idle") {
		t.Errorf("the fleet file was rewritten:\n%s", body)
	}
}

// Routing changes nothing: nothing is started, nothing is written.
func TestCmdFleetRouteStartsNothing(t *testing.T) {
	home := isolateConfig(t)
	node := newRoutableNode(t, "", false, 0)
	dir := t.TempDir()
	fleetPath := fleetFileIn(t, dir, "nodes:\n"+node.entry("idle-box"))
	outfitDir := routedOutfit(t, "qwen3-27b", fleetPath)

	out := captureStdout(t, func() {
		if err := cmdFleetRoute([]string{filepath.Join(outfitDir, "Outfit")}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "would wake idle-box") {
		t.Errorf("output should name the node a launch would wake, got:\n%s", out)
	}
	if !strings.Contains(out, "Nothing has been started") {
		t.Errorf("output should say nothing was started, got:\n%s", out)
	}
	if node.started {
		t.Error("fleet route started an engine")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Errorf("fleet route wrote a harness config (stat: %v)", err)
	}
}

// An Outfit naming no fleet, with no --fleet, has nothing to report.
func TestCmdFleetRouteNeedsAFleet(t *testing.T) {
	outfitDir := routedOutfit(t, "qwen3-27b", "")
	err := cmdFleetRoute([]string{filepath.Join(outfitDir, "Outfit")})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "--fleet") {
		t.Errorf("error should suggest --fleet, got: %v", err)
	}
}

// A relative FLEET is resolved against the Outfit that names it, as PRESET and
// REMOTE are — otherwise the same Outfit routes from one directory and not
// another.
func TestRelativeFleetResolvesAgainstTheOutfit(t *testing.T) {
	node := newRoutableNode(t, "qwen3-27b", true, 10)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	mustWrite(t, filepath.Join(dir, "Outfit"),
		"PROVIDER llamacpp\nMODEL qwen3-27b\nFLEET fleet.yaml\n")

	// Run from somewhere else entirely: the Outfit still finds its fleet.
	t.Chdir(t.TempDir())
	sel, path, err := readOutfit("test", filepath.Join(dir, "Outfit"))
	if err != nil {
		t.Fatal(err)
	}
	captureStderr(t, func() {
		c, err := routeThroughFleet(sel, path, routeOptions{})
		if err != nil {
			t.Fatalf("a relative FLEET should resolve beside its Outfit: %v", err)
		}
		if c.Node.Name != "gpu-box" {
			t.Errorf("chose %q", c.Node.Name)
		}
	})
}
