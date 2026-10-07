package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// stubNode is a node that answers whatever the test gives it and implements
// nothing else, standing in for a kind with no cloud capabilities.
type stubNode struct {
	name  string
	stats metrics.Stats
	logs  daemon.LogsResponse
	err   error
	// readOffset records that the node was read the ordinary way, which is
	// what a node with no queryable log must fall back to.
	readOffset bool
}

func (n *stubNode) Name() string { return n.name }
func (n *stubNode) Status(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, n.err
}
func (n *stubNode) Metrics(context.Context) (metrics.Stats, error) { return n.stats, n.err }
func (n *stubNode) Start(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *stubNode) StartWith(context.Context, *inference.DeployConfig, string) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *stubNode) Stop(context.Context) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, nil
}
func (n *stubNode) Logs(_ context.Context, _ int64, _ int) (daemon.LogsResponse, error) {
	n.readOffset = true
	return n.logs, n.err
}

// costingNode is a node that can be priced, for asserting the priced call
// reaches its capability.
type costingNode struct {
	*stubNode
	cost Cost
	err  error
}

func (n *costingNode) Cost(context.Context) (Cost, error) { return n.cost, n.err }

// queryingNode is a node whose log can be narrowed.
type queryingNode struct {
	*stubNode
	got  LogQuery
	resp daemon.LogsResponse
}

func (n *queryingNode) LogsMatching(_ context.Context, q LogQuery) (daemon.LogsResponse, error) {
	n.got = q
	return n.resp, nil
}

// A node that can be priced carries its cost; one that cannot is read exactly
// as the unpriced call reads it, and neither fails.
func TestPricedMetricsCall(t *testing.T) {
	plain := &stubNode{name: "box", stats: metrics.Stats{State: "running"}}
	r := PricedMetricsCall(context.Background(), plain)
	if !r.OK() {
		t.Fatalf("an unpriceable node should still read: %+v", r)
	}
	if r.Cost.Reported() {
		t.Errorf("a node that cannot be priced reported %+v", r.Cost)
	}

	priced := &costingNode{
		stubNode: &stubNode{name: "prod", stats: metrics.Stats{State: "running"}},
		cost:     Cost{SoFar: 3.94, PerHour: 1.75},
	}
	r = PricedMetricsCall(context.Background(), priced)
	if !r.Cost.Reported() || r.Cost.SoFar != 3.94 {
		t.Errorf("cost = %+v, want the node's", r.Cost)
	}
}

// A price that cannot be fetched leaves the cost unreported rather than
// failing a reading that otherwise succeeded.
func TestPricedMetricsCallSwallowsAPriceFailure(t *testing.T) {
	n := &costingNode{
		stubNode: &stubNode{name: "prod", stats: metrics.Stats{State: "running"}},
		err:      context.DeadlineExceeded,
	}
	r := PricedMetricsCall(context.Background(), n)
	if !r.OK() {
		t.Fatalf("a failed price should not fail the reading: %+v", r)
	}
	if r.Cost.Reported() {
		t.Errorf("cost = %+v, want none", r.Cost)
	}
}

// A node whose metrics call fails is not asked for a price: there is nothing
// to price and the failure is already the answer.
func TestPricedMetricsCallSkipsAFailedNode(t *testing.T) {
	n := &costingNode{
		stubNode: &stubNode{name: "prod", err: context.Canceled},
		cost:     Cost{SoFar: 9, PerHour: 9},
	}
	r := PricedMetricsCall(context.Background(), n)
	if r.OK() {
		t.Fatal("a node that did not answer should not read as OK")
	}
	if r.Cost.Reported() {
		t.Errorf("a node that did not answer reported a cost: %+v", r.Cost)
	}
}

// A queryable log is narrowed by what the caller asked; one that is not is
// read from its tail, so the flag narrows what it can and leaves the rest.
func TestQueriedLogsCall(t *testing.T) {
	q := LogQuery{Source: "boot", Since: 30 * time.Minute, Instance: "i-42"}

	queryable := &queryingNode{
		stubNode: &stubNode{name: "prod"},
		resp:     daemon.LogsResponse{Content: "boot output"},
	}
	r := QueriedLogsCall(q, 500)(context.Background(), queryable)
	if !r.OK() {
		t.Fatalf("queried read failed: %+v", r)
	}
	if queryable.got.Source != "boot" || queryable.got.Instance != "i-42" {
		t.Errorf("the query did not reach the node: %+v", queryable.got)
	}
	if queryable.got.Limit != 500 {
		t.Errorf("limit = %d, want the caller's", queryable.got.Limit)
	}
	if r.Logs.Content != "boot output" {
		t.Errorf("content = %q, want the node's", r.Logs.Content)
	}

	plain := &stubNode{name: "box", logs: daemon.LogsResponse{Content: "engine output"}}
	r = QueriedLogsCall(q, 500)(context.Background(), plain)
	if !plain.readOffset {
		t.Error("a node with no queryable log should be read the ordinary way")
	}
	if r.Logs.Content != "engine output" {
		t.Errorf("content = %q, want the node's own output", r.Logs.Content)
	}
}

// A cloud environment's queried read reaches its log store with the window
// the caller named. The store is CloudWatch, reached through the AWS SDK
// rather than the control plane's HTTP endpoints, so this substitutes the
// FetchLogsFn variable rather than an httptest server.
func TestCloudNodeLogsMatching(t *testing.T) {
	registerStatsEnv(t, "prod", "http://unused.invalid")
	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	s, ok := node.(SourceLogger)
	if !ok {
		t.Fatal("a cloud node should implement SourceLogger")
	}

	restore := FetchLogsFn
	t.Cleanup(func() { FetchLogsFn = restore })
	var got cloud.LogQuery
	FetchLogsFn = func(_ context.Context, _ cloud.Config, q cloud.LogQuery) (cloud.LogResult, error) {
		got = q
		return cloud.LogResult{Events: []cloud.LogEvent{{Message: "boot line"}}}, nil
	}

	resp, err := s.LogsMatching(context.Background(), LogQuery{Source: "boot", Limit: 10, Instance: "i-1"})
	if err != nil {
		t.Fatalf("LogsMatching: %v", err)
	}
	if got.Source != "boot" || got.Limit != 10 || got.Instance != "i-1" {
		t.Errorf("query reaching the log store = %+v, want the caller's filters", got)
	}
	if resp.Content != "boot line\n" {
		t.Errorf("content = %q, want the stub event rendered", resp.Content)
	}
}
