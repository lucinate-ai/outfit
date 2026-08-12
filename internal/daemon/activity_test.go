//go:build !windows

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// baseTime is a fixed clock origin, so an idle duration is arithmetic rather
// than something to wait for.
var baseTime = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func tokens(running, counter int) *metrics.TokenStats {
	return &metrics.TokenStats{Running: running, Counter: counter}
}

func TestActivityObserve(t *testing.T) {
	var a activity

	// Nothing observed yet: no activity to report.
	if _, ok := a.snapshot(); ok {
		t.Fatal("fresh activity reports a last-active time")
	}

	// The first counter is a baseline, not movement — a start already counted
	// as activity, so reading it as a change would double-count.
	a.observe(tokens(0, 100), baseTime)
	if _, ok := a.snapshot(); ok {
		t.Error("the first sample counted as activity, want baseline only")
	}

	// An unchanged counter with nothing in flight is stillness.
	a.observe(tokens(0, 100), baseTime.Add(time.Minute))
	if _, ok := a.snapshot(); ok {
		t.Error("an unchanged counter counted as activity")
	}

	// A moved counter is activity even with nothing in flight — that is the
	// request that started and finished between two samples.
	moved := baseTime.Add(2 * time.Minute)
	a.observe(tokens(0, 150), moved)
	if got, ok := a.snapshot(); !ok || !got.Equal(moved) {
		t.Errorf("after a moved counter: last active = %v (%v), want %v", got, ok, moved)
	}

	// A lower counter is an engine restart: a sign of life, not stillness.
	reset := baseTime.Add(3 * time.Minute)
	a.observe(tokens(0, 5), reset)
	if got, _ := a.snapshot(); !got.Equal(reset) {
		t.Errorf("after a counter reset: last active = %v, want %v", got, reset)
	}

	// Requests in flight are activity regardless of the counter.
	inFlight := baseTime.Add(4 * time.Minute)
	a.observe(tokens(2, 5), inFlight)
	if got, _ := a.snapshot(); !got.Equal(inFlight) {
		t.Errorf("with requests in flight: last active = %v, want %v", got, inFlight)
	}

	// A failed sample is a non-observation: it neither counts as activity nor
	// clears the baseline, so the next real sample still compares correctly.
	a.observe(nil, baseTime.Add(5*time.Minute))
	if got, _ := a.snapshot(); !got.Equal(inFlight) {
		t.Errorf("a failed sample moved last active to %v, want %v", got, inFlight)
	}
	a.observe(tokens(0, 5), baseTime.Add(6*time.Minute))
	if got, _ := a.snapshot(); !got.Equal(inFlight) {
		t.Errorf("the sample after a failure was misread as movement (last active %v)", got)
	}
}

func TestActivityMarkActiveDropsBaseline(t *testing.T) {
	var a activity
	a.observe(tokens(0, 900), baseTime)

	// A start is activity, and the next engine's counters begin from scratch:
	// the previous engine's baseline must not survive into it.
	started := baseTime.Add(time.Minute)
	a.markActive(started)
	if got, ok := a.snapshot(); !ok || !got.Equal(started) {
		t.Fatalf("after markActive: last active = %v (%v), want %v", got, ok, started)
	}
	a.observe(tokens(0, 3), baseTime.Add(2*time.Minute))
	if got, _ := a.snapshot(); !got.Equal(started) {
		t.Errorf("the new engine's first counter counted as movement (last active %v)", got)
	}
}

func TestDaemonStatusIdleTime(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	now := baseTime
	d.Now = func() time.Time { return now }

	// Nothing has ever run: no activity is reported, rather than the daemon's
	// own start time being claimed as some.
	if got := d.Status(); got.LastActiveAt != "" || got.IdleSeconds != 0 {
		t.Fatalf("status before any engine = %+v, want no activity", got)
	}

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)

	// A start is activity, so a freshly started engine is never long-idle.
	got := d.Status()
	if got.LastActiveAt != baseTime.Format(time.RFC3339) {
		t.Errorf("lastActiveAt = %q, want %q", got.LastActiveAt, baseTime.Format(time.RFC3339))
	}
	if got.IdleSeconds != 0 {
		t.Errorf("idleSeconds on a fresh start = %d, want 0", got.IdleSeconds)
	}

	// Idle time grows with the clock while nothing happens.
	now = baseTime.Add(20 * time.Minute)
	if got := d.Status(); got.IdleSeconds != 1200 {
		t.Errorf("idleSeconds after 20 min = %d, want 1200", got.IdleSeconds)
	}

	// Stopping the engine leaves the record alone: a stopped engine still
	// reports when real work last happened.
	if err := d.Sup.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := d.Status()
	if stopped.LastActiveAt != baseTime.Format(time.RFC3339) || stopped.IdleSeconds != 1200 {
		t.Errorf("status after stop = %+v, want the activity record preserved", stopped)
	}
}

// fakeEngine serves a llama.cpp-dialect /metrics whose counter the test moves.
type fakeEngine struct {
	mu      sync.Mutex
	counter int
}

func (f *fakeEngine) set(n int) {
	f.mu.Lock()
	f.counter = n
	f.mu.Unlock()
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/metrics" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	counter := f.counter
	f.mu.Unlock()
	fmt.Fprintf(w, "llamacpp:requests_processing 0\n"+
		"llamacpp:requests_deferred 0\n"+
		"llamacpp:prompt_tokens_total %d\n"+
		"llamacpp:tokens_predicted_total 0\n"+
		"llamacpp:n_decode_total 0\n", counter)
}

// fakeClock is a clock the test moves by hand. It is mutex-guarded because the
// sampler goroutine reads it concurrently.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// waitForBaseline polls until the sampler has taken its first reading. Moving
// the engine's counter before that lands would just move the baseline, and the
// movement would never be seen.
func waitForBaseline(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.act.mu.Lock()
		have := d.act.haveCounter
		d.act.mu.Unlock()
		if have {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the sampler never took a first reading")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForActive polls until the recorded last-active time is want, which is
// how the test waits on a background sampler without a wall-clock sleep.
func waitForActive(t *testing.T, d *Daemon, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, ok := d.act.snapshot(); ok && got.Equal(want) {
			return
		}
		if time.Now().After(deadline) {
			got, ok := d.act.snapshot()
			t.Fatalf("last active = %v (%v), want %v", got, ok, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSampleActivity(t *testing.T) {
	engineMetrics := &fakeEngine{counter: 100}
	engine := httptest.NewServer(engineMetrics)
	defer engine.Close()

	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	clock := &fakeClock{t: baseTime}
	d.Now = clock.now
	d.SampleInterval = time.Millisecond
	d.SetScrape(metrics.ScrapeTarget{BaseURL: engine.URL, Engine: "llamacpp"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.SampleActivity(ctx)

	// Nothing is running, so sampling stays quiet however long it ticks.
	time.Sleep(20 * time.Millisecond)
	if _, ok := d.act.snapshot(); ok {
		t.Fatal("sampled activity with no engine running")
	}

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	// The start itself is the activity on record; the sampler's first reading
	// only establishes the counter baseline.
	waitForActive(t, d, baseTime)
	waitForBaseline(t, d)

	// The counter moves; the sampler notices without anyone calling the API.
	moved := baseTime.Add(time.Minute)
	clock.set(moved)
	engineMetrics.set(500)
	waitForActive(t, d, moved)

	// Cancelling ends the loop: a later move goes unobserved.
	cancel()
	time.Sleep(20 * time.Millisecond)
	clock.set(baseTime.Add(2 * time.Minute))
	engineMetrics.set(900)
	time.Sleep(50 * time.Millisecond)
	if got, _ := d.act.snapshot(); !got.Equal(moved) {
		t.Errorf("the sampler kept running after cancellation (last active %v)", got)
	}
}

// TestSampleOnceNoTarget covers the two quiet paths: an engine with no metrics
// endpoint is skipped, and an unreachable one records nothing rather than
// being read as either active or idle.
func TestSampleOnceNoTarget(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.Now = func() time.Time { return baseTime }
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	// StartEngine marked activity; clear the record so a sample is the only
	// thing that could set it.
	d.act = activity{}

	// No scrape target: nothing to sample.
	d.sampleOnce(context.Background())
	if _, ok := d.act.snapshot(); ok {
		t.Error("sampled with no scrape target")
	}

	// A target that does not answer: a non-observation, not activity.
	d.SetScrape(metrics.ScrapeTarget{BaseURL: "http://127.0.0.1:1", Engine: "llamacpp"})
	d.sampleOnce(context.Background())
	if _, ok := d.act.snapshot(); ok {
		t.Error("a failed scrape counted as activity")
	}
}

// TestStatusAPIReportsActivity checks the fields survive the JSON round-trip
// the control plane actually reads.
func TestStatusAPIReportsActivity(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	now := baseTime
	d.Now = func() time.Time { return now }
	srv := httptest.NewServer(d.Handler(""))
	defer srv.Close()

	status := func() map[string]any {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + "/v1/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var decoded map[string]any
		json.NewDecoder(resp.Body).Decode(&decoded)
		return decoded
	}

	// Before any engine, both fields are absent rather than zero — that is
	// what the control plane keys off.
	got := status()
	if _, ok := got["lastActiveAt"]; ok {
		t.Errorf("status before any engine carries lastActiveAt: %v", got)
	}
	if _, ok := got["idleSeconds"]; ok {
		t.Errorf("status before any engine carries idleSeconds: %v", got)
	}

	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	now = baseTime.Add(90 * time.Second)

	got = status()
	if got["lastActiveAt"] != baseTime.Format(time.RFC3339) {
		t.Errorf("lastActiveAt = %v, want %v", got["lastActiveAt"], baseTime.Format(time.RFC3339))
	}
	if got["idleSeconds"] != float64(90) {
		t.Errorf("idleSeconds = %v, want 90", got["idleSeconds"])
	}
	d.Sup.Stop()
}

// TestMarkActiveExported covers the wrapper `outfit serve --api` uses: it
// starts its engine through the supervisor directly, so it stamps the activity
// record itself rather than going through StartEngine.
func TestMarkActiveExported(t *testing.T) {
	d := testDaemon(t, "exit 0")
	d.Now = func() time.Time { return baseTime }

	if _, ok := d.act.snapshot(); ok {
		t.Fatal("a fresh daemon reports activity")
	}
	d.MarkActive()
	if got, ok := d.act.snapshot(); !ok || !got.Equal(baseTime) {
		t.Errorf("after MarkActive: last active = %v (%v), want %v", got, ok, baseTime)
	}
	if got := d.Status(); got.LastActiveAt != baseTime.Format(time.RFC3339) {
		t.Errorf("status after MarkActive = %+v, want the record reported", got)
	}
}

// TestMetricsObservesActivity covers the single-observe path: the background
// sampler is the only thing that reads the engine's counters, and /v1/metrics
// reports what it last saw. One observer means the record and the reply cannot
// hold different ideas of the latest counter.
func TestMetricsObservesActivity(t *testing.T) {
	engineMetrics := &fakeEngine{counter: 100}
	engine := httptest.NewServer(engineMetrics)
	defer engine.Close()

	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	clock := &fakeClock{t: baseTime}
	d.Now = clock.now
	d.SetScrape(metrics.ScrapeTarget{BaseURL: engine.URL, Engine: "llamacpp"})
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	defer d.Sup.Stop()

	// The first sample is the counter baseline, so the record still reads from
	// the start — the sampler is driven by hand here, one reading at a time.
	clock.set(baseTime.Add(time.Minute))
	d.sampleOnce(context.Background())
	if stats := d.Metrics(context.Background()); stats.Tokens == nil {
		t.Fatal("metrics carried no token stats")
	}
	if got, _ := d.act.snapshot(); !got.Equal(baseTime) {
		t.Errorf("the first metrics scrape counted as activity (last active %v)", got)
	}

	// A moved counter seen by the sampler is activity.
	moved := baseTime.Add(2 * time.Minute)
	clock.set(moved)
	engineMetrics.set(500)
	d.sampleOnce(context.Background())
	if got, _ := d.act.snapshot(); !got.Equal(moved) {
		t.Errorf("a moved counter seen via metrics was not recorded (last active %v)", got)
	}

	// A failed scrape is a non-observation on this path too: the record holds,
	// and the reply simply omits the token stats.
	engine.Close()
	clock.set(baseTime.Add(3 * time.Minute))
	d.sampleOnce(context.Background())
	stats := d.Metrics(context.Background())
	if stats.Tokens != nil {
		t.Errorf("a failed scrape still reported tokens: %+v", stats.Tokens)
	}
	if got, _ := d.act.snapshot(); !got.Equal(moved) {
		t.Errorf("a failed scrape moved last active to %v, want %v", got, moved)
	}
}

// TestMetricsReportsActivity covers what /v1/metrics now says about activity:
// the same answer /v1/status gives, from the same record, including after the
// engine has stopped and when there is nothing to report at all.
func TestMetricsReportsActivity(t *testing.T) {
	engineMetrics := &fakeEngine{counter: 100}
	engine := httptest.NewServer(engineMetrics)
	defer engine.Close()

	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	clock := &fakeClock{t: baseTime}
	d.Now = clock.now

	// A daemon that has never run an engine has nothing to report, and says so
	// by omission rather than by a zero value.
	stats := d.Metrics(context.Background())
	if stats.LastActiveAt != "" || stats.IdleSeconds != 0 {
		t.Errorf("a daemon that has served nothing reported activity: %+v", stats)
	}
	if body, _ := json.Marshal(stats); bytes.Contains(body, []byte("lastActiveAt")) ||
		bytes.Contains(body, []byte("idleSeconds")) {
		t.Errorf("absent activity still serialised: %s", body)
	}

	d.SetScrape(metrics.ScrapeTarget{BaseURL: engine.URL, Engine: "llamacpp"})
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	// The sampler takes a reading as soon as an engine is up; that first one
	// is the counter baseline, which is what lets the next one see movement.
	d.sampleOnce(context.Background())

	// Starting counts as activity, so the record reads from the start time.
	// Five minutes later the engine has done nothing more, and metrics reports
	// the start as the last activity with the elapsed time as the idle.
	clock.set(baseTime.Add(5 * time.Minute))
	stats = d.Metrics(context.Background())
	if stats.LastActiveAt != baseTime.Format(time.RFC3339) {
		t.Errorf("metrics last active = %q, want %q", stats.LastActiveAt, baseTime.Format(time.RFC3339))
	}
	if stats.IdleSeconds != 300 {
		t.Errorf("metrics idle = %d, want 300", stats.IdleSeconds)
	}

	// The two endpoints answer from one record: they cannot disagree.
	if status := d.Status(); status.LastActiveAt != stats.LastActiveAt ||
		status.IdleSeconds != stats.IdleSeconds {
		t.Errorf("status %q/%d and metrics %q/%d disagree",
			status.LastActiveAt, status.IdleSeconds, stats.LastActiveAt, stats.IdleSeconds)
	}

	// Work moves the record forward, and metrics reports the new time.
	worked := baseTime.Add(6 * time.Minute)
	clock.set(worked)
	engineMetrics.set(500)
	d.sampleOnce(context.Background())
	if stats = d.Metrics(context.Background()); stats.LastActiveAt != worked.Format(time.RFC3339) {
		t.Errorf("after work: metrics last active = %q, want %q", stats.LastActiveAt, worked.Format(time.RFC3339))
	}
	if stats.IdleSeconds != 0 {
		t.Errorf("an engine that just worked reported %ds idle, want 0", stats.IdleSeconds)
	}

	// Polling is not itself activity: reading the record must not refresh it,
	// or a --watch loop would keep an idle engine looking busy for as long as
	// someone is watching. Polling no longer even reads the engine, so this
	// holds by construction — it is asserted because it is the property that
	// matters, not the mechanism that delivers it.
	for i := 1; i <= 3; i++ {
		clock.set(worked.Add(time.Duration(i) * time.Minute))
		stats = d.Metrics(context.Background())
	}
	if stats.LastActiveAt != worked.Format(time.RFC3339) {
		t.Errorf("polling moved last active to %q, want %q", stats.LastActiveAt, worked.Format(time.RFC3339))
	}
	if stats.IdleSeconds != 180 {
		t.Errorf("after three quiet polls: idle = %d, want 180", stats.IdleSeconds)
	}

	// Stopping the engine leaves the record alone. Every running-engine figure
	// goes, and the activity stays — that is the whole point of keeping it.
	if err := d.Sup.Stop(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateStopped)
	clock.set(worked.Add(10 * time.Minute))
	stats = d.Metrics(context.Background())
	if stats.Tokens != nil || stats.CPU != nil || stats.Memory != nil || len(stats.GPUs) > 0 {
		t.Errorf("a stopped engine reported running-engine figures: %+v", stats)
	}
	if stats.LastActiveAt != worked.Format(time.RFC3339) || stats.IdleSeconds != 600 {
		t.Errorf("a stopped engine reported %q/%d, want %q/600",
			stats.LastActiveAt, stats.IdleSeconds, worked.Format(time.RFC3339))
	}
}

// TestMetricsReportsScrapeFailure covers the diagnosability half of the same
// bug: a scrape that fails must say so. Omitting the token block silently is
// what let a scraper pointed at the wrong port go unnoticed.
func TestMetricsReportsScrapeFailure(t *testing.T) {
	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	clock := &fakeClock{t: baseTime}
	d.Now = clock.now
	// A target nothing is listening on — the shape of the real failure.
	d.SetScrape(metrics.ScrapeTarget{BaseURL: "http://127.0.0.1:1", Engine: "llamacpp"})
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	defer d.Sup.Stop()

	d.sampleOnce(context.Background())
	stats := d.Metrics(context.Background())
	if stats.Tokens != nil {
		t.Errorf("a failed scrape still reported tokens: %+v", stats.Tokens)
	}
	if len(stats.Errors) == 0 {
		t.Fatal("a failed scrape reported no error — the silence this bug hid behind")
	}
	// The message names where it tried, so a wrong address is obvious rather
	// than something to infer from an absent block.
	if !strings.Contains(stats.Errors[0], "127.0.0.1:1") {
		t.Errorf("error %q does not name the address it tried", stats.Errors[0])
	}
}

// TestMetricsDoesNotBlockOnABusyEngine is the regression this caching exists
// for. llama.cpp serves /metrics from the same queue it serves inference from,
// so an engine processing a prompt does not answer a scrape until it finishes.
// The handler used to scrape inline, so /v1/metrics blocked for as long as the
// engine had work — past the fleet client's own timeout, which meant the fleet
// view went blank exactly when there was something worth watching.
func TestMetricsDoesNotBlockOnABusyEngine(t *testing.T) {
	// An engine that never answers its metrics endpoint, which is what a busy
	// llama.cpp looks like for the duration of a long prompt.
	blocked := make(chan struct{})
	defer close(blocked)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer engine.Close()

	d := testDaemon(t, `trap 'exit 0' TERM
while true; do sleep 0.05; done`)
	d.SetScrape(metrics.ScrapeTarget{BaseURL: engine.URL, Engine: "llamacpp"})
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d.Sup, StateRunning)
	defer d.Sup.Stop()

	done := make(chan metrics.Stats, 1)
	go func() { done <- d.Metrics(context.Background()) }()
	select {
	case stats := <-done:
		// The counters are absent — nothing has been sampled — but the
		// engine's state and everything else still answer.
		if stats.State != string(StateRunning) {
			t.Errorf("state = %q, want running", stats.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metrics blocked on a busy engine: a client watching the fleet would time out")
	}
}
