package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
)

// logsGet calls the logs endpoint on a test server and decodes the reply.
func logsGet(t *testing.T, srv *httptest.Server, query, token string) (int, LogsResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/logs"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out LogsResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decoding reply: %v", err)
		}
	}
	return resp.StatusCode, out
}

// logsServer serves a daemon whose engine log holds content.
func logsServer(t *testing.T, content, token string) *httptest.Server {
	t.Helper()
	d := testDaemon(t, "")
	if content != "" {
		if err := os.WriteFile(d.Sup.LogPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(d.Handler(token))
	t.Cleanup(srv.Close)
	return srv
}

func TestLogsEndpointReadsTheEngineLog(t *testing.T) {
	srv := logsServer(t, "loading weights\nserving\n", "")

	code, got := logsGet(t, srv, "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Content != "loading weights\nserving\n" {
		t.Errorf("content = %q, want the log", got.Content)
	}
	if got.NextOffset != got.Size {
		t.Errorf("offset = %d, want the end (%d)", got.NextOffset, got.Size)
	}
	if got.Path == "" {
		t.Error("reply should name the log file it read")
	}
}

func TestLogsEndpointResumesFromACursor(t *testing.T) {
	srv := logsServer(t, "one\n", "")

	_, first := logsGet(t, srv, "", "")
	code, second := logsGet(t, srv, "?offset="+strconv.FormatInt(first.NextOffset, 10), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if second.Content != "" {
		t.Errorf("content = %q, want nothing new", second.Content)
	}
	if second.NextOffset != first.NextOffset {
		t.Errorf("cursor moved from %d to %d with no new output",
			first.NextOffset, second.NextOffset)
	}
}

func TestLogsEndpointReadsAfterTheEngineIsGone(t *testing.T) {
	// The log outlives the process that wrote it: nothing is running here, and
	// the output is still served.
	srv := logsServer(t, "panic: out of memory\n", "")

	code, got := logsGet(t, srv, "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Content != "panic: out of memory\n" {
		t.Errorf("content = %q, want the crashed engine's output", got.Content)
	}
}

func TestLogsEndpointReportsAMissingLog(t *testing.T) {
	srv := logsServer(t, "", "")

	code, got := logsGet(t, srv, "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a missing log is a state, not a failure", code)
	}
	if !got.Missing {
		t.Error("a node whose engine never ran should report a missing log")
	}
}

func TestLogsEndpointRejectsBadParameters(t *testing.T) {
	srv := logsServer(t, "one\n", "")

	for _, query := range []string{"?offset=abc", "?limit=abc", "?offset=-5"} {
		code, _ := logsGet(t, srv, query, "")
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, code)
		}
	}
}

func TestLogsEndpointRequiresTheToken(t *testing.T) {
	srv := logsServer(t, "one\n", "s3cret")

	if code, _ := logsGet(t, srv, "", ""); code != http.StatusUnauthorized {
		t.Errorf("status without a token = %d, want 401", code)
	}
	if code, _ := logsGet(t, srv, "", "s3cret"); code != http.StatusOK {
		t.Errorf("status with the token = %d, want 200", code)
	}
}

// An unreadable log is a 500 with a reason, not a 200 carrying an empty log:
// the operator must be able to tell "the engine said nothing" from "I could
// not read what it said".
func TestLogsEndpointReportsAnUnreadableLog(t *testing.T) {
	d := testDaemon(t, "")
	// Point the log at a directory, which opens but cannot be read.
	d.Sup.LogPath = t.TempDir()
	srv := httptest.NewServer(d.Handler(""))
	t.Cleanup(srv.Close)

	code, got := logsGet(t, srv, "", "")
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}
	if got.Content != "" || got.Missing {
		t.Errorf("reply = %+v, want no log body on a failure", got)
	}
}

// The tail sentinel is negative; the handler rejects negative offsets. Passing
// it explicitly must still read the tail rather than 400.
func TestLogsEndpointAcceptsTheTailSentinel(t *testing.T) {
	srv := logsServer(t, "serving\n", "")

	code, got := logsGet(t, srv, "?offset="+strconv.FormatInt(TailLog, 10), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the tail sentinel", code)
	}
	if got.Content != "serving\n" {
		t.Errorf("content = %q, want the tail", got.Content)
	}
}
