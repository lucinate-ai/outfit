package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// runWork works the work command group the way the binary does, the output
// kept for the test to read: the stdout the command wrote, and its error.
func runWork(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stdout := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	defer func() {
		os.Stdout = old
		f.Close()
	}()
	err = cmdWork(args)
	data, _ := os.ReadFile(stdout)
	return string(data), err
}

// workAPIRequest is one request as the work list API stub records it.
type workAPIRequest struct {
	method string
	path   string
	bearer string
	body   []byte
}

// workAPIStub stands in for the orchestrator's work list API for a test: it
// records the request it sees, and answers the way the test says.
func workAPIStub(t *testing.T, status int, reply any) (string, *workAPIRequest) {
	t.Helper()
	got := &workAPIRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.bearer = r.Header.Get("Authorization")
		got.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if reply != nil {
			json.NewEncoder(w).Encode(reply)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

// workAPIErrorReply is the shape the work list API's refusals take.
func workAPIErrorReply(message string) any {
	return map[string]any{"error": map[string]any{"message": message, "type": "orchestrator_error"}}
}

func TestWorkRequest_SendsTheBearerAndSurfacesTheRefusal(t *testing.T) {
	base, got := workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`the items file already carries an item with id "a"`))
	_, err := workRequest(base, "sekret", http.MethodPost, "/v1/items", nil)
	if err == nil || !strings.Contains(err.Error(), `already carries an item with id "a"`) {
		t.Fatalf("the API's refusal is the command's error: %v", err)
	}
	if got.method != "POST" || got.path != "/v1/items" {
		t.Errorf("the request goes to the API's path: %s %s", got.method, got.path)
	}
	if got.bearer != "Bearer sekret" {
		t.Errorf("the token goes as a bearer: %q", got.bearer)
	}
}

func TestWorkRequest_NoTokenIsNoBearer(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"ok": true})
	if _, err := workRequest(base, "", http.MethodGet, "/v1/items", nil); err != nil {
		t.Fatal(err)
	}
	if got.bearer != "" {
		t.Errorf("no token, no bearer: %q", got.bearer)
	}
}

func TestWorkRequest_AnAnswerWithoutAMessageNamesTheStatus(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusInternalServerError, nil)
	if _, err := workRequest(base, "", http.MethodGet, "/v1/items", nil); err == nil ||
		!strings.Contains(err.Error(), "answered 500") {
		t.Errorf("an answer without a message names the status and the address: %v", err)
	}
}

func TestWorkAdd_SendsTheFieldsAndReportsTheAdd(t *testing.T) {
	base, got := workAPIStub(t, http.StatusCreated, map[string]any{"object": "item", "id": "b"})
	out, err := runWork(t, "add", "--url", base,
		"--id", "b", "--instructions", "do b", "--dir", ".",
		"--tag", "kind=gpu", "--tag", "org=me", "--priority", "3")
	if err != nil {
		t.Fatalf("the add: %v (out %s)", err, out)
	}
	if !strings.Contains(out, `item "b" added`) {
		t.Errorf("the add reports the item: %s", out)
	}
	if got.method != "POST" || got.path != "/v1/items" {
		t.Errorf("the item goes to the API's add path: %s %s", got.method, got.path)
	}
	var body workAddBody
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("the add sends a JSON item: %v\n%s", err, got.body)
	}
	if body.ID != "b" || body.Instructions != "do b" || body.Dir != "." ||
		len(body.Tags) != 2 || body.Tags[0] != "kind=gpu" || body.Tags[1] != "org=me" || body.Priority != 3 {
		t.Errorf("the fields go as the API takes them: %+v", body)
	}
}

func TestWorkAdd_TheRefusalsReadTheWayTheAPIStatesThem(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`the items file already carries an item with id "a"`))
	_, err := runWork(t, "add", "--url", base, "--id", "a", "--instructions", "again", "--dir", ".")
	if err == nil || !strings.Contains(err.Error(), `already carries an item with id "a"`) {
		t.Errorf("an id the file carries is refused, the way the API states it: %v", err)
	}

	base, _ = workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`item "a" is recorded done: an ended item is not added again`))
	_, err = runWork(t, "add", "--url", base, "--id", "a", "--instructions", "again", "--dir", ".")
	if err == nil || !strings.Contains(err.Error(), "an ended item is not added again") {
		t.Errorf("an ended id is refused, naming the record: %v", err)
	}

	base, _ = workAPIStub(t, http.StatusBadRequest,
		workAPIErrorReply(`item "a" has no working directory`))
	_, err = runWork(t, "add", "--url", base, "--id", "a", "--instructions", "do")
	if err == nil || !strings.Contains(err.Error(), "has no working directory") {
		t.Errorf("a field the validation refuses is refused, naming the fault: %v", err)
	}
}

func TestWorkRemove_CallsTheRemovePathAndReports(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"ok": true, "id": "a"})
	out, err := runWork(t, "remove", "a", "--url", base)
	if err != nil {
		t.Fatalf("the remove: %v (out %s)", err, out)
	}
	if !strings.Contains(out, `item "a" removed`) {
		t.Errorf("the remove reports the item: %s", out)
	}
	if got.method != "DELETE" || got.path != "/v1/items/a" {
		t.Errorf("the remove calls the API's remove path for the id: %s %s", got.method, got.path)
	}
}

func TestWorkRemove_TheRefusalsReadTheWayTheAPIStatesThem(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`item "a" is running: abort it first, then remove it`))
	_, err := runWork(t, "remove", "a", "--url", base)
	if err == nil || !strings.Contains(err.Error(), "abort it first") {
		t.Errorf("a running item is refused, naming the abort that goes first: %v", err)
	}

	base, _ = workAPIStub(t, http.StatusNotFound,
		workAPIErrorReply(`the items file carries no item with id "b"`))
	_, err = runWork(t, "remove", "b", "--url", base)
	if err == nil || !strings.Contains(err.Error(), `no item with id "b"`) {
		t.Errorf("an id the file does not carry is refused, naming it: %v", err)
	}
}

func TestWorkAbort_CallsTheAbortPathAndReports(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"ok": true, "id": "a"})
	out, err := runWork(t, "abort", "a", "--url", base)
	if err != nil {
		t.Fatalf("the abort: %v (out %s)", err, out)
	}
	if !strings.Contains(out, "back in the backlog") {
		t.Errorf("the abort reports the item stopped: %s", out)
	}
	if got.method != "POST" || got.path != "/v1/items/a/abort" {
		t.Errorf("the abort calls the API's abort path for the id: %s %s", got.method, got.path)
	}
}

func TestWorkAbort_TheRefusalsReadTheWayTheAPIStatesThem(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`item "a" is not running: it is backlog`))
	_, err := runWork(t, "abort", "a", "--url", base)
	if err == nil || !strings.Contains(err.Error(), "it is backlog") {
		t.Errorf("a not-running item is refused, naming its state: %v", err)
	}

	base, _ = workAPIStub(t, http.StatusNotFound,
		workAPIErrorReply(`the items file carries no item with id "b"`))
	_, err = runWork(t, "abort", "b", "--url", base)
	if err == nil || !strings.Contains(err.Error(), `no item with id "b"`) {
		t.Errorf("an id the file does not carry is refused, naming it: %v", err)
	}
}

func TestWorkRetry_CallsTheRetryPathAndReports(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"ok": true, "id": "a"})
	out, err := runWork(t, "retry", "a", "--url", base)
	if err != nil {
		t.Fatalf("the retry: %v (out %s)", err, out)
	}
	if !strings.Contains(out, `item "a" is back in the backlog`) {
		t.Errorf("the retry reports the item back in the backlog: %s", out)
	}
	if got.method != "POST" || got.path != "/v1/items/a/retry" {
		t.Errorf("the retry calls the API's retry path for the id: %s %s", got.method, got.path)
	}
}

func TestWorkRetry_TheRefusalsReadTheWayTheAPIStatesThem(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusConflict,
		workAPIErrorReply(`item "a" is not failed: it is running`))
	_, err := runWork(t, "retry", "a", "--url", base)
	if err == nil || !strings.Contains(err.Error(), "it is running") {
		t.Errorf("a not-failed item is refused, naming its state: %v", err)
	}

	base, _ = workAPIStub(t, http.StatusNotFound,
		workAPIErrorReply(`the items file carries no item with id "b"`))
	_, err = runWork(t, "retry", "b", "--url", base)
	if err == nil || !strings.Contains(err.Error(), `no item with id "b"`) {
		t.Errorf("an id the file does not carry is refused, naming it: %v", err)
	}
}

func TestWorkRetry_NeedsOneIDAndAnAddress(t *testing.T) {
	if _, err := runWork(t, "retry", "--url", "http://127.0.0.1:1"); err == nil {
		t.Error("retry with no id is refused")
	}
	if _, err := runWork(t, "retry", "a", "b", "--url", "http://127.0.0.1:1"); err == nil {
		t.Error("retry with two ids is refused")
	}
	_, err := runWork(t, "retry", "a")
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("retry with no address names the flag: %v", err)
	}
}

func TestWorkList_PlainLinesInFileOrder(t *testing.T) {
	reply := map[string]any{"object": "list", "data": []any{
		map[string]any{"id": "a", "instructions": "do a", "dir": ".", "state": "backlog"},
		map[string]any{"id": "b", "instructions": "do b", "dir": ".", "state": "running",
			"node": "n", "startedAt": "2026-09-14T00:00:00Z"},
		map[string]any{"id": "c", "instructions": "do c", "dir": ".", "state": "done",
			"node": "n", "startedAt": "2026-09-14T00:00:00Z", "endedAt": "2026-09-14T00:01:00Z"},
		map[string]any{"id": "d", "instructions": "do d", "dir": ".", "state": "failed",
			"why": "it failed", "endedAt": "2026-09-14T00:02:00Z"},
	}}
	base, got := workAPIStub(t, http.StatusOK, reply)
	out, err := runWork(t, "list", "--url", base)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "GET" || got.path != "/v1/items" {
		t.Errorf("the list reads the API's list path: %s %s", got.method, got.path)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("one line per item:\n%s", out)
	}
	if lines[0] != "a\tbacklog\t-\t-\t-" {
		t.Errorf("no record is backlog, dashes where the values are absent: %q", lines[0])
	}
	if lines[1] != "b\trunning\tn\t2026-09-14T00:00:00Z\t-" {
		t.Errorf("the running record joins its item: %q", lines[1])
	}
	if lines[2] != "c\tdone\tn\t2026-09-14T00:00:00Z\t2026-09-14T00:01:00Z" {
		t.Errorf("the done record joins its item: %q", lines[2])
	}
	if lines[3] != "d\tfailed\t-\t-\t2026-09-14T00:02:00Z" {
		t.Errorf("the failed record joins its item: %q", lines[3])
	}
	// No state colour off a terminal.
	if strings.Contains(out, "\033[") {
		t.Errorf("no colour where there is no terminal:\n%q", out)
	}

	// The alias works the same list.
	out, err = runWork(t, "ls", "--url", base)
	if err != nil || !strings.HasPrefix(out, "a\tbacklog") {
		t.Errorf("the ls alias is the list: %v\n%s", err, out)
	}
}

func TestWorkListLine_TheStateColourWhereThereIsATerminal(t *testing.T) {
	v := orchestratorItemView("a", "done", "", "", "")
	if line := workListLine(v, true); !strings.Contains(line, ansiGreen) || !strings.Contains(line, ansiReset) {
		t.Errorf("a terminal draws the done state in its colour: %q", line)
	}
	if line := workListLine(v, false); strings.Contains(line, "\033[") {
		t.Errorf("no terminal, no colour: %q", line)
	}
	v = orchestratorItemView("a", "failed", "", "", "")
	if line := workListLine(v, true); !strings.Contains(line, ansiRed) {
		t.Errorf("the failed state is its colour: %q", line)
	}
	v = orchestratorItemView("a", "running", "n", "2026-09-14T00:00:00Z", "")
	if line := workListLine(v, true); !strings.Contains(line, ansiYellow) {
		t.Errorf("the running state is its colour: %q", line)
	}
	v = orchestratorItemView("a", "backlog", "", "", "")
	if line := workListLine(v, true); strings.Contains(line, "\033[") {
		t.Errorf("the backlog state takes no colour: %q", line)
	}
}

func TestWorkListTable_LongIDsStayAligned(t *testing.T) {
	items := []orchestrator.ItemView{
		orchestratorItemView("describe-fleet-yaml", "done", "dev-4", "", "2026-09-14T08:36:17Z"),
		orchestratorItemView("a", "backlog", "", "", ""),
		orchestratorItemView("mark-fleet-yaml-pr-ready2", "running", "dev-1", "2026-09-14T21:42:23Z", ""),
	}
	table := workListTable(items)
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != len(items)+1 {
		t.Fatalf("one line per item, plus the heading row:\n%s", table)
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "STATE") ||
		!strings.Contains(lines[0], "NODE") || !strings.Contains(lines[0], "STARTED") || !strings.Contains(lines[0], "ENDED") {
		t.Errorf("the table opens with a heading row naming the columns:\n%s", lines[0])
	}
	// Strip the state's colour codes before measuring: the column widths are
	// computed on the plain text, so the visible columns must line up once
	// the codes are gone.
	strip := strings.NewReplacer(ansiGreen, "", ansiRed, "", ansiYellow, "", ansiGrey, "", ansiReset, "")
	nodeCol := -1
	for i, line := range lines[1:] {
		plain := strip.Replace(line)
		idx := strings.Index(plain, "dev-") // the node column, present on 2 of 3 rows
		if idx == -1 {
			continue
		}
		if nodeCol == -1 {
			nodeCol = idx
		} else if idx != nodeCol {
			t.Errorf("row %d: node column starts at %d, want %d (long id threw the table out of line):\n%s", i, idx, nodeCol, table)
		}
	}
	if nodeCol == -1 {
		t.Fatalf("no row carried a node to check alignment against:\n%s", table)
	}
}

func TestWork_NoURLFailsBeforeTheCall(t *testing.T) {
	_, got := workAPIStub(t, http.StatusOK, map[string]any{"ok": true})
	_, err := runWork(t, "add", "--id", "a", "--instructions", "do", "--dir", ".")
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("no --url, the command fails naming the flag: %v", err)
	}
	if got.method != "" {
		t.Errorf("no address, no call: the stub saw %s %s", got.method, got.path)
	}
}

func TestWork_TwoTokenFlagsAtOnceIsARefusal(t *testing.T) {
	_, err := runWork(t, "list", "--url", "http://127.0.0.1:1",
		"--api-token", "one", "--api-token-file", filepath.Join(t.TempDir(), "token"))
	if err == nil || !strings.Contains(err.Error(), "--api-token") || !strings.Contains(err.Error(), "--api-token-file") {
		t.Errorf("two token flags at once is a refusal, naming both: %v", err)
	}
}

func TestWork_TheTokenComesFromTheEnvironment(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
	t.Setenv(daemon.TokenEnvVar, "sekret")
	if _, err := runWork(t, "list", "--url", base); err != nil {
		t.Fatal(err)
	}
	if got.bearer != "Bearer sekret" {
		t.Errorf("the environment's token goes as the bearer: %q", got.bearer)
	}
}

func TestWork_TheTokenComesFromTheFile(t *testing.T) {
	base, got := workAPIStub(t, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
	t.Setenv(daemon.TokenEnvVar, "")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("filetok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runWork(t, "list", "--url", base, "--api-token-file", path); err != nil {
		t.Fatal(err)
	}
	if got.bearer != "Bearer filetok" {
		t.Errorf("the file's token, trimmed, goes as the bearer: %q", got.bearer)
	}
}

func TestWork_TheRefusedBearerIsReported(t *testing.T) {
	base, _ := workAPIStub(t, http.StatusUnauthorized,
		workAPIErrorReply("missing or invalid bearer token"))
	t.Setenv(daemon.TokenEnvVar, "")
	_, err := runWork(t, "list", "--url", base)
	if err == nil || !strings.Contains(err.Error(), "missing or invalid bearer token") {
		t.Errorf("the API's refusal is the command's error: %v", err)
	}
}

// orchestratorItemView builds a work list view for the line's tests.
func orchestratorItemView(id, state, node, started, ended string) orchestrator.ItemView {
	return orchestrator.ItemView{ID: id, State: state, Node: node, StartedAt: started, EndedAt: ended}
}
