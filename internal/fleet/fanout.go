package fleet

import (
	"context"
	"sync"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// Call is one node operation, as fanned out over the fleet.
type Call func(ctx context.Context, n Node) NodeResult

// StatusCall reads a node's engine state.
func StatusCall(ctx context.Context, n Node) NodeResult {
	status, err := n.Status(ctx)
	r := result(n.Name(), err)
	r.Status = status
	// A node that runs on an instance describes it; the status reply alone
	// carries none of that, and asking costs nothing once the call is made.
	if i, ok := n.(InstanceReporter); ok {
		r.Instance = i.Instance()
	}
	return r
}

// MetricsCall reads a node's engine and system metrics, and what its kind can
// say about the instance it runs on. The latter costs nothing: a node that
// answers it does so from the reading just taken.
func MetricsCall(ctx context.Context, n Node) NodeResult {
	stats, err := n.Metrics(ctx)
	r := result(n.Name(), err)
	r.Metrics = stats
	if i, ok := n.(InstanceReporter); ok {
		r.Instance = i.Instance()
	}
	return r
}

// PricedMetricsCall is MetricsCall plus what the node has cost, for a caller
// that asked. A node that cannot be priced is read exactly as MetricsCall
// reads it, and a price that cannot be fetched leaves the cost unreported:
// asking a fleet what it costs is not a command a fleet of unpriceable nodes
// should refuse.
func PricedMetricsCall(ctx context.Context, n Node) NodeResult {
	r := MetricsCall(ctx, n)
	if !r.OK() {
		return r
	}
	if c, ok := n.(Coster); ok {
		if cost, err := c.Cost(ctx); err == nil {
			r.Cost = cost
		}
	}
	return r
}

// LogsCall reads a slice of a node's engine log. offsets says where to resume
// each node from, by node name; a node absent from it is read from the tail.
// The cursor is per node because each node's log is its own file with its own
// position — there is no fleet-wide position to hold.
func LogsCall(offsets map[string]int64, limit int) Call {
	return func(ctx context.Context, n Node) NodeResult {
		offset, ok := offsets[n.Name()]
		if !ok {
			offset = daemon.TailLog
		}
		logs, err := n.Logs(ctx, offset, limit)
		r := result(n.Name(), err)
		r.Logs = logs
		return r
	}
}

// QueriedLogsCall reads each node's log narrowed by what the caller asked for,
// where the node's kind can narrow it. A node whose log is a byte offset into
// one file cannot, and is read exactly as LogsCall reads it — so a query
// against a fleet of such nodes returns their output rather than nothing.
//
// limit doubles as the byte budget for a node read by offset and the event cap
// for a node read by query, because it means the same thing to a caller either
// way: how much to bring back.
func QueriedLogsCall(q LogQuery, limit int) Call {
	return func(ctx context.Context, n Node) NodeResult {
		if s, ok := n.(SourceLogger); ok {
			query := q
			query.Limit = limit
			logs, err := s.LogsMatching(ctx, query)
			r := result(n.Name(), err)
			r.Logs = logs
			return r
		}
		logs, err := n.Logs(ctx, daemon.TailLog, limit)
		r := result(n.Name(), err)
		r.Logs = logs
		return r
	}
}

// fanOutEach runs one result producer per position concurrently and returns one
// result per position, in the order given, so the rendering is stable between
// refreshes. It never returns an error: a producer that fails is a typed
// NodeResult, so one bad position is a row rather than a blanked view.
//
// Each result is stamped with the time its own call returned, not the time the
// round did: the calls run concurrently and take differing times, so a round's
// results are readings of different moments, and a display that orders them
// needs each one's own.
func fanOutEach(producers []func() NodeResult) []NodeResult {
	results := make([]NodeResult, len(producers))
	var wg sync.WaitGroup
	for i, produce := range producers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := produce()
			r.At = time.Now()
			results[i] = r
		}(i)
	}
	wg.Wait()
	return results
}

// FanOut runs call against every node in the fleet file concurrently and returns
// one result per node, in fleet-file order. A node that cannot be built (an
// unresolved token) or cannot be reached is a typed NodeResult rather than an
// error: one bad node is a row, not a blanked view. Only a problem with the fleet
// file itself — resolved before this — fails a command.
func (c *Config) FanOut(ctx context.Context, call Call) []NodeResult {
	producers := make([]func() NodeResult, len(c.Nodes))
	for i, entry := range c.Nodes {
		producers[i] = func() NodeResult {
			node, err := c.NewNode(entry)
			if err != nil {
				// The node could not even be built — an unresolved token
				// reference. Say so against this node, not as an auth failure.
				return NodeResult{Name: entry.Name, Outcome: OutcomeConfigError, Err: err}
			}
			return call(ctx, node)
		}
	}
	return fanOutEach(producers)
}

// FanOutNodes runs call over an explicit set of nodes concurrently and returns
// one result per node, in the order the set is given. It is the seam that lets an
// observable be driven regardless of where its nodes come from — a fleet file's
// daemon nodes, a cloud environment, or a mix — through the one fan-out the rest
// of the client already shares.
//
// As with Config.FanOut it never returns an error: a node that cannot be reached
// is a typed NodeResult, so one bad node is a row rather than a blanked view.
func FanOutNodes(ctx context.Context, call Call, nodes []Node) []NodeResult {
	producers := make([]func() NodeResult, len(nodes))
	for i, node := range nodes {
		producers[i] = func() NodeResult { return call(ctx, node) }
	}
	return fanOutEach(producers)
}
