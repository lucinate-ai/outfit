// Serve: launching a local inference server for a Spinloop. The engine is chosen
// by the Spinloop's PROVIDER, the same way `spinloop cloud deploy` picks a cloud
// runner, so one file describes both what dresses the harness and what serves
// it. Kept out of main.go so the dispatch-coverage scan in complete_test.go only
// ever sees run()'s own switch.

package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/contextsize"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/preset"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
	"github.com/spinloop-ai/spinloop/internal/spinloopsrc"
	"golang.org/x/term"
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

// mtlxBinary is the MTPLX executable that `serve` launches. A package var so
// tests can point it at a stub instead of a real install.
var mtlxBinary = "mtplx"

// stdoutIsTerminal reports whether serve's stdout is a terminal — the check
// that decides whether the run draws the serve view. A variable so a test
// takes either path without a terminal.
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

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

// serveEngine is a local inference server `spinloop serve` can launch: which
// binary to run, the subcommand it needs, the dialect its preset is written in,
// and how the Spinloop's own instructions turn into its flags.
type serveEngine struct {
	// binary is resolved late so tests can stub it after engineFor has run.
	binary     func() string
	subcommand []string
	dialect    preset.Dialect
	// params turns the Spinloop's instructions into this engine's flags.
	params func(spinloop.Selection) ([]preset.Param, error)
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
	// defaultBaseURL is where the engine listens when the Spinloop states no
	// BASEURL, so the scraper can still find /metrics.
	defaultBaseURL string
	// apiKeyFileFlag is the engine's option for reading its API key from a
	// file. Empty means the engine has no such option and so cannot be
	// gated: the alternative is passing the key as a literal argument,
	// where every local user can read it, which is worse than not gating.
	apiKeyFileFlag string
	// defaultBindLoopback says whether this engine binds loopback when its
	// command states no --host. It decides whether a node can tell a remote
	// router "my engine answers only on this machine" — llama.cpp binds
	// 127.0.0.1 by default, vLLM binds every interface.
	defaultBindLoopback bool
	// positional yields arguments placed directly after the subcommand —
	// vLLM takes its model positionally rather than behind a flag.
	positional func(sel spinloop.Selection) []string
}

// engines is the set of local inference servers `serve` can launch, keyed by
// the PROVIDER that selects one. It is the single source of truth for what
// serve runs: the error for an unservable PROVIDER and the help text both name
// the engines from here rather than from a list written out by hand.
var engines = map[string]serveEngine{
	"llamacpp": {
		binary:              func() string { return llamaServerBinary },
		dialect:             preset.LlamaCpp,
		params:              llamacppServeParams,
		needsModel:          true,
		installHint:         "install llama.cpp (e.g. brew install llama.cpp) or check the path",
		metricsArgs:         []string{"--metrics"},
		metricsEngine:       "llamacpp",
		apiKeyFileFlag:      "--api-key-file",
		defaultBaseURL:      "http://127.0.0.1:8080",
		defaultBindLoopback: true,
	},
	"omlx": {
		binary:      resolveOMLXBinary,
		subcommand:  []string{"serve"},
		dialect:     preset.OMLX,
		params:      omlxServeParams,
		installHint: "install oMLX (https://omlx.ai) or check the path",
	},
	"mtplx": {
		binary:      func() string { return mtlxBinary },
		subcommand:  []string{"serve"},
		dialect:     preset.MTPLX,
		params:      mtplxServeParams,
		needsModel:  true,
		installHint: "install MTPLX (https://mtplx.com) or check the path",
		// MTPLX has no Prometheus endpoint to scrape, so there is no
		// metrics switch to append and no dialect for the scraper.
		apiKeyFileFlag:      "--api-key-file",
		defaultBaseURL:      "http://127.0.0.1:8000",
		defaultBindLoopback: true,
	},
	"vllm": {
		binary:      func() string { return vllmBinary },
		subcommand:  []string{"serve"},
		dialect:     preset.VLLM,
		params:      vllmServeParams,
		needsModel:  true,
		installHint: "install vLLM (pip install vllm) or check the path",
		// vLLM serves /metrics unconditionally, so no switch to append.
		metricsEngine:  "vllm",
		defaultBaseURL: "http://127.0.0.1:8000",
		positional: func(sel spinloop.Selection) []string {
			if sel.Model == "" {
				return nil
			}
			return []string{sel.Model}
		},
	},
}

// engineFor maps a Spinloop's PROVIDER to the engine `serve` launches locally.
// It is the local twin of runnerFor: PROVIDER already names the engine, so no
// separate keyword is needed. Providers that are not self-hosted engines have
// nothing to launch.
func engineFor(provider string) (serveEngine, error) {
	engine, ok := engines[provider]
	if !ok {
		return serveEngine{}, fmt.Errorf(
			"PROVIDER %q cannot be served locally: serve runs a self-hosted engine, so use %s",
			provider, orList(servedProviders()))
	}
	return engine, nil
}

// servedProviders lists the PROVIDERs serve can launch, in stable order.
func servedProviders() []string {
	names := make([]string, 0, len(engines))
	for name := range engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// orList joins names the way an English sentence does: "a, b or c".
func orList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// cmdServe reads a Spinloop and runs the inference server its PROVIDER names.
// With a PRESET it turns the matching preset section into the command; without
// one it derives the command from the Spinloop's own instructions. Either way it
// prints the command before running it. The Spinloop path defaults to ./Spinloop.
// Serve is strictly foreground; with --api the control API is served alongside
// the engine. Long-lived supervision is `spinloop daemon`'s job.
func serveCmd() *cobra.Command {
	var (
		dryRun   bool
		apiOn    bool
		apiAddr  string
		logLevel string
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "run the Spinloop's inference server",
		Long: fmt.Sprintf(`runs the inference server the Spinloop's PROVIDER names — %s.
With a PRESET it turns the matching section into the command, reading it in
that engine's flag vocabulary; otherwise it derives one from the Spinloop's
own instructions. Prints the command before running it; --dry-run/-n prints
without launching the server.

On a terminal the server runs under a full-screen view: its metrics — each
resource series as a gauge of the current reading with its retained history
as a bar beneath — above its log, which follows new output as it is written.
The arrow keys scroll the log, page up and page down move it by a page, f
pauses and resumes the follow, and q or Ctrl+C leaves, stopping the server
with it. Piped and redirected runs forward the server's output as they
always have, with no view.`, orList(servedProviders())),
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runServe(args, dryRun, apiOn, apiAddr, logLevel)
		},
	}
	fs := c.Flags()
	fs.BoolVarP(&dryRun, "dry-run", "n", false, "print the server command without running it")
	fs.BoolVarP(&apiOn, "api", "a", false, "expose the control API beside the foreground engine")
	fs.StringVar(&apiAddr, "api-addr", daemon.DefaultAPIAddr, "control API listen address")
	// Accepted with or without --api, so the same command line works both
	// ways. Without --api there is no API to summarise and nothing supervised,
	// so it governs nothing — which is better than rejecting it.
	fs.StringVar(&logLevel, "log-level", "", logLevelUsage)
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "log-level", compLogLevel)
	return c
}

// runServe is the body of `spinloop serve`.
func runServe(args []string, dryRun, apiOn bool, apiAddr, logLevel string) error {
	// A mistyped level is refused up front, whether or not this serve will host
	// the API — a flag spinloop accepted and then ignored would be worse than a
	// flag it rejected. The API path resolves it again once the Spinloop's .env
	// is loaded, which is where an SPINLOOP_LOG_LEVEL set there gets its say.
	if _, err := daemon.ResolveLevel(logLevel); err != nil {
		return err
	}

	var path string
	if len(args) > 0 {
		path = args[0]
	}
	sel, spinloopPath, err := readSpinloop("spinloop serve <file>", path)
	if err != nil {
		return err
	}
	engine, err := engineFor(sel.Provider)
	if err != nil {
		return err
	}
	// On a terminal the view draws on stdout, so the command and the
	// narration go to stderr there — and stay on stdout wherever a pipe or
	// redirect reads them. Never for a dry run, which prints and stops.
	viewOn := !dryRun && stdoutIsTerminal()
	narration := os.Stdout
	if viewOn {
		narration = os.Stderr
	}
	argv, err := buildServeArgv(narration, engine, sel, spinloopPath)
	if err != nil {
		return err
	}
	if apiOn || viewOn {
		// A supervised engine gets its metrics endpoint switched on, exactly
		// as the cloud path does for a deployed one. Applied here so the
		// printed command is the one that runs; the shared construction
		// applies it to its own copy, where it is a no-op.
		argv = withMetricsArgs(argv, engine)
	}

	fmt.Fprintf(narration, "%s\n\n", preset.FormatCommand(argv))
	if dryRun {
		return nil
	}

	if viewOn {
		return runServeView(sel, spinloopPath, engine, argv, apiOn, apiAddr, logLevel)
	}
	if apiOn {
		return runServeForegroundAPI(sel, spinloopPath, engine, argv, apiAddr, logLevel)
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

// buildServeArgv turns a Spinloop into the engine command, from its PRESET
// section when it names one and from the Spinloop's own instructions otherwise,
// narrating which source it used to w. It is `spinloop serve`'s alone: the
// daemon reads no Spinloop, so nothing else builds a command this way.
func buildServeArgv(w io.Writer, engine serveEngine, sel spinloop.Selection, spinloopPath string) ([]string, error) {
	// Anything the Spinloop states overrides the preset's own values.
	params, err := engine.params(sel)
	if err != nil {
		return nil, err
	}

	if sel.Preset != "" {
		presetPath, err := resolvePresetPath(sel.Preset, spinloopPath)
		if err != nil {
			return nil, err
		}
		data, err := spinloopsrc.Fetch(presetPath)
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
		argv := pre.CommandIn(engine.dialect, engine.binary(), subcommandFor(engine, sel), sec, params)
		fmt.Fprintf(w, "Using preset %s (model %s)\n\n", presetPath, sec.Name)
		return argv, nil
	}

	if engine.needsModel && sel.Model == "" {
		return nil, fmt.Errorf("serve needs a PRESET or a MODEL (an HF repo like org/model:quant, or a path to a .gguf)")
	}
	argv := assembleEngineArgv(engine, subcommandFor(engine, sel), params, nil)
	if sel.Model != "" {
		fmt.Fprintf(w, "Serving %s from %s\n\n", sel.Model, spinloopPath)
	} else {
		// An engine that needs no model to start (oMLX serves a whole
		// directory) has nothing to name but itself.
		fmt.Fprintf(w, "Starting %s from %s\n\n", sel.Provider, spinloopPath)
	}
	return argv, nil
}

// subcommandFor returns the engine's subcommand with the positional model riding
// along right after it (before any flags). It copies the engine's own subcommand
// before appending, so a positional never aliases that slice.
func subcommandFor(engine serveEngine, sel spinloop.Selection) []string {
	sub := append([]string{}, engine.subcommand...)
	if engine.positional != nil {
		sub = append(sub, engine.positional(sel)...)
	}
	return sub
}

// assembleEngineArgv is the one place that turns an engine and its params into a
// command: the binary, its subcommand, the params rendered in the engine's
// dialect, then any trailing args the caller appends (a deploy config's serveArgs).
// The preset-less `serve` path and the daemon's deploy-config path both draw from
// here, and the preset path uses subcommandFor for its subcommand.
func assembleEngineArgv(engine serveEngine, subcommand []string, params []preset.Param, trailing []string) []string {
	argv := append([]string{engine.binary()}, subcommand...)
	argv = append(argv, engine.dialect.Flags(params)...)
	argv = append(argv, trailing...)
	return argv
}

// resolvePresetPath resolves a Spinloop's PRESET value against the Spinloop's
// own source: a relative one resolves against the Spinloop's local directory,
// or against its URL when the Spinloop was fetched from one, so a Spinloop and
// its preset travel together either way (the same rule REMOTE uses).
func resolvePresetPath(presetValue, spinloopPath string) (string, error) {
	return spinloopsrc.Resolve(spinloopPath, presetValue)
}

// parseParallel validates a Spinloop's PARALLEL value: a plain positive
// integer count of concurrent request slots, with none of CONTEXT's k/m/g
// suffix leniency — a slot count, not a size. Empty is not a valid input;
// callers check sel.Parallel != "" first, exactly as they do for sel.Context.
func parseParallel(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid PARALLEL %q: must be a positive integer", s)
	}
	return n, nil
}

// vllmServeParams turns the vLLM settings a Spinloop states into preset
// params. The model is deliberately absent — `vllm serve` takes it as its
// positional argument (see the engine's positional func). ALIAS names what is
// served, CONTEXT caps the model length, BASEURL binds the address.
//
// PARALLEL maps to --max-num-seqs, a concurrency cap independent of context:
// vLLM shares one dynamically-allocated KV-cache pool across requests via
// continuous batching, so --max-model-len (from CONTEXT) already bounds a
// single request and is never scaled by how many run at once — unlike
// llama.cpp, where --ctx-size is a total budget split across slots.
func vllmServeParams(sel spinloop.Selection) ([]preset.Param, error) {
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
	if sel.Parallel != "" {
		n, err := parseParallel(sel.Parallel)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "max-num-seqs", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// omlxServeParams turns the oMLX settings a Spinloop states into preset params.
// The bind address and PARALLEL map: oMLX serves a whole --model-dir and picks
// the model per request, so MODEL and ALIAS keep their usual job of naming what
// the harness asks for, and CONTEXT sizes the harness's window rather than the
// server (oMLX has no context flag). Everything else — the model directory,
// memory guard, SSD cache — comes from the PRESET or from oMLX's own settings.
//
// PARALLEL maps to --max-concurrent-requests: like vLLM, oMLX serves
// concurrent requests against its own paged/tiered KV cache rather than
// dividing a fixed budget per slot, so there is no context flag to scale
// either way.
//
// The API key is deliberately not passed: serve prints the command it runs, and
// oMLX takes its key as a command-line flag, so passing one would put the secret
// on screen and in the process table. Auth belongs in oMLX's own settings.
func omlxServeParams(sel spinloop.Selection) ([]preset.Param, error) {
	var params []preset.Param
	if sel.Parallel != "" {
		n, err := parseParallel(sel.Parallel)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "max-concurrent-requests", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// mtplxServeParams turns the MTPLX settings a Spinloop states into preset
// params: MODEL names the weights — an MTPLX-optimised HF repo or a local
// path, both taken verbatim by --model — and ALIAS, CONTEXT, and BASEURL fill
// in the served name, the context window, and the bind address.
//
// --download is always passed: MTPLX fetches an optimised pack it does not
// have, the same job -hf does for llama-server, and it is a no-op when the
// model is already present.
//
// PARALLEL maps to --max-active-requests, the cap on admitted concurrent
// requests. How admitted requests execute — the scheduler mode, serial or
// parallel — is a preset concern: PARALLEL never selects it. And like vLLM
// and oMLX there is no context flag to scale: --context-window bounds a
// single request, independently of how many are admitted.
func mtplxServeParams(sel spinloop.Selection) ([]preset.Param, error) {
	var params []preset.Param
	if sel.Model != "" {
		params = append(params, preset.Param{Key: "model", Value: sel.Model})
	}
	// MTPLX fetches an optimised model it does not have; a no-op when the
	// model is already present.
	params = append(params, preset.Param{Key: "download", Value: ""})
	if sel.Alias != "" {
		params = append(params, preset.Param{Key: "model-id", Value: sel.Alias})
	}
	if sel.Context != "" {
		n, err := contextsize.Parse(sel.Context)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "context-window", Value: strconv.Itoa(n)})
	}
	if sel.Parallel != "" {
		n, err := parseParallel(sel.Parallel)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "max-active-requests", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// llamacppServeParams turns the llama-server settings a Spinloop states into
// preset params: the provider-native MODEL supplies the model source (hf for a
// Hugging Face repo, model for a .gguf path); ALIAS, CONTEXT, PARALLEL, and
// BASEURL fill in the rest. They seed a preset-less command and, with a
// preset, override its values.
//
// PARALLEL maps to --parallel, the slot count. llama.cpp treats --ctx-size as
// a total KV-cache budget divided across those slots, so when the Spinloop
// states CONTEXT too, the rendered ctx-size is scaled by the slot count —
// context_tokens * n — rather than passed through unscaled, so CONTEXT keeps
// meaning "context per request" the way it does for every other engine. A
// CONTEXT supplied only by a PRESET's own ctx-size (the Spinloop itself states
// none) is left exactly as the preset wrote it: the multiply only applies to
// a Spinloop-stated CONTEXT.
func llamacppServeParams(sel spinloop.Selection) ([]preset.Param, error) {
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
	var parallel int
	if sel.Parallel != "" {
		n, err := parseParallel(sel.Parallel)
		if err != nil {
			return nil, err
		}
		parallel = n
		params = append(params, preset.Param{Key: "parallel", Value: strconv.Itoa(n)})
	}
	if sel.Context != "" {
		n, err := contextsize.Parse(sel.Context)
		if err != nil {
			return nil, err
		}
		if parallel > 0 {
			n *= parallel
		}
		params = append(params, preset.Param{Key: "ctx-size", Value: strconv.Itoa(n)})
	}
	bind, err := bindAddressParams(sel)
	if err != nil {
		return nil, err
	}
	return append(params, bind...), nil
}

// bindAddressParams turns a Spinloop's BASEURL into the host and port flags that
// bind a server to the endpoint the harness will call. Both engines spell these
// the same way. With no BASEURL it yields nothing, so the server's own defaults
// stand.
func bindAddressParams(sel spinloop.Selection) ([]preset.Param, error) {
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

// cmdServe runs the command through the tree — the seam the suite calls.
func cmdServe(args []string) error { return execCmd(serveCmd(), args) }
