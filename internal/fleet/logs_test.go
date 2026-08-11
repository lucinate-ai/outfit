package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// stubLogsDaemon serves /v1/logs, echoing back the query it was asked so the
// tests can assert on the cursor the client sent. A body of "" with serveLogs
// false omits the route entirely, standing in for a daemon that predates it.
func stubLogsDaemon(t *testing.T, content string, serveLogs bool) (*httptest.Server, *string) {
	t.Helper()
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"state": "running"})
	})
	if serveLogs {
		mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			json.NewEncoder(w).Encode(daemon.LogsResponse{
				Content:    content,
				NextOffset: int64(len(content)),
				Size:       int64(len(content)),
			})
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &gotQuery
}

func TestClientLogsReadsTheTailByDefault(t *testing.T) {
	srv, query := stubLogsDaemon(t, "serving\n", true)
	cfg := fleetFor(t, srv, "")
	entry, _ := cfg.Node("box")
	node, err := cfg.NewNode(entry)
	if err != nil {
		t.Fatal(err)
	}

	got, err := node.Logs(context.Background(), daemon.TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "serving\n" {
		t.Errorf("content = %q, want the node's log", got.Content)
	}
	// The sentinel must not go on the wire — the endpoint would reject it as a
	// negative offset.
	if *query != "" {
		t.Errorf("query = %q, want no offset when reading the tail", *query)
	}
}

func TestClientLogsSendsTheCursorAndLimit(t *testing.T) {
	srv, query := stubLogsDaemon(t, "", true)
	cfg := fleetFor(t, srv, "")
	entry, _ := cfg.Node("box")
	node, err := cfg.NewNode(entry)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := node.Logs(context.Background(), 42, 100); err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(*query)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("offset") != "42" || q.Get("limit") != "100" {
		t.Errorf("query = %q, want the cursor and limit passed through", *query)
	}
}

func TestLogsCallFansOutFromPerNodeCursors(t *testing.T) {
	srv, query := stubLogsDaemon(t, "line\n", true)
	cfg := fleetFor(t, srv, "")

	// A node named in the cursor map resumes from it.
	results := cfg.FanOut(context.Background(), LogsCall(map[string]int64{"box": 17}, 0))
	if len(results) != 1 || !results[0].OK() {
		t.Fatalf("results = %+v, want one OK node", results)
	}
	if results[0].Logs.Content != "line\n" {
		t.Errorf("content = %q, want the node's log", results[0].Logs.Content)
	}
	q, err := url.ParseQuery(*query)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("offset") != "17" {
		t.Errorf("query = %q, want the node's own cursor", *query)
	}

	// A node absent from the map is read from the tail.
	results = cfg.FanOut(context.Background(), LogsCall(nil, 0))
	if !results[0].OK() {
		t.Fatalf("result = %+v, want OK", results[0])
	}
	if *query != "" {
		t.Errorf("query = %q, want the tail for a node with no cursor", *query)
	}
}

func TestLogsCallReportsADaemonWithoutTheEndpoint(t *testing.T) {
	srv, _ := stubLogsDaemon(t, "", false)
	cfg := fleetFor(t, srv, "")

	results := cfg.FanOut(context.Background(), LogsCall(nil, 0))
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one", results)
	}
	if results[0].Outcome != OutcomeUnsupported {
		t.Errorf("outcome = %q, want %q for a daemon that predates the endpoint",
			results[0].Outcome, OutcomeUnsupported)
	}
}

func TestLogsCallReportsAnUnreachableNode(t *testing.T) {
	srv, _ := stubLogsDaemon(t, "", true)
	cfg := fleetFor(t, srv, "")
	srv.Close() // the box is gone

	results := cfg.FanOut(context.Background(), LogsCall(nil, 0))
	if results[0].Outcome != OutcomeUnreachable {
		t.Errorf("outcome = %q, want unreachable", results[0].Outcome)
	}
}

func TestLogsCallReportsARejectedToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid bearer token"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := fleetFor(t, srv, "")

	results := cfg.FanOut(context.Background(), LogsCall(nil, 0))
	if results[0].Outcome != OutcomeUnauthorized {
		t.Errorf("outcome = %q, want unauthorized", results[0].Outcome)
	}
}

// Only is exercised through the fleet command, so the fleet package's own
// coverage never sees it. It is exported API and its error is what an operator
// reads after a typo, so it is worth pinning here.
func TestOnlyNarrowsToOneNodeAndNamesTheRest(t *testing.T) {
	srv, _ := stubLogsDaemon(t, "x\n", true)
	cfg := fleetFor(t, srv, "")
	// fleetFor writes a single node called "box"; add a second by hand so the
	// narrowing has something to exclude.
	cfg.Nodes = append(cfg.Nodes, NodeConfig{Name: "other", Host: "127.0.0.1", Port: 1})

	only, err := cfg.Only("other")
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Nodes) != 1 || only.Nodes[0].Name != "other" {
		t.Errorf("nodes = %+v, want just the named one", only.Nodes)
	}
	// The original is untouched: narrowing must not mutate the loaded fleet.
	if len(cfg.Nodes) != 2 {
		t.Errorf("the source config now has %d nodes, want it left alone", len(cfg.Nodes))
	}

	_, err = cfg.Only("nope")
	if err == nil {
		t.Fatal("an unknown node should be an error")
	}
	for _, want := range []string{"nope", "box", "other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}
