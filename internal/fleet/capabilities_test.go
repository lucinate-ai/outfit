package fleet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countingStatsServer answers every call with body and counts the calls, so a
// test can assert that reading a retained fact costs none.
func countingStatsServer(t *testing.T, body string, calls *int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// registerStatsEnv registers an environment whose stats call is the given URL,
// which registerCloudEnv does not set — the capabilities answer from a stats
// reading, so a test of them needs one.
func registerStatsEnv(t *testing.T, name, url string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SPINLOOP_CONFIG_DIR", home)
	dir := filepath.Join(home, "clouds", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"start_url":%q,"stop_url":%q,"stats_url":%q,"region":"us-east-1","environment":%q}`,
		url, url, url, name)
	if err := os.WriteFile(filepath.Join(dir, "cloud.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A daemon node implements none of the three: its kind has no answer for any
// of them, so it does not pretend to.
func TestDaemonNodeImplementsNoCloudCapability(t *testing.T) {
	cfg := &Config{Nodes: []NodeConfig{{Name: "box", Host: "127.0.0.1", Port: 1}}}
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.(Coster); ok {
		t.Error("a daemon node should not implement Coster: it has no instance type or region")
	}
	if _, ok := node.(SourceLogger); ok {
		t.Error("a daemon node should not implement SourceLogger: its log is a byte offset")
	}
	if _, ok := node.(InstanceReporter); ok {
		t.Error("a daemon node should not implement InstanceReporter: it runs on a machine, not an instance")
	}
}

// A cloud node implements all three, so a caller reaches them by assertion
// rather than by asking what kind it is.
func TestCloudNodeImplementsTheCloudCapabilities(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running"}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)
	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.(Coster); !ok {
		t.Error("a cloud node should implement Coster")
	}
	if _, ok := node.(SourceLogger); !ok {
		t.Error("a cloud node should implement SourceLogger")
	}
	if _, ok := node.(InstanceReporter); !ok {
		t.Error("a cloud node should implement InstanceReporter")
	}
}

// The instance facts come from the reading already taken, not a second call.
func TestCloudNodeInstanceComesFromTheMetricsReading(t *testing.T) {
	stubAWSCreds(t)
	calls := 0
	srv := countingStatsServer(t, `{"state":"running","version":"1.40.0","instanceId":"i-0abc","instanceType":"g6e.xlarge","uptimeSeconds":7200}`, &calls)
	registerStatsEnv(t, "prod", srv)
	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}

	i, _ := node.(InstanceReporter)
	if got := i.Instance(); got != (Instance{}) {
		t.Errorf("instance before any reading = %+v, want empty", got)
	}
	if _, err := node.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := calls
	got := i.Instance()
	if got.Version != "1.40.0" || got.ID != "i-0abc" || got.Type != "g6e.xlarge" {
		t.Errorf("instance = %+v, want the reading's facts", got)
	}
	if calls != before {
		t.Errorf("reading the instance cost %d extra call(s), want none", calls-before)
	}
}

// A node with nothing to price reports no cost rather than a zero one: "$0.00"
// claims it cost nothing, which is a different statement.
func TestCloudNodeCostIsUnreportedWithoutAReading(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"stopped"}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)
	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	c, ok := node.(Coster)
	if !ok {
		t.Fatal("a cloud node should implement Coster")
	}
	cost, err := c.Cost(context.Background())
	if err != nil {
		t.Fatalf("an unpriceable node is not an error: %v", err)
	}
	if cost.Reported() {
		t.Errorf("cost = %+v, want none reported before a reading", cost)
	}
}

// Cost.Reported tells a caller whether there is a figure at all, so a renderer
// never has to decide what a zero means.
func TestCostReported(t *testing.T) {
	if (Cost{}).Reported() {
		t.Error("the zero Cost reports nothing")
	}
	if (Cost{SoFar: 0, PerHour: 1.25}).Reported() != true {
		t.Error("a rate with no elapsed time is still a figure")
	}
}

// A query names its own window, so nothing is retained between calls the way
// a follow's cursor is.
func TestLogQueryDefaults(t *testing.T) {
	q := LogQuery{}
	if q.Source != "" || q.Limit != 0 || q.Since != 0 || q.Instance != "" {
		t.Errorf("the zero LogQuery narrows nothing, got %+v", q)
	}
	q = LogQuery{Source: "boot", Since: time.Hour, Instance: "i-1", Limit: 10}
	if q.Source != "boot" || q.Since != time.Hour || q.Instance != "i-1" || q.Limit != 10 {
		t.Errorf("LogQuery does not carry what it was given: %+v", q)
	}
}
