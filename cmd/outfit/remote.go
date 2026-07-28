package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/preset"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemote dispatches the remote subcommands, which control the
// scale-to-zero GPU inference instance defined in the cloud-vm-llm repo:
// start boots it and prints the endpoint exports, stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), status reports instance state and endpoint health, and deploy sets
// what the instance will serve from the Outfit itself. Each subcommand takes
// an optional Outfit path; see resolveRemoteConfig for how the remote config
// is found.
func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote <start|stop|status|deploy> [path]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return cmdRemoteStart(rest)
	case "stop":
		return cmdRemoteStop(rest)
	case "status":
		return cmdRemoteStatus(rest)
	case "deploy":
		return cmdRemoteDeploy(rest)
	default:
		return fmt.Errorf(
			"unknown remote subcommand %q (expected start, stop, status or deploy)", sub)
	}
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
		return remote.LoadConfigFile(remoteConfigPath(sel.Remote, outfitPath), os.Getenv)
	}
	if defaultOutfitExists() {
		sel, outfitPath, err := readOutfit("remote", "")
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote != "" {
			return remote.LoadConfigFile(remoteConfigPath(sel.Remote, outfitPath), os.Getenv)
		}
	}
	return remote.LoadConfig(os.Getenv)
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

func remoteConfigPath(remoteValue, outfitPath string) string {
	if filepath.IsAbs(remoteValue) {
		return remoteValue
	}
	return filepath.Join(filepath.Dir(outfitPath), remoteValue)
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
				p.line(fmt.Sprintf("still starting (%s elapsed)", p.elapsed()))
			}
		}
	}()
	return p
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

func cmdRemoteStart(args []string) error {
	fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
	var timeout time.Duration
	fs.DurationVar(&timeout, "timeout", 15*time.Minute, "overall time to wait for the endpoint")
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
	resp, err := remote.Start(ctx, cfg, progress.line)
	if err != nil {
		return err
	}
	progress.close()
	progress.line(fmt.Sprintf("ready after %s", progress.elapsed()))

	// stdout carries only the result, so `eval "$(outfit remote start)"` works.
	fmt.Printf("export OPENAI_BASE_URL=%s\n", resp.BaseURL)
	fmt.Printf("export OPENAI_API_KEY=%s\n", resp.APIKey)
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

func cmdRemoteDeploy(args []string) error {
	fs := flag.NewFlagSet("remote deploy", flag.ContinueOnError)
	var dryRun bool
	fs.BoolVar(&dryRun, "dry-run", false, "print the config that would be deployed, without sending it")
	fs.BoolVar(&dryRun, "n", false, "print the config without sending it (shorthand)")
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
	dc, err := deployConfigFor(sel, outfitPath)
	if err != nil {
		return err
	}

	fmt.Printf("Deploying from %s\n", outfitPath)
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

	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}
	resp, err := remote.Deploy(context.Background(), cfg, dc)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("deployed")
	if resp.Seeding {
		fmt.Printf("seeding the weights on %s — this takes ~15-20 min.\n", resp.SeedInstanceID)
		fmt.Println("Wait for it to finish before `outfit remote start`, or the instance will")
		fmt.Println("start against an incomplete download.")
	} else {
		fmt.Println("weights already in place — `outfit remote start` will serve this.")
	}
	return nil
}
