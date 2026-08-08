// Serve: launching a local inference server for an Outfit. The engine is chosen
// by the Outfit's PROVIDER, the same way `outfit remote deploy` picks a cloud
// runner, so one file describes both what dresses the harness and what serves
// it. Kept out of main.go so the dispatch-coverage scan in complete_test.go only
// ever sees run()'s own switch.

package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/preset"
)

// llamaServerBinary is the llama.cpp server executable that `serve` launches.
// It is a package var so tests can point it at a stub instead of a real build.
var llamaServerBinary = "llama-server"

// omlxBinary is the oMLX executable that `serve` launches. Empty means "look it
// up"; tests set it to a stub, exactly as they do for llamaServerBinary.
var omlxBinary = ""

// vllmBinary is the vLLM executable that `serve` launches. A package var so
// tests can point it at a stub instead of a real install.
var vllmBinary = "vllm"

// omlxBundleBinary is where the macOS app installs its CLI. oMLX ships as a
// signed app rather than a PATH install, so a user who has only ever launched
// it from the menu bar still has this and nothing on their PATH.
const omlxBundleBinary = "/Applications/oMLX.app/Contents/MacOS/omlx-cli"

// resolveOMLXBinary finds the oMLX CLI: an explicit override wins, then the
// PATH, then the app bundle. The bundle path is returned unchecked when nothing
// else matches, so the failure surfaces as serve's usual not-found hint naming
// a concrete path rather than as a bare "omlx-cli".
func resolveOMLXBinary() string {
	if omlxBinary != "" {
		return omlxBinary
	}
	if p, err := exec.LookPath("omlx-cli"); err == nil {
		return p
	}
	return omlxBundleBinary
}

// serveEngine is a local inference server `outfit serve` can launch: which
// binary to run, the subcommand it needs, the dialect its preset is written in,
// and how the Outfit's own instructions turn into its flags.
type serveEngine struct {
	// binary is resolved late so tests can stub it after engineFor has run.
	binary     func() string
	subcommand []string
	dialect    preset.Dialect
	// params turns the Outfit's instructions into this engine's flags.
	params func(outfit.Selection) ([]preset.Param, error)
	// needsModel marks an engine that cannot start without being told what to
	// load. oMLX serves a whole model directory and picks per request, so it
	// starts happily with neither a PRESET nor a MODEL.
	needsModel bool
	// installHint completes "<binary> not found — ...".
	installHint string
	// metricsArgs switches the engine's own /metrics endpoint on. Appended
	// only for a supervised engine (daemon or --api serve) — a plain
	// foreground serve runs exactly the command it always has.
	metricsArgs []string
	// metricsEngine names the Prometheus dialect the engine speaks, for the
	// scraper; empty means the engine has no metrics endpoint to scrape.
	metricsEngine string
	// defaultBaseURL is where the engine listens when the Outfit states no
	// BASEURL, so the scraper can still find /metrics.
	defaultBaseURL string
	// positional yields arguments placed directly after the subcommand —
	// vLLM takes its model positionally rather than behind a flag.
	positional func(sel outfit.Selection) []string
}

// engineFor maps an Outfit's PROVIDER to the engine `serve` launches locally.
// It is the local twin of runnerFor: PROVIDER already names the engine, so no
// separate keyword is needed. Providers that are not self-hosted engines have
// nothing to launch.
func engineFor(provider string) (serveEngine, error) {
	switch provider {
	case "llamacpp":
		return serveEngine{
			binary:         func() string { return llamaServerBinary },
			dialect:        preset.LlamaCpp,
			params:         llamacppServeParams,
			needsModel:     true,
			installHint:    "install llama.cpp (e.g. brew install llama.cpp) or check the path",
			metricsArgs:    []string{"--metrics"},
			metricsEngine:  "llamacpp",
			defaultBaseURL: "http://127.0.0.1:8080",
		}, nil
	case "omlx":
		return serveEngine{
			binary:      resolveOMLXBinary,
			subcommand:  []string{"serve"},
			dialect:     preset.OMLX,
			params:      omlxServeParams,
			installHint: "install oMLX (https://omlx.ai) or check the path",
		}, nil
	case "vllm":
		return serveEngine{
			binary:      func() string { return vllmBinary },
			subcommand:  []string{"serve"},
			dialect:     preset.VLLM,
			params:      vllmServeParams,
			needsModel:  true,
			installHint: "install vLLM (pip install vllm) or check the path",
			// vLLM serves /metrics unconditionally, so no switch to append.
			metricsEngine:  "vllm",
			defaultBaseURL: "http://127.0.0.1:8000",
			positional: func(sel outfit.Selection) []string {
				if sel.Model == "" {
					return nil
				}
				return []string{sel.Model}
			},
		}, nil
	default:
		return serveEngine{}, fmt.Errorf(
			"PROVIDER %q cannot be served locally: serve runs a self-hosted engine, so use llamacpp, omlx or vllm",
			provider)
	}
}

// cmdServe reads an Outfit and runs the inference server its PROVIDER names.
// With a PRESET it turns the matching preset section into the command; without
// one it derives the command from the Outfit's own instructions. Either way it
// prints the command before running it. The Outfit path defaults to ./Outfit.
// Serve is strictly foreground; with --api the control API is served alongside
// the engine. Long-lived supervision is `outfit daemon`'s job.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var (
		dryRun  bool
		apiOn   bool
		apiAddr string
	)
	fs.BoolVar(&dryRun, "dry-run", false, "print the server command without running it")
	fs.BoolVar(&dryRun, "n", false, "print the command without running it (shorthand)")
	fs.BoolVar(&apiOn, "api", false, "expose the control API beside the foreground engine")
	fs.BoolVar(&apiOn, "a", false, "expose the control API (shorthand)")
	fs.StringVar(&apiAddr, "api-addr", daemon.DefaultAPIAddr, "control API listen address")
	if err := fs.Parse(sortFlagsBeforeArgs(args)); err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	sel, outfitPath, err := readOutfit("outfit serve <file>", path)
	if err != nil {
		return err
	}
	engine, err := engineFor(sel.Provider)
	if err != nil {
		return err
	}
	argv, err := buildServeArgv(engine, sel, outfitPath)
	if err != nil {
		return err
	}
	if apiOn {
		// A supervised engine gets its metrics endpoint switched on, exactly
		// as the cloud path does for a deployed one.
		argv = withMetricsArgs(argv, engine)
	}

	fmt.Printf("%s\n\n", preset.FormatCommand(argv))
	if dryRun {
		return nil
	}

	if apiOn {
		return runServeForegroundAPI(sel, outfitPath, engine, argv, apiAddr)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found — %s", argv[0], engine.installHint)
		}
		return err
	}
	return nil
}

// buildServeArgv turns an Outfit into the engine command, from its PRESET
// section when it names one and from the Outfit's own instructions otherwise,
// narrating which source it used. It is the single construction both a
// foreground serve and the daemon's Outfit-sourced starts run through.
func buildServeArgv(engine serveEngine, sel outfit.Selection, outfitPath string) ([]string, error) {
	// Anything the Outfit states overrides the preset's own values.
	params, err := engine.params(sel)
	if err != nil {
		return nil, err
	}

	binary := engine.binary()
	// A positional-model engine takes the model right after its subcommand,
	// before any flags — riding along with the subcommand puts it there in
	// both the preset and preset-less builds.
	subcommand := engine.subcommand
	if engine.positional != nil {
		subcommand = append(append([]string{}, subcommand...), engine.positional(sel)...)
	}
	var argv []string
	if sel.Preset != "" {
		presetPath := resolvePresetPath(sel.Preset, outfitPath)
		data, err := os.ReadFile(presetPath)
		if err != nil {
			return nil, fmt.Errorf("reading preset %s: %w", presetPath, err)
		}
		pre, err := preset.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", presetPath, err)
		}
		// The preset's sections are named by the friendly ALIAS, not the
		// provider-native MODEL, so that is what selects one.
		sec, err := pre.Select(sel.Alias)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", presetPath, err)
		}
		argv = pre.CommandIn(engine.dialect, binary, subcommand, sec, params)
		fmt.Printf("Using preset %s (model %s)\n\n", presetPath, sec.Name)
	} else {
		if engine.needsModel && sel.Model == "" {
			return nil, fmt.Errorf("serve needs a PRESET or a MODEL (an HF repo like org/model:quant, or a path to a .gguf)")
		}
		argv = append([]string{binary}, subcommand...)
		argv = append(argv, engine.dialect.Flags(params)...)
		if sel.Model != "" {
			fmt.Printf("Serving %s from %s\n\n", sel.Model, outfitPath)
		} else {
			// An engine that needs no model to start (oMLX serves a whole
			// directory) has nothing to name but itself.
			fmt.Printf("Starting %s from %s\n\n", sel.Provider, outfitPath)
		}
	}
	return argv, nil
}

// resolvePresetPath resolves an Outfit's PRESET value: a relative one is taken
// against the Outfit's own directory, so an Outfit and its preset travel
// together (the same rule REMOTE uses).
func resolvePresetPath(presetValue, outfitPath string) string {
	if filepath.IsAbs(presetValue) {
		return presetValue
	}
	return filepath.Join(filepath.Dir(outfitPath), presetValue)
}

// vllmServeParams turns the vLLM settings an Outfit states into preset
// params. The model is deliberately absent — `vllm serve` takes it as its
// positional argument (see the engine's positional func). ALIAS names what is
// served, CONTEXT caps the model length, BASEURL binds the address.
func vllmServeParams(sel outfit.Selection) ([]preset.Param, error) {
	var params []preset.Param
	if sel.Alias != "" {
		params = append(params, preset.Param{Key: "served-model-name", Value: sel.Alias})
	}
	if sel.Context != "" {
		n, err := contextsize.Parse(sel.Context)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "max-model-len", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// omlxServeParams turns the oMLX settings an Outfit states into preset params.
// Only the bind address maps: oMLX serves a whole --model-dir and picks the
// model per request, so MODEL and ALIAS keep their usual job of naming what the
// harness asks for, and CONTEXT sizes the harness's window rather than the
// server (oMLX has no context flag). Everything else — the model directory,
// memory guard, SSD cache — comes from the PRESET or from oMLX's own settings.
//
// The API key is deliberately not passed: serve prints the command it runs, and
// oMLX takes its key as a command-line flag, so passing one would put the secret
// on screen and in the process table. Auth belongs in oMLX's own settings.
func omlxServeParams(sel outfit.Selection) ([]preset.Param, error) {
	return bindAddressParams(sel)
}

// llamacppServeParams turns the llama-server settings an Outfit states into
// preset params: the provider-native MODEL supplies the model source (hf for a
// Hugging Face repo, model for a .gguf path); ALIAS, CONTEXT, and BASEURL fill
// in the rest. They seed a preset-less command and, with a preset, override its
// values.
func llamacppServeParams(sel outfit.Selection) ([]preset.Param, error) {
	var params []preset.Param
	if sel.Model != "" {
		if isModelPath(sel.Model) {
			params = append(params, preset.Param{Key: "model", Value: sel.Model})
		} else {
			params = append(params, preset.Param{Key: "hf", Value: sel.Model})
		}
	}
	if sel.Alias != "" {
		params = append(params, preset.Param{Key: "alias", Value: sel.Alias})
	}
	if sel.Context != "" {
		n, err := contextsize.Parse(sel.Context)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "ctx-size", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// bindAddressParams turns an Outfit's BASEURL into the host and port flags that
// bind a server to the endpoint the harness will call. Both engines spell these
// the same way. With no BASEURL it yields nothing, so the server's own defaults
// stand.
func bindAddressParams(sel outfit.Selection) ([]preset.Param, error) {
	if sel.BaseURL == "" {
		return nil, nil
	}
	host, port, err := hostPortFromURL(sel.BaseURL)
	if err != nil {
		return nil, err
	}
	var params []preset.Param
	if host != "" {
		params = append(params, preset.Param{Key: "host", Value: host})
	}
	if port != "" {
		params = append(params, preset.Param{Key: "port", Value: port})
	}
	return params, nil
}

// isModelPath reports whether a MODEL value is a local file rather than a
// Hugging Face repo: an absolute or explicitly-relative path, a home-relative
// path, or anything ending in .gguf. Everything else is treated as org/model.
func isModelPath(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), ".gguf") {
		return true
	}
	return strings.HasPrefix(model, "/") ||
		strings.HasPrefix(model, "./") ||
		strings.HasPrefix(model, "../") ||
		strings.HasPrefix(model, "~")
}

// hostPortFromURL extracts the host and port from a BASEURL so serve can bind
// llama-server to the same endpoint the harness will call. A bare host:port
// with no scheme is accepted too.
func hostPortFromURL(raw string) (host, port string, err error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid BASEURL %q: %w", raw, err)
	}
	return u.Hostname(), u.Port(), nil
}
