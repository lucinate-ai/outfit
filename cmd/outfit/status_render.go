// Shared status rendering. Both `outfit remote status` (one cloud endpoint) and
// `outfit fleet status` (a node per machine) report the same facts about an
// outfit-driven inference endpoint: its state, what it is serving, how long since
// it last did work, and its outfit version. Those facts live here, computed once,
// so the two commands cannot word or compute them differently. Each command layers
// its own layout and any facts the other does not carry on top: the remote keeps
// its key-value block and its endpoint's address and health, the fleet keeps its
// one-node-per-row table.

package main

import "fmt"

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
}

// servingText is the "what it serves" text: runner and model, then the uptime and
// the since-last-work and version, in the order and wording both commands use.
// The fleet renders it as its table cell; the remote reads the same pieces for
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
	if f.UptimeSeconds > 0 {
		serving += fmt.Sprintf("  (up %s)", formatDuration(f.UptimeSeconds))
	}
	// How long since it last did work — deliberately not labelled "idle": that
	// word is already an engine state meaning nothing has been started. Shown
	// only when there is a recorded last-active time; without one there is
	// nothing to measure from.
	if f.LastActiveAt != "" {
		serving += fmt.Sprintf("  (last active %s ago)", formatDuration(f.IdleSeconds))
	}
	if f.Version != "" {
		serving += fmt.Sprintf("  (%s)", f.Version)
	}
	return serving
}
