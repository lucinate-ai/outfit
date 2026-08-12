package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/fleet"
)

// logNode serves /v1/logs returning content. When serveLogs is false the route
// is absent, standing in for a daemon older than the endpoint.
func logNode(t *testing.T, content string, serveLogs bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"state": "running"})
	})
	if serveLogs {
		mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(daemon.LogsResponse{
				Content:    content,
				NextOffset: int64(len(content)),
				Size:       int64(len(content)),
				Missing:    content == "",
			})
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// oneLogFleet writes a fleet of a single node serving content.
func oneLogFleet(t *testing.T, content string) {
	t.Helper()
	srv := logNode(t, content, true)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))
}

func TestCmdFleetLogsPrintsOneNodeUnlabelled(t *testing.T) {
	oneLogFleet(t, "loading weights\nserving\n")

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("fleet logs returned %v", err)
		}
	})
	if out != "loading weights\nserving\n" {
		t.Errorf("output = %q, want the node's log with no prefix", out)
	}
}

func TestCmdFleetLogsLabelsSeveralNodes(t *testing.T) {
	a := logNode(t, "from a\n", true)
	b := logNode(t, "from b\n", true)
	hostA, portA := hostPort(t, a)
	hostB, portB := hostPort(t, b)
	writeFleetFile(t, fmt.Sprintf(
		"nodes:\n  - name: alpha\n    host: %s\n    port: %d\n  - name: beta\n    host: %s\n    port: %d\n",
		hostA, portA, hostB, portB))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("fleet logs returned %v", err)
		}
	})
	if !strings.Contains(out, "alpha  from a") || !strings.Contains(out, "beta  from b") {
		t.Errorf("output =\n%s\nwant each line attributed to its node", out)
	}
}

func TestCmdFleetLogsReadsOneNamedNode(t *testing.T) {
	a := logNode(t, "from a\n", true)
	b := logNode(t, "from b\n", true)
	hostA, portA := hostPort(t, a)
	hostB, portB := hostPort(t, b)
	writeFleetFile(t, fmt.Sprintf(
		"nodes:\n  - name: alpha\n    host: %s\n    port: %d\n  - name: beta\n    host: %s\n    port: %d\n",
		hostA, portA, hostB, portB))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs", "beta"}); err != nil {
			t.Errorf("fleet logs beta returned %v", err)
		}
	})
	if !strings.Contains(out, "from b") {
		t.Errorf("output =\n%s\nwant the named node's log", out)
	}
	if strings.Contains(out, "from a") {
		t.Errorf("output =\n%s\nwant the other node left alone", out)
	}
	// One node, so no prefix.
	if strings.Contains(out, "beta  from b") {
		t.Errorf("output =\n%s\nwant no node prefix when only one node is read", out)
	}
}

func TestCmdFleetLogsUnknownNodeNamesTheKnownOnes(t *testing.T) {
	oneLogFleet(t, "x\n")

	err := cmdFleet([]string{"logs", "nope"})
	if err == nil {
		t.Fatal("an unknown node should be an error")
	}
	if !strings.Contains(err.Error(), "box") {
		t.Errorf("error = %q, want it to name the known nodes", err)
	}
}

func TestCmdFleetLogsReportsNodesWithNothingToGive(t *testing.T) {
	up := logNode(t, "serving\n", true)
	old := logNode(t, "", false)
	hostUp, portUp := hostPort(t, up)
	hostOld, portOld := hostPort(t, old)
	writeFleetFile(t, fmt.Sprintf(
		"nodes:\n  - name: up\n    host: %s\n    port: %d\n"+
			"  - name: old\n    host: %s\n    port: %d\n"+
			"  - name: down\n    host: 127.0.0.1\n    port: 1\n",
		hostUp, portUp, hostOld, portOld))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("one bad node must not fail the command, got %v", err)
		}
	})
	if !strings.Contains(out, "serving") {
		t.Errorf("output =\n%s\nwant the healthy node's log", out)
	}
	if !strings.Contains(out, "upgrade outfit on this node") {
		t.Errorf("output =\n%s\nwant the old daemon named as needing an upgrade", out)
	}
	if !strings.Contains(out, "down") || !strings.Contains(out, "unreachable") {
		t.Errorf("output =\n%s\nwant the unreachable node reported", out)
	}
}

func TestCmdFleetLogsReportsAMissingLog(t *testing.T) {
	oneLogFleet(t, "")

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("fleet logs returned %v", err)
		}
	})
	if !strings.Contains(out, "nothing has run here yet") {
		t.Errorf("output =\n%s\nwant a node that never ran an engine reported distinctly", out)
	}
}

func TestCmdFleetLogsJSON(t *testing.T) {
	oneLogFleet(t, "serving\n")

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs", "--format", "json"}); err != nil {
			t.Errorf("fleet logs --format json returned %v", err)
		}
	})
	var entries []struct {
		Node       string `json:"node"`
		Outcome    string `json:"outcome"`
		Content    string `json:"content"`
		NextOffset int64  `json:"nextOffset"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(entries) != 1 || entries[0].Node != "box" || entries[0].Content != "serving\n" {
		t.Errorf("entries = %+v, want one carrying the node and its content", entries)
	}
	if entries[0].NextOffset != int64(len("serving\n")) {
		t.Errorf("nextOffset = %d, want the cursor carried through", entries[0].NextOffset)
	}
}

func TestCmdFleetLogsRejectsBadFlags(t *testing.T) {
	oneLogFleet(t, "x\n")

	if err := cmdFleet([]string{"logs", "--format", "yaml"}); err == nil ||
		!strings.Contains(err.Error(), "--format") {
		t.Errorf("bad format: got %v", err)
	}
	if err := cmdFleet([]string{"logs", "--limit", "0"}); err == nil ||
		!strings.Contains(err.Error(), "--limit") {
		t.Errorf("bad limit: got %v", err)
	}
}

func TestLastLinesKeepsTheTail(t *testing.T) {
	if got := lastLines("a\nb\nc\n", 2); got != "b\nc\n" {
		t.Errorf("lastLines = %q, want the last two", got)
	}
	if got := lastLines("a\nb\n", 5); got != "a\nb\n" {
		t.Errorf("lastLines = %q, want everything when fewer than asked", got)
	}
	if got := lastLines("", 3); got != "" {
		t.Errorf("lastLines = %q, want empty", got)
	}
	// A final line with no newline is still a line.
	if got := lastLines("a\nb", 1); got != "b" {
		t.Errorf("lastLines = %q, want the unterminated last line", got)
	}
}

func TestFollowFleetLogsResumesPerNodeAndStopsWhenCancelled(t *testing.T) {
	prev := fleetLogsInterval
	fleetLogsInterval = time.Millisecond
	t.Cleanup(func() { fleetLogsInterval = prev })

	// The node appends between polls; the cursor means the second poll sees
	// only what is new.
	//
	// A poll can still be in flight when the loop returns, so the handler's
	// bookkeeping is shared with the test goroutine.
	var mu sync.Mutex
	polls := 0
	var gotOffsets []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		polls++
		gotOffsets = append(gotOffsets, r.URL.Query().Get("offset"))
		seen := polls
		mu.Unlock()
		body := daemon.LogsResponse{Content: "first\n", NextOffset: 6, Size: 6}
		if seen >= 2 {
			body = daemon.LogsResponse{Content: "second\n", NextOffset: 13, Size: 13}
		}
		json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))

	cfg, err := fleet.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	if err := followFleetLogsLoop(ctx, cfg, 200, "text", &buf); err != nil {
		t.Fatalf("a cancelled follow is a clean exit, got: %v", err)
	}
	if !strings.Contains(buf.String(), "first") || !strings.Contains(buf.String(), "second") {
		t.Errorf("output =\n%s\nwant both polls' output", buf.String())
	}
	// A poll can outlive the loop, so read a copy rather than the live slice.
	mu.Lock()
	got := append([]string(nil), gotOffsets...)
	mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("polled %d times, want at least 2", len(got))
	}
	if got[0] != "" {
		t.Errorf("first poll offset = %q, want the tail", got[0])
	}
	if got[1] != "6" {
		t.Errorf("second poll offset = %q, want to resume from the first reply", got[1])
	}
}

// A truncated log strands the cursor past the end. The daemon reports that and
// hands back the new end; the follow loop must adopt it and carry on, or it
// would wait forever on a position the file will never reach again.
func TestFollowFleetLogsResumesAfterTheLogIsTruncated(t *testing.T) {
	prev := fleetLogsInterval
	fleetLogsInterval = time.Millisecond
	t.Cleanup(func() { fleetLogsInterval = prev })

	var mu sync.Mutex
	polls := 0
	var offsets []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		polls++
		offsets = append(offsets, r.URL.Query().Get("offset"))
		seen := polls
		mu.Unlock()
		switch seen {
		case 1:
			// A long log; the cursor ends up at 900.
			json.NewEncoder(w).Encode(daemon.LogsResponse{
				Content: "before\n", NextOffset: 900, Size: 900,
			})
		case 2:
			// The file was replaced by a shorter one: the cursor is stale.
			json.NewEncoder(w).Encode(daemon.LogsResponse{
				NextOffset: 4, Size: 4, StaleOffset: true,
			})
		default:
			json.NewEncoder(w).Encode(daemon.LogsResponse{
				Content: "after\n", NextOffset: 11, Size: 11,
			})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))

	cfg, err := fleet.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	if err := followFleetLogsLoop(ctx, cfg, 200, "text", &buf); err != nil {
		t.Fatalf("a cancelled follow is a clean exit, got: %v", err)
	}
	// A poll can outlive the loop, so read a copy rather than the live slice.
	mu.Lock()
	got := append([]string(nil), offsets...)
	mu.Unlock()
	if len(got) < 3 {
		t.Fatalf("polled %d times, want at least 3", len(got))
	}
	// The poll after the stale reply must ask from the log's new end, not
	// from the stranded cursor.
	if got[2] != "4" {
		t.Errorf("third poll offset = %q, want 4 — the end the daemon reported", got[2])
	}
	if got[2] == "900" {
		t.Error("the follow kept the stranded cursor and would never advance")
	}
	// And output resumes rather than stopping at the truncation.
	if !strings.Contains(buf.String(), "after") {
		t.Errorf("output =\n%s\nwant output after the truncation", buf.String())
	}
}

// A node whose token is rejected is a row, not a failure — and the reason is
// shown rather than the node silently vanishing from the output.
func TestCmdFleetLogsReportsARejectedToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid bearer token"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("a rejected token must not fail the command, got %v", err)
		}
	})
	if !strings.Contains(out, "unauthorized") {
		t.Errorf("output =\n%s\nwant the node reported as unauthorized", out)
	}
	// The reason, not just the outcome.
	if !strings.Contains(out, "bearer token") {
		t.Errorf("output =\n%s\nwant the daemon's own reason shown", out)
	}
	// And it must not be mistaken for the upgrade case, which has a different fix.
	if strings.Contains(out, "upgrade outfit") {
		t.Errorf("output =\n%s\nwant a rejected token distinguished from an old daemon", out)
	}
}

// A node that answered, has a log, and simply has not written to it is
// distinct from one that never ran an engine — the first will produce output
// eventually, the second needs a start.
func TestCmdFleetLogsDistinguishesAnEmptyLogFromAMissingOne(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(daemon.LogsResponse{Content: "", NextOffset: 0, Size: 0})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))

	out := captureStdout(t, func() {
		if err := cmdFleet([]string{"logs"}); err != nil {
			t.Errorf("fleet logs returned %v", err)
		}
	})
	if !strings.Contains(out, "no output") {
		t.Errorf("output =\n%s\nwant an existing but empty log reported as no output", out)
	}
	if strings.Contains(out, "nothing has run here yet") {
		t.Errorf("output =\n%s\nwant an empty log distinguished from a missing one", out)
	}
}

// Following in JSON emits a document per poll, so a streaming consumer can
// decode them one after another.
func TestFollowFleetLogsJSON(t *testing.T) {
	prev := fleetLogsInterval
	fleetLogsInterval = time.Millisecond
	t.Cleanup(func() { fleetLogsInterval = prev })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(daemon.LogsResponse{Content: "line\n", NextOffset: 5, Size: 5})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, port := hostPort(t, srv)
	writeFleetFile(t, fmt.Sprintf("nodes:\n  - name: box\n    host: %s\n    port: %d\n", host, port))

	cfg, err := fleet.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	if err := followFleetLogsLoop(ctx, cfg, 200, "json", &buf); err != nil {
		t.Fatalf("a cancelled follow is a clean exit, got: %v", err)
	}
	dec := json.NewDecoder(&buf)
	documents := 0
	for {
		var entries []fleetLogJSON
		if err := dec.Decode(&entries); err != nil {
			break
		}
		documents++
		if len(entries) != 1 || entries[0].Node != "box" {
			t.Errorf("document %d = %+v, want one entry for box", documents, entries)
		}
	}
	if documents == 0 {
		t.Errorf("no decodable JSON documents in:\n%s", buf.String())
	}
}
