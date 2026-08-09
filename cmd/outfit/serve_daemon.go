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
	"net/http"
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
// whole purpose, so it is always on. Nothing starts on boot: the engine runs
// only when a start request asks, sourced from the request's own deploy
// config, the stored one, or the adjacent Outfit, in that order.
func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	var apiAddr string
	fs.StringVar(&apiAddr, "api-addr", daemon.DefaultAPIAddr, "control API listen address")
	if err := fs.Parse(sortFlagsBeforeArgs(args)); err != nil {
		return err
	}
	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}

	sel, outfitPath, hasOutfit, err := resolveDaemonOutfit(path)
	if err != nil {
		return err
	}
	if hasOutfit {
		// The Outfit's local environment (its .env, then ENV) must be in
		// place before the API token is read from it.
		if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
			return err
		}
	}
	token := os.Getenv(daemon.TokenEnvVar)

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
		if dc != nil {
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
			return argv, nil
		}
		if !hasOutfit {
			return nil, fmt.Errorf(
				"nothing to serve: the daemon started with no Outfit and no deploy config has been pushed")
		}
		engine, err := engineFor(sel.Provider)
		if err != nil {
			return nil, err
		}
		argv, err := buildServeArgv(engine, sel, outfitPath)
		if err != nil {
			return nil, err
		}
		argv = withMetricsArgs(argv, engine)
		model := sel.Model
		if model == "" {
			model = sel.Alias
		}
		d.SetServed(sel.Provider, model)
		d.SetScrape(scrapeTargetFor(engine, sel.BaseURL, argv))
		return argv, nil
	}
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

	// Foreground until signalled; a running engine is stopped before the
	// daemon exits. Backgrounding is the user's business (tmux, systemd,
	// launchd).
	<-sigCh
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
	srv := &http.Server{Handler: d.Handler(token)}
	fmt.Printf("control API on %s\n\n", ln.Addr())
	go srv.Serve(ln)

	waitErr := sup.Wait()
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

// resolveDaemonOutfit resolves the Outfit a daemon serves from. An explicit
// path must resolve; with none, a missing ./Outfit is fine — the daemon can
// start idle on a stored or future deploy config — but a present one that
// fails to read is still an error.
func resolveDaemonOutfit(path string) (outfit.Selection, string, bool, error) {
	if path == "" {
		if _, err := os.Stat(outfit.DefaultFile); err != nil {
			return outfit.Selection{}, "", false, nil
		}
	}
	sel, outfitPath, err := readOutfit("outfit serve <file>", path)
	if err != nil {
		return outfit.Selection{}, "", false, err
	}
	return sel, outfitPath, true, nil
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

// scrapeTargetFor locates the engine's own /metrics for the collector: the
// Outfit's BASEURL when it states one, the engine's default bind otherwise,
// with the API key lifted from the command — a literal --api-key, or the
// contents of an --api-key-file (how the cloud delivers it) — so a gated
// /metrics still answers. An engine with no metrics endpoint yields the zero
// target (no scrape).
func scrapeTargetFor(engine serveEngine, baseURL string, argv []string) metrics.ScrapeTarget {
	if engine.metricsEngine == "" {
		return metrics.ScrapeTarget{}
	}
	if baseURL == "" {
		baseURL = engine.defaultBaseURL
	}
	key := ""
	for i, a := range argv {
		if i+1 >= len(argv) {
			break
		}
		switch a {
		case "--api-key":
			key = argv[i+1]
		case "--api-key-file":
			if data, err := os.ReadFile(argv[i+1]); err == nil {
				key = strings.TrimSpace(string(data))
			}
		}
	}
	return metrics.ScrapeTarget{BaseURL: baseURL, Engine: engine.metricsEngine, APIKey: key}
}
