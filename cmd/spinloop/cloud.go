package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/contextsize"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/opencode"
	"github.com/spinloop-ai/spinloop/internal/preset"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
	"github.com/spinloop-ai/spinloop/internal/spinloopsrc"
)

// cmdCloud dispatches the cloud subcommands, which control the
// scale-to-zero GPU inference instance defined in this repo's remote/:
// start boots it and prints the endpoint exports, pause stops it without
// terminating it (the sweep terminates stopped instances after their
// retention, and a later start re-wakes them), stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), status reports instance state and endpoint health, keep sets the
// retention deadline to prevent the sweep from terminating the instance
// early, and deploy sets what the instance will serve from the Spinloop itself.
// Each subcommand acts on one registered environment — the --env flag names
// it, and without the flag the `default` environment is used; see
// resolveCloudConfig. Each also takes an optional Spinloop path, read for
// its ENV instructions and adjacent .env only. The group itself does nothing
// — see groupFallback for what a bare or mistyped invocation gets.

// cmdCloud runs the cloud subcommands through the tree — the seam the suite
// calls directly.
func cmdCloud(args []string) error {
	return execCmd(cloudCmd(), args)
}

// applySpinloopEnv makes the cloud commands respect the Spinloop's local
// environment. The AWS SDK's credential chain reads the process environment
// directly, and the SPINLOOP_CLOUD_*/AWS_REGION lookups (Viper's AutomaticEnv
// included) ultimately do the same, so the values have to be present in the
// environment itself — a lookup closure would not reach the SDK. It therefore
// mutates this process's environment, in two passes that give the precedence
// ENV > process environment > .env:
//
//  1. the .env beside the Spinloop fills only gaps — a variable already set in
//     the environment wins, so a deliberately exported credential is not shadowed;
//  2. the Spinloop's ENV instructions override both.
//
// ENV (and .env) apply only to this local process; they are never sent to the
// deployed instance — deployConfigFor builds the deploy payload from Spinloop
// fields alone. spinloopPath is the Spinloop's own path; its directory is where
// its .env lives. A URL-sourced Spinloop has no local directory to look beside,
// so its .env read is skipped entirely — not attempted against a nonsense
// path — leaving the Spinloop's own ENV instructions and the process
// environment as the two remaining sources.
func applySpinloopEnv(sel spinloop.Selection, spinloopPath string) error {
	if !spinloopsrc.IsURL(spinloopPath) {
		vars, err := opencode.ParseEnvFile(filepath.Join(filepath.Dir(spinloopPath), ".env"))
		if err != nil {
			return err
		}
		for key, value := range vars {
			if os.Getenv(key) == "" {
				if err := os.Setenv(key, value); err != nil {
					return err
				}
			}
		}
	}
	for _, e := range sel.Env {
		if err := os.Setenv(e.Key, e.Value); err != nil {
			return err
		}
	}
	return nil
}

// envFlagUsage is the --env flag's help text on every cloud subcommand that
// acts on one environment.
const envFlagUsage = "the registered environment to act on (required)"

// resolveCloudConfig loads the cloud config a subcommand acts on. The --env
// flag names an environment in the per-user registry, read from
// clouds/<name>/cloud.json; a name with no registered configuration fails
// saying the environment is not registered and how to create it. Without the
// flag, the per-user config is the fallback — the `default` environment, then
// the legacy single file, then the SPINLOOP_CLOUD_* overrides alone — so
// `spinloop cloud` still works outside any project.
//
// A Spinloop given as an argument (or the ./Spinloop in the working directory,
// when no argument was given) is read for its ENV instructions and adjacent
// .env only, applied before any control-plane work: it never selects an
// environment.
func resolveCloudConfig(envName, spinloopArg string) (cloud.Config, error) {
	if spinloopArg != "" {
		sel, spinloopPath, err := readSpinloop("cloud", spinloopArg)
		if err != nil {
			return cloud.Config{}, err
		}
		if err := applySpinloopEnv(sel, spinloopPath); err != nil {
			return cloud.Config{}, err
		}
	} else if defaultSpinloopNamed() {
		sel, spinloopPath, err := readSpinloop("cloud", "")
		if err != nil {
			return cloud.Config{}, err
		}
		if err := applySpinloopEnv(sel, spinloopPath); err != nil {
			return cloud.Config{}, err
		}
	}
	if envName == "" {
		return cloud.Config{}, errNoEnvironment()
	}
	if !cloud.IsEnvName(envName) {
		return cloud.Config{}, fmt.Errorf("%q is not an environment name: an environment name is a plain identifier, with no path", envName)
	}
	return cloud.LoadEnvironment(envName, viperGetenv())
}

// errNoEnvironment is what a cloud subcommand fails with when it names no
// environment. There is no environment to fall back to: several of these
// subcommands change the state of a cloud instance, and one nobody named is
// not one to start, stop or terminate. The registered names are listed, so the
// next thing to type is in the error rather than a directory listing away.
func errNoEnvironment() error {
	var b strings.Builder
	b.WriteString("no environment named: pass --env <name>")
	envs, err := cloud.ListEnvironments()
	if err != nil || len(envs) == 0 {
		b.WriteString(" (none registered yet — `spinloop cloud deploy --env <name>` creates one)")
		return errors.New(b.String())
	}
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	fmt.Fprintf(&b, " (registered: %s)", strings.Join(names, ", "))
	return errors.New(b.String())
}

// defaultSpinloopNamed reports whether there is a default Spinloop for readSpinloop
// to resolve — one named by SPINLOOP_ALIAS, or a ./Spinloop here. Only the
// existence question is answered; readSpinloop does the resolving, so a variable
// naming an alias that is unregistered or dangling fails there rather than
// falling silently back to the per-user config.
func defaultSpinloopNamed() bool {
	// Same Viper read as readSpinloop's empty-path branch: the gate and the
	// reader must count the same default, or a variable-named Spinloop is read
	// by apply and skipped here.
	return cliViper.GetString("alias") != "" || defaultSpinloopExists()
}

// defaultSpinloopExists reports whether the working directory holds a file
// named exactly "Spinloop". A plain os.Stat would do, except that on
// case-insensitive filesystems (macOS, Windows) it also matches a file named
// "spinloop" — such as the binary `make build` drops in this repo's root — so
// the directory listing is checked for the exact name instead.
func defaultSpinloopExists() bool {
	entries, err := os.ReadDir(".")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == spinloop.DefaultFile && !entry.IsDir() {
			return true
		}
	}
	return false
}

// spinloopArg returns the optional positional Spinloop path after the flags.
// spinloopArg is the first positional argument of a cloud subcommand — the
// Spinloop path — or "" when none was given.
func spinloopArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// heartbeatEvery is how often a start that is still waiting says so. The start
// endpoint blocks until the model is serving, which on a cold start is minutes,
// so without this the command looks hung. A variable so tests can shorten it.
var heartbeatEvery = 30 * time.Second

// startProgress reports what a slow start is doing, as a renderer over the
// phases fleet.StartPhases builds from cloud.Start's callbacks — the same
// phases the dashboard tile draws, so the two surfaces cannot word one
// situation differently. Everything it writes goes to stderr, so `spinloop
// cloud start | grep '^export '` still yields just the exports while the user
// watching the terminal still sees progress.
type startProgress struct {
	mu    sync.Mutex
	since time.Time
	phase fleet.StartPhase // what the start is doing now; each report replaces it
	done  chan struct{}
	stop  sync.Once
}

func newStartProgress(every time.Duration) *startProgress {
	p := &startProgress{since: time.Now(), done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				p.line(p.heartbeat())
			}
		}
	}()
	return p
}

// report prints one phase as the start enters it, and keeps it for the
// heartbeat to redraw. Called from cloud.Start's own goroutine.
func (p *startProgress) report(phase fleet.StartPhase) {
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
	p.line(fleet.RenderPhase(phase, time.Now()))
}

// callbacks are the pair to hand cloud.Start: the phases they build are what
// report renders.
func (p *startProgress) callbacks() (progress func(string), onState func(string)) {
	return fleet.StartPhases(p.report)
}

// heartbeat is the periodic line. A phase can hold for minutes — the attempt
// that obtains capacity keeps one request open for the whole boot and reports
// nothing while it does — so the line is the current phase redrawn at the
// current time, which counts a wait down and a boot up, plus how long the
// whole start has been running.
func (p *startProgress) heartbeat() string {
	p.mu.Lock()
	phase := p.phase
	p.mu.Unlock()
	return fmt.Sprintf("%s (%s elapsed)", fleet.RenderPhase(phase, time.Now()), p.elapsed())
}

func (p *startProgress) elapsed() time.Duration {
	return time.Since(p.since).Round(time.Second)
}

// line prints one progress line. Serialised, so a heartbeat landing at the same
// moment as a retry notice cannot interleave with it.
func (p *startProgress) line(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(os.Stderr, msg)
}

func (p *startProgress) close() {
	p.stop.Do(func() { close(p.done) })
}

// The variables a cloud endpoint is addressed by. The instance is started
// with its API key as --api-key on an OpenAI-compatible server, so these are
// the names every consumer uses: the export lines below, and the environment
// `spinloop harness open` hands the agent it launches.
const (
	cloudBaseURLEnv = "OPENAI_BASE_URL"
	cloudAPIKeyEnv  = "OPENAI_API_KEY"
)

// printCloudEnv prints the cloud endpoint's environment variables as shell
// export lines to stdout, suitable for eval. Nothing else on this path may
// write to stdout, or the eval fails on the stray line.
func printCloudEnv(resp *cloud.Response) {
	fmt.Printf("export %s=%s\n", cloudBaseURLEnv, resp.BaseURL)
	fmt.Printf("export %s=%s\n", cloudAPIKeyEnv, resp.APIKey)
}

func cloudEnvCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:   "env",
		Short: "print the running endpoint's env vars",
		Long: `returns the running endpoint's environment variables without
starting it.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudEnv(envName, args)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudEnv is the body of `spinloop cloud env`.
func runCloudEnv(envName string, args []string) error {
	cfg, err := resolveCloudConfig(envName, spinloopArg(args))
	if err != nil {
		return err
	}
	resp, err := cloud.Env(context.Background(), cfg)
	if err != nil {
		return err
	}
	printCloudEnv(resp)
	return nil
}

func cloudStartCmd() *cobra.Command {
	var timeout time.Duration
	const timeoutUsage = "overall time to wait for the endpoint"
	var envName string
	var printEnv bool
	var keepD string
	c := &cobra.Command{
		Use:   "start",
		Short: "boot the instance and print its endpoint",
		Long: `boots the instance and, with --print-env, prints the exports your
agent needs.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudStart(envName, args, timeout, printEnv, keepD)
		},
	}
	fs := c.Flags()
	fs.DurationVarP(&timeout, "timeout", "t", 15*time.Minute, timeoutUsage)
	fs.StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	fs.BoolVar(&printEnv, "print-env", false, "print export lines to stdout for eval")
	fs.StringVar(&keepD, "keep", "", "retain instance until now + DURATION (e.g. 4h, 60m), preventing the idle sweep from stopping it")
	return c
}

// runCloudStart is the body of `spinloop cloud start`.
func runCloudStart(envName string, args []string, timeout time.Duration, printEnv bool, keepD string) error {
	cfg, err := resolveCloudConfig(envName, spinloopArg(args))
	if err != nil {
		return err
	}

	var retainUntil *time.Time
	if keepD != "" {
		d, err := time.ParseDuration(keepD)
		if err != nil {
			return fmt.Errorf("invalid --keep duration %q: %w", keepD, err)
		}
		if d <= 0 {
			return fmt.Errorf("--keep duration must be positive, got %q", keepD)
		}
		t := time.Now().Add(d)
		retainUntil = &t
	}

	progress := newStartProgress(heartbeatEvery)
	defer progress.close()
	progress.line(fmt.Sprintf(
		"Starting the endpoint. It boots, fetches the weights, and loads them into the GPU,\nwhich takes several minutes from cold; waiting up to %s.", timeout))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	onProgress, onState := progress.callbacks()
	resp, err := cloud.Start(ctx, cfg, onProgress, onState, retainUntil)
	if err != nil {
		return err
	}
	progress.close()
	progress.line(fmt.Sprintf("ready after %s", progress.elapsed()))

	// Probe the inference endpoint to check whether this network can actually
	// reach it — the control plane (SigV4 Lambda URLs) works from anywhere,
	// but the inference port is guarded by a security group that admits only
	// one CIDR. A changed network means start succeeds but inference hangs.
	if err := cloud.ProbeReachability(resp.BaseURL); err != nil {
		cidr := "<your-ip>/32"
		if detected, detErr := detectPublicCIDRFn(context.Background()); detErr == nil {
			cidr = detected
		}
		fmt.Fprintln(os.Stderr, "the endpoint is ready but not reachable from this network — its ingress admits a different address.")
		fmt.Fprintf(os.Stderr, "Re-admit this machine with:\n  spinloop cloud deploy --overwrite --allowed-cidr %s\n", cidr)
	}

	if retainUntil != nil || resp.RetainUntil != "" {
		retainStr := resp.RetainUntil
		if retainStr == "" && retainUntil != nil {
			retainStr = retainUntil.Format(time.RFC3339)
		}
		progress.line(fmt.Sprintf("retain until: %s", retainStr))
	}

	if printEnv {
		envCtx, envCancel := context.WithTimeout(context.Background(), 30*time.Second)
		envResp, err := cloud.Env(envCtx, cfg)
		envCancel()
		if err != nil {
			return err
		}
		printCloudEnv(envResp)
	}
	return nil
}

// cmdCloudList prints the registered cloud environments, each with its base
// URL and region, marking any whose cloud.json is missing or unreadable. It
// contacts no endpoint. Environments are registered under
// ~/.config/spinloop/clouds/<name>/ (by `spinloop cloud bootstrap`, or by hand).
func cloudListCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "ls",
		Short:             "list the registered environments",
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: noPositionals,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runCloudList()
		},
	}
}

// runCloudList is the body of `spinloop cloud ls`.
func runCloudList() error {
	envs, err := cloud.ListEnvironments()
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		fmt.Println("No cloud environments registered. Register one with `spinloop cloud bootstrap`.")
		return nil
	}
	for _, e := range envs {
		if !e.OK {
			fmt.Printf("%s\t(missing or unreadable cloud.json)\n", e.Name)
			continue
		}
		base := e.BaseURL
		if base == "" {
			base = "(no base URL)"
		}
		fmt.Printf("%s\t%s\t%s\n", e.Name, base, e.Region)
	}
	return nil
}

func cloudKeepCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:               "keep",
		Short:             "defer the instance's idle termination",
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: keepSlot,
		RunE: func(c *cobra.Command, rest []string) error {
			resolve(c)
			return runCloudKeep(envName, rest)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudKeep is the body of `spinloop cloud keep`.
func runCloudKeep(envName string, rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("usage: spinloop cloud keep <duration> [path]")
	}
	durationStr := rest[0]
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", durationStr, err)
	}
	if d <= 0 {
		return fmt.Errorf("duration must be positive, got %q", durationStr)
	}
	// The Spinloop path is the second positional, after the duration.
	spinloopPath := ""
	if len(rest) > 1 {
		spinloopPath = rest[1]
	}
	cfg, err := resolveCloudConfig(envName, spinloopPath)
	if err != nil {
		return err
	}
	retainUntil := time.Now().Add(d)
	_, err = cloud.Keep(context.Background(), cfg, retainUntil)
	if err != nil {
		return err
	}
	fmt.Printf("retain until: %s (in %s)\n", retainUntil.Format(time.RFC3339), d)
	return nil
}

func cloudPauseCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:               "pause",
		Short:             "stop the instance without terminating it",
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudPause(envName, args)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudPause is the body of `spinloop cloud pause`.
func runCloudPause(envName string, args []string) error {
	cfg, err := resolveCloudConfig(envName, spinloopArg(args))
	if err != nil {
		return err
	}
	resp, err := cloud.Pause(context.Background(), cfg, false)
	if err != nil {
		return err
	}
	// State is "stopping" (EC2 has not finished) or "stopped" (it already
	// was); either way the instance is re-wakeable, and the control plane's
	// sweep terminates it once the stop retention passes.
	fmt.Printf("state: %s\n", resp.State)
	fmt.Println("the endpoint can be re-woken with `spinloop cloud start`, or terminated now with `spinloop cloud stop`")
	return nil
}

func cloudRestartCmd() *cobra.Command {
	var force bool
	var timeout time.Duration
	const timeoutUsage = "overall time to wait for the endpoint"
	var envName string
	c := &cobra.Command{
		Use:   "restart",
		Short: "stop the instance and bring it back",
		Long: `stops the instance without terminating it, then wakes it and blocks
until the model serves again. The boot disk and its weights survive, and the
endpoint's address is unchanged. With --force the graceful engine stop is
skipped: for when the engine or its daemon will not answer it.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudRestart(envName, args, force, timeout)
		},
	}
	fs := c.Flags()
	fs.BoolVarP(&force, "force", "F", false, "skip the graceful engine stop")
	fs.DurationVarP(&timeout, "timeout", "t", 15*time.Minute, timeoutUsage)
	fs.StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudRestart is the body of `spinloop cloud restart`. It stops the
// instance in the pause manner — without terminating it, so the boot disk and
// weights survive and the address does not change — and reuses the wake's own
// deadline and retry handling to block until the model serves again.
func runCloudRestart(envName string, args []string, force bool, timeout time.Duration) error {
	cfg, err := resolveCloudConfig(envName, spinloopArg(args))
	if err != nil {
		return err
	}

	progress := newStartProgress(heartbeatEvery)
	defer progress.close()

	// A lenient status call up front: its reply only feeds a progress line
	// saying where the instance is now (running, or already stopped / no
	// instance). It gates nothing — the stop Lambda is correct for every
	// state — so a failed check just means we do not know the starting point.
	if status, err := cloud.Status(context.Background(), cfg); err == nil {
		switch status.State {
		case "stopped", "undeployed":
			progress.line(fmt.Sprintf("the instance is already %s; waking it", status.State))
		default:
			progress.line(fmt.Sprintf("the instance is %s; stopping it, then waking it", status.State))
		}
	}

	stopPhase := "stopping the instance, then waking it"
	if force {
		stopPhase = "stopping the instance (the graceful engine stop is skipped), then waking it"
	}
	progress.line(stopPhase)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	onProgress, onState := progress.callbacks()
	resp, err := cloud.Restart(ctx, cfg, force, onProgress, onState)
	if err != nil {
		return err
	}
	progress.close()
	progress.line(fmt.Sprintf("ready after %s", progress.elapsed()))
	fmt.Printf("base url: %s\n", resp.BaseURL)
	return nil
}

func cloudStopCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:               "stop",
		Short:             "shut the instance down",
		Long:              `shuts the instance down rather than waiting for the idle timer.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudStop(envName, args)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudStop is the body of `spinloop cloud stop`.
func runCloudStop(envName string, args []string) error {
	cfg, err := resolveCloudConfig(envName, spinloopArg(args))
	if err != nil {
		return err
	}
	resp, err := cloud.Stop(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("state: %s\n", resp.State)
	return nil
}

func formatDuration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func formatBytes(b int64) string {
	const unit = 1024.0
	if b < int64(unit) {
		return fmt.Sprintf("%d B", b)
	}
	kb := float64(b) / unit
	if kb < unit {
		return fmt.Sprintf("%.0f KB", kb)
	}
	mb := kb / unit
	if mb < unit {
		return fmt.Sprintf("%.0f MB", mb)
	}
	return fmt.Sprintf("%.1f GB", mb/unit)
}

// getOnDemandPrice fetches the hourly on-demand price for an instance type
// from the AWS Price List API. Uses GetProducts with a filter on instance type
// and operation (Linux/Windows). Returns the price per hour.
func getOnDemandPrice(ctx context.Context, region, instanceType string) (float64, error) {
	return cloud.GetOnDemandPrice(ctx, region, instanceType)
}

// runnerFor maps a Spinloop's PROVIDER to the inference runner the cloud should
// run. PROVIDER already names the engine — `spinloop serve` starts that engine
// locally, `spinloop cloud deploy` asks the cloud for the same one — so no
// separate keyword is needed. Providers that are not self-hosted engines have
// nothing to deploy.
func runnerFor(provider string) (string, error) {
	switch provider {
	case "llamacpp", "vllm":
		return provider, nil
	default:
		return "", fmt.Errorf(
			"PROVIDER %q cannot be deployed: cloud deploy runs a self-hosted engine, so use llamacpp or vllm",
			provider)
	}
}

// providerForRunner is runnerFor's reverse: it maps a deployed environment's
// runner back to the catalogue provider that engine kind is configured under,
// so a launch with no Spinloop can key a provider selection off what an
// environment reports it is running. Since runnerFor's mapping is an identity
// for every runner it accepts, this only has to reject anything else — no
// runner value that isn't llamacpp or vllm can ever reach a deploy-config,
// because runnerFor already refused it at deploy time.
func providerForRunner(runner string) (string, error) {
	switch runner {
	case "llamacpp", "vllm":
		return runner, nil
	default:
		return "", fmt.Errorf("environment reports an unrecognised runner %q", runner)
	}
}

// nodeRunnerFor is the runner resolver for the node path — waking a fleet node
// that already exists. It accepts every engine `serve` can run and a daemon can
// supervise: llamacpp, vllm, and mtplx. MTPLX is Apple-Silicon-only and has no
// machine image, so it never becomes a cloud runner (see runnerFor).
func nodeRunnerFor(provider string) (string, error) {
	switch provider {
	case "llamacpp", "vllm", "mtplx":
		return provider, nil
	default:
		return "", fmt.Errorf(
			"PROVIDER %q cannot be woken: a fleet node runs a self-hosted engine, so use llamacpp, vllm or mtplx",
			provider)
	}
}

// splitModelQuant splits a model reference into the Hugging Face repo and an
// optional quant tag, as used by llama.cpp's -hf (org/model:QUANT). Repo ids
// cannot contain a colon, so the first one separates them.
func splitModelQuant(model string) (repo, quant string) {
	if i := strings.Index(model, ":"); i >= 0 {
		return model[:i], model[i+1:]
	}
	return model, ""
}

// cloudOwnedFlags are the llama-server settings the cloud sets itself, from the
// deploy config and the instance's own environment. They are dropped from a
// preset before it becomes serveArgs, so a preset written for a local run does
// not fight the deployment — binding to 127.0.0.1, say, or serving from a local
// .gguf path or an HF repo that the instance does not use (it syncs the weights
// from S3 instead). Keyed by canonical name, so short aliases match too.
var cloudOwnedFlags = map[string]bool{
	"host": true, "port": true,
	"model": true, "model-url": true,
	"hf-repo": true, "hf-file": true, "hf-token": true,
	"api-key": true, "api-key-file": true,
	"ctx-size": true, "alias": true, "metrics": true,
	// MTPLX's own spellings of the settings the destination computes from the
	// deploy config: the served name, the context window, and the slot count.
	// Keyed by canonical name like the rest, so a preset's raw values do not
	// double-define the computed flags. They collide with no llama.cpp or vLLM
	// key, so the cloud path is untouched.
	"model-id": true, "context-window": true, "max-active-requests": true,
	// Companion weights: the cloud syncs these from S3 and names them at its
	// own paths, so the preset's local paths must not travel. Only the
	// location is cloud-owned — how the engine is asked to use a drafter
	// (--spec-type) stays the user's, exactly as it is for a local run.
	"spec-draft-model": true, "mmproj": true,
	// Parallelism: spinloop computes each runner's own flag from
	// DeployConfig.Parallel, exactly as it computes ctx-size — a preset's
	// raw value would otherwise survive in serveArgs and double-define the
	// flag alongside the computed one.
	"parallel": true, "max-num-seqs": true, "max-concurrent-requests": true,
}

// companionRoleForFlag maps a preset flag naming a companion weight to the
// deploy-config role the cloud knows it by. Keyed by canonical name, so every
// spelling the preset dialect accepts (`md`, `model-draft`, `mm`) resolves
// here too.
var companionRoleForFlag = map[string]string{
	"spec-draft-model": "draft",
	"mmproj":           "mmproj",
}

// isCloudOwned reports whether the cloud sets this preset key itself.
func isCloudOwned(key string) bool {
	return cloudOwnedFlags[preset.CanonicalKey(key)]
}

// parallelPresetKey names the preset key holding a runner's slot count, in
// that engine's own vocabulary — the same split the *ServeParams functions
// make when they render it. It is what a deploy reads back out of a preset,
// so the value survives cloudOwnedFlags dropping it from the passthrough args.
// A runner with no parallelism key yields "", which reads as "not set".
func parallelPresetKey(runner string) string {
	switch runner {
	case "llamacpp":
		return "parallel" // also matches the np alias, via CanonicalKey
	case "vllm":
		return "max-num-seqs"
	case "omlx":
		return "max-concurrent-requests"
	case "mtplx":
		return "max-active-requests"
	default:
		return ""
	}
}

// modelPresetKey names the preset key holding the model, in that runner's own
// vocabulary — the same split the *ServeParams functions make when they read
// it. A runner with no preset model key yields "", which reads as "not set".
func modelPresetKey(runner string) string {
	switch runner {
	case "mtplx":
		return "model"
	case "llamacpp", "vllm":
		return "hf"
	default:
		return ""
	}
}

// contextPresetKey names the preset key holding the context window, in that
// runner's own vocabulary.
func contextPresetKey(runner string) string {
	switch runner {
	case "mtplx":
		return "context-window"
	case "llamacpp", "vllm":
		return "ctx-size"
	default:
		return ""
	}
}

// isNodeOwned reports whether a fleet node's daemon sets this preset key
// itself. It is the cloud's set minus the bind and the companion paths: a node
// is a machine the operator owns, so where its engine listens is their choice
// to make. Dropping the bind left a woken engine on llama.cpp's loopback
// default however the preset was written — reachable from nobody else in the
// fleet, which is the whole point of a fleet.
//
// The companion paths go the same way, for the same reason. The cloud owns them
// because it seeds the weights from Hugging Face and puts them where it likes;
// a node has the files already, at the path its preset names, so dropping them
// would lose the drafter with nothing to replace it.
func isNodeOwned(key string) bool {
	switch preset.CanonicalKey(key) {
	case "host", "port", "spec-draft-model", "mmproj":
		return false
	}
	return isCloudOwned(key)
}

// dropOwned returns the params the destination does not set for itself.
func dropOwned(owned func(string) bool, params []preset.Param) []preset.Param {
	var kept []preset.Param
	for _, p := range params {
		if !owned(p.Key) {
			kept = append(kept, p)
		}
	}
	return kept
}

// deployConfigFor turns a Spinloop (plus its preset, if any) into the config the
// deploy Lambda accepts. The Spinloop is the single source of truth: PROVIDER
// picks the runner, MODEL or the preset's hf names the weights, CONTEXT the
// window, ALIAS the served name, and whatever else the preset sets becomes the
// runner's own flags.
//
// A context size is required, because a cloud instance has to be sized before
// it is provisioned. deployConfigWithoutContext is the same derivation for a
// caller where that is not true.
func deployConfigFor(sel spinloop.Selection, spinloopPath string) (inference.DeployConfig, error) {
	return deployConfig(sel, spinloopPath, deployTarget{
		runner:         runnerFor,
		requireContext: true,
		seedsWeights:   true,
		owns:           isCloudOwned,
	})
}

// deployConfigForNode derives the same config for a machine that already
// exists — a fleet node being woken. Three things differ from a cloud
// deployment, all because the machine is the operator's rather than the
// deployment's: sizing falls back to the engine's own default, so a Spinloop
// that `spinloop serve` runs happily needs no CONTEXT added merely to be
// routed; the preset's bind survives, so an engine told to listen on 0.0.0.0
// does; and the Spinloop's own BASEURL is carried too, so a node with no
// preset does not wake onto the engine's default — llama.cpp's loopback,
// reachable from nobody but the node itself.
func deployConfigForNode(sel spinloop.Selection, spinloopPath string) (inference.DeployConfig, error) {
	return deployConfig(sel, spinloopPath, deployTarget{
		runner:         nodeRunnerFor,
		owns:           isNodeOwned,
		carriesBaseURL: true,
	})
}

// deployTarget is what the derivation cannot decide for itself: which runners
// it accepts, whether a context size is required, which preset flags the
// destination assigns, whether it fetches the weights itself (and so needs
// companions named), and whether the Spinloop's BASEURL is rendered into the
// engine's bind — a machine the operator owns listens wherever the Spinloop
// says; the cloud assigns its own port, so it keeps the field empty.
type deployTarget struct {
	runner         func(provider string) (string, error)
	requireContext bool
	seedsWeights   bool
	owns           func(key string) bool
	carriesBaseURL bool
}

func deployConfig(sel spinloop.Selection, spinloopPath string, target deployTarget) (inference.DeployConfig, error) {
	var dc inference.DeployConfig

	runner, err := target.runner(sel.Provider)
	if err != nil {
		return dc, err
	}
	dc.Runner = runner

	// The preset supplies the model and the runner flags when the Spinloop does
	// not state them, so a single preset can drive both serve and deploy. The
	// [*] globals are a separate layer that the chosen section overrides —
	// exactly as preset.Args does for a local serve — so settings written there
	// (commonly ngl and jinja) are not lost.
	var global, params []preset.Param
	if sel.Preset != "" {
		presetPath, err := resolvePresetPath(sel.Preset, spinloopPath)
		if err != nil {
			return dc, err
		}
		data, err := spinloopsrc.Fetch(presetPath)
		if err != nil {
			return dc, fmt.Errorf("reading preset %s: %w", presetPath, err)
		}
		pre, err := preset.Parse(data)
		if err != nil {
			return dc, fmt.Errorf("%s: %w", presetPath, err)
		}
		sec, err := pre.Select(sel.Alias)
		if err != nil {
			return dc, fmt.Errorf("%s: %w", presetPath, err)
		}
		global, params = pre.Global, sec.Params
	}

	model := sel.Model
	if model == "" {
		model = presetValue(modelPresetKey(dc.Runner), global, params)
	}
	if model == "" {
		return dc, fmt.Errorf(
			"nothing to deploy: set MODEL in %s, or %s in its preset",
			spinloopPath, modelPresetKey(dc.Runner))
	}
	// A local model path is refused only where the destination fetches the
	// weights itself — the cloud, which cannot ship a file. A node has the file
	// the Spinloop points at, so the node path carries the path as the model to
	// load; today's unconditional check also blocked llamacpp and vllm node
	// wakes with local weights, which moving it unblocks too.
	if target.seedsWeights && isModelPath(model) {
		return dc, fmt.Errorf(
			"cannot deploy the local model file %q: the cloud downloads weights from Hugging Face, so name a repo (org/model:QUANT)",
			model)
	}
	dc.ModelID, dc.Quant = splitModelQuant(model)

	context := sel.Context
	if context == "" {
		context = presetValue(contextPresetKey(dc.Runner), global, params)
	}
	if context == "" && target.requireContext {
		return dc, fmt.Errorf("no context size: set CONTEXT in %s, or %s in its preset", spinloopPath, contextPresetKey(dc.Runner))
	}
	if context != "" {
		n, err := contextsize.Parse(context)
		if err != nil {
			return dc, err
		}
		dc.ContextSize = n
	}

	// Parallel falls back to the preset's own parallelism key the same way
	// context falls back to ctx-size, since that value is about to be dropped
	// from serveArgs below (it is cloud-owned) — capturing it here is what
	// keeps it from being silently lost rather than re-emitted as the
	// deployment's own computed flag. The key is the runner's own spelling:
	// reading only llama.cpp's would drop a vLLM preset's max-num-seqs on the
	// floor, since dropOwned removes it either way.
	parallel := sel.Parallel
	if key := parallelPresetKey(dc.Runner); parallel == "" && key != "" {
		parallel = presetValue(key, global, params)
	}
	if parallel != "" {
		n, err := parseParallel(parallel)
		if err != nil {
			return dc, err
		}
		dc.Parallel = n
	}

	// The served name is what a coding agent asks for. ALIAS is the friendly
	// name; without one the repo id is served under its own name.
	dc.ServedModelName = sel.Alias
	if dc.ServedModelName == "" {
		dc.ServedModelName = dc.ModelID
	}

	// Companion weights are read from the same preset keys that drive a local
	// serve, so one preset still works both ways. Only where the destination
	// fetches the weights itself: it pulls them from the model's own repo, so
	// only the filename travels and the local path the user downloaded to is
	// meaningless there. A node already has its files, and keeps its own path.
	if target.seedsWeights {
		dc.Companions = companionsFrom(global, params)
	}

	// The Spinloop's own bind is the final layer, where it travels at all: it
	// wins over the preset's host and port exactly as it does for a local
	// serve, so a woken engine listens wherever the Spinloop says rather than
	// on the engine's default.
	layers := [][]preset.Param{dropOwned(target.owns, global), dropOwned(target.owns, params)}
	if target.carriesBaseURL {
		bind, err := bindAddressParams(sel)
		if err != nil {
			return dc, err
		}
		layers = append(layers, bind)
	}
	dc.ServeArgs = preset.Flags(layers...)
	if dc.ServeArgs == nil {
		dc.ServeArgs = []string{}
	}
	return dc, nil
}

// companionsFrom collects the companion weights a preset names, as role ->
// filename. The value is reduced to its base name: a companion ships in the
// model's own repo, so the basename is what the seed asks Hugging Face for.
func companionsFrom(layers ...[]preset.Param) map[string]string {
	companions := map[string]string{}
	for _, layer := range layers {
		for _, p := range layer {
			role, ok := companionRoleForFlag[preset.CanonicalKey(p.Key)]
			if !ok || strings.TrimSpace(p.Value) == "" {
				continue
			}
			companions[role] = filepath.Base(strings.TrimSpace(p.Value))
		}
	}
	if len(companions) == 0 {
		return nil
	}
	return companions
}

// presetValue returns a preset param's value across layers, or "" when it is
// not set. Later layers win, matching preset.Flags — so a section overrides the
// [*] globals.
func presetValue(key string, layers ...[]preset.Param) string {
	want := preset.CanonicalKey(key)
	value := ""
	for _, layer := range layers {
		for _, p := range layer {
			if preset.CanonicalKey(p.Key) == want {
				value = p.Value
			}
		}
	}
	return value
}

// Seams for the deploy flow, so tests drive it without AWS or a network.
var (
	deployDiscoverFn   = cloud.DiscoverControlPlane
	cloudDeployFn      = cloud.Deploy
	cloudStatusFn      = cloud.Status
	detectPublicCIDRFn = detectPublicCIDR
)

// metricsWatchInterval is the polling interval for --watch mode.
// Tests override this to avoid sleeping 60 seconds.
var metricsWatchInterval = 60 * time.Second

func cloudDeployCmd() *cobra.Command {
	var (
		dryRun          bool
		overwrite       bool
		reseed          bool
		allowedCidr     string
		region          string
		spinloopVersion string
		instanceType    string
		apiKeyEnv       string
		envName         string
	)
	c := &cobra.Command{
		Use:   "deploy",
		Short: "set what the instance serves, from the Spinloop",
		Long: `creates the environment named by --env — its own
address, API key and allowed CIDR — and says what it serves (PROVIDER picks
the engine, just as it does for serve). --spinloop-version pins the spinloop
release a fresh boot of the environment installs; without it, a boot
installs the latest published release. --instance-type names the EC2 instance
type the environment's instances launch as; without it, they launch as the
control plane's default. The type is recorded on the environment and applies
from its next fresh launch.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudDeploy(args, envName, dryRun, overwrite, reseed, allowedCidr, region, spinloopVersion, instanceType, apiKeyEnv)
		},
	}
	fs := c.Flags()
	fs.BoolVarP(&dryRun, "dry-run", "n", false, "print the config that would be deployed, without sending it")
	fs.BoolVar(&overwrite, "overwrite", false, "proceed against an already-registered or live environment")
	fs.BoolVar(&reseed, "reseed", false, "re-fetch the weights even if they are already in S3 (starts a ~20-minute seed)")
	fs.StringVar(&allowedCidr, "allowed-cidr", "", "who may reach this environment's instance (default: your public IP as a /32, on first deploy)")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	fs.StringVar(&spinloopVersion, "spinloop-version", "", "spinloop release the environment's instances install at boot (default: latest)")
	fs.StringVar(&instanceType, "instance-type", "", "EC2 instance type the environment's instances launch as (e.g. g6e.xlarge; default: the control plane's default type)")
	fs.StringVar(&apiKeyEnv, "api-key-env", "", "the environment variable holding the API key to store for this environment (a variable name, never the key itself)")
	fs.StringVar(&envName, "env", "", "the environment deploy creates and registers (required)")
	compRegister(c, "env", compEnvs)
	return c
}

// runCloudDeploy is the body of `spinloop cloud deploy`.
func runCloudDeploy(args []string, envName string, dryRun, overwrite, reseed bool, allowedCidr, region, spinloopVersion, instanceType, apiKeyEnv string) error {
	_, spinloopPath, dc, err := deriveDeployTarget("spinloop cloud deploy <file>", spinloopArg(args), envName)
	if err != nil {
		return err
	}
	outcome, err := runDeploy(spinloopPath, envName, dc, deployOpts{
		dryRun:          dryRun,
		overwrite:       overwrite,
		reseed:          reseed,
		allowedCidr:     allowedCidr,
		region:          region,
		spinloopVersion: spinloopVersion,
		instanceType:    instanceType,
		apiKeyEnv:       apiKeyEnv,
	})
	if err != nil {
		return err
	}
	fmt.Print(outcome.Text)
	return nil
}

// deriveDeployTarget turns a raw Spinloop argument (a bare alias name, a
// path, or a URL — whatever readSpinloop accepts) and the environment name
// the caller supplies into everything a deploy needs: the Spinloop it read,
// the deploy config it derives, and the name to register under. usage is
// readSpinloop's error-message context, so a caller other than `cloud deploy`
// (namely `fleet deploy`, which names the environment with the node's own
// name) gets a message naming itself rather than a hard-coded command line.
//
// The Spinloop says what the environment serves; the name arrives from
// outside it — the --env flag for `cloud deploy`, the node name for `fleet
// deploy` — so a node's resolved Spinloop source and a standalone `cloud
// deploy` of the same file agree about what they deploy and each names its
// own environment.
func deriveDeployTarget(usage, spinloopArg, env string) (sel spinloop.Selection, spinloopPath string, dc inference.DeployConfig, err error) {
	sel, spinloopPath, err = readSpinloop(usage, spinloopArg)
	if err != nil {
		return
	}
	// Respect the Spinloop's local environment (.env beside it, then its ENV
	// lines) before any AWS work, so the credentials the deploy signs with, the
	// region, and the SPINLOOP_CLOUD_* overrides all see it. ENV stays local — it
	// never enters dc, so nothing here reaches the deployed instance.
	if err = applySpinloopEnv(sel, spinloopPath); err != nil {
		return
	}
	dc, err = deployConfigFor(sel, spinloopPath)
	if err != nil {
		return
	}
	if env == "" {
		err = fmt.Errorf("deploy must name the environment it creates: pass --env <name> (e.g. --env %s)", dc.ServedModelName)
		return
	}
	if !cloud.IsEnvName(env) {
		err = fmt.Errorf("%q is not an environment name: an environment name is a plain identifier, with no path", env)
		return
	}
	return
}

// deployOpts are the flags a deploy takes, independent of the Spinloop file
// itself — shared by `cloud deploy` and `fleet deploy`, which each collect
// them from their own flag set.
type deployOpts struct {
	dryRun          bool
	overwrite       bool
	reseed          bool
	allowedCidr     string
	region          string
	spinloopVersion string
	// instanceType names the EC2 instance type the environment's instances
	// launch as. Empty means launch as the control plane's default. It is
	// recorded on the environment at deploy time and read back on the next
	// fresh launch.
	instanceType string
	// apiKeyEnv names the environment variable holding an externally
	// supplied key to store as the environment's engine key, never a
	// literal on the command line. Empty means the control plane manages
	// its own key as it always has.
	apiKeyEnv string
}

// deployOutcome is a successful (or dry-run) deploy's result: the plan/result
// text a standalone `cloud deploy` prints verbatim, plus the values a
// caller driving several nodes wants without re-parsing that text. A failed
// or guarded deploy is reported through the error runDeploy returns instead
// — an errDeployGuarded distinguishes "needs --overwrite" from any other
// failure.
type deployOutcome struct {
	Text          string
	DryRun        bool
	BaseURL       string
	Seeding       bool
	SeedID        string
	EnvConfigPath string
}

// errDeployGuarded means the named environment is already registered or
// live, and --overwrite was not given. Distinct from any other deploy
// failure so a caller driving several nodes (fleet deploy) can label this
// one "guarded" rather than "failed".
type errDeployGuarded struct {
	env, what string
}

func (e *errDeployGuarded) Error() string {
	return fmt.Sprintf("environment %q %s — pass --overwrite to redeploy over it", e.env, e.what)
}

// runDeploy is everything a deploy does once its target is known: validate
// the deploy-only flags, print the plan, and — unless --dry-run — clobber-
// guard, discover the control plane, deploy, and register the environment.
// It writes nothing to stdout itself; the caller decides what to do with
// deployOutcome.Text, which is how `fleet deploy` labels several nodes'
// outcomes instead of interleaving raw prints from concurrent goroutines.
func runDeploy(spinloopPath, env string, dc inference.DeployConfig, opts deployOpts) (deployOutcome, error) {
	if opts.allowedCidr != "" && !cidrPattern.MatchString(opts.allowedCidr) {
		return deployOutcome{}, fmt.Errorf("--allowed-cidr must be an IPv4 CIDR (e.g. 203.0.113.7/32), got %q", opts.allowedCidr)
	}
	// The spinloop release a fresh boot installs: empty (or `latest`) means the
	// boot's own default, a pin means exactly that release. Normalised the way
	// the control plane is — the v a tag carries is not part of the version —
	// and checked here, so a typo is named now rather than as a 404 inside a
	// boot nobody is watching.
	if pin := strings.TrimPrefix(strings.TrimSpace(opts.spinloopVersion), "v"); pin != "" && pin != "latest" {
		if !spinloopVersionPattern.MatchString(pin) {
			return deployOutcome{}, fmt.Errorf("--spinloop-version must be a release version (e.g. 1.26.1) or latest, got %q", opts.spinloopVersion)
		}
		dc.SpinloopVersion = pin
	}
	// The EC2 instance type the environment's instances launch as: empty (or
	// whitespace) means the control plane's own default, a name means exactly
	// that machine. Checked here, so a typo is named now rather than as a
	// RunInstances failure inside a deploy nobody is watching.
	if name := strings.TrimSpace(opts.instanceType); name != "" {
		if !cloud.IsInstanceType(name) {
			return deployOutcome{}, fmt.Errorf(
				"--instance-type must be an EC2 instance type (a family and size separated by a dot, e.g. g6e.xlarge), got %q",
				opts.instanceType)
		}
		dc.InstanceType = name
	}
	// A supplied key arrives the way every other secret does: as a reference
	// to an environment variable, never a literal on the command line. By
	// this point deriveDeployTarget has already applied the Spinloop's local
	// environment (a process-wide side effect), so the variable is
	// resolvable from here whichever caller is deploying; one set nowhere
	// fails before anything is sent.
	var apiKey string
	if opts.apiKeyEnv != "" {
		apiKey = os.Getenv(opts.apiKeyEnv)
		if apiKey == "" {
			return deployOutcome{}, fmt.Errorf(
				"--api-key-env: %s is not set: export it, or put it in the .env beside the Spinloop",
				opts.apiKeyEnv)
		}
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "Deploying from %s\n", spinloopPath)
	fmt.Fprintf(&buf, "  environment: %s\n", env)
	fmt.Fprintf(&buf, "  runner:  %s\n", dc.Runner)
	fmt.Fprintf(&buf, "  model:   %s", dc.ModelID)
	if dc.Quant != "" {
		fmt.Fprintf(&buf, " (%s)", dc.Quant)
	}
	buf.WriteByte('\n')
	fmt.Fprintf(&buf, "  context: %d\n", dc.ContextSize)
	if dc.Parallel > 0 {
		fmt.Fprintf(&buf, "  parallel: %d\n", dc.Parallel)
	}
	fmt.Fprintf(&buf, "  served:  %s\n", dc.ServedModelName)
	// Companions are easy to get wrong quietly — a renamed file yields no
	// drafter and a slower endpoint with no error — so show what was picked up.
	for _, role := range slices.Sorted(maps.Keys(dc.Companions)) {
		fmt.Fprintf(&buf, "  %-8s %s\n", role+":", dc.Companions[role])
	}
	if len(dc.ServeArgs) > 0 {
		fmt.Fprintf(&buf, "  args:    %s\n", strings.Join(dc.ServeArgs, " "))
	}
	// Worth stating: a re-seed costs a ~20-minute instance and re-downloads the
	// weights, so --reseed --dry-run must not look like a plain deploy.
	if opts.reseed {
		buf.WriteString("  reseed:  yes — the weights will be re-fetched even if already in S3\n")
	}
	// A fresh boot's spinloop: latest is a promise, not an absence, so the plan
	// always says which release a boot will install.
	spinloopVer := dc.SpinloopVersion
	if spinloopVer == "" {
		spinloopVer = "latest"
	}
	fmt.Fprintf(&buf, "  spinloop:  %s\n", spinloopVer)
	// A machine is worth stating too: a name is a promise, the default a
	// statement — so the plan always says what the environment launches as.
	instanceType := dc.InstanceType
	if instanceType == "" {
		instanceType = "the control plane's default"
	}
	fmt.Fprintf(&buf, "  instance:  %s\n", instanceType)
	// A key is worth stating too — it rotates — but the value is never
	// printed, in the dry run or the report.
	if opts.apiKeyEnv != "" {
		fmt.Fprintf(&buf, "  api key:  stored from %s\n", opts.apiKeyEnv)
	}
	if opts.dryRun {
		return deployOutcome{Text: buf.String(), DryRun: true}, nil
	}

	// The control URLs come from the control plane's stack outputs — the
	// environment may not exist yet, so there is nothing local to resolve.
	ctx := context.Background()
	awsCfg, err := cloud.LoadAWSConfig(ctx, resolveRegion(opts.region))
	if err != nil {
		return deployOutcome{}, err
	}
	layer, err := deployDiscoverFn(ctx, awsCfg, controlPlaneStackName)
	if err != nil {
		return deployOutcome{}, err
	}
	cfg := layer.Config
	cfg.Environment = env

	// Refuse to clobber silently: an environment that is already registered, or
	// whose instance is live, needs explicit consent to redeploy over.
	envConfigPath, err := cloud.EnvConfigPath(env)
	if err != nil {
		return deployOutcome{}, err
	}
	registered := false
	if _, err := os.Stat(envConfigPath); err == nil {
		registered = true
	}
	live := false
	if status, err := cloudStatusFn(ctx, cfg); err == nil {
		live = status.State == "running" || status.State == "pending" || status.State == "starting"
	}
	if (registered || live) && !opts.overwrite {
		what := "is already registered"
		if live {
			what = "has a live instance"
		}
		return deployOutcome{}, &errDeployGuarded{env: env, what: what}
	}

	// Ingress is per environment. A fresh environment needs a CIDR (default:
	// the caller's public address); an existing one keeps its ingress unless a
	// CIDR is given explicitly.
	allowedCidr := opts.allowedCidr
	if allowedCidr == "" && !registered {
		allowedCidr, err = detectPublicCIDRFn(ctx)
		if err != nil {
			return deployOutcome{}, fmt.Errorf("detecting your public IP for the allowed CIDR: %w (pass --allowed-cidr)", err)
		}
		fmt.Fprintf(&buf, "  ingress: %s (your public IP; override with --allowed-cidr)\n", allowedCidr)
	}

	resp, err := cloudDeployFn(ctx, cfg, dc, allowedCidr, opts.reseed, apiKey)
	if err != nil {
		return deployOutcome{}, err
	}

	// Register the environment so REMOTE <env> (and the other cloud
	// subcommands) resolve to it from now on.
	cfg.BaseURL = resp.BaseURL
	if err := cloud.SaveEnvironment(env, cfg); err != nil {
		return deployOutcome{}, err
	}

	buf.WriteByte('\n')
	fmt.Fprintf(&buf, "deployed: environment %s at %s\n", env, resp.BaseURL)
	fmt.Fprintf(&buf, "registered: %s\n", envConfigPath)
	// A rotation is worth stating out loud: it invalidates the key a live
	// agent may still be holding, and the action — never the value — is all
	// the reply carries.
	if resp.APIKeyAction == "rotated" {
		buf.WriteString("api key: rotated — the previous key no longer works\n")
	} else if resp.APIKeyAction != "" {
		buf.WriteString("api key: created\n")
	}
	if resp.Seeding {
		fmt.Fprintf(&buf, "seeding the weights — follow it with `spinloop cloud seed status %s`.\n", resp.SeedID)
		buf.WriteString("Wait for it to finish before `spinloop cloud start`, or the instance will\n")
		buf.WriteString("start against an incomplete download.\n")
	} else {
		buf.WriteString("weights already in place — `spinloop cloud start` will serve this.\n")
	}

	return deployOutcome{
		Text:          buf.String(),
		BaseURL:       resp.BaseURL,
		Seeding:       resp.Seeding,
		SeedID:        resp.SeedID,
		EnvConfigPath: envConfigPath,
	}, nil
}

// cidrPattern matches an IPv4 CIDR, the same shape the deploy Lambda accepts.
var cidrPattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/\d{1,2}$`)

// spinloopVersionPattern matches a release version pin, the same charset the
// deploy Lambda accepts after its leading v is stripped (1.26.1, not v1.26.1).
var spinloopVersionPattern = regexp.MustCompile(`^[0-9A-Za-z.-]+$`)

// detectPublicCIDR returns the caller's public IPv4 address as a /32, the
// default ingress for a fresh environment.
func detectPublicCIDR(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://checkip.amazonaws.com", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(data))
	cidr := ip + "/32"
	if !cidrPattern.MatchString(cidr) {
		return "", fmt.Errorf("unexpected public-IP response %q", ip)
	}
	return cidr, nil
}

// Test seams: the suite calls these the way the tree does — each runs its
// command through execCmd rather than parsing a private FlagSet.
func cmdCloudBootstrap(args []string) error { return execCmd(cloudBootstrapCmd(), args) }
func cmdCloudStart(args []string) error     { return execCmd(cloudStartCmd(), args) }
func cmdCloudPause(args []string) error     { return execCmd(cloudPauseCmd(), args) }
func cmdCloudRestart(args []string) error   { return execCmd(cloudRestartCmd(), args) }
func cmdCloudStop(args []string) error      { return execCmd(cloudStopCmd(), args) }
func cmdCloudDeploy(args []string) error    { return execCmd(cloudDeployCmd(), args) }
func cmdCloudEnv(args []string) error       { return execCmd(cloudEnvCmd(), args) }
func cmdCloudList(args []string) error      { return execCmd(cloudListCmd(), args) }
func cmdCloudKeep(args []string) error      { return execCmd(cloudKeepCmd(), args) }
