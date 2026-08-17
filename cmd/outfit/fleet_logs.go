package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/spf13/cobra"
)

// fleetLogsInterval is how often a follow asks each node for more. A variable
// so tests need not wait on it.
var fleetLogsInterval = 3 * time.Second

// bytesPerLineGuess converts a line budget into a byte budget for the first
// read. The endpoint speaks bytes and the operator thinks in lines, so the
// client bridges the two: ask for generously more than the lines could need,
// then keep the last N lines of what came back. A log whose lines are longer
// than this simply yields fewer lines than asked for, which the daemon's own
// cap would impose anyway.
const bytesPerLineGuess = 512

// fleetLogsCmd prints the engine output of the fleet's nodes. Unlike start and
// stop it fans out by default: reading is safe, and "what did my engines say?"
// is a fleet-wide question. Naming a node narrows it to that one.
func fleetLogsCmd() *cobra.Command {
	var (
		path   string
		follow bool
		limit  int
		format string
	)
	const followUsage = "keep printing new output as it arrives"
	c := &cobra.Command{
		Use:           "logs",
		Short:         "tail the engines' logs",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runFleetLogs(path, follow, limit, format, args)
		},
	}
	fs := c.Flags()
	fs.StringVar(&path, "fleet", "", fleetFileUsage)
	fs.BoolVarP(&follow, "follow", "f", false, followUsage)
	fs.IntVar(&limit, "limit", 200, "lines of backlog to print per node")
	fs.StringVar(&format, "format", "text", "output format: text (default) or json")
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	return c
}

// runFleetLogs is the body of `outfit fleet logs`.
func runFleetLogs(path string, follow bool, limit int, format string, args []string) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("--format must be \"text\" or \"json\", got %q", format)
	}
	if limit <= 0 {
		return fmt.Errorf("--limit must be positive, got %d", limit)
	}

	cfg, err := fleet.Resolve(path)
	if err != nil {
		return err
	}
	// A named node restricts the read; the fleet file still supplies its
	// details, so an unknown name is caught here rather than at the socket.
	if len(args) > 0 {
		if cfg, err = cfg.Only(args[0]); err != nil {
			return err
		}
	}

	if follow {
		return followFleetLogs(cfg, limit, format)
	}
	return runFleetLogsOnce(context.Background(), cfg, limit, format, os.Stdout)
}

// runFleetLogsOnce reads each node's backlog and prints it.
func runFleetLogsOnce(ctx context.Context, cfg *fleet.Config, limit int, format string, w io.Writer) error {
	results := cfg.FanOut(ctx, fleet.LogsCall(nil, limit*bytesPerLineGuess))
	for i := range results {
		if results[i].OK() {
			results[i].Logs.Content = lastLines(results[i].Logs.Content, limit)
		}
	}
	if format == "json" {
		return writeFleetLogsJSON(w, results)
	}
	writeFleetLogsText(w, results, labelFleetLogs(results))
	return nil
}

// labelFleetLogs reports whether lines need a node prefix: only when more than
// one node actually produced output. Reading one node should read like that
// node's own log.
func labelFleetLogs(results []fleet.NodeResult) bool {
	speaking := 0
	for _, r := range results {
		if r.OK() && r.Logs.Content != "" {
			speaking++
		}
	}
	return speaking > 1
}

// writeFleetLogsText prints each node's output in its own order, then the
// nodes that had nothing to give. Output is deliberately not interleaved
// across nodes: engine output carries no timestamp the client can trust, so
// merging several machines' lines would invent a chronology.
func writeFleetLogsText(w io.Writer, results []fleet.NodeResult, label bool) {
	for _, r := range results {
		if !r.OK() || r.Logs.Content == "" {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(r.Logs.Content, "\n"), "\n") {
			if label {
				fmt.Fprintf(w, "%s  %s\n", r.Name, line)
				continue
			}
			fmt.Fprintln(w, line)
		}
	}
	for _, r := range results {
		if note := fleetLogsNote(r); note != "" {
			fmt.Fprintf(w, "%s  %s\n", r.Name, note)
		}
	}
}

// fleetLogsNote is what to say about a node that printed nothing — why it had
// nothing, or why it could not be asked. A node that answered with output has
// no note.
func fleetLogsNote(r fleet.NodeResult) string {
	if !r.OK() {
		if r.Outcome == fleet.OutcomeUnsupported {
			return "this daemon does not serve /v1/logs — upgrade outfit on this node"
		}
		return fmt.Sprintf("%s: %s", r.Outcome, r.Detail())
	}
	switch {
	case r.Logs.Missing:
		return "no engine log — nothing has run here yet"
	case r.Logs.Content == "":
		return "no output"
	}
	return ""
}

// fleetLogJSON is one node's entry in the machine-readable output.
type fleetLogJSON struct {
	Node       string `json:"node"`
	Outcome    string `json:"outcome"`
	Content    string `json:"content,omitempty"`
	NextOffset int64  `json:"nextOffset,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
	Error      string `json:"error,omitempty"`
}

func writeFleetLogsJSON(w io.Writer, results []fleet.NodeResult) error {
	out := make([]fleetLogJSON, 0, len(results))
	for _, r := range results {
		out = append(out, fleetLogJSON{
			Node:       r.Name,
			Outcome:    string(r.Outcome),
			Content:    r.Logs.Content,
			NextOffset: r.Logs.NextOffset,
			Size:       r.Logs.Size,
			Missing:    r.Logs.Missing,
			Error:      r.Detail(),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// lastLines keeps the final n lines of s, which is how a line-shaped --limit
// is honoured over a byte-shaped endpoint.
func lastLines(s string, n int) string {
	if s == "" || n <= 0 {
		return s
	}
	lines := strings.SplitAfter(s, "\n")
	// SplitAfter leaves a trailing "" when s ends in a newline.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "")
}

// followFleetLogs prints each node's backlog, then whatever those nodes append.
// Every node carries its own cursor: each log is its own file with its own
// position, so there is no fleet-wide offset to hold.
func followFleetLogs(cfg *fleet.Config, limit int, format string) error {
	return followUntilInterrupted(func(ctx context.Context) error {
		return followFleetLogsLoop(ctx, cfg, limit, format, os.Stdout)
	})
}

// followFleetLogsLoop is the polling itself, with the interrupt wiring left to
// its caller so it can be driven directly.
func followFleetLogsLoop(ctx context.Context, cfg *fleet.Config, limit int,
	format string, w io.Writer) error {
	offsets := map[string]int64{}
	// Labelling is decided across the session, not per poll: once two nodes
	// have spoken the prefix stays, so a quiet poll does not silently drop it.
	spoken := map[string]bool{}
	for first := true; ; first = false {
		budget := 0
		if first {
			budget = limit * bytesPerLineGuess
		}
		results := cfg.FanOut(ctx, fleet.LogsCall(offsets, budget))
		if ctx.Err() != nil {
			return nil
		}
		for i := range results {
			r := &results[i]
			if !r.OK() {
				continue
			}
			// A stale cursor means the log was truncated under us; resume from
			// wherever it now ends rather than waiting for a position that
			// will never arrive.
			offsets[r.Name] = r.Logs.NextOffset
			if first {
				r.Logs.Content = lastLines(r.Logs.Content, limit)
			}
			if r.Logs.Content != "" {
				spoken[r.Name] = true
			}
		}
		if format == "json" {
			if err := writeFleetLogsJSON(w, results); err != nil {
				return err
			}
		} else {
			// Only the first pass reports the quiet nodes; afterwards silence
			// is the normal state and repeating it every few seconds is noise.
			writeFleetLogsText(w, quietened(results, first), len(spoken) > 1)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(fleetLogsInterval):
		}
	}
}

// quietened blanks the per-node notes after the first pass, so a follow does
// not repeat "no output" for every idle node on every poll.
func quietened(results []fleet.NodeResult, first bool) []fleet.NodeResult {
	if first {
		return results
	}
	kept := make([]fleet.NodeResult, 0, len(results))
	for _, r := range results {
		if r.OK() && r.Logs.Content != "" {
			kept = append(kept, r)
		}
	}
	return kept
}
