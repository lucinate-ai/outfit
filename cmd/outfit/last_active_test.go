package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The last-active figure appears in four places — `remote metrics` in bar,
// table and json, `remote status`, and `fleet metrics` — and every one of them
// gates on the timestamp rather than the seconds. These tests hold that line,
// because gating on the seconds hides the busiest engine there is: the daemon
// omits idleSeconds at zero, so an engine working this instant sends a
// timestamp and nothing else.

// statsServer stands in for the stats Lambda, replying with whatever the test
// wants `outfit remote metrics` to render.
func statsServer(t *testing.T, body string) {
	t.Helper()
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	writeRemoteConfig(t, server.URL)
	t.Setenv("OUTFIT_REMOTE_STATS_URL", server.URL)
}

const runningWithActivity = `{
	"environment": "dev",
	"state": "running",
	"instanceType": "g6e.xlarge",
	"modelId": "unsloth/Qwen3.6-27B",
	"uptimeSeconds": 3725,
	"cpu": {"utilization": 24},
	"memory": {"total": 17179869184, "used": 4294967296},
	"tokens": {"running": 1, "promptTokens": 50000, "generationTokens": 120000, "requests": 342},
	"lastActiveAt": "2026-08-10T10:00:00Z",
	"idleSeconds": 125
}`

func TestRemoteMetricsBarShowsLastActive(t *testing.T) {
	statsServer(t, runningWithActivity)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=bar"}); err != nil {
			t.Fatalf("cmdRemoteMetrics: %v", err)
		}
	})
	if !strings.Contains(out, "last active 2m 5s ago") {
		t.Errorf("bar format missing the last-active line:\n%s", out)
	}
	// It belongs between the header and the bars: a fact about the endpoint,
	// read before the utilisation readings rather than among them.
	header := strings.Index(out, "g6e.xlarge")
	active := strings.Index(out, "last active")
	bars := strings.Index(out, "CPU")
	if !(header < active && active < bars) {
		t.Errorf("last-active line is not between the header and the bars:\n%s", out)
	}
}

func TestRemoteMetricsTableShowsLastActive(t *testing.T) {
	statsServer(t, runningWithActivity)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=table"}); err != nil {
			t.Fatalf("cmdRemoteMetrics: %v", err)
		}
	})
	// Padded to the same key column as its neighbours, and beside uptime.
	if !strings.Contains(out, "last active:  2m 5s ago") {
		t.Errorf("table format missing the last-active row:\n%s", out)
	}
	if strings.Index(out, "uptime:") > strings.Index(out, "last active:") {
		t.Errorf("last active should follow uptime:\n%s", out)
	}
}

func TestRemoteMetricsJSONCarriesLastActive(t *testing.T) {
	statsServer(t, runningWithActivity)

	out := captureStdout(t, func() {
		if err := cmdRemoteMetrics([]string{"--format=json"}); err != nil {
			t.Fatalf("cmdRemoteMetrics: %v", err)
		}
	})
	var decoded struct {
		LastActiveAt string `json:"lastActiveAt"`
		IdleSeconds  int    `json:"idleSeconds"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	// Unformatted: a consumer wanting a duration has the seconds, one wanting
	// the fact has the timestamp.
	if decoded.LastActiveAt != "2026-08-10T10:00:00Z" || decoded.IdleSeconds != 125 {
		t.Errorf("json = %+v, want the fields carried through unchanged", decoded)
	}
}

// A stopped endpoint draws no bars and no token block, but when it last did
// work is exactly what a stopped endpoint is worth asking about.
func TestRemoteMetricsStoppedStillShowsLastActive(t *testing.T) {
	statsServer(t, `{
		"environment": "dev",
		"state": "stopped",
		"runner": "llamacpp",
		"modelId": "unsloth/Qwen3.6-27B",
		"lastActiveAt": "2026-08-10T10:00:00Z",
		"idleSeconds": 3600
	}`)

	// The bar format indents the line to the bar-label column; the table
	// format makes it a key-value row. Same fact, each format's own idiom.
	for format, want := range map[string]string{
		"bar":   "last active 1h 0m 0s ago",
		"table": "last active:  1h 0m 0s ago",
	} {
		t.Run(format, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := cmdRemoteMetrics([]string{"--format=" + format}); err != nil {
					t.Fatalf("cmdRemoteMetrics: %v", err)
				}
			})
			if !strings.Contains(out, want) {
				t.Errorf("%s format dropped the last-active figure when stopped:\n%s", format, out)
			}
			if strings.Contains(out, "CPU") || strings.Contains(out, "prompt tokens") {
				t.Errorf("%s format drew running-engine figures for a stopped endpoint:\n%s", format, out)
			}
		})
	}
}

// The omit-at-zero trap: idleSeconds is absent while the engine is working, so
// a renderer gating on it would hide the line exactly when it matters most.
func TestLastActiveZeroIdleStillRenders(t *testing.T) {
	statsServer(t, `{
		"environment": "dev",
		"state": "running",
		"cpu": {"utilization": 90},
		"lastActiveAt": "2026-08-10T10:00:00Z"
	}`)

	for format, want := range map[string]string{
		"bar":   "last active 0s ago",
		"table": "last active:  0s ago",
	} {
		t.Run(format, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := cmdRemoteMetrics([]string{"--format=" + format}); err != nil {
					t.Fatalf("cmdRemoteMetrics: %v", err)
				}
			})
			if !strings.Contains(out, want) {
				t.Errorf("%s format hid an engine that is working right now:\n%s", format, out)
			}
		})
	}
}

func TestLastActiveOmittedWithoutATimestamp(t *testing.T) {
	statsServer(t, `{
		"environment": "dev",
		"state": "running",
		"uptimeSeconds": 60,
		"cpu": {"utilization": 24}
	}`)

	for _, format := range []string{"bar", "table"} {
		t.Run(format, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := cmdRemoteMetrics([]string{"--format=" + format}); err != nil {
					t.Fatalf("cmdRemoteMetrics: %v", err)
				}
			})
			// No line at all, rather than one implying it has sat unused.
			if strings.Contains(out, "last active") {
				t.Errorf("%s format invented activity from nothing:\n%s", format, out)
			}
			if !strings.Contains(out, "CPU") {
				t.Errorf("%s format lost the rest of the report:\n%s", format, out)
			}
		})
	}
}

// statusServer stands in for the start Lambda's GET branch, which is what
// `outfit remote status` calls.
func statusServer(t *testing.T, body string) {
	t.Helper()
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	writeRemoteConfig(t, server.URL)
}

func TestRemoteStatusShowsLastActive(t *testing.T) {
	statusServer(t, `{
		"state": "running",
		"healthy": true,
		"base_url": "http://198.51.100.7:8000",
		"lastActiveAt": "2026-08-10T10:00:00Z",
		"idleSeconds": 125
	}`)

	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Fatalf("cmdRemoteStatus: %v", err)
		}
	})
	if !strings.Contains(out, "last active: 2m 5s ago") {
		t.Errorf("status missing the last-active line:\n%s", out)
	}
	// The lines it already printed are untouched.
	for _, want := range []string{"state: running", "healthy: true", "base_url: http://198.51.100.7:8000"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestRemoteStatusZeroIdleStillRenders(t *testing.T) {
	statusServer(t, `{"state": "running", "healthy": true, "lastActiveAt": "2026-08-10T10:00:00Z"}`)

	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Fatalf("cmdRemoteStatus: %v", err)
		}
	})
	if !strings.Contains(out, "last active: 0s ago") {
		t.Errorf("status hid an endpoint that is working right now:\n%s", out)
	}
}

// A stopped instance cannot be asked — reaching the daemon needs a running
// box — so the control plane sends nothing and the command claims nothing.
func TestRemoteStatusOmitsLastActiveWhenAbsent(t *testing.T) {
	statusServer(t, `{"state": "stopped", "healthy": false}`)

	out := captureStdout(t, func() {
		if err := cmdRemoteStatus(nil); err != nil {
			t.Fatalf("cmdRemoteStatus: %v", err)
		}
	})
	if strings.Contains(out, "last active") {
		t.Errorf("status invented activity for a stopped instance:\n%s", out)
	}
	if !strings.Contains(out, "state: stopped") {
		t.Errorf("status lost the rest of the report:\n%s", out)
	}
}

// fleetNodeWithMetrics writes a one-node fleet whose /v1/metrics reply is
// whatever the test needs.
func fleetNodeWithMetrics(t *testing.T, metrics map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(metrics)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))
}

func TestFleetMetricsShowsLastActive(t *testing.T) {
	fleetNodeWithMetrics(t, map[string]any{
		"state": "running", "runner": "llamacpp", "modelId": "org/qwen",
		"cpu":          map[string]any{"utilization": 30.0},
		"lastActiveAt": "2026-08-10T10:00:00Z",
		"idleSeconds":  125,
	})

	for _, format := range []string{"bar", "table"} {
		t.Run(format, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := cmdFleet([]string{"metrics", "--format=" + format}); err != nil {
					t.Fatalf("cmdFleet metrics: %v", err)
				}
			})
			if !strings.Contains(out, "last active 2m 5s ago") {
				t.Errorf("fleet %s metrics missing the last-active line:\n%s", format, out)
			}
		})
	}
}

// The JSON branch is a separate render path from the text one — it wraps each
// node's stats in `any` before marshalling — so a consumer scripting against
// it needs its own assurance the pair survives the trip.
func TestFleetMetricsJSONCarriesLastActive(t *testing.T) {
	fleetNodeWithMetrics(t, map[string]any{
		"state": "running", "runner": "llamacpp", "modelId": "org/qwen",
		"lastActiveAt": "2026-08-10T10:00:00Z",
		"idleSeconds":  125,
	})

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"metrics", "--format=json"}); err != nil {
			t.Fatalf("cmdFleet metrics: %v", err)
		}
	})
	var decoded []struct {
		Node    string `json:"node"`
		Metrics struct {
			LastActiveAt string `json:"lastActiveAt"`
			IdleSeconds  int    `json:"idleSeconds"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(decoded) != 1 {
		t.Fatalf("got %d entries, want one per node", len(decoded))
	}
	if decoded[0].Metrics.LastActiveAt != "2026-08-10T10:00:00Z" || decoded[0].Metrics.IdleSeconds != 125 {
		t.Errorf("node metrics = %+v, want the activity pair carried through", decoded[0].Metrics)
	}
}

func TestFleetMetricsOmitsLastActiveWithoutActivity(t *testing.T) {
	fleetNodeWithMetrics(t, map[string]any{
		"state": "running", "runner": "llamacpp", "modelId": "org/qwen",
		"cpu": map[string]any{"utilization": 30.0},
	})

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"metrics"}); err != nil {
			t.Fatalf("cmdFleet metrics: %v", err)
		}
	})
	if strings.Contains(out, "last active") {
		t.Errorf("fleet metrics claimed activity a node never reported:\n%s", out)
	}
}

// A node whose engine has stopped still answers the question the figure
// exists for, so the line survives the non-running short-circuit.
func TestFleetMetricsStoppedNodeStillShowsLastActive(t *testing.T) {
	fleetNodeWithMetrics(t, map[string]any{
		"state": "stopped", "runner": "llamacpp", "modelId": "org/qwen",
		"lastActiveAt": "2026-08-10T10:00:00Z",
		"idleSeconds":  600,
	})

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"metrics"}); err != nil {
			t.Fatalf("cmdFleet metrics: %v", err)
		}
	})
	if !strings.Contains(out, "last active 10m 0s ago") {
		t.Errorf("a stopped node dropped its last-active figure:\n%s", out)
	}
}
