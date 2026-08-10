package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stubNode serves a daemon control API for one fleet node.
func stubNode(t *testing.T, state string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"state": state, "runner": "llamacpp", "model": "org/qwen", "uptimeSeconds": 65,
		})
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"state": state, "runner": "llamacpp", "modelId": "org/qwen",
			"cpu":    map[string]any{"utilization": 30.0},
			"memory": map[string]any{"total": 1000, "used": 400},
			"tokens": map[string]any{"running": 2, "promptTokens": 4096, "generationTokens": 1024, "requests": 17},
		})
	})
	mux.HandleFunc("POST /v1/start", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"state": "running"})
	})
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"state": "stopped"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// hostPort splits an httptest server URL into host and port.
func hostPort(t *testing.T, srv *httptest.Server) (string, int) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), port
}

// writeFleetFile writes a fleet.yaml in a temp dir, chdirs there, and returns
// the directory — so the commands resolve ./fleet.yaml the way a user would.
func writeFleetFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// twoNodeFleet writes a fleet of one reachable node and one that is down.
func twoNodeFleet(t *testing.T, state string) *httptest.Server {
	t.Helper()
	up := stubNode(t, state)
	host, port := hostPort(t, up)
	writeFleetFile(t, fmt.Sprintf(
		"nodes:\n  - name: up\n    host: %s\n    port: %d\n  - name: down\n    host: 127.0.0.1\n    port: 1\n",
		host, port))
	return up
}

func TestCmdFleetStatusRendersEveryNode(t *testing.T) {
	twoNodeFleet(t, "running")
	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"status"}); err != nil {
			// One unreachable node must not fail the command.
			t.Errorf("fleet status returned %v", err)
		}
	})
	if !strings.Contains(out, "NODE") || !strings.Contains(out, "STATE") {
		t.Errorf("no header:\n%s", out)
	}
	// The reachable node shows its state and what it serves.
	if !strings.Contains(out, "up") || !strings.Contains(out, "running") || !strings.Contains(out, "org/qwen") {
		t.Errorf("reachable node row missing:\n%s", out)
	}
	// The unreachable node is a row with its reason, not an omission.
	if !strings.Contains(out, "down") || !strings.Contains(out, "unreachable") {
		t.Errorf("unreachable node not rendered:\n%s", out)
	}
}

func TestCmdFleetMetricsFormats(t *testing.T) {
	twoNodeFleet(t, "running")

	t.Run("bar", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := cmdFleet([]string{"metrics"}); err != nil {
				t.Error(err)
			}
		})
		if !strings.Contains(out, "CPU") || !strings.Contains(out, "RAM") {
			t.Errorf("no resource bars:\n%s", out)
		}
		if !strings.Contains(out, "prompt tokens") {
			t.Errorf("no token block:\n%s", out)
		}
		if !strings.Contains(out, "unreachable") {
			t.Errorf("unreachable node omitted:\n%s", out)
		}
	})

	t.Run("table", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := cmdFleet([]string{"metrics", "--format=table"}); err != nil {
				t.Error(err)
			}
		})
		if !strings.Contains(out, "CPU:") || !strings.Contains(out, "RAM:") {
			t.Errorf("no table lines:\n%s", out)
		}
	})

	t.Run("json covers the whole fleet", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := cmdFleet([]string{"metrics", "--format=json"}); err != nil {
				t.Error(err)
			}
		})
		var decoded []struct {
			Node    string          `json:"node"`
			Outcome string          `json:"outcome"`
			Error   string          `json:"error"`
			Metrics json.RawMessage `json:"metrics"`
		}
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, out)
		}
		if len(decoded) != 2 {
			t.Fatalf("got %d entries, want one per node", len(decoded))
		}
		// File order is preserved and every node is accounted for — the
		// unreachable one included, with its reason.
		if decoded[0].Node != "up" || decoded[0].Outcome != "ok" || len(decoded[0].Metrics) == 0 {
			t.Errorf("entry 0 = %+v", decoded[0])
		}
		if decoded[1].Node != "down" || decoded[1].Outcome != "unreachable" || decoded[1].Error == "" {
			t.Errorf("entry 1 = %+v", decoded[1])
		}
	})
}

func TestCmdFleetMetricsRejectsBadFormat(t *testing.T) {
	twoNodeFleet(t, "running")
	if err := cmdFleet([]string{"metrics", "--format=csv"}); err == nil {
		t.Fatal("--format=csv accepted")
	}
}

func TestCmdFleetStartStopDriveOneNode(t *testing.T) {
	twoNodeFleet(t, "idle")

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"start", "up"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "up") || !strings.Contains(out, "running") {
		t.Errorf("start output = %q", out)
	}

	out = captureStdout(t, func() {
		if err := cmdFleet([]string{"stop", "up"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "stopped") {
		t.Errorf("stop output = %q", out)
	}
}

// Mutating verbs are single-node by contract: with no node they list the
// fleet and touch nothing.
func TestCmdFleetStartStopRequireANode(t *testing.T) {
	twoNodeFleet(t, "idle")
	for _, verb := range []string{"start", "stop"} {
		err := cmdFleet([]string{verb})
		if err == nil {
			t.Fatalf("fleet %s with no node was accepted", verb)
		}
		// The message lists the fleet so the user can see what to type.
		for _, want := range []string{"up", "down"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("fleet %s error %q does not list node %q", verb, err, want)
			}
		}
	}
}

func TestCmdFleetUnknownNodeNamesTheKnownOnes(t *testing.T) {
	twoNodeFleet(t, "idle")
	err := cmdFleet([]string{"stop", "nope"})
	if err == nil {
		t.Fatal("unknown node accepted")
	}
	if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "up") {
		t.Errorf("error %q should name the unknown node and the known ones", err)
	}
}

func TestCmdFleetMissingFileNamesThePath(t *testing.T) {
	t.Chdir(t.TempDir())
	err := cmdFleet([]string{"status"})
	if err == nil {
		t.Fatal("missing fleet file accepted")
	}
	if !strings.Contains(err.Error(), "fleet.yaml") {
		t.Errorf("error %q does not name the expected path", err)
	}
}

func TestCmdFleetExplicitPath(t *testing.T) {
	up := stubNode(t, "running")
	host, port := hostPort(t, up)
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster.yaml")
	body := fmt.Sprintf("nodes:\n  - name: solo\n    host: %s\n    port: %d\n", host, port)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Somewhere else entirely, so only --fleet can find it.
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"status", "--fleet", path}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "solo") {
		t.Errorf("explicit --fleet not used:\n%s", out)
	}
}

func TestCmdFleetUnknownSubcommand(t *testing.T) {
	if err := cmdFleet([]string{"wat"}); err == nil {
		t.Fatal("unknown subcommand accepted")
	}
	if err := cmdFleet(nil); err == nil {
		t.Fatal("no subcommand accepted")
	}
}

// Watch mode redraws until interrupted, then exits cleanly.
func TestCmdFleetMetricsWatchExitsOnInterrupt(t *testing.T) {
	twoNodeFleet(t, "running")

	// Keep the loop tight so the test does not wait a minute for a refresh.
	orig := metricsWatchInterval
	metricsWatchInterval = 50 * time.Millisecond
	t.Cleanup(func() { metricsWatchInterval = orig })

	done := make(chan error, 1)
	go func() { done <- cmdFleet([]string{"metrics", "--watch"}) }()

	// Let it draw at least twice, so the clear-and-redraw path runs.
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch exited with %v, want a clean exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not exit on SIGINT")
	}
}

// The daemon tracks when its engine last did work; the fleet view is where
// "which node is doing nothing?" gets asked, so the row shows it.
func TestCmdFleetStatusShowsIdleTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"state": "running", "runner": "llamacpp", "model": "org/qwen",
			"uptimeSeconds": 600,
			"lastActiveAt":  "2026-08-10T10:00:00Z",
			"idleSeconds":   125,
		})
	}))
	defer srv.Close()
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: busy\n    host: %s\n    port: %d\n", host, port))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"status"}); err != nil {
			t.Error(err)
		}
	})
	// 125s formats the same way the uptime column does, so the two read alike.
	if !strings.Contains(out, "last active 2m 5s ago") {
		t.Errorf("last-active time missing or misformatted:\n%s", out)
	}
}

// A daemon that has recorded no activity yet must not be reported as idle
// since boot — there is no last-active time to measure from.
func TestCmdFleetStatusOmitsIdleWithoutActivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"state": "running", "runner": "llamacpp", "model": "org/qwen",
			"uptimeSeconds": 600,
		})
	}))
	defer srv.Close()
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: fresh\n    host: %s\n    port: %d\n", host, port))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"status"}); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "last active") {
		t.Errorf("activity claimed when the daemon recorded none:\n%s", out)
	}
	// The rest of the row is unaffected.
	if !strings.Contains(out, "up 10m 0s") {
		t.Errorf("uptime missing:\n%s", out)
	}
}
