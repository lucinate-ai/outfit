package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/lucinate-ai/outfit/internal/metrics"
)

// DefaultSampleInterval is how often the daemon reads the engine's counters
// when nothing says otherwise. It only has to be short relative to the idle
// thresholds a caller applies (15 minutes in the cloud default) and long
// relative to one scrape (a 5s client timeout) — the whole point being that a
// quiet moment between two requests cannot be mistaken for idleness the way a
// single five-minute scrape can. It is deliberately not a flag or an env var:
// nothing about a deployment needs to tune it.
const DefaultSampleInterval = 15 * time.Second

// activity is the daemon's record of when its engine last did any work. It
// holds the last counter it observed so the next sample can tell movement from
// stillness, and the time of the most recent sample that counted as activity.
type activity struct {
	mu          sync.Mutex
	lastActive  time.Time
	lastCounter int
	haveCounter bool
	haveActive  bool
}

// observe folds one sample into the record. A nil tokens is a sample that
// failed — the engine's metrics endpoint did not answer — and deliberately
// changes nothing: it is not activity, and it is not evidence of idleness
// either, so a transient failure neither extends nor shortens the idle
// duration. "Unreachable means idle" is the control plane's policy to apply,
// against a daemon it cannot reach at all; a daemon that *is* answering should
// not be made to lie in either direction.
func (a *activity) observe(tokens *metrics.TokenStats, now time.Time) {
	if tokens == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// "Changed", not "increased": an engine restart resets its counters, and a
	// counter that went backwards is a sign of life, not of stillness. The
	// first counter seen only establishes the baseline — a start already
	// counted as activity (markActive), so reading it as movement would
	// double-count.
	moved := a.haveCounter && tokens.Counter != a.lastCounter
	a.lastCounter, a.haveCounter = tokens.Counter, true
	if tokens.Running > 0 || moved {
		a.lastActive, a.haveActive = now, true
	}
}

// markActive records activity outright, with no sample behind it, and drops
// the counter baseline. Starting an engine goes through here: a freshly
// started engine has never been idle, and its counters are about to start from
// scratch, so the previous engine's baseline must not survive into it.
func (a *activity) markActive(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastActive, a.haveActive = now, true
	a.lastCounter, a.haveCounter = 0, false
}

// snapshot reports when the engine was last active, and whether it ever has
// been. A daemon that has never run an engine reports nothing rather than
// claiming its own start time as activity.
func (a *activity) snapshot() (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastActive, a.haveActive
}

// MarkActive records that the engine has just started doing work. StartEngine
// calls it for itself; it is exported for the foreground `serve --api` path,
// which starts its engine through the supervisor directly.
func (d *Daemon) MarkActive() {
	d.act.markActive(d.now())
}

// SampleActivity reads the running engine's counters on a recurring schedule
// until ctx is cancelled — the daemon's own view of engine activity, taken
// whether or not anyone is asking. It samples only while an engine is running
// and only when a scrape target is known, so an idle daemon and an engine with
// no metrics endpoint both cost nothing and report no errors.
func (d *Daemon) SampleActivity(ctx context.Context) {
	interval := d.SampleInterval
	if interval <= 0 {
		interval = DefaultSampleInterval
	}
	// Take a reading immediately: /v1/metrics reports the last sample, so
	// waiting a whole interval first would leave a freshly started daemon
	// reporting no counters for no reason.
	d.sampleOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sampleOnce(ctx)
		}
	}
}

// sampleOnce takes one reading, feeding both a success and a failure through
// observe so there is exactly one place where a sample becomes activity. The
// reading is also kept, because /v1/metrics reports it rather than scraping
// the engine itself (see engineSample).
func (d *Daemon) sampleOnce(ctx context.Context) {
	if state, _, _ := d.Sup.Status(); state != StateRunning {
		return
	}
	// Copy the target under the lock and release before the HTTP call, as
	// Metrics does — a scrape must never hold the daemon's mutex.
	d.mu.Lock()
	scrape := d.scrape
	d.mu.Unlock()
	if scrape.BaseURL == "" {
		return
	}
	tokens, err := metrics.ScrapeTokenStats(ctx, scrape)
	if err != nil {
		tokens = nil
	}
	d.sample.record(tokens, err, scrape.BaseURL)
	d.act.observe(tokens, d.now())
}

// engineSample holds the most recent reading of the engine's counters, taken
// by the background sampler. /v1/metrics reports this rather than scraping on
// demand, because an engine that is busy does not answer its own metrics
// endpoint: llama.cpp serves /metrics from the same queue it serves inference
// from, so a scrape taken while a prompt is being processed waits for that
// prompt to finish. A handler that scraped inline therefore blocked for as
// long as the engine was busy — past any client's timeout — and the fleet view
// went blank exactly when there was something to watch.
//
// The cost is staleness bounded by the sample interval, which for counters
// rendered in a refreshing view is not a cost at all.
type engineSample struct {
	mu sync.Mutex
	// tokens is the last successful reading, nil when the last one failed.
	tokens *metrics.TokenStats
	// err is the last reading's failure, kept so a scraper pointed at the
	// wrong port is still reported rather than showing as absent counters.
	err error
	// baseURL is where that reading was taken, for the error message.
	baseURL string
	// have marks that a reading has been attempted at all.
	have bool
}

// record stores one reading, success or failure.
func (e *engineSample) record(tokens *metrics.TokenStats, err error, baseURL string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tokens, e.err, e.baseURL, e.have = tokens, err, baseURL, true
}

// read returns the last reading: the counters, and the failure to report when
// there are none. Both are zero before the first sample, which reads as an
// engine that has not been observed yet rather than as an error.
func (e *engineSample) read() (*metrics.TokenStats, error, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.have {
		return nil, nil, ""
	}
	return e.tokens, e.err, e.baseURL
}

// forget drops the reading, so a stopped engine's counters are not reported
// against the next one.
func (e *engineSample) forget() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tokens, e.err, e.baseURL, e.have = nil, nil, "", false
}
