// Shared status rendering. Both `spinloop cloud status` (one cloud endpoint) and
// `spinloop fleet status` (a node per machine) report the same facts about an
// spinloop-driven inference endpoint: its state, what it is serving, how long since
// it last did work, and its spinloop version. Those facts live here, computed once,
// so the two commands cannot word or compute them differently. Each command layers
// its own layout and any facts the other does not carry on top: the cloud keeps
// its key-value block and its endpoint's address and health, the fleet keeps its
// one-node-per-row table.

package main

import (
	"fmt"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// statusFact is the shared status view: the facts both status commands have in
// common. A command fills it from its own native reply and reads the shared text
// from it, rather than each assembling the same facts by hand.
type statusFact struct {
	State         string
	Model         string
	Runner        string
	Version       string
	UptimeSeconds int
	LastActiveAt  string
	IdleSeconds   int
	// Ready is the daemon's readiness reading, daemon.ReadyYes or
	// daemon.ReadyNo, empty when none applies. Only ReadyNo is rendered: a
	// running engine that has answered is the ordinary case and needs no mark,
	// and an absent reading is not evidence of anything to report.
	Ready string
	// Endpoint is where this node's engine answers, for a node whose address
	// the control plane publishes. Empty for a daemon, whose address a client
	// composes from the host it already has.
	Endpoint string
	// RetainUntil is the instance's retention deadline while it has one, RFC
	// 3339. Empty for a node that is not retained, and for every node that is
	// a machine rather than an instance.
	RetainUntil string
}

// servingText is the "what it serves" text: runner and model, then the uptime and
// the since-last-work and version, in the order and wording both commands use.
// The fleet renders it as its table cell; the cloud reads the same pieces for
// its lines.
func (f statusFact) servingText() string {
	var serving string
	if f.Model != "" {
		serving = f.Model
	}
	if f.Runner != "" {
		if serving == "" {
			serving = f.Runner
		} else {
			serving = f.Runner + "  " + serving
		}
	}
	// A node whose engine process is up but has not answered its health check
	// is not servable yet, however long its uptime says it has been running.
	// Shown next to the model, before the timings, because it changes what the
	// rest of the row means.
	if f.Ready == daemon.ReadyNo {
		serving += "  (not ready)"
	}
	if f.UptimeSeconds > 0 {
		serving += fmt.Sprintf("  (up %s)", formatDuration(f.UptimeSeconds))
	}
	// How long since it last did work — deliberately not labelled "idle": that
	// word is already an engine state meaning nothing has been started. Shown
	// only when there is a recorded active time; without one there is nothing
	// to measure from.
	if f.LastActiveAt != "" {
		serving += fmt.Sprintf("  (active %s ago)", formatDuration(f.IdleSeconds))
	}
	if f.Version != "" {
		serving += fmt.Sprintf("  (%s)", f.Version)
	}
	// How long an instance is kept, where it is kept at all — the same figure
	// and wording the metrics views draw, so one fact reads one way.
	if keep := keepText(f.RetainUntil, metricsNow()); keep != "" {
		serving += "  (" + keep + ")"
	}
	// Where its engine answers, last because it is the longest and the least
	// often read: a node's address matters when you are about to use it, not
	// when you are scanning states.
	if f.Endpoint != "" {
		serving += "  " + f.Endpoint
	}
	return serving
}
