package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/outfitsrc"
	"github.com/lucinate-ai/outfit/internal/preset"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemote dispatches the remote subcommands, which control the
// scale-to-zero GPU inference instance defined in this repo's remote/:
// start boots it and prints the endpoint exports, pause stops it without
// terminating it (the sweep terminates stopped instances after their
// retention, and a later start re-wakes them), stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), status reports instance state and endpoint health, keep sets the
// retention deadline to prevent the sweep from terminating the instance
// early, and deploy sets what the instance will serve from the Outfit itself.
// Each subcommand takes an optional Outfit path; see resolveRemoteConfig for
// how the remote config is found.
func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote <bootstrap|start|pause|stop|status|metrics|logs|deploy|env|ls|keep> [path]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "bootstrap":
		return cmdRemoteBootstrap(rest)
	case "start":
		return cmdRemoteStart(rest)
	case "pause":
		return cmdRemotePause(rest)
	case "stop":
		return cmdRemoteStop(rest)
	case "status":
		return cmdRemoteStatus(rest)
	case "metrics":
		return cmdRemoteMetrics(rest)
	case "logs":
		return cmdRemoteLogs(rest)
	case "deploy":
		return cmdRemoteDeploy(rest)
	case "env":
		return cmdRemoteEnv(rest)
	case "ls":
		return cmdRemoteList(rest)
	case "keep":
		return cmdRemoteKeep(rest)
	default:
		return fmt.Errorf(
			"unknown remote subcommand %q (expected bootstrap, start, pause, stop, status, metrics, logs, deploy, env, ls or keep)", sub)
	}
}

// applyOutfitEnv makes the remote commands respect the Outfit's local
// environment. The AWS SDK's credential chain reads the process environment
// directly, and the OUTFIT_REMOTE_*/AWS_REGION lookups read os.Getenv, so the
// values have to be present in the environment itself — a lookup closure would
// not reach the SDK. It therefore mutates this process's environment, in two
// passes that give the precedence ENV > process environment > .env:
//
//  1. the .env beside the Outfit fills only gaps — a variable already set in
//     the environment wins, so a deliberately exported credential is not shadowed;
//  2. the Outfit's ENV instructions override both.
//
// ENV (and .env) apply only to this local process; they are never sent to the
// deployed instance — deployConfigFor builds the deploy payload from Outfit
// fields alone. outfitPath is the Outfit's own path; its directory is where
// its .env lives. A URL-sourced Outfit has no local directory to look beside,
// so its .env read is skipped entirely — not attempted against a nonsense
// path — leaving the Outfit's own ENV instructions and the process
// environment as the two remaining sources.
func applyOutfitEnv(sel outfit.Selection, outfitPath string) error {
	if !outfitsrc.IsURL(outfitPath) {
		vars, err := opencode.ParseEnvFile(filepath.Join(filepath.Dir(outfitPath), ".env"))
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

// resolveRemoteConfig loads the remote config, preferring an Outfit's REMOTE
// instruction over the per-user file. An explicit [path] argument must name
// an Outfit (or a directory holding one) with a REMOTE instruction. With no
// argument, ./Outfit is consulted when present; the per-user config
// (~/.config/outfit/remote.json) is the fallback, so `outfit remote` still
// works outside any project. A relative REMOTE resolves against the Outfit's
// own source — a local directory when the Outfit was read from disk,
// URL-relative resolution when it was fetched from a URL — the same rule
// PRESET uses.
func resolveRemoteConfig(outfitArg string) (remote.Config, error) {
	if outfitArg != "" {
		sel, outfitPath, err := readOutfit("remote", outfitArg)
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote == "" {
			return remote.Config{}, fmt.Errorf("%s has no REMOTE instruction", outfitPath)
		}
		if err := applyOutfitEnv(sel, outfitPath); err != nil {
			return remote.Config{}, err
		}
		return resolveRemoteConfigForOutfit(sel.Remote, outfitPath)
	}
	if defaultOutfitNamed() {
		sel, outfitPath, err := readOutfit("remote", "")
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote != "" {
			if err := applyOutfitEnv(sel, outfitPath); err != nil {
				return remote.Config{}, err
			}
			return resolveRemoteConfigForOutfit(sel.Remote, outfitPath)
		}
	}
	return remote.LoadDefault(os.Getenv)
}

// resolveRemotePath turns an Outfit's REMOTE value into the config to read. A
// bare name selects an environment from the per-user registry (always local);
// a path or URL resolves against the Outfit's own source — outfitPath, the
// Outfit's own file (not its directory), so a relative REMOTE resolves
// correctly whether the Outfit came from local disk or a URL. Both the
// control commands and apply's base-URL lookup go through here, so the two
// never diverge.
func resolveRemotePath(remoteValue, outfitPath string) (string, error) {
	if remote.IsEnvName(remoteValue) {
		return remote.EnvConfigPath(remoteValue)
	}
	return outfitsrc.Resolve(outfitPath, remoteValue)
}

// defaultOutfitNamed reports whether there is a default Outfit for readOutfit
// to resolve — one named by OUTFIT_ALIAS, or a ./Outfit here. Only the
// existence question is answered; readOutfit does the resolving, so a variable
// naming an alias that is unregistered or dangling fails there rather than
// falling silently back to the per-user config.
func defaultOutfitNamed() bool {
	return os.Getenv(outfitAliasEnv) != "" || defaultOutfitExists()
}

// defaultOutfitExists reports whether the working directory holds a file
// named exactly "Outfit". A plain os.Stat would do, except that on
// case-insensitive filesystems (macOS, Windows) it also matches a file named
// "outfit" — such as the binary `make build` drops in this repo's root — so
// the directory listing is checked for the exact name instead.
func defaultOutfitExists() bool {
	entries, err := os.ReadDir(".")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == outfit.DefaultFile && !entry.IsDir() {
			return true
		}
	}
	return false
}

// remoteConfig reads the remote config an Outfit's REMOTE names — a registry
// environment or a file/URL, per resolveRemotePath. A config that is absent
// yields the zero Config rather than an error, since an Outfit may name a
// remote config before the deployment that writes it exists; only a real
// read, fetch, or parse failure is reported.
func remoteConfig(remoteValue, outfitPath string) (remote.Config, error) {
	path, err := resolveRemotePath(remoteValue, outfitPath)
	if err != nil {
		return remote.Config{}, err
	}
	if outfitsrc.IsURL(path) {
		data, err := outfitsrc.Fetch(path)
		if err != nil {
			if errors.Is(err, outfitsrc.ErrNotFound) {
				return remote.Config{}, nil
			}
			return remote.Config{}, err
		}
		return remote.LoadConfigBytes(data, path, os.Getenv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return remote.Config{}, nil
		}
		return remote.Config{}, err
	}
	var cfg remote.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return remote.Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

// remoteBaseURL returns the endpoint address recorded in the remote config an
// Outfit's REMOTE names. The deployment generates that config, so the address
// lives there rather than in the hand-written Outfit — but only as a fallback:
// an Outfit that states its own BASEURL never asks. A config that is absent, or
// that predates base_url, yields "" rather than an error.
func remoteBaseURL(remoteValue, outfitPath string) (string, error) {
	cfg, err := remoteConfig(remoteValue, outfitPath)
	if err != nil {
		return "", err
	}
	return cfg.BaseURL, nil
}

// remoteEnvName returns the harness provider name an Outfit's REMOTE implies: the
// bare name when REMOTE is a name, otherwise the environment field of the
// remote.json it names. It yields "" when there is no REMOTE, or when a
// path-form REMOTE names a config that is absent or records no environment — in
// which case the caller keeps the PROVIDER value as the name.
func remoteEnvName(remoteValue, outfitPath string) (string, error) {
	if remoteValue == "" {
		return "", nil
	}
	if remote.IsEnvName(remoteValue) {
		return remoteValue, nil
	}
	cfg, err := remoteConfig(remoteValue, outfitPath)
	if err != nil {
		return "", err
	}
	return cfg.Environment, nil
}

// resolveRemoteConfigForOutfit resolves the remote config for an Outfit's
// REMOTE value, given the Outfit's own path. Unlike resolveRemoteConfig it
// does not consult the working directory or the per-user fallback — the REMOTE
// is already known from the parsed Outfit, so it goes straight to resolving
// that path, fetching over HTTP when it resolves to a URL.
func resolveRemoteConfigForOutfit(remoteValue, outfitPath string) (remote.Config, error) {
	path, err := resolveRemotePath(remoteValue, outfitPath)
	if err != nil {
		return remote.Config{}, err
	}
	if outfitsrc.IsURL(path) {
		data, err := outfitsrc.Fetch(path)
		if err != nil {
			return remote.Config{}, err
		}
		return remote.LoadConfigBytes(data, path, os.Getenv)
	}
	return remote.LoadConfigFile(path, os.Getenv)
}

// outfitArg returns the optional positional Outfit path after the flags.
func outfitArg(fs *flag.FlagSet) string {
	if rest := fs.Args(); len(rest) > 0 {
		return rest[0]
	}
	return ""
}

// heartbeatEvery is how often a start that is still waiting says so. The start
// endpoint blocks until the model is serving, which on a cold start is minutes,
// so without this the command looks hung. A variable so tests can shorten it.
var heartbeatEvery = 30 * time.Second

// startProgress reports what a slow start is doing. Everything it writes goes
// to stderr, so `outfit remote start | grep '^export '` still yields just the
// exports while the user watching the terminal still sees progress.
type startProgress struct {
	mu    sync.Mutex
	since time.Time
	state string // most recent state the endpoint reported; "" until the first poll
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

// setState records the state of the latest poll so the heartbeat can describe
// what is actually happening. Called from remote.Start on every poll.
func (p *startProgress) setState(state string) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

// heartbeat is the periodic line. It reflects the latest state so it does not
// claim the instance is booting when it is really blocked on capacity. Any
// state other than no-capacity (including the unset state before the first
// poll) reads as a normal cold start.
func (p *startProgress) heartbeat() string {
	p.mu.Lock()
	state := p.state
	p.mu.Unlock()
	if state == "no-capacity" {
		return fmt.Sprintf("still waiting for capacity (%s elapsed)", p.elapsed())
	}
	return fmt.Sprintf("still starting (%s elapsed)", p.elapsed())
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

// The variables a remote endpoint is addressed by. The instance is started
// with its API key as --api-key on an OpenAI-compatible server, so these are
// the names every consumer uses: the export lines below, and the environment
// `outfit harness` hands the agent it launches.
const (
	remoteBaseURLEnv = "OPENAI_BASE_URL"
	remoteAPIKeyEnv  = "OPENAI_API_KEY"
)

// printRemoteEnv prints the remote endpoint's environment variables as shell
// export lines to stdout, suitable for eval. Nothing else on this path may
// write to stdout, or the eval fails on the stray line.
func printRemoteEnv(resp *remote.Response) {
	fmt.Printf("export %s=%s\n", remoteBaseURLEnv, resp.BaseURL)
	fmt.Printf("export %s=%s\n", remoteAPIKeyEnv, resp.APIKey)
}

func cmdRemoteEnv(args []string) error {
	fs := flag.NewFlagSet("remote env", flag.ContinueOnError)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}
	resp, err := remote.Env(context.Background(), cfg)
	if err != nil {
		return err
	}
	printRemoteEnv(resp)
	return nil
}

// sortFlagsBeforeArgs moves flag arguments (starting with -) before positional
// arguments, so Go's flag package parses them regardless of order. The stdlib
// flag package stops at the first non-flag argument, so flags after a positional
// arg are silently ignored.
//
// fs is consulted so a flag written in the separated form (--fleet path) keeps
// its value: without it, the value is left behind with the positionals and the
// next token — often the positional itself — is bound to the flag instead.
// Boolean flags never consume a following token, so they are moved alone.
func sortFlagsBeforeArgs(fs *flag.FlagSet, args []string) []string {
	flags, pos := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		// "--" ends flag parsing; everything after it is positional.
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		// An inline value (--flag=value) is already self-contained.
		if strings.Contains(a, "=") {
			continue
		}
		if i+1 < len(args) && flagTakesValue(fs, a) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, pos...)
}

// flagTakesValue reports whether the named flag consumes the following
// argument. Unknown flags are assumed not to: guessing wrong would swallow a
// positional, and the flag package reports the unknown flag anyway.
func flagTakesValue(fs *flag.FlagSet, arg string) bool {
	if fs == nil {
		return false
	}
	name := strings.TrimLeft(arg, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	// The flag package treats a flag whose value implements IsBoolFlag as
	// standalone (-v), so only non-boolean flags take the next argument.
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !bf.IsBoolFlag()
}

func cmdRemoteStart(args []string) error {
	fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
	var timeout time.Duration
	const timeoutUsage = "overall time to wait for the endpoint"
	fs.DurationVar(&timeout, "timeout", 15*time.Minute, timeoutUsage)
	fs.DurationVar(&timeout, "t", 15*time.Minute, timeoutUsage+" (shorthand)")
	var printEnv bool
	fs.BoolVar(&printEnv, "env", false, "print export lines to stdout for eval")
	fs.BoolVar(&printEnv, "e", false, "print export lines to stdout for eval (shorthand)")
	var keepD string
	fs.StringVar(&keepD, "keep", "", "retain instance until now + DURATION (e.g. 4h, 60m), preventing the idle sweep from stopping it")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
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
	resp, err := remote.Start(ctx, cfg, progress.line, progress.setState, retainUntil)
	if err != nil {
		return err
	}
	progress.close()
	progress.line(fmt.Sprintf("ready after %s", progress.elapsed()))

	// Probe the inference endpoint to check whether this network can actually
	// reach it — the control plane (SigV4 Lambda URLs) works from anywhere,
	// but the inference port is guarded by a security group that admits only
	// one CIDR. A changed network means start succeeds but inference hangs.
	if err := remote.ProbeReachability(resp.BaseURL); err != nil {
		cidr := "<your-ip>/32"
		if detected, detErr := detectPublicCIDRFn(context.Background()); detErr == nil {
			cidr = detected
		}
		fmt.Fprintln(os.Stderr, "the endpoint is ready but not reachable from this network — its ingress admits a different address.")
		fmt.Fprintf(os.Stderr, "Re-admit this machine with:\n  outfit remote deploy --overwrite --allowed-cidr %s\n", cidr)
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
		envResp, err := remote.Env(envCtx, cfg)
		envCancel()
		if err != nil {
			return err
		}
		printRemoteEnv(envResp)
	}
	return nil
}

// cmdRemoteList prints the registered remote environments, each with its base
// URL and region, marking any whose remote.json is missing or unreadable. It
// contacts no endpoint. Environments are registered under
// ~/.config/outfit/remotes/<name>/ (by `outfit remote bootstrap`, or by hand).
func cmdRemoteList(args []string) error {
	fs := flag.NewFlagSet("remote ls", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	envs, err := remote.ListEnvironments()
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		fmt.Println("No remote environments registered. Register one with `outfit remote bootstrap`.")
		return nil
	}
	for _, e := range envs {
		if !e.OK {
			fmt.Printf("%s\t(missing or unreadable remote.json)\n", e.Name)
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

func cmdRemoteKeep(args []string) error {
	fs := flag.NewFlagSet("remote keep", flag.ContinueOnError)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: outfit remote keep <duration> [path]")
	}
	durationStr := rest[0]
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", durationStr, err)
	}
	if d <= 0 {
		return fmt.Errorf("duration must be positive, got %q", durationStr)
	}
	// The Outfit path is the second positional, after the duration.
	outfitPath := ""
	if len(rest) > 1 {
		outfitPath = rest[1]
	}
	cfg, err := resolveRemoteConfig(outfitPath)
	if err != nil {
		return err
	}
	retainUntil := time.Now().Add(d)
	_, err = remote.Keep(context.Background(), cfg, retainUntil)
	if err != nil {
		return err
	}
	fmt.Printf("retain until: %s (in %s)\n", retainUntil.Format(time.RFC3339), d)
	return nil
}

func cmdRemotePause(args []string) error {
	fs := flag.NewFlagSet("remote pause", flag.ContinueOnError)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}
	resp, err := remote.Pause(context.Background(), cfg)
	if err != nil {
		return err
	}
	// State is "stopping" (EC2 has not finished) or "stopped" (it already
	// was); either way the instance is re-wakeable, and the control plane's
	// sweep terminates it once the stop retention passes.
	fmt.Printf("state: %s\n", resp.State)
	fmt.Println("the endpoint can be re-woken with `outfit remote start`, or terminated now with `outfit remote stop`")
	return nil
}

func cmdRemoteStop(args []string) error {
	fs := flag.NewFlagSet("remote stop", flag.ContinueOnError)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}
	resp, err := remote.Stop(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("state: %s\n", resp.State)
	return nil
}

func cmdRemoteStatus(args []string) error {
	fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}
	resp, err := remote.Status(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("state: %s\n", resp.State)
	if resp.Healthy != nil {
		fmt.Printf("healthy: %t\n", *resp.Healthy)
	}
	// Version comes from the stats Lambda, which reads the daemon over SSM.
	// Only attempt it when the instance is running — the daemon won't answer
	// when the instance is stopped, so the version is naturally unavailable.
	if resp.State == "running" || resp.State == "ready" {
		if stats, err := remote.Stats(context.Background(), cfg); err == nil && stats.Version != "" {
			fmt.Printf("version: %s\n", stats.Version)
		}
	}
	if resp.BaseURL != "" {
		fmt.Printf("base_url: %s\n", resp.BaseURL)
	}
	if text := lastActiveText(resp.LastActiveAt, resp.IdleSeconds); text != "" {
		fmt.Printf("last active: %s\n", text)
	}
	if resp.RetainUntil != "" {
		fmt.Printf("retain until: %s\n", resp.RetainUntil)
	}
	return nil
}

// cmdRemoteMetrics queries the stats Lambda for instance metrics: token usage,
// GPU, CPU, and RAM utilization. With --format=json it outputs JSON; the
// default is a key-value table. With --cost, it looks up the on-demand price
// for the instance type from the AWS Price List API. With --watch it polls
// every 60 seconds until interrupted.
func cmdRemoteMetrics(args []string) error {
	fs := flag.NewFlagSet("remote metrics", flag.ContinueOnError)
	var (
		withCost bool
		format   string
		watch    bool
	)
	fs.BoolVar(&withCost, "cost", false, "include cost estimate from AWS Price List API")
	fs.StringVar(&format, "format", "bar", "output format: bar (default), table or json")
	fs.BoolVar(&watch, "watch", false, "poll metrics every 60 seconds")
	fs.BoolVar(&watch, "w", false, "shorthand for --watch")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	if format != "table" && format != "json" && format != "bar" {
		return fmt.Errorf("--format must be \"table\", \"bar\", or \"json\", got %q", format)
	}

	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}

	if watch {
		return runMetricsWatch(cfg, format, withCost)
	}
	return runMetricsOnce(context.Background(), cfg, format, withCost, os.Stdout)
}

func runMetricsOnce(ctx context.Context, cfg remote.Config, format string, withCost bool, w io.Writer) error {
	resp, err := remote.Stats(ctx, cfg)
	if err != nil {
		return err
	}

	if format == "json" {
		return formatMetricsJSON(resp, withCost, cfg, w)
	}
	if format == "bar" {
		return formatMetricsBar(resp, cfg, w)
	}
	return formatMetricsTable(ctx, resp, withCost, cfg, w)
}

func runMetricsWatch(cfg remote.Config, format string, withCost bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	first := true
	for {
		// Fetch into a buffer first so the clear-and-render is instant.
		var buf strings.Builder
		if err := runMetricsOnce(ctx, cfg, format, withCost, &buf); err != nil {
			if ctx.Err() != nil {
				return nil
			}
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

func formatMetricsTable(ctx context.Context, resp *remote.StatsResponse, withCost bool, cfg remote.Config, w io.Writer) error {
	fmt.Fprintf(w, "environment:  %s\n", resp.Environment)
	fmt.Fprintf(w, "state:        %s\n", resp.State)

	if resp.State != "running" {
		if resp.Runner != "" {
			fmt.Fprintf(w, "runner:       %s\n", resp.Runner)
		}
		if resp.ModelID != "" {
			fmt.Fprintf(w, "model:        %s\n", resp.ModelID)
		}
		renderLastActiveKeyValue(w, resp.LastActiveAt, resp.IdleSeconds)
		return nil
	}

	if resp.InstanceID != "" {
		fmt.Fprintf(w, "instance:     %s\n", resp.InstanceID)
	}
	if resp.InstanceType != "" {
		fmt.Fprintf(w, "instanceType: %s\n", resp.InstanceType)
	}
	if resp.Runner != "" {
		fmt.Fprintf(w, "runner:       %s\n", resp.Runner)
	}
	if resp.ModelID != "" {
		fmt.Fprintf(w, "model:        %s\n", resp.ModelID)
	}
	if resp.Version != "" {
		fmt.Fprintf(w, "version:      %s\n", resp.Version)
	}
	if resp.UptimeSeconds > 0 {
		fmt.Fprintf(w, "uptime:       %s\n", formatDuration(resp.UptimeSeconds))
	}
	renderLastActiveKeyValue(w, resp.LastActiveAt, resp.IdleSeconds)

	renderTokenLines(w, resp.Tokens)
	renderGPUTable(w, resp.GPUs)
	renderCPUMemTable(w, resp.CPU, resp.Memory)

	if withCost && resp.UptimeSeconds > 0 && resp.InstanceType != "" {
		if price, err := getOnDemandPrice(ctx, cfg.Region, resp.InstanceType); err == nil {
			hours := float64(resp.UptimeSeconds) / 3600.0
			fmt.Fprintf(w, "  cost so far:  $%.2f (%.4f/hr)\n", hours*price, price)
		}
	}

	renderCollectionErrors(os.Stderr, resp.Errors)

	return nil
}

func formatMetricsJSON(resp *remote.StatsResponse, withCost bool, cfg remote.Config, w io.Writer) error {
	var costInfo *float64
	if withCost && resp.UptimeSeconds > 0 && resp.InstanceType != "" {
		if price, err := getOnDemandPrice(context.Background(), cfg.Region, resp.InstanceType); err == nil {
			hours := float64(resp.UptimeSeconds) / 3600.0
			cost := hours * price
			costInfo = &cost
		}
	}

	if costInfo != nil {
		type output struct {
			remote.StatsResponse
			Cost *float64 `json:"cost"`
		}
		out := output{
			StatsResponse: *resp,
			Cost:          costInfo,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
	} else {
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
	}

	renderCollectionErrors(os.Stderr, resp.Errors)

	return nil
}

func formatMetricsBar(resp *remote.StatsResponse, cfg remote.Config, w io.Writer) error {
	fmt.Fprintf(w, "%s  %s", resp.Environment, resp.State)
	if resp.InstanceType != "" {
		fmt.Fprintf(w, "  %s", resp.InstanceType)
	}
	if resp.ModelID != "" {
		fmt.Fprintf(w, "  %s", resp.ModelID)
	}
	if resp.Version != "" {
		fmt.Fprintf(w, "  %s", resp.Version)
	}
	fmt.Fprintln(w)

	// Before the early return: a stopped endpoint draws no bars, but when it
	// last did work is exactly what a stopped endpoint is worth asking about.
	renderLastActiveIndented(w, resp.LastActiveAt, resp.IdleSeconds)

	if resp.State != "running" {
		return nil
	}

	renderStatBars(w, resp.CPU, resp.Memory, resp.GPUs)
	renderTokenLines(w, resp.Tokens)
	renderCollectionErrors(os.Stderr, resp.Errors)

	return nil
}

func renderBar(w io.Writer, label string, pct float64) {
	const width = 25
	colour := "\033[92m"
	if pct > 90 {
		colour = "\033[31m"
	} else if pct >= 80 {
		colour = "\033[33m"
	}
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	fmt.Fprintf(w, "  %-9s ", label)
	fmt.Fprintf(w, "%s", colour)
	for i := 0; i < filled; i++ {
		fmt.Fprint(w, "█")
	}
	fmt.Fprintf(w, "\033[0m")
	for i := 0; i < empty; i++ {
		fmt.Fprint(w, "░")
	}
	fmt.Fprintf(w, " %.0f%%\n", pct)
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
	return remote.GetOnDemandPrice(ctx, region, instanceType)
}

// runnerFor maps an Outfit's PROVIDER to the inference runner the cloud should
// run. PROVIDER already names the engine — `outfit serve` starts that engine
// locally, `outfit remote deploy` asks the cloud for the same one — so no
// separate keyword is needed. Providers that are not self-hosted engines have
// nothing to deploy.
func runnerFor(provider string) (string, error) {
	switch provider {
	case "llamacpp", "vllm":
		return provider, nil
	default:
		return "", fmt.Errorf(
			"PROVIDER %q cannot be deployed: remote deploy runs a self-hosted engine, so use llamacpp or vllm",
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
	// Companion weights: the cloud syncs these from S3 and names them at its
	// own paths, so the preset's local paths must not travel. Only the
	// location is cloud-owned — how the engine is asked to use a drafter
	// (--spec-type) stays the user's, exactly as it is for a local run.
	"spec-draft-model": true, "mmproj": true,
	// Parallelism: outfit computes each runner's own flag from
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

// deployConfigFor turns an Outfit (plus its preset, if any) into the config the
// deploy Lambda accepts. The Outfit is the single source of truth: PROVIDER
// picks the runner, MODEL or the preset's hf names the weights, CONTEXT the
// window, ALIAS the served name, and whatever else the preset sets becomes the
// runner's own flags.
//
// A context size is required, because a cloud instance has to be sized before
// it is provisioned. deployConfigWithoutContext is the same derivation for a
// caller where that is not true.
func deployConfigFor(sel outfit.Selection, outfitPath string) (remote.DeployConfig, error) {
	return deployConfig(sel, outfitPath, deployTarget{
		requireContext: true,
		seedsWeights:   true,
		owns:           isCloudOwned,
	})
}

// deployConfigForNode derives the same config for a machine that already
// exists — a fleet node being woken. Two things differ from a cloud
// deployment, both because the machine is the operator's rather than the
// deployment's: sizing falls back to the engine's own default, so an Outfit
// that `outfit serve` runs happily needs no CONTEXT added merely to be routed;
// and the preset's bind survives, so an engine told to listen on 0.0.0.0 does.
func deployConfigForNode(sel outfit.Selection, outfitPath string) (remote.DeployConfig, error) {
	return deployConfig(sel, outfitPath, deployTarget{owns: isNodeOwned})
}

// deployTarget is what the derivation cannot decide for itself: whether a
// context size is required, which preset flags the destination assigns, and
// whether it fetches the weights itself (and so needs companions named).
type deployTarget struct {
	requireContext bool
	seedsWeights   bool
	owns           func(key string) bool
}

func deployConfig(sel outfit.Selection, outfitPath string, target deployTarget) (remote.DeployConfig, error) {
	var dc remote.DeployConfig

	runner, err := runnerFor(sel.Provider)
	if err != nil {
		return dc, err
	}
	dc.Runner = runner

	// The preset supplies the model and the runner flags when the Outfit does
	// not state them, so a single preset can drive both serve and deploy. The
	// [*] globals are a separate layer that the chosen section overrides —
	// exactly as preset.Args does for a local serve — so settings written there
	// (commonly ngl and jinja) are not lost.
	var global, params []preset.Param
	if sel.Preset != "" {
		presetPath, err := resolvePresetPath(sel.Preset, outfitPath)
		if err != nil {
			return dc, err
		}
		data, err := outfitsrc.Fetch(presetPath)
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
		model = presetValue("hf", global, params)
	}
	if model == "" {
		return dc, fmt.Errorf(
			"nothing to deploy: set MODEL (an HF repo like org/model:QUANT) in %s, or hf in its preset",
			outfitPath)
	}
	if isModelPath(model) {
		return dc, fmt.Errorf(
			"cannot deploy the local model file %q: the cloud downloads weights from Hugging Face, so name a repo (org/model:QUANT)",
			model)
	}
	dc.ModelID, dc.Quant = splitModelQuant(model)

	context := sel.Context
	if context == "" {
		context = presetValue("ctx-size", global, params)
	}
	if context == "" && target.requireContext {
		return dc, fmt.Errorf("no context size: set CONTEXT in %s, or ctx-size in its preset", outfitPath)
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

	dc.ServeArgs = preset.Flags(dropOwned(target.owns, global), dropOwned(target.owns, params))
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
	deployDiscoverFn   = remote.DiscoverControlPlane
	remoteDeployFn     = remote.Deploy
	remoteStatusFn     = remote.Status
	detectPublicCIDRFn = detectPublicCIDR
)

// metricsWatchInterval is the polling interval for --watch mode.
// Tests override this to avoid sleeping 60 seconds.
var metricsWatchInterval = 60 * time.Second

func cmdRemoteDeploy(args []string) error {
	fs := flag.NewFlagSet("remote deploy", flag.ContinueOnError)
	var (
		dryRun      bool
		overwrite   bool
		reseed      bool
		allowedCidr string
		region      string
	)
	fs.BoolVar(&dryRun, "dry-run", false, "print the config that would be deployed, without sending it")
	fs.BoolVar(&dryRun, "n", false, "print the config without sending it (shorthand)")
	fs.BoolVar(&overwrite, "overwrite", false, "proceed against an already-registered or live environment")
	fs.BoolVar(&reseed, "reseed", false, "re-fetch the weights even if they are already in S3 (starts a ~20-minute seed)")
	fs.StringVar(&allowedCidr, "allowed-cidr", "", "who may reach this environment's instance (default: your public IP as a /32, on first deploy)")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}

	// deploy reads the Outfit for what to serve, so unlike the other
	// subcommands it always needs one — the per-user remote config alone is not
	// enough.
	sel, outfitPath, err := readOutfit("outfit remote deploy <file>", outfitArg(fs))
	if err != nil {
		return err
	}
	// Respect the Outfit's local environment (.env beside it, then its ENV
	// lines) before any AWS work, so the credentials the deploy signs with, the
	// region, and the OUTFIT_REMOTE_* overrides all see it. ENV stays local — it
	// never enters dc, so nothing here reaches the deployed instance.
	if err := applyOutfitEnv(sel, outfitPath); err != nil {
		return err
	}
	dc, err := deployConfigFor(sel, outfitPath)
	if err != nil {
		return err
	}
	// The environment name is the Outfit's REMOTE — the committed link between
	// the Outfit and its deployment. One source of truth: deploy registers the
	// environment under exactly the name the same Outfit's REMOTE resolves to.
	env := sel.Remote
	if env == "" || !remote.IsEnvName(env) {
		return fmt.Errorf(
			"%s must name its environment with `REMOTE <name>` (e.g. REMOTE %s) — deploy creates and registers that environment",
			outfitPath, dc.ServedModelName)
	}
	if allowedCidr != "" && !cidrPattern.MatchString(allowedCidr) {
		return fmt.Errorf("--allowed-cidr must be an IPv4 CIDR (e.g. 203.0.113.7/32), got %q", allowedCidr)
	}

	fmt.Printf("Deploying from %s\n", outfitPath)
	fmt.Printf("  environment: %s\n", env)
	fmt.Printf("  runner:  %s\n", dc.Runner)
	fmt.Printf("  model:   %s", dc.ModelID)
	if dc.Quant != "" {
		fmt.Printf(" (%s)", dc.Quant)
	}
	fmt.Println()
	fmt.Printf("  context: %d\n", dc.ContextSize)
	if dc.Parallel > 0 {
		fmt.Printf("  parallel: %d\n", dc.Parallel)
	}
	fmt.Printf("  served:  %s\n", dc.ServedModelName)
	// Companions are easy to get wrong quietly — a renamed file yields no
	// drafter and a slower endpoint with no error — so show what was picked up.
	for _, role := range slices.Sorted(maps.Keys(dc.Companions)) {
		fmt.Printf("  %-8s %s\n", role+":", dc.Companions[role])
	}
	if len(dc.ServeArgs) > 0 {
		fmt.Printf("  args:    %s\n", strings.Join(dc.ServeArgs, " "))
	}
	// Worth stating: a re-seed costs a ~20-minute instance and re-downloads the
	// weights, so --reseed --dry-run must not look like a plain deploy.
	if reseed {
		fmt.Println("  reseed:  yes — the weights will be re-fetched even if already in S3")
	}
	if dryRun {
		return nil
	}

	// The control URLs come from the control plane's stack outputs — the
	// environment may not exist yet, so there is nothing local to resolve.
	ctx := context.Background()
	awsCfg, err := remote.LoadAWSConfig(ctx, resolveRegion(region))
	if err != nil {
		return err
	}
	layer, err := deployDiscoverFn(ctx, awsCfg, controlPlaneStackName)
	if err != nil {
		return err
	}
	cfg := layer.Config
	cfg.Environment = env

	// Refuse to clobber silently: an environment that is already registered, or
	// whose instance is live, needs explicit consent to redeploy over.
	envConfigPath, err := remote.EnvConfigPath(env)
	if err != nil {
		return err
	}
	registered := false
	if _, err := os.Stat(envConfigPath); err == nil {
		registered = true
	}
	live := false
	if status, err := remoteStatusFn(ctx, cfg); err == nil {
		live = status.State == "running" || status.State == "pending" || status.State == "starting"
	}
	if (registered || live) && !overwrite {
		what := "is already registered"
		if live {
			what = "has a live instance"
		}
		return fmt.Errorf(
			"environment %q %s — pass --overwrite to redeploy over it", env, what)
	}

	// Ingress is per environment. A fresh environment needs a CIDR (default:
	// the caller's public address); an existing one keeps its ingress unless a
	// CIDR is given explicitly.
	if allowedCidr == "" && !registered {
		allowedCidr, err = detectPublicCIDRFn(ctx)
		if err != nil {
			return fmt.Errorf("detecting your public IP for the allowed CIDR: %w (pass --allowed-cidr)", err)
		}
		fmt.Printf("  ingress: %s (your public IP; override with --allowed-cidr)\n", allowedCidr)
	}

	resp, err := remoteDeployFn(ctx, cfg, dc, allowedCidr, reseed)
	if err != nil {
		return err
	}

	// Register the environment so REMOTE <env> (and the other remote
	// subcommands) resolve to it from now on.
	cfg.BaseURL = resp.BaseURL
	if err := remote.SaveEnvironment(env, cfg); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("deployed: environment %s at %s\n", env, resp.BaseURL)
	fmt.Printf("registered: %s\n", envConfigPath)
	if resp.Seeding {
		fmt.Printf("seeding the weights on %s — this takes ~15-20 min.\n", resp.SeedInstanceID)
		fmt.Println("Wait for it to finish before `outfit remote start`, or the instance will")
		fmt.Println("start against an incomplete download.")
	} else {
		fmt.Println("weights already in place — `outfit remote start` will serve this.")
	}
	return nil
}

// cidrPattern matches an IPv4 CIDR, the same shape the deploy Lambda accepts.
var cidrPattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/\d{1,2}$`)

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
