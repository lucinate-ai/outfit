package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// --- fixtures ---------------------------------------------------------------

// memStore is a Store that keeps its record in memory and its logs in a
// directory: the work list API's tests need no state on disk but the logs
// the API reads and removes.
type memStore struct {
	mu      sync.Mutex
	dir     string
	records map[string]ItemState
	closed  bool
}

func newMemStore(t *testing.T) *memStore {
	t.Helper()
	return &memStore{dir: t.TempDir(), records: map[string]ItemState{}}
}

func (m *memStore) Load() (map[string]ItemState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]ItemState, len(m.records))
	for id, st := range m.records {
		out[id] = st
	}
	return out, nil
}

func (m *memStore) Save(items map[string]ItemState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = make(map[string]ItemState, len(items))
	for id, st := range items {
		m.records[id] = st
	}
	return nil
}

func (m *memStore) LogPath(id string) string {
	return filepath.Join(m.dir, id+".log")
}

func (m *memStore) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

// seedRecord puts a record in the store the way the run would leave one,
// for the work list that loads the store next.
func (m *memStore) seedRecord(id string, st ItemState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[id] = st
}

// testWorkList builds a work list over a fresh items file and an in-memory
// store, the dispatcher nil: the API's tests take no launch. The seed puts
// the records the store carries before the work list loads it.
func testWorkList(t *testing.T, content string, seed func(*memStore)) (*WorkList, *memStore, string) {
	t.Helper()
	path := writeItems(t, content)
	store := newMemStore(t)
	if seed != nil {
		seed(store)
	}
	wl, err := NewWorkList(path, store, nil, "http://gateway:4000", nil)
	if err != nil {
		t.Fatal(err)
	}
	return wl, store, path
}

// workListServer serves the work list API over the work list on a test
// server, the token the callers must present.
func workListServer(t *testing.T, wl *WorkList, token string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(NewHandler(wl, token, nil))
}

// apiGet asks the work list API and decodes its JSON body.
func apiGet(t *testing.T, srv *httptest.Server, token, path string) (int, map[string]any, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{}
	json.Unmarshal(data, &body)
	return resp.StatusCode, body, string(data)
}

// --- 2.1: the surface -------------------------------------------------------

func TestAPI_TheWorkListShowsEveryItemWithItsRecord(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(
		itemSpec{id: "a", instr: "do a", dir: "./a"},
		itemSpec{id: "b", instr: "do b", dir: "./b"},
		itemSpec{id: "c", instr: "do c", dir: "./c"},
	), func(s *memStore) {
		s.seedRecord("b", ItemState{State: StateRunning, Node: "n1", StartedAt: "2026-01-01T00:00:00Z"})
		s.seedRecord("c", ItemState{State: StateFailed, Node: "n1", Why: "the agent ended in error", StartedAt: "2026-01-01T00:00:00Z", EndedAt: "2026-01-01T00:10:00Z"})
	})
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, body, raw := apiGet(t, srv, "", "/v1/items")
	if code != http.StatusOK {
		t.Fatalf("the work list is answered, got %d: %s", code, raw)
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 3 {
		t.Fatalf("every item the file carries is listed, got %s", raw)
	}
	// The file's order.
	wantIDs := []string{"a", "b", "c"}
	for i, id := range wantIDs {
		if got := data[i].(map[string]any)["id"]; got != id {
			t.Errorf("the file's order is the list's, got %s", raw)
		}
	}
	// Backlog where there is no record.
	a := data[0].(map[string]any)
	if a["state"] != StateBacklog {
		t.Errorf("an item with no record is backlog, got %s", raw)
	}
	if a["instructions"] != "do a" || a["dir"] != "./a" {
		t.Errorf("the item's fields are the file's, got %s", raw)
	}
	// Running with its node and its start.
	b := data[1].(map[string]any)
	if b["state"] != StateRunning || b["node"] != "n1" || b["startedAt"] != "2026-01-01T00:00:00Z" {
		t.Errorf("a running item shows its node and its start, got %s", raw)
	}
	// Failed with its end and its why.
	c := data[2].(map[string]any)
	if c["state"] != StateFailed || c["why"] != "the agent ended in error" || c["endedAt"] != "2026-01-01T00:10:00Z" {
		t.Errorf("a failed item shows its end and its why, got %s", raw)
	}
}

func TestAPI_ACallerReadsAnItemsOutput(t *testing.T) {
	wl, store, _ := testWorkList(t, itemsFile(
		itemSpec{id: "a", instr: "do a", dir: "./a"},
		itemSpec{id: "b", instr: "do b", dir: "./b"},
	), nil)
	if err := os.WriteFile(store.LogPath("a"), []byte("the agent said so\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	// The kept output, as it was kept.
	code, body, raw := apiGet(t, srv, "", "/v1/items/a/log")
	if code != http.StatusOK || body["log"] != "the agent said so\n" {
		t.Fatalf("the kept output is answered, got %d: %s", code, raw)
	}
	// An item with none is answered as having none, not as a fault.
	code, body, raw = apiGet(t, srv, "", "/v1/items/b/log")
	if code != http.StatusOK || body["log"] != nil {
		t.Fatalf("an item with no kept output is answered as having none, got %d: %s", code, raw)
	}
	// An id the file does not carry is refused, naming it.
	code, body, raw = apiGet(t, srv, "", "/v1/items/ghost/log")
	if code != http.StatusNotFound || !strings.Contains(raw, "ghost") {
		t.Fatalf("an id the file does not carry is refused, naming it, got %d: %s", code, raw)
	}
}

func TestAPI_AnUnknownPathIsNamedAsSuch(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/nope"},
		{http.MethodDelete, "/v1/items"},
		{http.MethodPost, "/v1/items/a/restart"},
		{http.MethodPut, "/v1/items/a/log"},
		{http.MethodGet, "/v1/items/a"},
	} {
		req, err := http.NewRequest(p.method, srv.URL+p.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s should be a 404, got %d", p.method, p.path, resp.StatusCode)
		}
		if !strings.Contains(string(data), "/v1/items") || !strings.Contains(string(data), "/health") {
			t.Errorf("the 404 names the paths the API serves, got %s", data)
		}
	}
}

func TestAPI_TheHealthCheckTouchesNoWork(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()
	code, body, raw := apiGet(t, srv, "", "/health")
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("the health check answers that the orchestrator is up, got %d: %s", code, raw)
	}
}

// --- 2.2: the bearer ---------------------------------------------------------

func TestAPI_ABearerTokenGatesEveryPath(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "the-token")
	defer srv.Close()

	paths := []struct{ method, path, body string }{
		{http.MethodGet, "/health", ""},
		{http.MethodGet, "/v1/items", ""},
		{http.MethodPost, "/v1/items", `{"id":"b","instructions":"do","dir":"./b"}`},
		{http.MethodGet, "/v1/items/a/log", ""},
		{http.MethodDelete, "/v1/items/a", ""},
		{http.MethodPost, "/v1/items/a/abort", ""},
		{http.MethodPost, "/v1/items/a/retry", ""},
		{http.MethodGet, "/nope", ""},
	}
	for token, wantStatus := range map[string]int{"": http.StatusUnauthorized, "the-wrong-token": http.StatusUnauthorized} {
		for _, p := range paths {
			body := strings.NewReader(p.body)
			req, err := http.NewRequest(p.method, srv.URL+p.path, body)
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
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != wantStatus {
				t.Errorf("a caller without the token is refused on every path: %s %s gave %d, want %d", p.method, p.path, resp.StatusCode, wantStatus)
			}
		}
	}
	// A caller with the token is served.
	code, _, raw := apiGet(t, srv, "the-token", "/v1/items")
	if code != http.StatusOK {
		t.Fatalf("a caller with the token is served, got %d: %s", code, raw)
	}
}

func TestAPI_ATokenlessHandlerServes(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()
	// On a loopback bind with no token, a caller needs nothing: the
	// tokenless handler is the one Listen permits there.
	for _, path := range []string{"/health", "/v1/items", "/v1/items/a/log"} {
		if code, _, raw := apiGet(t, srv, "", path); code != http.StatusOK {
			t.Errorf("a tokenless handler serves, %s gave %d: %s", path, code, raw)
		}
	}
}

// --- 2.3: the mutations -------------------------------------------------------

// apiDo sends one request to the work list API and returns its status and
// body.
func apiDo(t *testing.T, srv *httptest.Server, token, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestAPI_AnAddedItemEntersTheBacklog(t *testing.T) {
	wl, _, path := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items",
		`{"id":"b","instructions":"do b","dir":"./b","tags":["gpu=a100"],"priority":3}`)
	if code != http.StatusCreated {
		t.Fatalf("an add the file accepts is created, got %d: %s", code, raw)
	}
	// The items file carries it, still a valid items file.
	items, err := LoadItems(path)
	if err != nil {
		t.Fatalf("the file is still a valid items file after the add: %v", err)
	}
	if len(items) != 2 || items[1].ID != "b" || items[1].Tags[0] != "gpu=a100" || items[1].Priority != 3 {
		t.Errorf("the file carries the added item, in file order, got %+v", items)
	}
	// And the work list shows it backlog.
	code, body, raw := apiGet(t, srv, "", "/v1/items")
	if code != http.StatusOK {
		t.Fatalf("the work list is answered: %d: %s", code, raw)
	}
	data := body["data"].([]any)
	if len(data) != 2 || data[1].(map[string]any)["id"] != "b" || data[1].(map[string]any)["state"] != StateBacklog {
		t.Errorf("the added item enters the backlog, got %s", raw)
	}
}

func TestAPI_ADuplicateIDIsRefusedNamingIt(t *testing.T) {
	wl, _, path := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items",
		`{"id":"a","instructions":"again","dir":"./a"}`)
	if code != http.StatusConflict || !strings.Contains(raw, `\"a\"`) {
		t.Fatalf("a duplicate id is refused, naming it, got %d: %s", code, raw)
	}
	// The file is unchanged.
	items, err := LoadItems(path)
	if err != nil || len(items) != 1 {
		t.Errorf("the file is unchanged by a refused add, got %+v (%v)", items, err)
	}
}

func TestAPI_AFinishedIDIsRefusedNamingTheRecord(t *testing.T) {
	// The id the state records ended is not in the file: that is what the
	// record refusal is for.
	wl, _, path := testWorkList(t, itemsFile(itemSpec{id: "b", instr: "do b", dir: "./b"}),
		func(s *memStore) {
			s.seedRecord("a", ItemState{State: StateDone, Node: "n1", EndedAt: "2026-01-01T00:10:00Z"})
		})
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items",
		`{"id":"a","instructions":"again","dir":"./a"}`)
	if code != http.StatusConflict || !strings.Contains(raw, "done") {
		t.Fatalf("an id the state records done is refused, naming the record, got %d: %s", code, raw)
	}
	wl.mu.Lock()
	wl.records["a"] = ItemState{State: StateFailed, Node: "n1", Why: "it failed"}
	wl.mu.Unlock()
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items",
		`{"id":"a","instructions":"again","dir":"./a"}`)
	if code != http.StatusConflict || !strings.Contains(raw, "failed") {
		t.Fatalf("an id the state records failed is refused, naming the record, got %d: %s", code, raw)
	}
	items, err := LoadItems(path)
	if err != nil || len(items) != 1 {
		t.Errorf("the file is unchanged by a refused add, got %+v (%v)", items, err)
	}
}

func TestAPI_ANInvalidAddIsRefusedNamingTheFault(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	for name, body := range map[string]string{
		"no id":           `{"instructions":"do","dir":"./b"}`,
		"no instructions": `{"id":"b","dir":"./b"}`,
		"no directory":    `{"id":"b","instructions":"do"}`,
		"bad tag":         `{"id":"b","instructions":"do","dir":"./b","tags":["notatag"]}`,
		"not json":        `not: [a json body`,
	} {
		code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items", body)
		if code != http.StatusBadRequest {
			t.Errorf("%s is a bad request, got %d: %s", name, code, raw)
		}
	}
}

func TestAPI_ARemovalTakesTheItemItsRecordAndItsOutput(t *testing.T) {
	wl, store, path := testWorkList(t, itemsFile(
		itemSpec{id: "a", instr: "do a", dir: "./a"},
		itemSpec{id: "b", instr: "do b", dir: "./b"},
	), func(s *memStore) {
		s.seedRecord("a", ItemState{State: StateDone, Node: "n1"})
	})
	if err := os.WriteFile(store.LogPath("a"), []byte("the agent's output"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodDelete, "/v1/items/a", "")
	if code != http.StatusOK {
		t.Fatalf("a removal the work list accepts is answered, got %d: %s", code, raw)
	}
	// The file no longer carries it.
	items, err := LoadItems(path)
	if err != nil || len(items) != 1 || items[0].ID != "b" {
		t.Errorf("the file no longer carries the removed item, got %+v (%v)", items, err)
	}
	// Its record is gone from the state.
	if st, recorded := store.records["a"]; recorded {
		t.Errorf("the record is gone from the state, got %+v", st)
	}
	// And its kept output is gone with it.
	if _, err := os.Stat(store.LogPath("a")); !os.IsNotExist(err) {
		t.Errorf("the kept output is gone with the item, got %v", err)
	}
}

// TestAPI_ARemovalTakesTheDockerBackendsScopedConfigToo checks that a
// removal takes the docker backend's per-launch config directory out with
// the item's kept output — the same cleanup, one more directory.
func TestAPI_ARemovalTakesTheDockerBackendsScopedConfigToo(t *testing.T) {
	itemDir := filepath.Join(t.TempDir(), "a")
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: itemDir}), nil)
	configDir := ItemConfigDir(itemDir)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodDelete, "/v1/items/a", "")
	if code != http.StatusOK {
		t.Fatalf("a removal the work list accepts is answered, got %d: %s", code, raw)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("the docker backend's scoped config directory should be gone with the item, got %v", err)
	}
}

// TestAPI_ARemovalResolvesTheConfigDirAgainstBaseDir checks that Remove's
// own cleanup resolves a relative item dir against WithBaseDir the same
// way a launch already does — not the raw dir the items file carries.
func TestAPI_ARemovalResolvesTheConfigDirAgainstBaseDir(t *testing.T) {
	base := t.TempDir()
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "a"}), nil)
	wl = wl.WithBaseDir(base)
	configDir := ItemConfigDir(filepath.Join(base, "a"))
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodDelete, "/v1/items/a", "")
	if code != http.StatusOK {
		t.Fatalf("a removal the work list accepts is answered, got %d: %s", code, raw)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("the config directory under baseDir should be gone with the item, got %v", err)
	}
}

// TestAPI_ARemovalTakesTheItemsOwnLogCopyToo checks that a removal takes
// the item's own copy of its log out with the canonical one.
func TestAPI_ARemovalTakesTheItemsOwnLogCopyToo(t *testing.T) {
	itemDir := filepath.Join(t.TempDir(), "a")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: itemDir}), nil)
	logFile := ItemLogFile(itemDir)
	if err := os.WriteFile(logFile, []byte("the agent's output"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodDelete, "/v1/items/a", "")
	if code != http.StatusOK {
		t.Fatalf("a removal the work list accepts is answered, got %d: %s", code, raw)
	}
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Errorf("the item's own log copy should be gone with the item, got %v", err)
	}
}

func TestAPI_ARemovalItCannotMakeIsRefused(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}),
		func(s *memStore) {
			// A running item: the refusal names it and the abort that goes
			// first.
			s.seedRecord("a", ItemState{State: StateRunning, Node: "n1"})
		})
	wl.mu.Lock()
	wl.inflight["a"] = &flight{item: Item{ID: "a"}, node: Node{Name: "n1"}, child: newFakeChild(nil, make(chan struct{})), done: make(chan struct{})}
	wl.mu.Unlock()

	srv := workListServer(t, wl, "")
	defer srv.Close()
	code, raw := apiDo(t, srv, "", http.MethodDelete, "/v1/items/a", "")
	if code != http.StatusConflict || !strings.Contains(raw, `\"a\"`) || !strings.Contains(raw, "abort") {
		t.Fatalf("a running item's removal is refused, naming it and the abort, got %d: %s", code, raw)
	}
	// An id the file does not carry: the refusal names it.
	code, raw = apiDo(t, srv, "", http.MethodDelete, "/v1/items/ghost", "")
	if code != http.StatusNotFound || !strings.Contains(raw, "ghost") {
		t.Fatalf("an id the file does not carry is refused, naming it, got %d: %s", code, raw)
	}
}

func TestAPI_AnAbortReturnsTheItemToTheBacklog(t *testing.T) {
	wl, store, path := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}),
		func(s *memStore) {
			s.seedRecord("a", ItemState{State: StateRunning, Node: "n1"})
		})
	child := newFakeChild(nil, make(chan struct{}))
	fl := &flight{item: Item{ID: "a"}, node: Node{Name: "n1"}, child: child, done: make(chan struct{})}
	wl.mu.Lock()
	wl.inflight["a"] = fl
	wl.mu.Unlock()
	// The wait the run's pass would have started: it is what closes the
	// flight's done when the agent ends.
	go func() {
		err := child.Wait()
		close(fl.done)
		wl.results <- result{id: "a", err: err}
	}()

	srv := workListServer(t, wl, "")
	defer srv.Close()
	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items/a/abort", "")
	if code != http.StatusOK {
		t.Fatalf("an abort the work list accepts is answered, got %d: %s", code, raw)
	}
	// The agent is stopped, the way a clean interrupt stops it.
	if !child.wasStopped() {
		t.Error("the abort stops the item's agent")
	}
	// Its record is gone from the state.
	if st, recorded := store.records["a"]; recorded {
		t.Errorf("the record is gone from the state, got %+v", st)
	}
	// The file still carries it: back in the backlog.
	items, err := LoadItems(path)
	if err != nil || len(items) != 1 || items[0].ID != "a" {
		t.Errorf("the item is back in the backlog, got %+v (%v)", items, err)
	}
}

func TestAPI_AnAbortOfANonRunningItemIsRefused(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	// In the backlog: the refusal names the item and its state.
	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items/a/abort", "")
	if code != http.StatusConflict || !strings.Contains(raw, `\"a\"`) || !strings.Contains(raw, StateBacklog) {
		t.Fatalf("aborting a backlog item is refused, naming the item and its state, got %d: %s", code, raw)
	}
	// Done: the refusal names the record's state.
	wl.mu.Lock()
	wl.records["a"] = ItemState{State: StateDone, Node: "n1"}
	wl.mu.Unlock()
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/a/abort", "")
	if code != http.StatusConflict || !strings.Contains(raw, StateDone) {
		t.Fatalf("aborting a done item is refused, naming the state, got %d: %s", code, raw)
	}
	// Failed: likewise.
	wl.mu.Lock()
	wl.records["a"] = ItemState{State: StateFailed, Why: "it failed"}
	wl.mu.Unlock()
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/a/abort", "")
	if code != http.StatusConflict || !strings.Contains(raw, StateFailed) {
		t.Fatalf("aborting a failed item is refused, naming the state, got %d: %s", code, raw)
	}
	// An id the file does not carry: refused, naming it.
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/ghost/abort", "")
	if code != http.StatusNotFound || !strings.Contains(raw, "ghost") {
		t.Fatalf("an id the file does not carry is refused, naming it, got %d: %s", code, raw)
	}
}

func TestAPI_ARetryReturnsAFailedItemToTheBacklog(t *testing.T) {
	wl, store, path := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}),
		func(s *memStore) {
			s.seedRecord("a", ItemState{State: StateFailed, Why: "it failed", Node: "n1"})
		})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := workListServer(t, wl, "")
	defer srv.Close()

	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items/a/retry", "")
	if code != http.StatusOK {
		t.Fatalf("a retry the work list accepts is answered, got %d: %s", code, raw)
	}
	if st, recorded := store.records["a"]; recorded {
		t.Errorf("the record is gone from the state, got %+v", st)
	}
	for _, v := range wl.List() {
		if v.ID == "a" && v.State != StateBacklog {
			t.Errorf("the item shows backlog, got %s", v.State)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the items file is unchanged by a retry")
	}
}

func TestAPI_ARetryOfAnItemThatHasNotFailedIsRefused(t *testing.T) {
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := workListServer(t, wl, "")
	defer srv.Close()

	// In the backlog: the refusal names the item and its state.
	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items/a/retry", "")
	if code != http.StatusConflict || !strings.Contains(raw, `\"a\"`) || !strings.Contains(raw, StateBacklog) {
		t.Fatalf("retrying a backlog item is refused, naming the item and its state, got %d: %s", code, raw)
	}
	for _, state := range []string{StateDone, StateRunning} {
		wl.mu.Lock()
		wl.records["a"] = ItemState{State: state, Node: "n1"}
		wl.mu.Unlock()
		code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/a/retry", "")
		if code != http.StatusConflict || !strings.Contains(raw, state) {
			t.Fatalf("retrying a %s item is refused, naming the state, got %d: %s", state, code, raw)
		}
		wl.mu.Lock()
		_, kept := wl.records["a"]
		wl.mu.Unlock()
		if !kept {
			t.Errorf("a refused retry leaves the %s record in place", state)
		}
	}
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/ghost/retry", "")
	if code != http.StatusNotFound || !strings.Contains(raw, "ghost") {
		t.Fatalf("an id the file does not carry is refused, naming it, got %d: %s", code, raw)
	}
	code, _ = apiDo(t, srv, "", http.MethodGet, "/v1/items/a/retry", "")
	if code != http.StatusNotFound {
		t.Errorf("a retry is a POST only, got %d for a GET", code)
	}
}

// A retried item is admitted again by the run's loop, like any backlog item.
func TestWorkList_ARetriedItemIsAdmittedAgain(t *testing.T) {
	topo := &fakeTopo{}
	topo.set(Topology{Wake: true, Nodes: []Node{runningNode("n", "org/m", nil)}})
	h := &fakeHarness{name: "opencode", bin: "unused"}
	var mu sync.Mutex
	launches := 0
	rec := &launchRecorder{factory: func(bin string, args []string, dir, logPath string, env []string) (Child, error) {
		mu.Lock()
		launches++
		n := launches
		mu.Unlock()
		if n == 1 {
			return newFakeChild(errors.New("boom"), nil), nil
		}
		return newFakeChild(nil, nil), nil
	}}
	d := NewDispatcher(h, "http://gateway:4000", "the-token", false)
	d.start = rec.start
	dir := t.TempDir()
	path := writeItems(t, itemsFile(itemSpec{id: "a", dir: dir}))
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	wl, err := NewWorkList(path, store, d, "http://gateway:4000", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wl.Close()
	srv := workListServer(t, wl, "")
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Gateway:    "http://gateway:4000",
			ItemsPath:  path,
			Topologist: topo,
			Dispatcher: d,
			Tick:       2 * time.Millisecond,
			WorkList:   wl,
		})
	}()
	if err := waitForState(t, path, "a", StateFailed); err != nil {
		t.Fatal(err)
	}
	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items/a/retry", "")
	if code != http.StatusOK {
		t.Fatalf("the retry is answered: %d: %s", code, raw)
	}
	if err := waitForState(t, path, "a", StateDone); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("the interrupt ends the run without an error: %v", err)
	}
}

// --- 1.2: the loop and the API share one state --------------------------------

// The two callers the design names: the loop, and a handler-shaped caller
// driving the same work list. The shared state must stay consistent under
// both, so the test runs under -race and leaves the work list to check.
func TestWorkList_TheLoopAndTheAPIShareOneState(t *testing.T) {
	topo := &fakeTopo{}
	topo.set(Topology{Wake: true, Nodes: []Node{runningNode("n", "org/m", nil)}})
	h := &fakeHarness{name: "opencode", bin: "unused"}
	rec := &launchRecorder{factory: func(bin string, args []string, dir, logPath string, env []string) (Child, error) {
		return newFakeChild(nil, nil), nil
	}}
	d := NewDispatcher(h, "http://gateway:4000", "the-token", false)
	d.start = rec.start
	dir := t.TempDir()
	path := writeItems(t, itemsFile(itemSpec{id: "a", dir: dir}))
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	wl, err := NewWorkList(path, store, d, "http://gateway:4000", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Gateway:    "http://gateway:4000",
			ItemsPath:  path,
			Topologist: topo,
			Dispatcher: d,
			Tick:       2 * time.Millisecond,
			WorkList:   wl,
		})
	}()

	stateOf := func(id string) string {
		for _, v := range wl.List() {
			if v.ID == id {
				return v.State
			}
		}
		return ""
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			id := fmt.Sprintf("x%d", i)
			if err := wl.Add(Item{ID: id, Instructions: "do", Dir: dir}); err != nil {
				t.Errorf("an add from the API is accepted: %v", err)
				return
			}
			// Wait for the run to take it up and end it, then read its
			// output and take it out.
			deadline := time.Now().Add(10 * time.Second)
			for stateOf(id) != StateDone && stateOf(id) != StateFailed && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if _, _, err := wl.Log(id); err != nil {
				t.Errorf("a log read is answered while the item stands: %v", err)
				return
			}
			if err := wl.Remove(id); err != nil {
				t.Errorf("a removal of an ended item is accepted: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("the loop ends on the cancel, not an error: %v", err)
	}
	// The first item, the file's own, was worked too.
	if stateOf("a") != StateDone {
		t.Errorf("the file's own item is worked, got %q", stateOf("a"))
	}
}

// --- the call log -------------------------------------------------------

// TestAPI_EveryCallIsLoggedWithItsOutcome checks that a call the API accepts
// and a call it refuses both leave a record naming the method, the path —
// carrying the item's id where the path does — and the status the caller
// was answered with.
func TestAPI_EveryCallIsLoggedWithItsOutcome(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wl, _, _ := testWorkList(t, itemsFile(itemSpec{id: "a", instr: "do a", dir: "./a"}), nil)
	srv := httptest.NewServer(NewHandler(wl, "", logger))
	defer srv.Close()

	// A call the API accepts.
	code, _, raw := apiGet(t, srv, "", "/v1/items")
	if code != http.StatusOK {
		t.Fatalf("the list is answered: %d: %s", code, raw)
	}
	out := buf.String()
	if !strings.Contains(out, "method=GET") || !strings.Contains(out, "path=/v1/items") || !strings.Contains(out, "status=200") {
		t.Errorf("a served call is logged naming the method, path and status, got:\n%s", out)
	}

	// A call the API refuses, naming the item in the path.
	buf.Reset()
	code, raw = apiDo(t, srv, "", http.MethodDelete, "/v1/items/ghost", "")
	if code != http.StatusNotFound {
		t.Fatalf("removing an absent id is refused: %d: %s", code, raw)
	}
	out = buf.String()
	if !strings.Contains(out, "method=DELETE") || !strings.Contains(out, "path=/v1/items/ghost") || !strings.Contains(out, "status=404") {
		t.Errorf("a refused call is logged naming the method, path and status, got:\n%s", out)
	}
}

// --- 3.3: a change the API accepts reaches the run ----------------------------

// The run and the API on one work list: an add the API accepts is worked to
// done, an abort returns its item to the backlog and the run admits it
// again, and a remove the API accepts leaves the file, the state, and the
// output all without the item.
func TestAPI_TheRunsChangesReachTheRun(t *testing.T) {
	topo := &fakeTopo{}
	topo.set(Topology{Wake: true, Nodes: []Node{runningNode("n", "org/m", nil)}})
	h := &fakeHarness{name: "opencode", bin: "unused"}
	// A child that holds where its instructions say hold; the rest end at
	// once.
	var mu sync.Mutex
	var holds []chan struct{}
	rec := &launchRecorder{factory: func(bin string, args []string, dir, logPath string, env []string) (Child, error) {
		if len(args) > 0 && strings.Contains(args[len(args)-1], "hold") {
			release := make(chan struct{})
			mu.Lock()
			holds = append(holds, release)
			mu.Unlock()
			return newFakeChild(nil, release), nil
		}
		return newFakeChild(nil, nil), nil
	}}
	d := NewDispatcher(h, "http://gateway:4000", "the-token", false)
	d.start = rec.start
	dir := t.TempDir()
	path := writeItems(t, itemsFile(itemSpec{id: "a", dir: dir}))
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	wl, err := NewWorkList(path, store, d, "http://gateway:4000", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wl.Close()
	srv := workListServer(t, wl, "")
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Gateway:    "http://gateway:4000",
			ItemsPath:  path,
			Topologist: topo,
			Dispatcher: d,
			Tick:       2 * time.Millisecond,
			WorkList:   wl,
		})
	}()

	// The file's own item is worked to done.
	if err := waitForState(t, path, "a", StateDone); err != nil {
		t.Fatal(err)
	}
	// An add over the API is worked to done, like any item the run reads.
	code, raw := apiDo(t, srv, "", http.MethodPost, "/v1/items",
		fmt.Sprintf(`{"id":"b","instructions":"do b","dir":"%s"}`, dir))
	if code != http.StatusCreated {
		t.Fatalf("the add is accepted: %d: %s", code, raw)
	}
	if err := waitForState(t, path, "b", StateDone); err != nil {
		t.Fatal(err)
	}
	// An item the agent holds: it goes running, and stays there.
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items",
		fmt.Sprintf(`{"id":"c","instructions":"hold the line","dir":"%s"}`, dir))
	if code != http.StatusCreated {
		t.Fatalf("the add is accepted: %d: %s", code, raw)
	}
	if err := waitForState(t, path, "c", StateRunning); err != nil {
		t.Fatal(err)
	}
	// Its removal is refused while it runs.
	code, raw = apiDo(t, srv, "", http.MethodDelete, "/v1/items/c", "")
	if code != http.StatusConflict || !strings.Contains(raw, "abort") {
		t.Fatalf("a running item's removal is refused, naming the abort, got %d: %s", code, raw)
	}
	// The abort stops it, and the run admits the item again.
	code, raw = apiDo(t, srv, "", http.MethodPost, "/v1/items/c/abort", "")
	if code != http.StatusOK {
		t.Fatalf("the abort is answered: %d: %s", code, raw)
	}
	mu.Lock()
	firstHold := len(holds)
	mu.Unlock()
	if err := waitForState(t, path, "c", StateRunning); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	reAdmitted := len(holds) > firstHold
	mu.Unlock()
	if !reAdmitted {
		t.Fatal("the run admits the aborted item again")
	}
	// The interrupt re-queues the held item, and the run is down when the
	// remove goes.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("the interrupt ends the run without an error: %v", err)
	}
	code, raw = apiDo(t, srv, "", http.MethodDelete, "/v1/items/c", "")
	if code != http.StatusOK {
		t.Fatalf("the remove is answered: %d: %s", code, raw)
	}
	items, err := LoadItems(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == "c" {
			t.Error("the file no longer carries the removed item")
		}
	}
	sf := readState(t, path)
	if st, recorded := sf.Items["c"]; recorded {
		t.Errorf("the state no longer carries the removed item's record, got %+v", st)
	}
	if _, err := os.Stat(store.LogPath("c")); !os.IsNotExist(err) {
		t.Errorf("the removed item's output is gone, got %v", err)
	}
}

// waitForState polls the state beside the items file until the item reaches
// the wanted state, or the deadline passes.
func waitForState(t *testing.T, path, id, want string) error {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := readState(t, path).Items[id]; st.State == want {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	st := readState(t, path).Items[id]
	return fmt.Errorf("item %s never reached %s, it is %s (%s)", id, want, st.State, st.Why)
}
