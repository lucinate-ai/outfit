//go:build !windows

package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		// Case and surrounding space are the operator's business, not ours.
		{"WARN", slog.LevelWarn},
		{"  Error  ", slog.LevelError},
		// Unset is the default, so a caller can hand over whatever it resolved
		// without testing for "" first.
		{"", slog.LevelInfo},
	} {
		got, err := ParseLevel(tc.in)
		if err != nil {
			t.Errorf("ParseLevel(%q) = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseLevelRejectsUnknownNamingTheAcceptedSet(t *testing.T) {
	// A mistyped level must not quietly become the default: the log would be
	// discovered to be wrong only when it was needed.
	_, err := ParseLevel("verbose")
	if err == nil {
		t.Fatal("ParseLevel(\"verbose\") = nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "verbose") {
		t.Errorf("error does not name the offending value: %q", msg)
	}
	for _, name := range levelNames {
		if !strings.Contains(msg, name) {
			t.Errorf("error does not name accepted level %q: %q", name, msg)
		}
	}
	// slog's own parser accepts offsets; outfit's vocabulary is the four names.
	if _, err := ParseLevel("info+2"); err == nil {
		t.Error("ParseLevel(\"info+2\") was accepted")
	}
}

func TestResolveLevelPrefersFlagOverEnvironment(t *testing.T) {
	t.Setenv(LevelEnvVar, "error")

	got, err := ResolveLevel("warn")
	if err != nil || got != slog.LevelWarn {
		t.Fatalf("ResolveLevel(\"warn\") = %v, %v; want warn", got, err)
	}
	// No flag: the variable decides.
	if got, err := ResolveLevel(""); err != nil || got != slog.LevelError {
		t.Fatalf("ResolveLevel(\"\") = %v, %v; want error", got, err)
	}
	// Neither: info.
	t.Setenv(LevelEnvVar, "")
	if got, err := ResolveLevel(""); err != nil || got != slog.LevelInfo {
		t.Fatalf("unset ResolveLevel = %v, %v; want info", got, err)
	}
	// A flag of nothing but spaces is no flag at all, so the variable still
	// decides rather than the whitespace parsing as the default.
	t.Setenv(LevelEnvVar, "error")
	if got, err := ResolveLevel("   "); err != nil || got != slog.LevelError {
		t.Fatalf("ResolveLevel(\"   \") = %v, %v; want the environment's error", got, err)
	}

	// A bad variable is still an error — it is not silently ignored because it
	// came from the environment rather than the command line.
	t.Setenv(LevelEnvVar, "chatty")
	if _, err := ResolveLevel(""); err == nil {
		t.Error("a bad OUTFIT_LOG_LEVEL was accepted")
	}
}

func TestLevelNamesMatchWhatParses(t *testing.T) {
	// The CLI's flag help and its tab completion are built from LevelNames, so
	// a name offered there that ParseLevel rejects would complete to a value
	// that refuses to start.
	names := LevelNames()
	if len(names) == 0 {
		t.Fatal("LevelNames() is empty")
	}
	for _, name := range names {
		if _, err := ParseLevel(name); err != nil {
			t.Errorf("LevelNames offers %q, which ParseLevel rejects: %v", name, err)
		}
	}
	// And it is a copy: a caller mangling the slice cannot reach the parser's
	// own list.
	names[0] = "mangled"
	if LevelNames()[0] == "mangled" {
		t.Error("LevelNames returns the package's own slice")
	}
}

func TestNilLoggerDiscards(t *testing.T) {
	// The default has to be silence: every existing test constructs a Daemon
	// and a Supervisor directly, and none of them asked to print.
	d := &Daemon{Sup: NewSupervisor("")}
	if d.log() == nil {
		t.Fatal("Daemon.log() = nil")
	}
	if d.log().Enabled(t.Context(), slog.LevelError) {
		t.Error("a nil Daemon.Logger is not discarding")
	}
	s := NewSupervisor("")
	if s.log().Enabled(t.Context(), slog.LevelError) {
		t.Error("a nil Supervisor.Logger is not discarding")
	}
}

// syncBuffer is a bytes.Buffer safe to read while a logger writes to it. The
// supervisor records an engine's exit from its own goroutine, so a test that
// polls for that record is reading while that goroutine writes: slog
// serialises its handler, but the buffer underneath is still ours to guard.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// logged is one record parsed out of captured output.
type logged struct {
	level  string
	fields map[string]string
}

// captureLogger returns a logger writing into buf at the given level.
func captureLogger(buf *syncBuffer, level slog.Level) *slog.Logger {
	return NewLogger(buf, level)
}

// parseRecords turns the text handler's output into records. The text handler
// writes key=value pairs, quoting values that need it.
func parseRecords(t *testing.T, out string) []logged {
	t.Helper()
	var recs []logged
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		rec := logged{fields: map[string]string{}}
		for _, field := range splitFields(line) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			value = strings.Trim(value, `"`)
			if key == "level" {
				rec.level = value
			}
			rec.fields[key] = value
		}
		recs = append(recs, rec)
	}
	return recs
}

// splitFields splits a text-handler line on spaces, keeping quoted values
// (the message is `msg="api request"`) in one piece.
func splitFields(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

// requestRecords keeps only the API request summaries.
func requestRecords(recs []logged) []logged {
	var out []logged
	for _, r := range recs {
		if r.fields["msg"] == "api request" {
			out = append(out, r)
		}
	}
	return out
}

// loggingDaemon builds a daemon whose handler logs into buf at level.
func loggingDaemon(t *testing.T, buf *syncBuffer, level slog.Level) *Daemon {
	t.Helper()
	d := testDaemon(t, "exit 0")
	d.Logger = captureLogger(buf, level)
	d.Sup.Logger = d.Logger
	return d
}

func TestRequestSummaryFields(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelInfo)
	srv := httptest.NewServer(d.Handler("sekrit"))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/v1/logs?offset=0&limit=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	recs := requestRecords(parseRecords(t, buf.String()))
	if len(recs) != 1 {
		t.Fatalf("got %d summaries, want 1: %s", len(recs), buf.String())
	}
	rec := recs[0]
	if rec.level != "INFO" {
		t.Errorf("level = %s, want INFO for a served request", rec.level)
	}
	if got := rec.fields["method"]; got != "GET" {
		t.Errorf("method = %q", got)
	}
	// The query is part of the path: cursors and bounds are what diagnosis
	// needs, and they are all the API's parameters ever carry.
	if got := rec.fields["path"]; got != "/v1/logs?offset=0&limit=10" {
		t.Errorf("path = %q, want the request URI with its query", got)
	}
	if got := rec.fields["status"]; got != "200" {
		t.Errorf("status = %q", got)
	}
	for _, key := range []string{"duration", "bytes", "remote"} {
		if rec.fields[key] == "" {
			t.Errorf("summary carries no %s: %s", key, buf.String())
		}
	}
}

func TestRequestSummaryGradesBySeverity(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelDebug)
	srv := httptest.NewServer(d.Handler("sekrit"))
	defer srv.Close()
	client := srv.Client()

	do := func(method, path, token string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	do("GET", "/v1/status", "sekrit")        // 200 -> INFO
	do("GET", "/v1/status", "")              // 401 -> WARN, from outside the auth layer
	do("GET", "/v1/nope", "sekrit")          // 404 from the mux -> WARN
	do("GET", "/v1/logs?offset=x", "sekrit") // 400 bad input -> WARN

	recs := requestRecords(parseRecords(t, buf.String()))
	if len(recs) != 4 {
		t.Fatalf("got %d summaries, want 4: %s", len(recs), buf.String())
	}
	want := []struct{ status, level string }{
		{"200", "INFO"},
		{"401", "WARN"},
		{"404", "WARN"},
		{"400", "WARN"},
	}
	for i, w := range want {
		if recs[i].fields["status"] != w.status || recs[i].level != w.level {
			t.Errorf("summary %d = status %s at %s, want %s at %s",
				i, recs[i].fields["status"], recs[i].level, w.status, w.level)
		}
	}
}

func TestRequestSummaryNeverCarriesTheToken(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelDebug)
	srv := httptest.NewServer(d.Handler("sekrit"))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong-token-offered")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	out := buf.String()
	if !strings.Contains(out, "401") {
		t.Errorf("the rejection was not summarised: %s", out)
	}
	// Neither the token offered nor the one expected may appear.
	for _, secret := range []string{"wrong-token-offered", "sekrit", "Bearer"} {
		if strings.Contains(out, secret) {
			t.Errorf("log discloses %q: %s", secret, out)
		}
	}
}

func TestRequestSummaryNeverCarriesABody(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelDebug)
	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()

	// A deploy config's serve args are exactly the sort of thing that can hold
	// a credential.
	body, err := json.Marshal(remote.DeployConfig{
		Runner:    "llamacpp",
		ModelID:   "org/model",
		ServeArgs: []string{"--api-key", "not-in-the-log"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Post(srv.URL+"/v1/deploy-config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	out := buf.String()
	if strings.Contains(out, "not-in-the-log") {
		t.Errorf("log carries the request body: %s", out)
	}
	if strings.Contains(out, "org/model") {
		t.Errorf("log carries the request body: %s", out)
	}
}

func TestEveryRouteIsSummarised(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelDebug)
	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()

	// An endpoint does not opt in to being summarised: the middleware is
	// outside the mux, so every route in the table is covered by construction.
	// This asserts that, so a route added later cannot quietly escape it.
	for _, route := range Routes() {
		method, path, ok := strings.Cut(route.Pattern, " ")
		if !ok {
			t.Fatalf("route pattern %q is not \"METHOD /path\"", route.Pattern)
		}
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	recs := requestRecords(parseRecords(t, buf.String()))
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.fields["method"]+" "+r.fields["path"]] = true
	}
	for _, route := range Routes() {
		if !seen[route.Pattern] {
			t.Errorf("no summary for %s; got %v", route.Pattern, seen)
		}
	}
}

func TestSummariesAreSilencedAtWarnButFailuresAreNot(t *testing.T) {
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelWarn)
	srv := httptest.NewServer(d.Handler("sekrit"))
	defer srv.Close()

	// The fleet-poll case: many successful status reads, one bad token.
	for range 5 {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/status", nil)
		req.Header.Set("Authorization", "Bearer sekrit")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := srv.Client().Get(srv.URL + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	recs := requestRecords(parseRecords(t, buf.String()))
	if len(recs) != 1 {
		t.Fatalf("got %d summaries at warn, want only the rejection: %s", len(recs), buf.String())
	}
	if recs[0].fields["status"] != "401" {
		t.Errorf("surviving summary = status %s, want the 401", recs[0].fields["status"])
	}
}

func TestLoggingDoesNotChangeTheResponse(t *testing.T) {
	// Whatever the level, a caller's reply is byte-identical.
	read := func(d *Daemon) (int, string, string) {
		t.Helper()
		srv := httptest.NewServer(d.Handler(""))
		defer srv.Close()
		resp, err := srv.Client().Get(srv.URL + "/v1/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
	}

	// One daemon, read twice — the same daemon either way, so any difference
	// is the logger's doing rather than two temp directories'.
	var buf syncBuffer
	d := testDaemon(t, "exit 0")
	qStatus, qType, qBody := read(d)
	d.Logger = captureLogger(&buf, slog.LevelDebug)
	d.Sup.Logger = d.Logger
	lStatus, lType, lBody := read(d)
	if qStatus != lStatus || qType != lType || qBody != lBody {
		t.Errorf("logged response differs:\n unlogged: %d %s %s\n   logged: %d %s %s",
			qStatus, qType, qBody, lStatus, lType, lBody)
	}
	if buf.Len() == 0 {
		t.Error("the logged daemon logged nothing, so this proved nothing")
	}
}

func TestLifecycleRecords(t *testing.T) {
	var buf syncBuffer
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.Logger = captureLogger(&buf, slog.LevelDebug)
	d.Sup.Logger = d.Logger

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "org/model"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	if err := d.Sup.Stop(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{
		"starting engine", // what the daemon resolved to serve
		"org/model",       // and what that was
		"engine started",  // the supervisor launching it
		"engine command",  // the full argv, at debug only
		"stopping engine", // the stop
		"engine exited",   // and the exit it produced
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lifecycle log missing %q:\n%s", want, out)
		}
	}
}

func TestFullEngineCommandIsDebugOnly(t *testing.T) {
	// A command built from a pushed deploy config can carry a literal
	// --api-key, so the argv is not an info-level detail.
	var buf syncBuffer
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	s.Logger = captureLogger(&buf, slog.LevelInfo)
	engine := stubEngine(t, "exit 0")
	if err := s.Start([]string{engine, "--api-key", "sekrit"}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateStopped)

	out := buf.String()
	if !strings.Contains(out, "engine started") {
		t.Fatalf("no start record: %s", out)
	}
	if strings.Contains(out, "sekrit") {
		t.Errorf("the engine's arguments reached an info-level record: %s", out)
	}
}

func TestCrashIsRecordedAtErrorSeverity(t *testing.T) {
	var buf syncBuffer
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	// At warn: a crash must survive a level that silences routine traffic.
	s.Logger = captureLogger(&buf, slog.LevelWarn)
	if err := s.Start([]string{stubEngine(t, "exit 3")}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateCrashed)

	// The record is written just after the state flips; give the goroutine a
	// moment rather than racing it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "engine crashed") {
		time.Sleep(10 * time.Millisecond)
	}
	recs := parseRecords(t, buf.String())
	if len(recs) == 0 {
		t.Fatalf("a crash at warn logged nothing")
	}
	var found bool
	for _, r := range recs {
		if r.fields["msg"] == "engine crashed" {
			found = true
			if r.level != "ERROR" {
				t.Errorf("crash recorded at %s, want ERROR", r.level)
			}
		}
	}
	if !found {
		t.Errorf("no crash record: %s", buf.String())
	}
}

func TestFailedStartRecordsTheReason(t *testing.T) {
	var buf syncBuffer
	d := testDaemon(t, "exit 0") // BuildArgv fails with nothing stored
	d.Logger = captureLogger(&buf, slog.LevelInfo)
	d.Sup.Logger = d.Logger

	if err := d.StartEngine(); err == nil {
		t.Fatal("start with nothing to serve succeeded")
	}
	out := buf.String()
	if !strings.Contains(out, "engine start failed") || !strings.Contains(out, "nothing to serve") {
		t.Errorf("the reason a start never happened was not recorded: %s", out)
	}
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("a failed start was not recorded at error severity: %s", out)
	}
}

func TestServerErrorIsRecordedAtErrorSeverity(t *testing.T) {
	// The severity table promises 5xx at error, and only a server-side failure
	// can show it. A log path that is a directory reads as an error rather than
	// a missing log, which is the daemon's own failure to answer.
	var buf syncBuffer
	d := loggingDaemon(t, &buf, slog.LevelError)
	d.Sup.LogPath = t.TempDir()
	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/v1/logs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (the log path is a directory)", resp.StatusCode)
	}

	recs := requestRecords(parseRecords(t, buf.String()))
	if len(recs) != 1 {
		t.Fatalf("got %d summaries at error level, want the 500: %s", len(recs), buf.String())
	}
	if recs[0].level != "ERROR" || recs[0].fields["status"] != "500" {
		t.Errorf("summary = status %s at %s, want 500 at ERROR",
			recs[0].fields["status"], recs[0].level)
	}
}

func TestSummaryDefaultsTheStatusWhenAHandlerSetsNone(t *testing.T) {
	// Every handler here goes through writeJSON, which always calls
	// WriteHeader — but the default is what keeps a future one honest, and a
	// summary reporting status 0 would be worse than useless.
	for _, tc := range []struct {
		name      string
		handler   http.HandlerFunc
		wantBytes bool
	}{
		{
			name:    "writes nothing at all",
			handler: func(http.ResponseWriter, *http.Request) {},
		},
		{
			name:      "writes a body without a header",
			handler:   func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hello")) },
			wantBytes: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf syncBuffer
			srv := httptest.NewServer(summarize(captureLogger(&buf, slog.LevelDebug), tc.handler))
			defer srv.Close()

			resp, err := srv.Client().Get(srv.URL + "/anything")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			recs := requestRecords(parseRecords(t, buf.String()))
			if len(recs) != 1 {
				t.Fatalf("got %d summaries, want 1: %s", len(recs), buf.String())
			}
			if got := recs[0].fields["status"]; got != "200" {
				t.Errorf("status = %q, want the 200 a silent handler actually sent", got)
			}
			if recs[0].level != "INFO" {
				t.Errorf("level = %s, want INFO", recs[0].level)
			}
			if wantBytes := recs[0].fields["bytes"] != "0"; wantBytes != tc.wantBytes {
				t.Errorf("bytes = %q, want non-zero: %v", recs[0].fields["bytes"], tc.wantBytes)
			}
		})
	}
}

func TestGraceEscalationIsRecordedAtWarn(t *testing.T) {
	// An engine that ignores SIGTERM is a property of that engine, and the
	// docs promise the escalation survives a level that silences routine
	// traffic.
	var buf syncBuffer
	logPath := filepath.Join(t.TempDir(), "engine.log")
	s := NewSupervisor(logPath)
	s.Logger = captureLogger(&buf, slog.LevelWarn)
	s.Grace = 100 * time.Millisecond
	// The trap has to be installed before the stop arrives, or the engine dies
	// politely and there is no escalation to record — so the engine announces
	// itself once it is ignoring TERM, and the test waits for that.
	engine := stubEngine(t, `trap '' TERM
echo deaf
while true; do sleep 0.05; done`)
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, _ := os.ReadFile(logPath); strings.Contains(string(data), "deaf") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the engine never reported ignoring TERM")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}

	recs := parseRecords(t, buf.String())
	var found bool
	for _, r := range recs {
		if strings.Contains(r.fields["msg"], "grace period") {
			found = true
			if r.level != "WARN" {
				t.Errorf("the escalation was recorded at %s, want WARN", r.level)
			}
			if r.fields["engine"] != engine {
				t.Errorf("record does not name the engine: %v", r.fields)
			}
		}
	}
	if !found {
		t.Errorf("a kill after the grace window went unrecorded: %s", buf.String())
	}
	// The ordinary stop record is silenced at warn; only the escalation is not.
	if strings.Contains(buf.String(), `msg="stopping engine"`) {
		t.Errorf("an ordinary stop was recorded at warn: %s", buf.String())
	}
}

func TestUnreadableStoredConfigIsRecorded(t *testing.T) {
	// The first thing a start does is read the stored config; a corrupt one
	// used to reach the caller and vanish.
	var buf syncBuffer
	d := testDaemon(t, "exit 0")
	d.Logger = captureLogger(&buf, slog.LevelInfo)
	d.Sup.Logger = d.Logger
	if err := os.WriteFile(d.configPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := d.StartEngine()
	if err == nil {
		t.Fatal("a start over a corrupt deploy config succeeded")
	}
	out := buf.String()
	if !strings.Contains(out, "engine start failed") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("the unreadable config was not recorded at error severity: %s", out)
	}
	// Nothing was started off the back of it.
	if state, _, _ := d.Sup.Status(); state != StateIdle {
		t.Errorf("state = %s after a failed start, want idle", state)
	}
	if strings.Contains(out, "engine started") {
		t.Errorf("an engine was started despite the failure: %s", out)
	}
}

func TestAFailedLaunchIsNotRecordedAsAStart(t *testing.T) {
	// The start record is written after cmd.Start returns, so a launch that
	// never happened must leave no trace of an engine that never ran — the
	// assertion that matters here is the absent record, not the error.
	var buf syncBuffer
	s := NewSupervisor(filepath.Join(t.TempDir(), "engine.log"))
	s.Logger = captureLogger(&buf, slog.LevelDebug)

	missing := filepath.Join(t.TempDir(), "no-such-engine")
	if err := s.Start([]string{missing}); err == nil {
		t.Fatal("starting a non-existent binary succeeded")
	}
	out := buf.String()
	if strings.Contains(out, "engine started") {
		t.Errorf("a launch that failed was recorded as a start: %s", out)
	}
	if strings.Contains(out, "engine command") {
		t.Errorf("a command that never ran was recorded: %s", out)
	}
	if state, _, _ := s.Status(); state != StateIdle {
		t.Errorf("state = %s after a failed launch, want idle", state)
	}
}

func TestStartEngineRecordsASupervisorFailure(t *testing.T) {
	// The daemon's own record of a start that the supervisor refused: the
	// engine binary is gone, so nothing ran and the reason has to survive.
	var buf syncBuffer
	d := testDaemon(t, "exit 0")
	d.Logger = captureLogger(&buf, slog.LevelInfo)
	d.Sup.Logger = d.Logger
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "org/model"}); err != nil {
		t.Fatal(err)
	}
	// Point the built command at nothing.
	d.BuildArgv = func(*remote.DeployConfig) ([]string, error) {
		return []string{filepath.Join(t.TempDir(), "no-such-engine")}, nil
	}

	if err := d.StartEngine(); err == nil {
		t.Fatal("a start with a missing binary succeeded")
	}
	out := buf.String()
	if !strings.Contains(out, "engine start failed") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("the supervisor's refusal was not recorded at error severity: %s", out)
	}
	// The daemon says it is starting before it knows the outcome; what it must
	// not do is claim the engine started.
	if strings.Contains(out, `msg="engine started"`) {
		t.Errorf("an engine that never launched was recorded as started: %s", out)
	}
}
