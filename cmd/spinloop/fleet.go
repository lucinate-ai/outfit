// The `fleet` command group: one spinloop observing every engine you run.
// It reads a fleet.yaml naming the machines, fans out over their daemon
// control APIs, and renders the cluster. Observation is fleet-wide; starting,
// stopping and deploying take one or more named nodes, or --all — never the
// whole fleet by default.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/spinloop-ai/spinloop/internal/config"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/fleet"
)

// cmdFleet runs the fleet subcommands through the tree — the seam the suite
// calls directly.
func cmdFleet(args []string) error {
	return execCmd(fleetCmd(), args)
}

// fleetFileFlag is the --fleet flag's help, shared by every fleet subcommand.
const fleetFileUsage = "path to the fleet file (default ./fleet.yaml)"

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
	// The shared facts come from the same source the cloud status view reads,
	// so the two cannot word or compute them differently.
	// A node that runs on an instance reports its release outside the status
	// reply, so the instance's answer fills what the reply left empty rather
	// than overriding a daemon that carries its own.
	version := r.Status.Version
	if version == "" {
		version = r.Instance.Version
	}
	f := statusFact{
		State:         r.Status.State,
		Model:         r.Status.Model,
		Runner:        r.Status.Runner,
		Version:       version,
		UptimeSeconds: r.Status.UptimeSeconds,
		LastActiveAt:  r.Status.LastActiveAt,
		IdleSeconds:   r.Status.IdleSeconds,
		Ready:         r.Status.Ready,
		Endpoint:      r.Instance.BaseURL,
		RetainUntil:   r.Instance.RetainUntil,
	}
	return f.State, f.servingText()
}

// runFleetMetricsWatch redraws the fleet until interrupted. Each refresh is
// rendered into a buffer first, so the screen is cleared and rewritten in one
// go — a slow node delays a refresh but never tears the display.
func runFleetMetricsWatch(cfg *fleet.Config, format string, call fleet.Call) error {
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
		results := cfg.FanOut(ctx, call)
		if ctx.Err() != nil {
			return nil
		}
		// A single-node target (an --env watch, or a fleet of one) with
		// nothing else to show for the poll ends the watch rather than
		// redrawing the same failure forever: this is the one node the
		// caller asked about, and it could not be read. A multi-node fleet
		// keeps drawing through a bad node so the rest of the fleet stays
		// visible.
		if len(results) == 1 && !results[0].OK() {
			return fmt.Errorf("%s: %s", results[0].Name, results[0].Detail())
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
	now := metricsNow()
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if !r.OK() {
			fmt.Fprintf(w, "%s  %s: %s\n", r.Name, r.Outcome, r.Detail())
			continue
		}
		stats := r.Metrics
		renderMetricsHeader(w, r, format)
		// Before the continue, for the same reason the cloud formats show it
		// before theirs: a node whose engine has stopped still has a useful
		// answer to "when did it last do anything?" — and, for a retained
		// cloud environment, "how long is it kept?".
		// The table spells its facts as key-value lines, so its active line is
		// spelled that way too; the compact formats indent theirs under the
		// header.
		if format == "table" {
			renderActiveKeyValue(w, stats.LastActiveAt, stats.IdleSeconds, retainUntilOf(r), now)
		} else {
			renderActiveIndented(w, stats.LastActiveAt, stats.IdleSeconds, retainUntilOf(r), now)
		}
		switch format {
		case "bar":
			// No state gate, for the same reason the cloud bar format has
			// none: a stopped node's retained history says what its engine
			// was doing until it stopped, and a stopped node's current
			// reading carries no figures for it to fall back on.
			renderStatBars(w, stats.CPU, stats.Memory, stats.GPUs, stats.History, barLineW)
			renderTokenLines(w, stats.Tokens)
		case "gauge":
			if stats.State != "running" {
				continue
			}
			renderStatGauges(w, stats.CPU, stats.Memory, stats.GPUs)
			renderTokenLines(w, stats.Tokens)
		default:
			if stats.State != "running" {
				continue
			}
			renderTokenLines(w, stats.Tokens)
			renderGPUTable(w, stats.GPUs)
			renderCPUMemTable(w, stats.CPU, stats.Memory)
		}
		renderCost(w, r.Cost)
		renderCollectionErrors(os.Stderr, stats.Errors)
	}
	return nil
}

// renderMetricsHeader draws the lines a node's figures open with. The table
// format spells each fact on its own line, as the environment-only format did;
// the compact formats put them on one line, as its bar and gauge did. Between
// them they carry every fact those formats carried: what the node is, what it
// runs on, what it serves, the release on it, and how long it has been up.
func renderMetricsHeader(w io.Writer, r fleet.NodeResult, format string) {
	stats := r.Metrics
	if format == "table" {
		fmt.Fprintf(w, "node:         %s\n", r.Name)
		fmt.Fprintf(w, "state:        %s\n", stats.State)
		for _, line := range []struct{ label, value string }{
			{"instance", r.Instance.ID},
			{"instanceType", r.Instance.Type},
			{"runner", stats.Runner},
			{"model", stats.ModelID},
			{"version", r.Instance.Version},
			{"endpoint", r.Instance.BaseURL},
		} {
			if line.value != "" {
				fmt.Fprintf(w, "%-13s %s\n", line.label+":", line.value)
			}
		}
		if stats.UptimeSeconds > 0 {
			fmt.Fprintf(w, "%-13s %s\n", "uptime:", formatDuration(stats.UptimeSeconds))
		}
		return
	}
	fmt.Fprintf(w, "%s  %s", r.Name, stats.State)
	for _, v := range []string{r.Instance.Type, stats.ModelID, r.Instance.Version} {
		if v != "" {
			fmt.Fprintf(w, "  %s", v)
		}
	}
	if stats.UptimeSeconds > 0 {
		fmt.Fprintf(w, "  (up %s)", formatDuration(stats.UptimeSeconds))
	}
	fmt.Fprintln(w)
}

// retainUntilOf is a node's retention deadline from whichever answer carried
// it: the shared stats where a reading has been taken, the instance the node
// describes otherwise. One fact, two replies, so a caller never has to pick.
func retainUntilOf(r fleet.NodeResult) string {
	if r.Metrics.RetainUntil != "" {
		return r.Metrics.RetainUntil
	}
	return r.Instance.RetainUntil
}

// renderCost draws what a node has cost, where the caller asked and the node
// could be priced. A node with no figure draws nothing: a zero would claim it
// cost nothing.
func renderCost(w io.Writer, c fleet.Cost) {
	if !c.Reported() {
		return
	}
	fmt.Fprintf(w, "  cost so far:  $%.2f (%.4f/hr)\n", c.SoFar, c.PerHour)
}

// fleetNodeJSON is one node in the JSON output: its metrics when it answered,
// its outcome and reason when it did not — so a consumer sees the whole fleet
// rather than silently missing the nodes that were down.
type fleetNodeJSON struct {
	Node    string `json:"node"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	Metrics *any   `json:"metrics,omitempty"`
	// The instance facts and the cost are what the node's kind could say
	// beyond the shared engine figures. Omitted for a node that said none,
	// which is every node that is a machine rather than an instance.
	Instance     string   `json:"instance,omitempty"`
	InstanceType string   `json:"instanceType,omitempty"`
	Version      string   `json:"version,omitempty"`
	BaseURL      string   `json:"baseUrl,omitempty"`
	RetainUntil  string   `json:"retainUntil,omitempty"`
	Cost         *float64 `json:"cost,omitempty"`
	CostPerHour  *float64 `json:"costPerHour,omitempty"`
}

func renderFleetMetricsJSON(w io.Writer, results []fleet.NodeResult) error {
	out := make([]fleetNodeJSON, 0, len(results))
	for _, r := range results {
		entry := fleetNodeJSON{
			Node: r.Name, Outcome: string(r.Outcome), Error: r.Detail(),
			Instance: r.Instance.ID, InstanceType: r.Instance.Type,
			Version: r.Instance.Version, BaseURL: r.Instance.BaseURL,
			RetainUntil: r.Instance.RetainUntil,
		}
		if r.OK() {
			var m any = r.Metrics
			entry.Metrics = &m
		}
		if r.Cost.Reported() {
			soFar, perHour := r.Cost.SoFar, r.Cost.PerHour
			entry.Cost, entry.CostPerHour = &soFar, &perHour
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

// fleetStartCmd starts one or more nodes' engines, or every node with --all.
// A kind: daemon node whose Spinloop source resolves is started with the
// deploy config that source derives (StartWith) — telling the daemon what to
// run, exactly as a routed wake already does for the Spinloop being
// launched. A kind: daemon node with no resolvable source, and a kind:
// cloud node regardless, get a plain start: a cloud environment's
// StartWith always refuses a config, since what it serves is fixed at
// deploy time.
func fleetStartCmd() *cobra.Command {
	var (
		path    string
		envName string
		all     bool
	)
	c := &cobra.Command{
		Use:           "start",
		Short:         "start one or more nodes' engines",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			cfg, err := resolveFleetTarget(fleetTarget{envName: envName, fleetPath: path})
			if err != nil {
				return err
			}
			return runFleetDrive("start", cfg, all, args, fleetStartCall(cfg))
		},
	}
	fs := c.Flags()
	fs.StringVarP(&path, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	fs.BoolVar(&all, "all", false, "start every node in the fleet")
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// fleetStopCmd stops one or more nodes' engines, or every node with --all.
// Stopping takes no config, so unlike start it has nothing to resolve — only
// its target selection is shared with start.
func fleetStopCmd() *cobra.Command {
	var (
		path    string
		envName string
		all     bool
	)
	c := &cobra.Command{
		Use:           "stop",
		Short:         "stop one or more nodes' engines",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			cfg, err := resolveFleetTarget(fleetTarget{envName: envName, fleetPath: path})
			if err != nil {
				return err
			}
			call := func(ctx context.Context, n fleet.Node) fleet.NodeResult {
				status, err := n.Stop(ctx)
				return fleet.Result(n.Name(), err, status)
			}
			return runFleetDrive("stop", cfg, all, args, call)
		},
	}
	fs := c.Flags()
	fs.StringVarP(&path, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	fs.BoolVar(&all, "all", false, "stop every node in the fleet")
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// fleetStartCall builds fleet start's per-node call, closing over cfg so it
// can recover each targeted node's NodeConfig (kind, File) from the bare
// Node fleet.Call is handed — Call's signature carries no NodeConfig, but
// every node it is called with came from cfg in the first place, so the
// lookup by name always succeeds.
func fleetStartCall(cfg *fleet.Config) fleet.Call {
	return func(ctx context.Context, n fleet.Node) fleet.NodeResult {
		entry, _ := cfg.Node(n.Name())
		if entry.Kind != fleet.KindDaemon {
			status, err := n.Start(ctx)
			return fleet.Result(n.Name(), err, status)
		}
		arg, source, err := resolveNodeSpinloop(entry, cfg.Dir)
		if err != nil {
			return fleet.Result(n.Name(), err, daemon.StatusResponse{})
		}
		sel, spinloopPath, err := readSpinloop(fmt.Sprintf("spinloop fleet start %s", n.Name()), arg)
		if err != nil {
			return fleet.Result(n.Name(), err, daemon.StatusResponse{})
		}
		if err := applySpinloopEnv(sel, spinloopPath); err != nil {
			return fleet.Result(n.Name(), err, daemon.StatusResponse{})
		}
		dc, err := deployConfigForNode(sel, spinloopPath)
		if err != nil {
			return fleet.Result(n.Name(), err, daemon.StatusResponse{})
		}
		engineKey, err := cfg.EngineToken(entry)
		if err != nil {
			return fleet.Result(n.Name(), err, daemon.StatusResponse{})
		}
		fmt.Printf("%s: using %s (%s)\n", n.Name(), spinloopPath, source)
		status, err := n.StartWith(ctx, &dc, engineKey)
		return fleet.Result(n.Name(), err, status)
	}
}

// runFleetDrive selects the nodes a mutating fleet command targets and fans
// call out over them: no names and no --all fails, listing the fleet's
// nodes; --all and names together fails as ambiguous; named nodes fail
// before anything is touched if any name is unknown. Replaces the old
// driveOneNode now that start and stop both take several nodes or --all,
// not just one.
func runFleetDrive(verb string, cfg *fleet.Config, all bool, names []string, call fleet.Call) error {
	if all && len(names) > 0 {
		return fmt.Errorf("spinloop fleet %s: --all is ambiguous with node names", verb)
	}
	var target *fleet.Config
	if all {
		target = cfg
	} else {
		if len(names) == 0 {
			return fmt.Errorf(
				"spinloop fleet %s needs a node, or --all: %s",
				verb, strings.Join(cfg.Names(), ", "))
		}
		narrowed, err := cfg.OnlyNames(names)
		if err != nil {
			return err
		}
		target = narrowed
	}
	results := target.FanOut(context.Background(), call)
	var bad []string
	for _, r := range results {
		if !r.OK() {
			bad = append(bad, r.Name)
			fmt.Printf("%s  %s: %s\n", r.Name, r.Outcome, r.Detail())
			continue
		}
		fmt.Printf("%s  %s\n", r.Name, r.Status.State)
	}
	if len(bad) > 0 {
		return fmt.Errorf("%s: failed: %s", verb, strings.Join(bad, ", "))
	}
	return nil
}

// fleetDeployCmd creates the AWS environment for one or more kind: cloud
// nodes, or every kind: cloud node with --all, deriving what each serves
// from its resolved Spinloop source — the same derivation and registration
// a standalone `spinloop cloud deploy` performs for one file, so the two
// can never disagree about what a given Spinloop deploys.
func fleetDeployCmd() *cobra.Command {
	var (
		path            string
		envName         string
		all             bool
		dryRun          bool
		overwrite       bool
		reseed          bool
		allowedCidr     string
		region          string
		spinloopVersion string
		apiKeyEnv       string
	)
	c := &cobra.Command{
		Use:   "deploy",
		Short: "create the AWS environment for one or more cloud nodes",
		Long: `deploys the AWS environment for each named kind: cloud node, or
every kind: cloud node with --all, deriving what to serve from each node's
own Spinloop source: its file field, or its name resolved as a registered
alias or a same-named subdirectory beside the fleet file. Reuses the same
derivation, consent, and registration behaviour as "spinloop cloud deploy".`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runFleetDeploy(fleetTarget{envName: envName, fleetPath: path}, all, args, deployOpts{
				dryRun:          dryRun,
				overwrite:       overwrite,
				reseed:          reseed,
				allowedCidr:     allowedCidr,
				region:          region,
				spinloopVersion: spinloopVersion,
				apiKeyEnv:       apiKeyEnv,
			})
		},
	}
	fs := c.Flags()
	fs.StringVarP(&path, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	fs.BoolVar(&all, "all", false, "deploy every kind: cloud node in the fleet")
	fs.BoolVarP(&dryRun, "dry-run", "n", false, "print the config that would be deployed, without sending it")
	fs.BoolVar(&overwrite, "overwrite", false, "proceed against an already-registered or live environment")
	fs.BoolVar(&reseed, "reseed", false, "re-fetch the weights even if they are already in S3 (starts a ~20-minute seed)")
	fs.StringVar(&allowedCidr, "allowed-cidr", "", "who may reach each environment's instance (default: your public IP as a /32, on first deploy)")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	fs.StringVar(&spinloopVersion, "spinloop-version", "", "spinloop release each environment's instances install at boot (default: latest)")
	fs.StringVar(&apiKeyEnv, "api-key-env", "", "name the environment variable holding the engine key to create or rotate, applied to every targeted node; with no flag each environment keeps its stored key")
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// runFleetDeploy is the body of `spinloop fleet deploy`.
func runFleetDeploy(target fleetTarget, all bool, names []string, opts deployOpts) error {
	cfg, err := resolveFleetTarget(target)
	if err != nil {
		return err
	}
	if all && len(names) > 0 {
		return fmt.Errorf("spinloop fleet deploy: --all is ambiguous with node names")
	}

	cloudNames := make([]string, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		if n.Kind == fleet.KindCloud {
			cloudNames = append(cloudNames, n.Name)
		}
	}

	var targets []string
	switch {
	case all:
		targets = cloudNames
	case len(names) == 0:
		return fmt.Errorf(
			"spinloop fleet deploy needs a node, or --all: %s",
			strings.Join(cloudNames, ", "))
	default:
		for _, name := range names {
			entry, ok := cfg.Node(name)
			if !ok {
				return fmt.Errorf("no node %q in %s (known nodes: %s)",
					name, cfg.Path, strings.Join(cfg.Names(), ", "))
			}
			if entry.Kind != fleet.KindCloud {
				return fmt.Errorf(
					"node %q is kind %q: fleet deploy provisions cloud environments, and %[1]s is not one",
					name, entry.Kind)
			}
		}
		targets = names
	}

	results := make([]fleetDeployResult, len(targets))
	done := make([]bool, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup

	// A live spinner only makes sense where the previous frame can be
	// erased — skip it entirely for a piped or redirected run (a log file,
	// CI) rather than spamming it with escape codes.
	var stop, spinnerDone chan struct{}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		stop = make(chan struct{})
		spinnerDone = make(chan struct{})
		go renderDeploySpinner(targets, results, done, &mu, stop, spinnerDone)
	}

	for i, name := range targets {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			r := deployOneNode(cfg, name, opts)
			mu.Lock()
			results[i] = r
			done[i] = true
			mu.Unlock()
		}(i, name)
	}
	wg.Wait()
	if stop != nil {
		// Stop and wait for the spinner's own erase before printing the
		// report below it — otherwise the two interleave.
		close(stop)
		<-spinnerDone
	}

	var bad []string
	for i, r := range results {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(deployHeader(r.node, r.outcome))
		fmt.Print(r.text())
		if r.outcome != deployRowOK {
			bad = append(bad, r.node)
		}
	}
	fmt.Println()
	fmt.Println(deploySummary(len(targets), len(bad)))
	if len(bad) > 0 {
		return fmt.Errorf("fleet deploy: failed or guarded: %s", strings.Join(bad, ", "))
	}
	return nil
}

// renderDeploySpinner redraws one line per target in place — a spinner
// beside whichever nodes are still deploying, a coloured mark beside
// whichever have finished — until stop is closed, then erases its own
// lines and closes done. A `fleet deploy --all` can be several concurrent
// AWS calls running for minutes; this is what keeps it from looking hung.
func renderDeploySpinner(targets []string, results []fleetDeployResult, done []bool, mu *sync.Mutex, stop <-chan struct{}, closeWhenDone chan<- struct{}) {
	defer close(closeWhenDone)
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	drawn := 0
	redraw := func() {
		if drawn > 0 {
			fmt.Printf("\033[%dA\033[J", drawn)
		}
		mu.Lock()
		for i, name := range targets {
			if done[i] {
				fmt.Printf("%s %s\n", nodeGlyph(results[i].outcome), name)
			} else {
				fmt.Printf("%s%s%s %s deploying...\n", ansiGrey, string(spinnerFrames[frame%len(spinnerFrames)]), ansiReset, name)
			}
		}
		mu.Unlock()
		drawn = len(targets)
		frame++
	}
	for {
		select {
		case <-stop:
			if drawn > 0 {
				fmt.Printf("\033[%dA\033[J", drawn)
			}
			return
		case <-ticker.C:
			redraw()
		}
	}
}

// nodeGlyph is the coloured mark a finished node's spinner line, and its
// report header, both show for the same outcome.
func nodeGlyph(outcome deployRowOutcome) string {
	switch outcome {
	case deployRowOK:
		return ansiGreen + "✓" + ansiReset
	case deployRowGuarded:
		return ansiYellow + "⚠" + ansiReset
	default:
		return ansiRed + "✗" + ansiReset
	}
}

// deployHeader is the line that separates one node's report from the next —
// the gap `fleet deploy --all`'s output used to lack entirely.
func deployHeader(node string, outcome deployRowOutcome) string {
	return nodeGlyph(outcome) + " " + node
}

// deploySummary is the final line naming how many of the targeted nodes
// deployed clean, so a large --all run's result is legible at a glance
// without counting rows.
func deploySummary(total, bad int) string {
	ok := total - bad
	if bad == 0 {
		return fmt.Sprintf("%s%d/%d deployed%s", ansiGreen, ok, total, ansiReset)
	}
	return fmt.Sprintf("%s%d/%d deployed%s, %s%d failed or guarded%s", ansiGreen, ok, total, ansiReset, ansiRed, bad, ansiReset)
}

// deployRowOutcome is one node's fleet-deploy outcome — a row, not an abort:
// one node's guard or failure never stops the others.
type deployRowOutcome int

const (
	deployRowOK deployRowOutcome = iota
	deployRowGuarded
	deployRowFailed
)

// fleetDeployResult is one targeted node's fleet-deploy outcome.
type fleetDeployResult struct {
	node    string
	outcome deployRowOutcome
	detail  string // guard/failure message, or the deploy's plan/result text on success
}

// text renders one node's result: the deploy's own plan/result text on
// success, or a labelled one-liner on guard or failure.
func (r fleetDeployResult) text() string {
	switch r.outcome {
	case deployRowGuarded:
		return fmt.Sprintf("  guarded: %s\n", r.detail)
	case deployRowFailed:
		return fmt.Sprintf("  failed: %s\n", r.detail)
	default:
		return r.detail
	}
}

// deployOneNode resolves and deploys a single targeted node. The node's own
// name is the registered environment the deploy creates: a kind: cloud node
// is only driveable by the fleet commands under that name, so there is no
// override. It never returns an error itself — a bad node becomes a
// fleetDeployResult, so the caller's fan-out can label it without aborting
// the others.
func deployOneNode(cfg *fleet.Config, name string, opts deployOpts) fleetDeployResult {
	entry, _ := cfg.Node(name)
	arg, source, err := resolveNodeSpinloop(entry, cfg.Dir)
	if err != nil {
		return fleetDeployResult{node: name, outcome: deployRowFailed, detail: err.Error()}
	}
	_, spinloopPath, dc, err := deriveDeployTarget(fmt.Sprintf("spinloop fleet deploy %s", name), arg, name)
	if err != nil {
		return fleetDeployResult{node: name, outcome: deployRowFailed, detail: err.Error()}
	}
	// The node's own instance type is per-node. opts is shared across the
	// concurrent deploy, so one node's machine must not leak to another —
	// copy it for this call and set the node's type (a node naming none
	// leaves it empty, i.e. the control plane's default).
	nodeOpts := opts
	nodeOpts.instanceType = entry.InstanceType
	outcome, err := runDeploy(spinloopPath, name, dc, nodeOpts)
	if err != nil {
		var guarded *errDeployGuarded
		if errors.As(err, &guarded) {
			return fleetDeployResult{node: name, outcome: deployRowGuarded, detail: err.Error()}
		}
		return fleetDeployResult{node: name, outcome: deployRowFailed, detail: err.Error()}
	}
	text := fmt.Sprintf("using %s (%s)\n%s", spinloopPath, source, outcome.Text)
	return fleetDeployResult{node: name, outcome: deployRowOK, detail: text}
}

// resolveNodeSpinloop resolves the argument to hand readSpinloop for one
// node's declared Spinloop source, trying in order and stopping at the
// first that resolves: node.File (relative to fleetDir), node.Name as a
// registered `spinloop alias`, node.Name as a subdirectory beside the fleet
// file containing a Spinloop file. source labels which one supplied it, for
// reporting. Neither `fleet deploy` nor `fleet start` falls back to acting
// without one — a node for which none resolves is a per-node failure naming
// all three.
//
// The alias tier resolves to the alias's own target path directly, rather
// than handing node.Name to readSpinloop and letting it resolve the alias
// itself: readSpinloop's resolveAlias deliberately lets a same-named path on
// disk beat a registered alias (so an existing invocation never changes
// meaning just because an alias gets registered later) — the opposite of
// this function's own precedence, alias before subdirectory. A node named
// the same as its own subdirectory would otherwise silently resolve to the
// subdirectory even with an alias registered, contradicting the order this
// function documents.
func resolveNodeSpinloop(node fleet.NodeConfig, fleetDir string) (arg, source string, err error) {
	if node.File != "" {
		return filepath.Join(fleetDir, node.File), fmt.Sprintf("file %s", node.File), nil
	}
	cfgFile, err := config.Load()
	if err != nil {
		return "", "", err
	}
	if aliasPath, ok := cfgFile.Alias(node.Name); ok {
		return aliasPath, fmt.Sprintf("alias %q", node.Name), nil
	}
	subdir := filepath.Join(fleetDir, node.Name)
	if info, statErr := os.Stat(subdir); statErr == nil && info.IsDir() {
		return subdir, fmt.Sprintf("subdirectory %s", subdir), nil
	}
	return "", "", fmt.Errorf(
		"node %q names no Spinloop source: no `file` field, no `spinloop alias` named %q, and no %s subdirectory beside the fleet file",
		node.Name, node.Name, node.Name)
}

// fleetRouteCmd reports the node a harness launch would choose for a Spinloop,
// and changes nothing: no config is pushed, no engine started, no harness
// config written. It is how a routing decision is checked before an agent
// depends on it, and how an unexpected choice is diagnosed after one.
func fleetRouteCmd() *cobra.Command {
	var path, envName, node, prefer string
	c := &cobra.Command{
		Use:           "route",
		Short:         "report which node a launch would take",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runFleetRoute(path, envName, node, prefer, args)
		},
	}
	fs := c.Flags()
	fs.StringVarP(&path, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	fs.StringVar(&node, "node", "", "report this node rather than choosing one")
	fs.StringVar(&prefer, "prefer", "", "rank nodes by `idle` or `active` (overrides the fleet file)")
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// runFleetRoute is the body of `spinloop fleet route`.
func runFleetRoute(path, envName, node, prefer string, args []string) error {
	var spinloopPath string
	if len(args) > 0 {
		spinloopPath = args[0]
	}
	sel, resolvedPath, err := readSpinloop("spinloop fleet route <spinloop>", spinloopPath)
	if err != nil {
		return err
	}

	// The fleet file: --fleet/-f when given, otherwise the fleet.yaml in the
	// working directory when the Spinloop was not named explicitly — no
	// positional and no SPINLOOP_ALIAS, the default Spinloop standing in for
	// the user. A named Spinloop routes only by flag, as a launch does.
	target := path
	if envName == "" && target == "" && spinloopPath == "" && !spinloopAliasInForce() {
		if _, err := os.Stat(fleet.DefaultFile); err == nil {
			target = fleet.DefaultFile
		}
	}
	if envName == "" && target == "" {
		return fmt.Errorf(
			"no fleet to route %s through: pass --fleet <path> or --env <name> to say which fleet to route through",
			resolvedPath)
	}
	cfg, err := resolveFleetTarget(fleetTarget{envName: envName, fleetPath: target})
	if err != nil {
		return err
	}
	preference, err := cfg.Preference(prefer)
	if err != nil {
		return err
	}
	// As for a launch: the model id a wake would push is a third name a node
	// may report itself serving.
	dc, dcErr := deployConfigForNode(sel, resolvedPath)
	want := fleet.Want{
		Model: sel.Model, Alias: sel.Alias, ModelID: dc.ModelID,
		Node: node, Prefer: preference,
	}

	fmt.Printf("Spinloop: %s\nFleet:  %s\nPrefer: %s\n\n", resolvedPath, describeTarget(cfg), preference)
	if sel.BaseURL != "" {
		fmt.Printf("This Spinloop pins BASEURL %s, so a launch would not route at all.\n", sel.BaseURL)
		return nil
	}
	if gw, ok := cfg.GatewaySection(); ok {
		// The file names a gateway: the choosing is already done — no node is
		// queried, and nothing is started.
		fmt.Printf("The fleet file names a gateway: a launch would point the agent at %s.\n", fleet.EndpointBaseURL(gw.URL))
		fmt.Println("No node is queried, and nothing is started.")
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
	if dcErr != nil {
		fmt.Printf("\nA launch could not start one either: %v\n", dcErr)
		return nil
	}
	if wake, ok := cfg.WouldWake(none.Results, fleet.ConstantConfig(dc, dcErr)); ok {
		if cfg.Wakes() {
			fmt.Printf("\nA launch would wake %s and wait for its engine. Nothing has been started.\n", wake.Name)
		} else {
			fmt.Printf("\nA launch would refuse: wake is off in %s. Start %s with `spinloop fleet start %s`. Nothing has been started.\n",
				cfg.Path, wake.Name, wake.Name)
		}
		return nil
	}
	fmt.Println("\nNo node could be woken for it either. Nothing has been started.")
	return nil
}

// cmdFleetRoute runs the command through the tree — the seam the suite calls.
func cmdFleetRoute(args []string) error { return execCmd(fleetRouteCmd(), args) }
