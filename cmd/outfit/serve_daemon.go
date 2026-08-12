// The daemon side of the CLI: `outfit daemon` hosts internal/daemon's
// supervisor and control API (never starting an engine on boot), and
// `outfit serve --api` exposes the same API over a foreground engine. The
// engine-specific knowledge stays in serve.go's engine table; this file only
// wires it into the daemon.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdDaemon is `outfit daemon`: a long-lived foreground process that
// supervises one engine and serves the control API — the API is the command's
// whole purpose, so it is always on.
//
// It is a worker: its inputs are its flags and its API, and nothing else. It
// reads no Outfit, no preset and no fleet file, so what a node runs is decided
// by the client that asks rather than by whatever file the daemon happened to
// be started next to. Nothing starts on boot either: the engine runs only when
// a start request asks, from the config that request carries or the one stored
// from a previous ask.
func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	var apiAddr, apiToken, apiTokenFile string
	fs.StringVar(&apiAddr, "api-addr", daemon.DefaultAPIAddr, "control API listen address")
	fs.StringVar(&apiTokenFile, "api-token-file", "", "read the control API's bearer token from this file")
	fs.StringVar(&apiToken, "api-token", "", "the control API's bearer token (visible in `ps`; prefer --api-token-file)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf(
			"outfit daemon takes no Outfit (got %q): it runs what a start request tells it to.\n"+
				"Push a config with `outfit fleet start <node>` from a machine holding the Outfit, "+
				"or launch through it with `outfit harness`",
			rest[0])
	}

	token, err := daemonToken(apiToken, apiTokenFile)
	if err != nil {
		return err
	}

	// The handler goes in before anything starts, so a signal at any point
	// from here on shuts down cleanly rather than killing the process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stateDir, err := daemon.StateDir()
	if err != nil {
		return err
	}
	sup := daemon.NewSupervisor(filepath.Join(stateDir, "engine.log"))
	d := &daemon.Daemon{
		Sup:            sup,
		Dir:            stateDir,
		Collector:      &metrics.Collector{},
		ValidateConfig: validateDeployConfig,
	}
	d.BuildArgv = func(dc *remote.DeployConfig) ([]string, error) {
		if dc == nil {
			return nil, fmt.Errorf(
				"nothing to serve: no deploy config has been pushed to this daemon.\n" +
					"Send one with a start request — `outfit harness` does it for you when its " +
					"Outfit names this fleet, and `outfit fleet start <node>` reuses the last one")
		}
		engine, err := engineFor(dc.Runner)
		if err != nil {
			return nil, err
		}
		argv, err := argvFromDeployConfig(engine, *dc)
		if err != nil {
			return nil, err
		}
		argv = withMetricsArgs(argv, engine)
		d.SetScrape(scrapeTargetFor(engine, "", argv))
		d.SetEngineEndpoint(engineEndpointFor(engine, "", argv))
		return argv, nil
	}
	d.EngineKeyArgs = engineKeyArgs
	// Nothing starts on boot; the listener is the daemon's whole job, so a
	// port conflict or the tokenless non-loopback refusal fails immediately.
	ln, err := daemon.Listen(apiAddr, token)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: d.Handler(token)}
	fmt.Printf("daemon ready: nothing runs until a start request arrives\n")
	fmt.Printf("engine log: %s\n", sup.LogPath)
	fmt.Printf("control API on %s\n", ln.Addr())
	go srv.Serve(ln)

	// Activity sampling runs for as long as the daemon does, independently of
	// anyone calling the API — that frequent, unprompted sampling is what
	// makes the idle time in /v1/status trustworthy.
	sampleCtx, stopSampling := context.WithCancel(context.Background())
	go d.SampleActivity(sampleCtx)

	// Foreground until signalled; a running engine is stopped before the
	// daemon exits. Backgrounding is the user's business (tmux, systemd,
	// launchd).
	<-sigCh
	stopSampling()
	sup.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	srv.Shutdown(shutdownCtx)
	cancel()
	return nil
}

// runServeForegroundAPI is `outfit serve --api`: the engine runs in the
// foreground with stdio forwarded as ever, with the control API alongside it. Start over the API always fails — the engine is
// foreground-managed and already running — and stop terminates it, after
// which serve exits exactly as it does when the engine exits on its own.
func runServeForegroundAPI(sel outfit.Selection, outfitPath string, engine serveEngine, argv []string, apiAddr string) error {
	if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
		return err
	}
	token := os.Getenv(daemon.TokenEnvVar)
	ln, err := daemon.Listen(apiAddr, token)
	if err != nil {
		return err
	}

	stateDir, err := daemon.StateDir()
	if err != nil {
		ln.Close()
		return err
	}
	sup := daemon.NewSupervisor("") // empty LogPath: stdio stays forwarded
	d := &daemon.Daemon{
		Sup: sup,
		Dir: stateDir,
		BuildArgv: func(*remote.DeployConfig) ([]string, error) {
			return nil, fmt.Errorf("the engine is foreground-managed by this serve; restart `outfit serve` to change it")
		},
		ValidateConfig: validateDeployConfig,
		Collector:      &metrics.Collector{},
	}
	model := sel.Model
	if model == "" {
		model = sel.Alias
	}
	d.SetServed(sel.Provider, model)
	d.SetScrape(scrapeTargetFor(engine, sel.BaseURL, argv))
	d.SetEngineEndpoint(engineEndpointFor(engine, sel.BaseURL, argv))

	// The engine sits in its own process group, so Ctrl+C reaches only this
	// process — relay it as a stop. Installed before the engine starts so no
	// window exists where a signal kills serve and orphans it.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		sup.Stop()
	}()

	if err := sup.Start(argv); err != nil {
		ln.Close()
		signal.Stop(sigCh)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found — %s", argv[0], engine.installHint)
		}
		return err
	}
	// The engine started through the supervisor directly rather than through
	// StartEngine, so the activity record is stamped here instead.
	d.MarkActive()
	srv := &http.Server{Handler: d.Handler(token)}
	fmt.Printf("control API on %s\n\n", ln.Addr())
	go srv.Serve(ln)
	sampleCtx, stopSampling := context.WithCancel(context.Background())
	go d.SampleActivity(sampleCtx)

	waitErr := sup.Wait()
	stopSampling()
	signal.Stop(sigCh)
	// Graceful shutdown: a stop requested over the API lands here while its
	// response is still in flight — let it finish rather than cutting the
	// connection.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	srv.Shutdown(shutdownCtx)
	cancel()
	if state, _, _ := sup.Status(); state == daemon.StateStopped {
		// Stopped on request (signal or API) or exited cleanly: not an error.
		return nil
	}
	return waitErr
}

// daemonToken resolves the control API's bearer token. Three sources, because
// the daemon reads no Outfit and so no longer picks one up from an adjacent
// `.env`: a file, the environment, or the command line.
//
// The literal flag is a convenience with a cost — a command line is readable by
// every local user — so it is the last resort and says so in its own help. Two
// sources at once is a conflict rather than a precedence: a silent winner
// between two credentials is how an afternoon disappears into a 401 against the
// wrong value.
func daemonToken(literal, file string) (string, error) {
	if literal != "" && file != "" {
		return "", fmt.Errorf("--api-token and --api-token-file both given: pass one")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading the API token from %s: %w", file, err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("the API token file %s is empty", file)
		}
		return token, nil
	}
	if literal != "" {
		return literal, nil
	}
	return os.Getenv(daemon.TokenEnvVar), nil
}

// argvFromDeployConfig builds the engine command from a pushed deploy config:
// the config's model, alias and context go through the same per-engine param
// mapping an Outfit's would, and its serveArgs — the preset already resolved
// by the pusher — are appended as-is.
func argvFromDeployConfig(engine serveEngine, dc remote.DeployConfig) ([]string, error) {
	model := dc.ModelID
	if dc.Quant != "" {
		model += ":" + dc.Quant
	}
	sel := outfit.Selection{Provider: dc.Runner, Model: model, Alias: dc.ServedModelName}
	if dc.ContextSize > 0 {
		sel.Context = strconv.Itoa(dc.ContextSize)
	}
	params, err := engine.params(sel)
	if err != nil {
		return nil, err
	}
	argv := append([]string{engine.binary()}, engine.subcommand...)
	if engine.positional != nil {
		argv = append(argv, engine.positional(sel)...)
	}
	argv = append(argv, engine.dialect.Flags(params)...)
	argv = append(argv, dc.ServeArgs...)
	fmt.Printf("Serving %s from the pushed deploy config\n\n", model)
	return argv, nil
}

// validateDeployConfig rejects a pushed deploy config this host cannot serve:
// a runner that is not a local engine, or a model-less config for an engine
// that needs one.
func validateDeployConfig(dc remote.DeployConfig) error {
	engine, err := engineFor(dc.Runner)
	if err != nil {
		return err
	}
	if engine.needsModel && dc.ModelID == "" {
		return fmt.Errorf("deploy config names no model: set modelId")
	}
	return nil
}

// withMetricsArgs appends the engine's metrics-endpoint switch, unless the
// command already carries it (a preset may set it itself).
func withMetricsArgs(argv []string, engine serveEngine) []string {
	if len(engine.metricsArgs) == 0 {
		return argv
	}
	for _, a := range argv {
		if a == engine.metricsArgs[0] {
			return argv
		}
	}
	return append(argv, engine.metricsArgs...)
}

// scrapeTargetFor locates the engine's own /metrics for the collector, with
// the API key lifted from the command — a literal --api-key, or the contents
// of an --api-key-file (how the cloud delivers it) — so a gated /metrics still
// answers. An engine with no metrics endpoint yields the zero target (no
// scrape).
//
// The address is taken from the engine's own --host/--port when it states
// them, because that is where the process actually binds. Only then the
// Outfit's BASEURL, and only then the engine's compiled-in default. Getting
// this order wrong is not hypothetical: a cloud llama.cpp instance is driven
// by a deploy config rather than an Outfit, so it stated no BASEURL, and the
// scraper fell back to llama.cpp's default 8080 while the engine sat on the
// deploy config's --port 8000. Every scrape was refused, silently, and the
// activity record — derived from those counters — never moved.
func scrapeTargetFor(engine serveEngine, baseURL string, argv []string) metrics.ScrapeTarget {
	if engine.metricsEngine == "" {
		return metrics.ScrapeTarget{}
	}
	bind := engineBindFrom(argv)
	if b := bindBaseURL(bind.host, bind.port); b != "" {
		baseURL = b
	}
	if baseURL == "" {
		baseURL = engine.defaultBaseURL
	}
	return metrics.ScrapeTarget{BaseURL: baseURL, Engine: engine.metricsEngine, APIKey: bind.key}
}

// engineKeyArgs gates an engine with a key the caller supplied, by pointing the
// engine at the file the daemon wrote. An engine with no key-file option is
// refused rather than gated with a literal argument: a command line is readable
// by every local user, which is the population the key exists to exclude.
func engineKeyArgs(dc *remote.DeployConfig, keyPath string) ([]string, error) {
	if dc == nil {
		return nil, fmt.Errorf("cannot gate an engine without knowing which one it is")
	}
	engine, err := engineFor(dc.Runner)
	if err != nil {
		return nil, err
	}
	if engine.apiKeyFileFlag == "" {
		return nil, fmt.Errorf(
			"%s cannot be gated with an API key: it has no option to read one from a file, "+
				"and outfit will not pass a key on a command line where any local user can read it",
			dc.Runner)
	}
	return []string{engine.apiKeyFileFlag, keyPath}, nil
}

// engineBind is what the engine's own command line says about where it listens
// and whether it is gated. Both the metrics scrape and the endpoint status
// reports read it, so the two cannot disagree about the same engine.
type engineBind struct {
	host string
	port string
	key  string
}

// engineBindFrom reads the bind and key out of an engine command. The key is a
// literal --api-key or the contents of an --api-key-file (how the cloud
// delivers it).
func engineBindFrom(argv []string) engineBind {
	var b engineBind
	for i, a := range argv {
		if i+1 >= len(argv) {
			break
		}
		switch a {
		case "--api-key":
			b.key = argv[i+1]
		case "--api-key-file":
			if data, err := os.ReadFile(argv[i+1]); err == nil {
				b.key = strings.TrimSpace(string(data))
			}
		case "--host":
			b.host = argv[i+1]
		case "--port":
			b.port = argv[i+1]
		}
	}
	return b
}

// engineEndpointFor describes where an engine serves inference, for the
// daemon's status to report to a router. It reads the same command line the
// scrape target does, so a node cannot advertise one address and be scraped on
// another.
//
// The port follows the same precedence as the scrape: the engine's own --port,
// then the Outfit's BASEURL, then the engine's compiled-in default. When none
// of those yields a port, nil is returned — a router is told nothing rather
// than a guess, and the fleet file's per-node override is the way through.
func engineEndpointFor(engine serveEngine, baseURL string, argv []string) *daemon.EngineEndpoint {
	bind := engineBindFrom(argv)
	port := bind.port
	path := ""
	if port == "" && baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil {
			port, path = u.Port(), u.Path
		}
	}
	if port == "" && engine.defaultBaseURL != "" {
		if u, err := url.Parse(engine.defaultBaseURL); err == nil {
			port = u.Port()
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return nil
	}
	// "/v1" is what an OpenAI-compatible client appends for itself, so
	// reporting it would be noise; anything else is a real prefix.
	if path == "/" || path == "/v1" {
		path = ""
	}
	return &daemon.EngineEndpoint{
		Port:         n,
		Path:         path,
		LoopbackOnly: bindIsLoopback(bind.host, engine.defaultBindLoopback),
		RequiresKey:  bind.key != "",
	}
}

// bindIsLoopback reports whether an engine bound to host answers only on this
// machine. An engine that states no --host falls back to whether its own
// default bind is loopback, which differs by engine: llama.cpp binds 127.0.0.1
// unless told otherwise, vLLM binds every interface.
func bindIsLoopback(host string, byDefault bool) bool {
	switch host {
	case "":
		return byDefault
	case "0.0.0.0", "::", "[::]", "*":
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

// bindBaseURL turns the engine's --host/--port into the URL to scrape it on,
// or "" when the command says neither and there is nothing to improve on.
//
// A wildcard bind is rewritten to loopback: the scrape is always to an engine
// on this host, and 0.0.0.0 names every interface rather than one to dial.
func bindBaseURL(host, port string) string {
	if host == "" && port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]", "*":
		host = "127.0.0.1"
	}
	if port == "" {
		return "http://" + host
	}
	return "http://" + net.JoinHostPort(host, port)
}
