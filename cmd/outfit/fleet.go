// The `fleet` command group: one outfit observing every engine you run.
// It reads a fleet.yaml naming the machines, fans out over their daemon
// control APIs, and renders the cluster. Observation is fleet-wide; starting
// and stopping an engine is deliberately one node at a time.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lucinate-ai/outfit/internal/fleet"
)

// cmdFleet dispatches the fleet subcommands.
func cmdFleet(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit fleet <status|metrics|logs|route|start|stop> [node] [--fleet <path>]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return cmdFleetStatus(rest)
	case "metrics":
		return cmdFleetMetrics(rest)
	case "logs":
		return cmdFleetLogs(rest)
	case "route":
		return cmdFleetRoute(rest)
	case "start":
		return cmdFleetStart(rest)
	case "stop":
		return cmdFleetStop(rest)
	default:
		return fmt.Errorf(
			"unknown fleet subcommand %q (expected status, metrics, logs, route, start or stop)", sub)
	}
}

// fleetFlags registers the flags every fleet subcommand shares and returns the
// flag set plus the --fleet target.
func fleetFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("fleet "+name, flag.ContinueOnError)
	path := fs.String("fleet", "", "path to the fleet file (default ./fleet.yaml)")
	return fs, path
}

// cmdFleetStatus reports every node's engine state, one row per node. A node
// that cannot be reached is a row, not a failure: the rest of the fleet still
// renders and the command still succeeds.
func cmdFleetStatus(args []string) error {
	fs, path := fleetFlags("status")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := fleet.Resolve(*path)
	if err != nil {
		return err
	}
	results := cfg.FanOut(context.Background(), fleet.StatusCall)
	renderFleetStatus(os.Stdout, results)
	return nil
}

// renderFleetStatus writes the status table: node, state, what it serves, and
// the reason when a node did not answer.
func renderFleetStatus(w io.Writer, results []fleet.NodeResult) {
	nameWidth := len("NODE")
	for _, r := range results {
		if len(r.Name) > nameWidth {
			nameWidth = len(r.Name)
		}
	}
	fmt.Fprintf(w, "%-*s  %-12s  %s\n", nameWidth, "NODE", "STATE", "SERVING")
	for _, r := range results {
		state, serving := fleetRow(r)
		fmt.Fprintf(w, "%-*s  %-12s  %s\n", nameWidth, r.Name, state, serving)
	}
}

// fleetRow renders one result's state and detail columns. A failed node shows
// its outcome as the state and the reason where the model would be, so the
// table stays one line per node however the node is doing.
func fleetRow(r fleet.NodeResult) (state, serving string) {
	if !r.OK() {
		return string(r.Outcome), r.Detail()
	}
	serving = r.Status.Model
	if r.Status.Runner != "" {
		if serving == "" {
			serving = r.Status.Runner
		} else {
			serving = r.Status.Runner + "  " + serving
		}
	}
	if r.Status.UptimeSeconds > 0 {
		serving += fmt.Sprintf("  (up %s)", formatDuration(r.Status.UptimeSeconds))
	}
	// How long since the node last did work — the question a fleet view exists
	// to answer. Deliberately not labelled "idle": the STATE column already
	// uses that word for "nothing started", and one table should not carry two
	// meanings of it. Shown only when the daemon has recorded activity —
	// without a last-active time there is nothing to measure from, and
	// reporting a node quiet since boot would invent data it does not have.
	if r.Status.LastActiveAt != "" {
		serving += fmt.Sprintf("  (last active %s ago)", formatDuration(r.Status.IdleSeconds))
	}
	return r.Status.State, serving
}

// cmdFleetMetrics renders every node's engine and system metrics. --watch
// redraws the whole fleet on an interval.
func cmdFleetMetrics(args []string) error {
	fs, path := fleetFlags("metrics")
	var (
		format string
		watch  bool
	)
	fs.StringVar(&format, "format", "bar", "output format: bar (default), table or json")
	fs.BoolVar(&watch, "watch", false, "redraw the fleet every 60 seconds")
	fs.BoolVar(&watch, "w", false, "shorthand for --watch")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	if format != "bar" && format != "table" && format != "json" {
		return fmt.Errorf("--format must be \"bar\", \"table\", or \"json\", got %q", format)
	}
	cfg, err := fleet.Resolve(*path)
	if err != nil {
		return err
	}
	if watch {
		return runFleetMetricsWatch(cfg, format)
	}
	results := cfg.FanOut(context.Background(), fleet.MetricsCall)
	return renderFleetMetrics(os.Stdout, results, format)
}

// runFleetMetricsWatch redraws the fleet until interrupted. Each refresh is
// rendered into a buffer first, so the screen is cleared and rewritten in one
// go — a slow node delays a refresh but never tears the display.
func runFleetMetricsWatch(cfg *fleet.Config, format string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	first := true
	for {
		var buf strings.Builder
		results := cfg.FanOut(ctx, fleet.MetricsCall)
		if ctx.Err() != nil {
			return nil
		}
		if err := renderFleetMetrics(&buf, results, format); err != nil {
			return err
		}
		if !first {
			fmt.Fprint(os.Stdout, "\033[2J\033[H")
		}
		first = false
		fmt.Fprint(os.Stdout, buf.String())

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(metricsWatchInterval):
		}
	}
}

// renderFleetMetrics writes every node's metrics in the chosen format.
// Unreachable nodes are reported rather than omitted, so the view always
// accounts for the whole fleet.
func renderFleetMetrics(w io.Writer, results []fleet.NodeResult, format string) error {
	if format == "json" {
		return renderFleetMetricsJSON(w, results)
	}
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if !r.OK() {
			fmt.Fprintf(w, "%s  %s: %s\n", r.Name, r.Outcome, r.Detail())
			continue
		}
		stats := r.Metrics
		fmt.Fprintf(w, "%s  %s", r.Name, stats.State)
		if stats.ModelID != "" {
			fmt.Fprintf(w, "  %s", stats.ModelID)
		}
		fmt.Fprintln(w)
		// Before the continue, for the same reason the remote formats show it
		// before theirs: a node whose engine has stopped still has a useful
		// answer to "when did this last do anything?".
		renderLastActiveIndented(w, stats.LastActiveAt, stats.IdleSeconds)
		if stats.State != "running" {
			continue
		}
		if format == "bar" {
			renderStatBars(w, stats.CPU, stats.Memory, stats.GPUs)
			renderTokenLines(w, stats.Tokens)
		} else {
			renderTokenLines(w, stats.Tokens)
			renderGPUTable(w, stats.GPUs)
			renderCPUMemTable(w, stats.CPU, stats.Memory)
		}
		renderCollectionErrors(os.Stderr, stats.Errors)
	}
	return nil
}

// fleetNodeJSON is one node in the JSON output: its metrics when it answered,
// its outcome and reason when it did not — so a consumer sees the whole fleet
// rather than silently missing the nodes that were down.
type fleetNodeJSON struct {
	Node    string `json:"node"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	Metrics *any   `json:"metrics,omitempty"`
}

func renderFleetMetricsJSON(w io.Writer, results []fleet.NodeResult) error {
	out := make([]fleetNodeJSON, 0, len(results))
	for _, r := range results {
		entry := fleetNodeJSON{Node: r.Name, Outcome: string(r.Outcome), Error: r.Detail()}
		if r.OK() {
			var m any = r.Metrics
			entry.Metrics = &m
		}
		out = append(out, entry)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// cmdFleetStart starts one named node's engine.
func cmdFleetStart(args []string) error {
	return driveOneNode("start", args, func(ctx context.Context, n fleet.Node) fleet.NodeResult {
		status, err := n.Start(ctx)
		return fleet.Result(n.Name(), err, status)
	})
}

// cmdFleetStop stops one named node's engine.
func cmdFleetStop(args []string) error {
	return driveOneNode("stop", args, func(ctx context.Context, n fleet.Node) fleet.NodeResult {
		status, err := n.Stop(ctx)
		return fleet.Result(n.Name(), err, status)
	})
}

// driveOneNode runs a mutating call against exactly one node. Fan-out is for
// observation: starting or stopping every engine at once is a footgun, so
// these demand a node name and otherwise list the fleet without touching
// anything.
func driveOneNode(verb string, args []string, call fleet.Call) error {
	fs, path := fleetFlags(verb)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := fleet.Resolve(*path)
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf(
			"outfit fleet %s needs a node: %s\n(%s acts on one node at a time, never the whole fleet)",
			verb, strings.Join(cfg.Names(), ", "), verb)
	}
	name := rest[0]
	entry, ok := cfg.Node(name)
	if !ok {
		return fmt.Errorf("no node %q in %s (known nodes: %s)",
			name, cfg.Path, strings.Join(cfg.Names(), ", "))
	}
	node, err := cfg.NewNode(entry)
	if err != nil {
		return err
	}
	r := call(context.Background(), node)
	if !r.OK() {
		return fmt.Errorf("%s %s: %s", verb, name, r.Detail())
	}
	fmt.Printf("%s  %s\n", name, r.Status.State)
	return nil
}

// cmdFleetRoute reports the node a harness launch would choose for an Outfit,
// and changes nothing: no config is pushed, no engine started, no harness
// config written. It is how a routing decision is checked before an agent
// depends on it, and how an unexpected choice is diagnosed after one.
func cmdFleetRoute(args []string) error {
	fs, path := fleetFlags("route")
	var node, prefer string
	fs.StringVar(&node, "node", "", "report this node rather than choosing one")
	fs.StringVar(&prefer, "prefer", "", "rank nodes by `idle` or `active` (overrides the fleet file)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}

	var outfitPath string
	if rest := fs.Args(); len(rest) > 0 {
		outfitPath = rest[0]
	}
	sel, resolvedPath, err := readOutfit("outfit fleet route <outfit>", outfitPath)
	if err != nil {
		return err
	}

	target, fromFlag := *path, true
	if target == "" {
		target, fromFlag = sel.Fleet, false
	}
	if target == "" {
		return fmt.Errorf(
			"%s names no FLEET: add one, or pass --fleet <path> to say which fleet to route through",
			resolvedPath)
	}
	if isEndpoint(target) {
		return fmt.Errorf(
			"FLEET %s names an endpoint, and gateway routing is not implemented yet: "+
				"name a fleet file to choose a node from", target)
	}
	cfg, err := fleet.Resolve(resolveFleetPath(target, fromFlag, resolvedPath))
	if err != nil {
		return err
	}
	preference, err := cfg.Preference(prefer)
	if err != nil {
		return err
	}
	want := fleet.Want{Model: sel.Model, Alias: sel.Alias, Node: node, Prefer: preference}

	fmt.Printf("Outfit: %s\nFleet:  %s\nPrefer: %s\n\n", resolvedPath, cfg.Path, preference)
	if sel.BaseURL != "" {
		fmt.Printf("This Outfit pins BASEURL %s, so a launch would not route at all.\n", sel.BaseURL)
		return nil
	}

	choice, err := cfg.Select(context.Background(), want)
	if err == nil {
		fmt.Printf("Would use %s at %s\n  %s\n", choice.Node.Name, choice.BaseURL, choice.Reason)
		return nil
	}

	// Nothing is serving it: say what a real launch would do next, and do
	// none of it.
	var none *fleet.ErrNoneServing
	if !errors.As(err, &none) {
		return err
	}
	fmt.Println(err)
	dc, dcErr := deployConfigWithoutContext(sel, resolvedPath)
	if dcErr != nil {
		fmt.Printf("\nA launch could not start one either: %v\n", dcErr)
		return nil
	}
	if wake, ok := cfg.WouldWake(none.Results, dc); ok {
		fmt.Printf("\nA launch would wake %s and wait for its engine. Nothing has been started.\n", wake.Name)
		return nil
	}
	fmt.Println("\nNo node could be woken for it either. Nothing has been started.")
	return nil
}
