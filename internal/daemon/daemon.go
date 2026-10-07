package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spinloop-ai/spinloop/internal/config"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// StateDir is where a daemon's own state lives: the stored deploy config and
// the engine log. One daemon per machine is the working assumption (a second
// one fails to bind the API port), so the directory is unkeyed.
func StateDir() (string, error) {
	home, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "daemon"), nil
}

// Daemon ties the supervisor to what it serves: it resolves the engine
// command, persists pushed deploy configs, and answers status and metrics.
// The engine-specific knowledge — how a deploy config or a Spinloop becomes an
// argv, and which runners this host can serve — is injected by the CLI, which
// owns the engine table.
type Daemon struct {
	Sup *Supervisor
	// Dir is the daemon's state directory; StateDir() outside tests.
	Dir string
	// BuildArgv turns the source of what to serve into the engine command.
	// dc is the stored deploy config, or nil to serve from the Spinloop.
	BuildArgv func(dc *inference.DeployConfig) ([]string, error)
	// EngineKeyArgs turns the path of the written key file into the
	// arguments that gate this engine, or an error when the engine has no
	// way to be gated by a file. Supplied by the CLI, which owns engine
	// flag spellings; nil means no engine here can be gated.
	EngineKeyArgs func(dc *inference.DeployConfig, keyPath string) ([]string, error)
	// ValidateConfig rejects a pushed deploy config this host cannot serve.
	ValidateConfig func(inference.DeployConfig) error
	// Collector gathers system stats; nil skips them.
	Collector *metrics.Collector
	// SampleInterval is how often SampleActivity reads the engine's
	// counters; zero means DefaultSampleInterval.
	SampleInterval time.Duration
	// Now is the clock idle durations are measured against; nil means
	// time.Now. Injected the same way Collector.Run and BuildArgv are, so a
	// test can age an engine without waiting.
	Now func() time.Time
	// Logger receives the API request summaries and the engine lifecycle
	// records. Nil discards, so a daemon constructed in a test is silent
	// unless it asks not to be; the CLI sets it, and sets Sup.Logger to the
	// same logger alongside.
	Logger *slog.Logger
	// Version is the spinloop binary's build-time version string. Passed by the
	// CLI at construction time.
	Version string

	act    activity
	sample engineSample
	ready  readiness
	hist   systemHistory
	system systemSample

	mu         sync.Mutex
	runner     string
	model      string
	servedName string
	scrape     metrics.ScrapeTarget
	endpoint   *EngineEndpoint
}

// log reads the daemon's logger, defaulting to discarding.
func (d *Daemon) log() *slog.Logger {
	return loggerOr(d.Logger)
}

// now reads the daemon's clock, defaulting to the wall clock.
func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// SetScrape records where the running engine's own endpoint lives; an empty
// BaseURL means the address could not be determined. The Engine names the
// metrics dialect to scrape — empty for an engine with no /metrics — while
// the BaseURL is still used to probe /health for readiness. It is set
// alongside each start, so it always describes the engine that runs.
func (d *Daemon) SetScrape(target metrics.ScrapeTarget) {
	d.mu.Lock()
	d.scrape = target
	d.mu.Unlock()
}

// SetEngineEndpoint records where the engine about to run serves inference,
// for status to report. Set alongside each start, beside SetScrape, so it
// always describes the engine that runs; nil means the endpoint could not be
// determined, and status then reports none rather than guessing one.
func (d *Daemon) SetEngineEndpoint(ep *EngineEndpoint) {
	d.mu.Lock()
	d.endpoint = ep
	d.mu.Unlock()
}

func (d *Daemon) engineEndpoint() *EngineEndpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.endpoint
}

// SetServed records what the daemon is serving, for status and metrics. The
// served name is the name the engine answers to — an alias when one is set —
// and is empty when the engine answers only to the model.
func (d *Daemon) SetServed(runner, model, servedName string) {
	d.mu.Lock()
	d.runner, d.model, d.servedName = runner, model, servedName
	d.mu.Unlock()
}

func (d *Daemon) served() (string, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runner, d.model, d.servedName
}

// configPath is the stored deploy config's location in the state directory.
func (d *Daemon) configPath() string {
	return filepath.Join(d.Dir, "deploy-config.json")
}

// StoredConfig reads the persisted deploy config; nil with no error when none
// has ever been pushed.
func (d *Daemon) StoredConfig() (*inference.DeployConfig, error) {
	data, err := os.ReadFile(d.configPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dc inference.DeployConfig
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", d.configPath(), err)
	}
	return &dc, nil
}

// Push validates and persists a deploy config, which subsequent starts serve.
// A running engine is deliberately untouched — the config takes effect on the
// next start. The file is 0600: serve args can carry sensitive flags.
func (d *Daemon) Push(dc inference.DeployConfig) error {
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
	d.SetServed(dc.Runner, dc.ModelID, dc.ServedModelName)
	return nil
}

// StartRequest is the body of a start call: what to run, and the key the
// engine is gated with. One definition so the client that sends it and the
// daemon that reads it cannot drift.
type StartRequest struct {
	inference.DeployConfig
	// EngineAPIKey gates the engine. It is supplied by the caller — a node
	// sources no key of its own — and travels with the config it
	// accompanies: a start carrying a config and no key opens the engine,
	// while a start carrying neither reuses what was stored.
	EngineAPIKey string `json:"engineApiKey,omitempty"`
}

// engineKeyFile is where a supplied key is kept: written for the engine to
// read, and re-read on a later bare start so a restart is gated the same way.
func (d *Daemon) engineKeyFile() string {
	return filepath.Join(d.Dir, "engine-api-key")
}

// SetEngineKey stores the key the engine is to be gated with, or clears it
// when given none. The file is 0600 and replaced rather than appended: one
// key, never an accumulation.
func (d *Daemon) SetEngineKey(key string) error {
	if key == "" {
		if err := os.Remove(d.engineKeyFile()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(d.engineKeyFile(), []byte(key), 0o600)
}

// storedEngineKeyPath is the key file's path when one is stored, else "".
func (d *Daemon) storedEngineKeyPath() string {
	if _, err := os.Stat(d.engineKeyFile()); err != nil {
		return ""
	}
	return d.engineKeyFile()
}

// StartEngine starts the engine from the stored deploy config, gated with the
// stored key when there is one. With nothing stored, BuildArgv reports what is
// missing.
func (d *Daemon) StartEngine() error {
	dc, err := d.StoredConfig()
	if err != nil {
		d.log().Error("engine start failed", slog.String("error", err.Error()))
		return err
	}
	source := "spinloop"
	if dc != nil {
		source = "deploy-config"
	}
	argv, err := d.BuildArgv(dc)
	if err != nil {
		// The reason a start never happened is the thing worth having: before
		// this, "nothing to serve" reached the caller and vanished.
		d.log().Error("engine start failed",
			slog.String("source", source), slog.String("error", err.Error()))
		return err
	}
	// The key reaches the engine as a path, never a value: a command line is
	// readable by every local user, and the key exists to exclude them.
	if keyPath := d.storedEngineKeyPath(); keyPath != "" {
		if d.EngineKeyArgs == nil {
			return fmt.Errorf("this engine cannot be gated: no key-file option is known for it")
		}
		keyArgs, err := d.EngineKeyArgs(dc, keyPath)
		if err != nil {
			return err
		}
		argv = append(argv, keyArgs...)
	}
	if dc != nil {
		d.SetServed(dc.Runner, dc.ModelID, dc.ServedModelName)
	}
	runner, model, _ := d.served()
	d.log().Info("starting engine",
		slog.String("source", source),
		slog.String("runner", runner),
		slog.String("model", model))
	if err := d.Sup.Start(argv); err != nil {
		d.log().Error("engine start failed",
			slog.String("source", source), slog.String("error", err.Error()))
		return err
	}
	// A start is activity in its own right. It is what stops a freshly woken
	// instance from reporting that it has been idle since before its engine
	// existed — the race the control plane used to close with a last-wake
	// timestamp of its own.
	d.act.markActive(d.now())
	// The previous engine's counters and system readings must not be reported
	// against this one, for the same reason its counter baseline is dropped.
	d.sample.forget()
	d.ready.forget()
	d.system.forget()
	d.hist.clear()
	return nil
}

// StatusResponse is the control API's status reply.
type StatusResponse struct {
	State  string `json:"state"`
	Runner string `json:"runner,omitempty"`
	Model  string `json:"model,omitempty"`
	// ServedName is the name the running engine answers to — the served name
	// its deploy config or Spinloop set — reported beside the model id when
	// set: an aliased engine answers to both, and a caller may know either.
	ServedName    string `json:"servedName,omitempty"`
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
	// Engine says where the running engine serves inference. Absent unless
	// an engine is running: an address for a process that does not exist is
	// worse than no address.
	Engine *EngineEndpoint `json:"engine,omitempty"`
	// Ready is "ready" or "not-ready": whether the running engine has last
	// answered its own health check, distinct from State reaching "running" —
	// the process can be alive while still loading weights. Absent, not
	// "not-ready", when it does not apply: no engine is running, the
	// running engine's runner has no known health-check convention, or this
	// daemon predates the check. Mirrored on metrics.Stats.Ready, from the
	// same record.
	Ready string `json:"ready,omitempty"`
	// Version is the spinloop binary's build-time version string. Set from the
	// daemon's Version field, which the CLI passes at construction time.
	Version string `json:"version"`
}

// EngineEndpoint is where the supervised engine answers inference requests.
// It reports parts rather than a URL on purpose: a daemon knows its engine
// binds 127.0.0.1:8080, which is useless to anyone else, and it cannot know
// the name a client reaches this host by — a LAN name, a tailscale name, a
// published container port. The caller composes these against the host it
// already has. A node that does know that name — a cloud environment, whose
// control plane publishes the instance's address — reports it in Host, and the
// caller uses it in place of the host it would otherwise supply.
type EngineEndpoint struct {
	// Host is the name or address a client reaches the engine by, when the
	// node knows it. A daemon leaves it empty — it cannot know a client-facing
	// name — but a cloud environment's status fills it with the instance's
	// published address, which is all a caller needs.
	Host string `json:"host,omitempty"`
	// Port is the port the engine listens on — the engine's, never the
	// control API's.
	Port int `json:"port"`
	// Path is the OpenAI-compatible path prefix, when it is not the usual
	// /v1. Empty means the default.
	Path string `json:"path,omitempty"`
	// LoopbackOnly marks an engine bound to loopback, which therefore
	// answers only on this machine. It turns a remote caller's connection
	// refused into something it can explain.
	LoopbackOnly bool `json:"loopbackOnly,omitempty"`
	// RequiresKey says the engine was started with an API key, so a caller
	// needs one. The key itself is never reported: a caller authorised to
	// drive this node is not thereby authorised to be handed its engine's
	// credential.
	RequiresKey bool `json:"requiresKey,omitempty"`
}

// Status reports the supervised state, what is being served, where the
// engine's log lives, and how long the engine has been idle.
func (d *Daemon) Status() StatusResponse {
	state, _, uptime := d.Sup.Status()
	runner, model, servedName := d.served()
	resp := StatusResponse{
		State:         string(state),
		Runner:        runner,
		Model:         model,
		ServedName:    servedName,
		UptimeSeconds: uptime,
		LogPath:       d.Sup.LogPath,
		Version:       d.Version,
	}
	resp.LastActiveAt, resp.IdleSeconds = d.activity()
	// Only a running engine has an address worth reporting.
	if state == StateRunning {
		if ep := d.engineEndpoint(); ep != nil {
			// The endpoint is derived when the command is built, which is
			// before the key arguments are appended — so whether a key is
			// required is the daemon's own fact, not something to read back
			// out of an argv it has not finished assembling. A caller that
			// picks up an already-running node has nothing else to go on.
			reported := *ep
			if d.storedEngineKeyPath() != "" {
				reported.RequiresKey = true
			}
			resp.Engine = &reported
		}
		resp.Ready = d.readinessField()
	}
	return resp
}

// ReadyYes and ReadyNo are the two values StatusResponse.Ready and
// metrics.Stats.Ready take when a readiness reading applies. An empty Ready is
// neither of them: no reading has landed, so readiness is unknown rather than
// false, and a caller that gates on ReadyNo passes an unknown through.
const (
	ReadyYes = "ready"
	ReadyNo  = "not-ready"
)

// readinessField renders the shared readiness record as the string
// /v1/status and /v1/metrics both report: ReadyYes, ReadyNo, or "" when
// no reading has landed — before the first check, or for a runner with no
// known health-check convention. Callers gate this on the engine running;
// it does not check that itself, since both callers already have.
func (d *Daemon) readinessField() string {
	ready, have := d.ready.read()
	if !have {
		return ""
	}
	if ready {
		return ReadyYes
	}
	return ReadyNo
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
	runner, model, servedName := d.served()
	stats := metrics.Stats{
		State:         string(state),
		Runner:        runner,
		ModelID:       model,
		ServedName:    servedName,
		UptimeSeconds: uptime,
	}
	if state == StateRunning {
		// The host's figures come from the background sampler, never from a
		// collection taken here. Reading CPU costs a host command — `top -l 1`
		// on macOS — that takes longer the busier the host is, so collecting
		// inline made this handler block for seconds on exactly the machine
		// someone had opened the dashboard to watch, past the fleet client's
		// timeout, and the node rendered as unreachable.
		d.system.apply(&stats)
		// The engine's counters come from the background sampler, never from
		// a scrape taken here. A busy engine does not answer its own metrics
		// endpoint — llama.cpp serves it from the queue it serves inference
		// from — so scraping inline made this handler block for as long as
		// the engine had work, which is precisely when a caller is watching.
		tokens, err, baseURL := d.sample.read()
		if err != nil {
			// Reported, not swallowed. A silent omission here once hid a
			// scraper pointed at the wrong port for every cloud llama.cpp
			// deployment: the token block simply never appeared, and with
			// no observation to make, the activity record never moved.
			// An absent *source* is omitted quietly; a source that is
			// there and failing is an error worth showing.
			stats.Errors = append(stats.Errors,
				fmt.Sprintf("engine metrics scrape (%s): %v", baseURL, err))
		}
		stats.Tokens = tokens
		stats.Ready = d.readinessField()
	}
	// Outside the running branch, so a stopped or crashed engine still reports
	// when work last happened — the record survives a stop precisely so it can
	// answer that once every other figure here has nothing to say. And after
	// the scrape, so a poll reports the activity its own reading just
	// established rather than the record as it stood one call ago.
	stats.LastActiveAt, stats.IdleSeconds = d.activity()
	// The same survival rule: the readings up to the stop say what the engine
	// was doing until it stopped, so they are reported while the running-
	// engine figures above are absent.
	stats.History = d.hist.snapshot()
	return stats
}
