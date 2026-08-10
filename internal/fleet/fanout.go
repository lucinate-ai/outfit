package fleet

import (
	"context"
	"sync"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// Call is one node operation, as fanned out over the fleet.
type Call func(ctx context.Context, n Node) NodeResult

// StatusCall reads a node's engine state.
func StatusCall(ctx context.Context, n Node) NodeResult {
	status, err := n.Status(ctx)
	r := result(n.Name(), err)
	r.Status = status
	return r
}

// MetricsCall reads a node's engine and system metrics.
func MetricsCall(ctx context.Context, n Node) NodeResult {
	stats, err := n.Metrics(ctx)
	r := result(n.Name(), err)
	r.Metrics = stats
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

// FanOut runs call against every node concurrently and returns one result per
// node, in fleet-file order so the rendering is stable between refreshes.
//
// It never returns an error: a node that cannot be built (an unresolved token)
// or cannot be reached is a typed NodeResult, so one bad node is a row rather
// than a blanked view. Only a problem with the fleet file itself — which is
// resolved before this — fails a command.
func (c *Config) FanOut(ctx context.Context, call Call) []NodeResult {
	results := make([]NodeResult, len(c.Nodes))
	var wg sync.WaitGroup
	for i, entry := range c.Nodes {
		wg.Add(1)
		go func(i int, entry NodeConfig) {
			defer wg.Done()
			node, err := c.NewNode(entry)
			if err != nil {
				// The node could not even be built — an unresolved token
				// reference. Say so against this node, not as an auth failure.
				results[i] = NodeResult{
					Name:    entry.Name,
					Outcome: OutcomeConfigError,
					Err:     err,
				}
				return
			}
			results[i] = call(ctx, node)
		}(i, entry)
	}
	wg.Wait()
	return results
}
