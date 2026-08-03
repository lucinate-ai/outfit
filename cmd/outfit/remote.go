package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/preset"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemote dispatches the remote subcommands, which control the
// scale-to-zero GPU inference instance defined in this repo's remote/:
// start boots it and prints the endpoint exports, stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), status reports instance state and endpoint health, and deploy sets
// what the instance will serve from the Outfit itself. Each subcommand takes
// an optional Outfit path; see resolveRemoteConfig for how the remote config
// is found.
func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote <bootstrap|start|stop|status|stats|deploy|env|ls> [path]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "bootstrap":
		return cmdRemoteBootstrap(rest)
	case "start":
		return cmdRemoteStart(rest)
	case "stop":
		return cmdRemoteStop(rest)
	case "status":
		return cmdRemoteStatus(rest)
	case "stats":
		return cmdRemoteStats(rest)
	case "deploy":
		return cmdRemoteDeploy(rest)
	case "env":
		return cmdRemoteEnv(rest)
	case "ls":
		return cmdRemoteList(rest)
	default:
		return fmt.Errorf(
			"unknown remote subcommand %q (expected bootstrap, start, stop, status, stats, deploy, env or ls)", sub)
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
// fields alone. dir is the Outfit's own directory, where its .env lives.
func applyOutfitEnv(sel outfit.Selection, dir string) error {
	vars, err := opencode.ParseEnvFile(filepath.Join(dir, ".env"))
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
// directory, so an Outfit and its remote config travel together — the same
// rule PRESET uses.
func resolveRemoteConfig(outfitArg string) (remote.Config, error) {
	if outfitArg != "" {
		sel, outfitPath, err := readOutfit("remote", outfitArg)
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote == "" {
			return remote.Config{}, fmt.Errorf("%s has no REMOTE instruction", outfitPath)
		}
		if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
			return remote.Config{}, err
		}
		return remote.LoadConfigFile(resolveRemotePath(sel.Remote, filepath.Dir(outfitPath)), os.Getenv)
	}
	if defaultOutfitExists() {
		sel, outfitPath, err := readOutfit("remote", "")
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote != "" {
			if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
				return remote.Config{}, err
			}
			return remote.LoadConfigFile(resolveRemotePath(sel.Remote, filepath.Dir(outfitPath)), os.Getenv)
		}
	}
	return remote.LoadDefault(os.Getenv)
}

// resolveRemotePath turns an Outfit's REMOTE value into the config file to read.
// A bare name selects an environment from the per-user registry; a path is
// resolved as a file, relative to the Outfit's directory when not absolute —
// the same rule PRESET uses. Both the control commands and apply's base-URL
// lookup go through here, so the two never diverge.
func resolveRemotePath(remoteValue, outfitDir string) string {
	if remote.IsEnvName(remoteValue) {
		return remote.EnvConfigPath(remoteValue)
	}
	return remoteConfigPath(remoteValue, outfitDir)
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

func remoteConfigPath(remoteValue, outfitDir string) string {
	if filepath.IsAbs(remoteValue) {
		return remoteValue
	}
	return filepath.Join(outfitDir, remoteValue)
}

// remoteConfig reads the remote config an Outfit's REMOTE names — a registry
// environment or a file, per resolveRemotePath. A config that is absent yields
// the zero Config rather than an error, since an Outfit may name a remote config
// before the deployment that writes it exists; only a real read or parse failure
// is reported.
func remoteConfig(remoteValue, outfitDir string) (remote.Config, error) {
	path := resolveRemotePath(remoteValue, outfitDir)
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
func remoteBaseURL(remoteValue, outfitDir string) (string, error) {
	cfg, err := remoteConfig(remoteValue, outfitDir)
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
func remoteEnvName(remoteValue, outfitDir string) (string, error) {
	if remoteValue == "" {
		return "", nil
	}
	if remote.IsEnvName(remoteValue) {
		return remoteValue, nil
	}
	cfg, err := remoteConfig(remoteValue, outfitDir)
	if err != nil {
		return "", err
	}
	return cfg.Environment, nil
}

// resolveRemoteConfigForOutfit resolves the remote config for an Outfit's
// REMOTE value, given the Outfit's directory. Unlike resolveRemoteConfig it
// does not consult the working directory or the per-user fallback — the REMOTE
// is already known from the parsed Outfit, so it goes straight to resolving
// that path.
func resolveRemoteConfigForOutfit(remoteValue, outfitDir string) (remote.Config, error) {
	path := resolveRemotePath(remoteValue, outfitDir)
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
// so without this the command looks hung.
const heartbeatEvery = 30 * time.Second

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

// printRemoteEnv prints the remote endpoint's environment variables as shell
// export lines to stdout, suitable for eval.
func printRemoteEnv(resp *remote.Response) {
	fmt.Printf("export OPENAI_BASE_URL=%s\n", resp.BaseURL)
	fmt.Printf("export OPENAI_API_KEY=%s\n", resp.APIKey)
}

func cmdRemoteEnv(args []string) error {
	fs := flag.NewFlagSet("remote env", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
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

func cmdRemoteStart(args []string) error {
	fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
	var timeout time.Duration
	const timeoutUsage = "overall time to wait for the endpoint"
	fs.DurationVar(&timeout, "timeout", 15*time.Minute, timeoutUsage)
	fs.DurationVar(&timeout, "t", 15*time.Minute, timeoutUsage+" (shorthand)")
	var printEnv bool
	fs.BoolVar(&printEnv, "env", false, "print export lines to stdout for eval")
	fs.BoolVar(&printEnv, "e", false, "print export lines to stdout for eval (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}

	progress := newStartProgress(heartbeatEvery)
	defer progress.close()
	progress.line(fmt.Sprintf(
		"Starting the endpoint. It boots, fetches the weights, and loads them into the GPU,\nwhich takes several minutes from cold; waiting up to %s.", timeout))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err = remote.Start(ctx, cfg, progress.line, progress.setState)
	if err != nil {
		return err
	}
	progress.close()
	progress.line(fmt.Sprintf("ready after %s", progress.elapsed()))

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

func cmdRemoteStop(args []string) error {
	fs := flag.NewFlagSet("remote stop", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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
	if resp.BaseURL != "" {
		fmt.Printf("base_url: %s\n", resp.BaseURL)
	}
	return nil
}

// cmdRemoteStats queries the stats Lambda for instance metrics: token usage,
// GPU, CPU, and RAM utilization. Output is a key-value table to stdout;
// progress and errors go to stderr. With --cost, it looks up the on-demand
// price for the instance type from the AWS Price List API.
func cmdRemoteStats(args []string) error {
	fs := flag.NewFlagSet("remote stats", flag.ContinueOnError)
	var withCost bool
	fs.BoolVar(&withCost, "cost", false, "include cost estimate from AWS Price List API")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}

	ctx := context.Background()
	resp, err := remote.Stats(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("environment:  %s\n", resp.Environment)
	fmt.Printf("state:        %s\n", resp.State)

	if resp.State != "running" {
		if resp.Runner != "" {
			fmt.Printf("runner:       %s\n", resp.Runner)
		}
		if resp.ModelID != "" {
			fmt.Printf("model:        %s\n", resp.ModelID)
		}
		return nil
	}

	// Running — show instance and metrics.
	if resp.InstanceID != "" {
		fmt.Printf("instance:     %s\n", resp.InstanceID)
	}
	if resp.InstanceType != "" {
		fmt.Printf("instanceType: %s\n", resp.InstanceType)
	}
	if resp.Runner != "" {
		fmt.Printf("runner:       %s\n", resp.Runner)
	}
	if resp.ModelID != "" {
		fmt.Printf("model:        %s\n", resp.ModelID)
	}
	if resp.UptimeSeconds > 0 {
		fmt.Printf("uptime:       %s\n", formatDuration(resp.UptimeSeconds))
	}

	// Token metrics.
	if resp.Tokens != nil {
		fmt.Println()
		fmt.Printf("  running:          %d\n", resp.Tokens.Running)
		fmt.Printf("  prompt tokens:    %d\n", resp.Tokens.PromptTokens)
		fmt.Printf("  generation tokens: %d\n", resp.Tokens.GenerationTokens)
		fmt.Printf("  requests:         %d\n", resp.Tokens.Requests)
	}

	// GPU stats.
	if len(resp.GPUs) > 0 {
		fmt.Println()
		for _, g := range resp.GPUs {
			memUsed := formatBytes(g.MemoryUsed)
			memTotal := formatBytes(g.MemoryTotal)
			fmt.Printf("  GPU %d: %s  util=%d%%  mem=%s/%s  temp=%dC\n",
				g.Index, g.Name, g.Utilization, memUsed, memTotal, g.Temperature)
		}
		// Aggregate for multi-GPU.
		if len(resp.GPUs) > 1 {
			var totalUtil, totalMemUsed, totalMemTotal int64
			for _, g := range resp.GPUs {
				totalUtil += int64(g.Utilization)
				totalMemUsed += g.MemoryUsed
				totalMemTotal += g.MemoryTotal
			}
			avgUtil := int(totalUtil) / len(resp.GPUs)
			fmt.Printf("  avg util: %d%%  total mem: %s/%s\n",
				avgUtil, formatBytes(totalMemUsed), formatBytes(totalMemTotal))
		}
	}

	// CPU.
	if resp.CPU != nil {
		fmt.Println()
		fmt.Printf("  CPU: %.0f%% util\n", resp.CPU.Utilization)
	}

	// Memory.
	if resp.Memory != nil {
		memUsed := formatBytes(resp.Memory.Used)
		memTotal := formatBytes(resp.Memory.Total)
		pct := float64(resp.Memory.Used) / float64(resp.Memory.Total) * 100
		fmt.Printf("  RAM: %s/%s (%.0f%%)\n", memUsed, memTotal, pct)
	}

	// Cost estimate.
	if withCost && resp.UptimeSeconds > 0 {
		if price, err := getOnDemandPrice(ctx, cfg.Region, "g6e.xlarge"); err == nil {
			hours := float64(resp.UptimeSeconds) / 3600.0
			fmt.Printf("  cost so far:  $%.2f (%.4f/hr)\n", hours*price, price)
		}
	}

	// Errors (to stderr).
	if len(resp.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "metric collection errors:")
		for _, e := range resp.Errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}

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
}

// isCloudOwned reports whether the cloud sets this preset key itself.
func isCloudOwned(key string) bool {
	return cloudOwnedFlags[preset.CanonicalKey(key)]
}

// dropCloudOwned returns the params the deployment should keep.
func dropCloudOwned(params []preset.Param) []preset.Param {
	var kept []preset.Param
	for _, p := range params {
		if !isCloudOwned(p.Key) {
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
func deployConfigFor(sel outfit.Selection, outfitPath string) (remote.DeployConfig, error) {
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
		presetPath := resolvePresetPath(sel.Preset, outfitPath)
		data, err := os.ReadFile(presetPath)
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
	if context == "" {
		return dc, fmt.Errorf("no context size: set CONTEXT in %s, or ctx-size in its preset", outfitPath)
	}
	n, err := contextsize.Parse(context)
	if err != nil {
		return dc, err
	}
	dc.ContextSize = n

	// The served name is what a coding agent asks for. ALIAS is the friendly
	// name; without one the repo id is served under its own name.
	dc.ServedModelName = sel.Alias
	if dc.ServedModelName == "" {
		dc.ServedModelName = dc.ModelID
	}

	dc.ServeArgs = preset.Flags(dropCloudOwned(global), dropCloudOwned(params))
	if dc.ServeArgs == nil {
		dc.ServeArgs = []string{}
	}
	return dc, nil
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
	deployDiscoverFn   = remote.DiscoverSharedLayer
	remoteDeployFn     = remote.Deploy
	remoteStatusFn     = remote.Status
	detectPublicCIDRFn = detectPublicCIDR
)

func cmdRemoteDeploy(args []string) error {
	fs := flag.NewFlagSet("remote deploy", flag.ContinueOnError)
	var (
		dryRun      bool
		overwrite   bool
		allowedCidr string
		region      string
	)
	fs.BoolVar(&dryRun, "dry-run", false, "print the config that would be deployed, without sending it")
	fs.BoolVar(&dryRun, "n", false, "print the config without sending it (shorthand)")
	fs.BoolVar(&overwrite, "overwrite", false, "proceed against an already-registered or live environment")
	fs.StringVar(&allowedCidr, "allowed-cidr", "", "who may reach this environment's instance (default: your public IP as a /32, on first deploy)")
	fs.StringVar(&region, "region", "", "AWS region of the shared layer (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(args); err != nil {
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
	if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
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
	fmt.Printf("  served:  %s\n", dc.ServedModelName)
	if len(dc.ServeArgs) > 0 {
		fmt.Printf("  args:    %s\n", strings.Join(dc.ServeArgs, " "))
	}
	if dryRun {
		return nil
	}

	// The control URLs come from the shared layer's stack outputs — the
	// environment may not exist yet, so there is nothing local to resolve.
	ctx := context.Background()
	awsCfg, err := remote.LoadAWSConfig(ctx, resolveRegion(region))
	if err != nil {
		return err
	}
	layer, err := deployDiscoverFn(ctx, awsCfg, sharedStackName)
	if err != nil {
		return err
	}
	cfg := layer.Config
	cfg.Environment = env

	// Refuse to clobber silently: an environment that is already registered, or
	// whose instance is live, needs explicit consent to redeploy over.
	registered := false
	if _, err := os.Stat(remote.EnvConfigPath(env)); err == nil {
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

	resp, err := remoteDeployFn(ctx, cfg, dc, allowedCidr)
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
	fmt.Printf("registered: %s\n", remote.EnvConfigPath(env))
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
