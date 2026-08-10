package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// StateDir is where a daemon's own state lives: the stored deploy config and
// the engine log. One daemon per machine is the working assumption (a second
// one fails to bind the API port), so the directory is unkeyed.
func StateDir() (string, error) {
	home, err := remote.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "daemon"), nil
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
	// SampleInterval is how often SampleActivity reads the engine's
	// counters; zero means DefaultSampleInterval.
	SampleInterval time.Duration
	// Now is the clock idle durations are measured against; nil means
	// time.Now. Injected the same way Collector.Run and BuildArgv are, so a
	// test can age an engine without waiting.
	Now func() time.Time

	act activity

	mu     sync.Mutex
	runner string
	model  string
	scrape metrics.ScrapeTarget
}

// now reads the daemon's clock, defaulting to the wall clock.
func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
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
	if err := d.Sup.Start(argv); err != nil {
		return err
	}
	// A start is activity in its own right. It is what stops a freshly woken
	// instance from reporting that it has been idle since before its engine
	// existed — the race the control plane used to close with a last-wake
	// timestamp of its own.
	d.act.markActive(d.now())
	return nil
}

// StatusResponse is the control API's status reply.
type StatusResponse struct {
	State         string `json:"state"`
	Runner        string `json:"runner,omitempty"`
	Model         string `json:"model,omitempty"`
	UptimeSeconds int    `json:"uptimeSeconds,omitempty"`
	LogPath       string `json:"logPath,omitempty"`
	// LastActiveAt is when the engine last did any work, RFC 3339. Empty
	// until an engine has run: a daemon that has served nothing reports no
	// activity rather than claiming its own start time as some.
	LastActiveAt string `json:"lastActiveAt,omitempty"`
	// IdleSeconds is how long it has been since LastActiveAt. Derived at
	// read time, so it is a convenience for a caller that would otherwise
	// parse a timestamp in a shell pipeline — LastActiveAt is the fact.
	IdleSeconds int `json:"idleSeconds,omitempty"`
}

// Status reports the supervised state, what is being served, where the
// engine's log lives, and how long the engine has been idle.
func (d *Daemon) Status() StatusResponse {
	state, _, uptime := d.Sup.Status()
	runner, model := d.served()
	resp := StatusResponse{
		State:         string(state),
		Runner:        runner,
		Model:         model,
		UptimeSeconds: uptime,
		LogPath:       d.Sup.LogPath,
	}
	resp.LastActiveAt, resp.IdleSeconds = d.activity()
	return resp
}

// activity renders the activity record as the pair both /v1/status and
// /v1/metrics report: an RFC 3339 timestamp and the seconds since it. Zero
// values mean "no engine has run", which both endpoints turn into absent
// fields. It lives in one place so the two replies cannot drift apart.
func (d *Daemon) activity() (lastActiveAt string, idleSeconds int) {
	lastActive, ok := d.act.snapshot()
	if !ok {
		return "", 0
	}
	// Idle stays zero when it rounds to nothing: an engine working right now
	// reports the timestamp and no duration, and callers gate on the former.
	if idle := int(d.now().Sub(lastActive) / time.Second); idle > 0 {
		idleSeconds = idle
	}
	return lastActive.UTC().Format(time.RFC3339), idleSeconds
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
	if state == StateRunning {
		if d.Collector != nil {
			d.Collector.System(ctx, &stats)
		}
		d.mu.Lock()
		scrape := d.scrape
		d.mu.Unlock()
		if scrape.BaseURL != "" {
			tokens, err := metrics.ScrapeTokenStats(ctx, scrape)
			if err != nil {
				// Reported, not swallowed. A silent omission here once hid a
				// scraper pointed at the wrong port for every cloud llama.cpp
				// deployment: the token block simply never appeared, and with
				// no observation to make, the activity record never moved.
				// An absent *source* is omitted quietly; a source that is
				// there and failing is an error worth showing.
				stats.Errors = append(stats.Errors,
					fmt.Sprintf("engine metrics scrape (%s): %v", scrape.BaseURL, err))
				tokens = nil
			}
			// Feed it through the same path the background sampler uses: one
			// place decides whether a sample counts as activity, so a client
			// polling metrics refreshes the record rather than racing it.
			d.act.observe(tokens, d.now())
			stats.Tokens = tokens
		}
	}
	// Outside the running branch, so a stopped or crashed engine still reports
	// when work last happened — the record survives a stop precisely so it can
	// answer that once every other figure here has nothing to say. And after
	// the scrape, so a poll reports the activity its own reading just
	// established rather than the record as it stood one call ago.
	stats.LastActiveAt, stats.IdleSeconds = d.activity()
	return stats
}
