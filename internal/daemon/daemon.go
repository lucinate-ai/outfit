package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// StateDir is where a daemon's own state lives: the stored deploy config and
// the engine log. One daemon per machine is the working assumption (a second
// one fails to bind the API port), so the directory is unkeyed.
func StateDir() string {
	return filepath.Join(remote.ConfigHome(), "daemon")
}

// Daemon ties the supervisor to what it serves: it resolves the engine
// command, persists pushed deploy configs, and answers status and metrics.
// The engine-specific knowledge — how a deploy config or an Outfit becomes an
// argv, and which runners this host can serve — is injected by the CLI, which
// owns the engine table.
type Daemon struct {
	Sup *Supervisor
	// Dir is the daemon's state directory; StateDir() outside tests.
	Dir string
	// BuildArgv turns the source of what to serve into the engine command.
	// dc is the stored deploy config, or nil to serve from the Outfit.
	BuildArgv func(dc *remote.DeployConfig) ([]string, error)
	// ValidateConfig rejects a pushed deploy config this host cannot serve.
	ValidateConfig func(remote.DeployConfig) error
	// Collector gathers system stats; nil skips them.
	Collector *metrics.Collector

	mu     sync.Mutex
	runner string
	model  string
	scrape metrics.ScrapeTarget
}

// SetScrape records where the running engine's own /metrics lives; an empty
// BaseURL means no engine scrape (an engine with no metrics endpoint). It is
// set alongside each start, so it always describes the engine that runs.
func (d *Daemon) SetScrape(target metrics.ScrapeTarget) {
	d.mu.Lock()
	d.scrape = target
	d.mu.Unlock()
}

// SetServed records what the daemon is serving, for status and metrics.
func (d *Daemon) SetServed(runner, model string) {
	d.mu.Lock()
	d.runner, d.model = runner, model
	d.mu.Unlock()
}

func (d *Daemon) served() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runner, d.model
}

// configPath is the stored deploy config's location in the state directory.
func (d *Daemon) configPath() string {
	return filepath.Join(d.Dir, "deploy-config.json")
}

// StoredConfig reads the persisted deploy config; nil with no error when none
// has ever been pushed.
func (d *Daemon) StoredConfig() (*remote.DeployConfig, error) {
	data, err := os.ReadFile(d.configPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dc remote.DeployConfig
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", d.configPath(), err)
	}
	return &dc, nil
}

// Push validates and persists a deploy config, which subsequent starts serve.
// A running engine is deliberately untouched — the config takes effect on the
// next start. The file is 0600: serve args can carry sensitive flags.
func (d *Daemon) Push(dc remote.DeployConfig) error {
	if d.ValidateConfig != nil {
		if err := d.ValidateConfig(dc); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(dc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.configPath(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	d.SetServed(dc.Runner, dc.ModelID)
	return nil
}

// StartEngine starts the engine from the stored deploy config when one
// exists, from the Outfit otherwise (BuildArgv with nil dc). With neither
// source BuildArgv reports what is missing.
func (d *Daemon) StartEngine() error {
	dc, err := d.StoredConfig()
	if err != nil {
		return err
	}
	argv, err := d.BuildArgv(dc)
	if err != nil {
		return err
	}
	if dc != nil {
		d.SetServed(dc.Runner, dc.ModelID)
	}
	return d.Sup.Start(argv)
}

// StatusResponse is the control API's status reply.
type StatusResponse struct {
	State         string `json:"state"`
	Runner        string `json:"runner,omitempty"`
	Model         string `json:"model,omitempty"`
	UptimeSeconds int    `json:"uptimeSeconds,omitempty"`
	LogPath       string `json:"logPath,omitempty"`
}

// Status reports the supervised state, what is being served, and where the
// engine's log lives.
func (d *Daemon) Status() StatusResponse {
	state, _, uptime := d.Sup.Status()
	runner, model := d.served()
	return StatusResponse{
		State:         string(state),
		Runner:        runner,
		Model:         model,
		UptimeSeconds: uptime,
		LogPath:       d.Sup.LogPath,
	}
}

// Metrics collects the full picture: supervised state, system stats, and —
// while the engine runs — its own token counters. Collection failures land in
// Errors; an absent source is simply omitted, per the engine-metrics spec.
func (d *Daemon) Metrics(ctx context.Context) metrics.Stats {
	state, _, uptime := d.Sup.Status()
	runner, model := d.served()
	stats := metrics.Stats{
		State:         string(state),
		Runner:        runner,
		ModelID:       model,
		UptimeSeconds: uptime,
	}
	if state != StateRunning {
		return stats
	}
	if d.Collector != nil {
		d.Collector.System(ctx, &stats)
	}
	d.mu.Lock()
	scrape := d.scrape
	d.mu.Unlock()
	if scrape.BaseURL != "" {
		if tokens, err := metrics.ScrapeTokenStats(ctx, scrape); err == nil {
			stats.Tokens = tokens
		}
	}
	return stats
}
