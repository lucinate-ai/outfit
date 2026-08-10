package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// stubDaemon serves the control API endpoints a node exposes. token is the
// bearer it requires ("" = none); state is what its status reports.
func stubDaemon(t *testing.T, token, state string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if token == "" {
			return true
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid bearer token"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"state": state, "runner": "llamacpp", "model": "org/model", "uptimeSeconds": 42,
		})
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"state": state, "runner": "llamacpp", "modelId": "org/model",
			"cpu":    map[string]any{"utilization": 30.0},
			"memory": map[string]any{"total": 1000, "used": 400},
		})
	})
	mux.HandleFunc("POST /v1/start", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if state == "running" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "an engine is already running (llama-server); stop it first"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"state": "running"})
	})
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"state": "stopped"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fleetFor writes a fleet.yaml pointing one node at srv, and returns the config.
func fleetFor(t *testing.T, srv *httptest.Server, tokenEnv string) *Config {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	body := fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", u.Hostname(), port)
	if tokenEnv != "" {
		body += "    tokenEnv: " + tokenEnv + "\n"
	}
	cfg, err := Load(writeFleet(t, body, ""))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNodeCallsSucceed(t *testing.T) {
	srv := stubDaemon(t, "", "idle")
	cfg := fleetFor(t, srv, "")
	entry, _ := cfg.Node("box")
	node, err := cfg.NewNode(entry)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	status, err := node.Status(ctx)
	if err != nil || status.State != "idle" || status.Runner != "llamacpp" {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	stats, err := node.Metrics(ctx)
	if err != nil || stats.CPU == nil || stats.Memory == nil {
		t.Fatalf("Metrics = %+v, %v", stats, err)
	}
	started, err := node.Start(ctx)
	if err != nil || started.State != "running" {
		t.Fatalf("Start = %+v, %v", started, err)
	}
	stopped, err := node.Stop(ctx)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("Stop = %+v, %v", stopped, err)
	}
}

func TestOutcomeOK(t *testing.T) {
	srv := stubDaemon(t, "sekrit", "running")
	t.Setenv("BOX_TOKEN", "sekrit")
	cfg := fleetFor(t, srv, "BOX_TOKEN")

	results := cfg.FanOut(context.Background(), StatusCall)
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	r := results[0]
	if !r.OK() || r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %q (%v)", r.Outcome, r.Err)
	}
	if r.Status.State != "running" || r.Name != "box" {
		t.Errorf("result = %+v", r)
	}
}

func TestOutcomeUnauthorized(t *testing.T) {
	srv := stubDaemon(t, "sekrit", "running")
	t.Setenv("BOX_TOKEN", "wrong-token")
	cfg := fleetFor(t, srv, "BOX_TOKEN")

	r := cfg.FanOut(context.Background(), StatusCall)[0]
	if r.Outcome != OutcomeUnauthorized {
		t.Fatalf("outcome = %q, want unauthorized (err: %v)", r.Outcome, r.Err)
	}
	// The daemon's own message reaches the row.
	if !strings.Contains(r.Detail(), "bearer token") {
		t.Errorf("detail = %q, want the daemon's message", r.Detail())
	}
}

func TestOutcomeUnreachable(t *testing.T) {
	// A port nothing listens on: the call never gets an answer.
	cfg, err := Load(writeFleet(t, "nodes:\n  - name: box\n    host: 127.0.0.1\n    port: 1\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.FanOut(context.Background(), StatusCall)[0]
	if r.Outcome != OutcomeUnreachable {
		t.Fatalf("outcome = %q, want unreachable (err: %v)", r.Outcome, r.Err)
	}
}

func TestOutcomeConfigError(t *testing.T) {
	srv := stubDaemon(t, "sekrit", "running")
	cfg := fleetFor(t, srv, "UNSET_BOX_TOKEN")
	t.Setenv("UNSET_BOX_TOKEN", "")

	r := cfg.FanOut(context.Background(), StatusCall)[0]
	if r.Outcome != OutcomeConfigError {
		t.Fatalf("outcome = %q, want config-error (err: %v)", r.Outcome, r.Err)
	}
	// Names the variable, so a typo is obvious — not reported as a 401.
	if !strings.Contains(r.Detail(), "UNSET_BOX_TOKEN") {
		t.Errorf("detail = %q, want it to name the variable", r.Detail())
	}
}

// A daemon that answers with an error (a refused start) is "failed": the box
// is healthy, the request was not — distinct from unreachable.
func TestOutcomeFailedOnDaemonRefusal(t *testing.T) {
	srv := stubDaemon(t, "", "running") // already running -> start 409s
	cfg := fleetFor(t, srv, "")
	entry, _ := cfg.Node("box")
	node, err := cfg.NewNode(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Start(context.Background())
	if err == nil {
		t.Fatal("start against a running engine did not error")
	}
	if got := classify(err); got != OutcomeFailed {
		t.Errorf("classify = %q, want failed", got)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error %q does not carry the daemon's message", err)
	}
}

// One bad node must not blank the rest of the fleet.
func TestFanOutMixedFleetKeepsOrder(t *testing.T) {
	up := stubDaemon(t, "", "running")
	u, _ := url.Parse(up.URL)
	port, _ := strconv.Atoi(u.Port())
	body := fmt.Sprintf(
		"nodes:\n  - name: down\n    host: 127.0.0.1\n    port: 1\n  - name: up\n    host: %s\n    port: %d\n",
		u.Hostname(), port)
	cfg, err := Load(writeFleet(t, body, ""))
	if err != nil {
		t.Fatal(err)
	}

	results := cfg.FanOut(context.Background(), MetricsCall)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// File order is preserved, so refreshes do not reshuffle rows.
	if results[0].Name != "down" || results[1].Name != "up" {
		t.Fatalf("order = %q, %q; want file order", results[0].Name, results[1].Name)
	}
	if results[0].Outcome != OutcomeUnreachable {
		t.Errorf("down node outcome = %q", results[0].Outcome)
	}
	if !results[1].OK() || results[1].Metrics.CPU == nil {
		t.Errorf("up node result = %+v", results[1])
	}
}

// A daemon that answers a fan-out call with an error yields `failed`, not
// `unreachable`: the node was reached, the request was refused, and the
// daemon's own message reaches the row.
func TestFanOutFailedCarriesTheDaemonsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "collector exploded"})
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg, err := Load(writeFleet(t, fmt.Sprintf(
		"nodes:\n  - name: box\n    host: %s\n    port: %d\n", u.Hostname(), port), ""))
	if err != nil {
		t.Fatal(err)
	}

	r := cfg.FanOut(context.Background(), StatusCall)[0]
	if r.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed (err: %v)", r.Outcome, r.Err)
	}
	if !strings.Contains(r.Detail(), "collector exploded") {
		t.Errorf("detail = %q, want the daemon's message", r.Detail())
	}
}
