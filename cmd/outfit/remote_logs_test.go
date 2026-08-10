package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// errFetchFailed stands in for a fetch failure that is not a cancellation.
var errFetchFailed = errors.New("reading logs failed")

// withRemoteEnvironment isolates the config home, registers one environment and
// puts an Outfit naming it in the working directory, so the command resolves the
// environment through the same path a real setup does.
func withRemoteEnvironment(t *testing.T, name string) {
	t.Helper()
	isolateConfig(t)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("Outfit",
		[]byte("PROVIDER llamacpp\nMODEL unsloth/Qwen3.6-27B-GGUF\nREMOTE "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := must1(remote.EnvConfigPath(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(remote.Config{
		StartURL:    "https://start.lambda-url.eu-west-1.on.aws/",
		StopURL:     "https://stop.lambda-url.eu-west-1.on.aws/",
		Region:      "eu-west-1",
		Environment: name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// stubLogsFetch substitutes the fetch for the duration of a test, handing the
// stub each query so it can assert on what the command asked for.
func stubLogsFetch(t *testing.T,
	fn func(q remote.LogQuery) (remote.LogResult, error)) {
	t.Helper()
	prev := logsFetchFn
	logsFetchFn = func(_ context.Context, _ remote.Config, q remote.LogQuery) (remote.LogResult, error) {
		return fn(q)
	}
	t.Cleanup(func() { logsFetchFn = prev })
}

// logEvent builds one event at a fixed instant, so rendered timestamps are
// stable across runs.
func logEvent(id string, offset time.Duration, source, instance, message string) remote.LogEvent {
	base := time.Date(2026, 8, 9, 11, 30, 0, 0, time.Local)
	return remote.LogEvent{
		Timestamp: base.Add(offset),
		Source:    source,
		Instance:  instance,
		Message:   message,
		ID:        id,
	}
}

func TestRunnerForAcceptsExactlyTheRunnersWithLogGroups(t *testing.T) {
	for _, runner := range remote.Runners {
		if _, err := runnerFor(runner); err != nil {
			t.Errorf("deploy rejects runner %q, but its engine log group %q is read: %v",
				runner, remote.EngineLogGroup(runner), err)
		}
	}
	// The reverse direction: a runner deploy accepts must have a group read for
	// it, or its instances' logs would be unreachable.
	for _, provider := range []string{"llamacpp", "vllm"} {
		runner, err := runnerFor(provider)
		if err != nil {
			t.Fatalf("runnerFor(%q): %v", provider, err)
		}
		found := false
		for _, known := range remote.Runners {
			if known == runner {
				found = true
			}
		}
		if !found {
			t.Errorf("runner %q can be deployed but has no engine log group in remote.Runners", runner)
		}
	}
}

func TestRemoteLogsRejectsBadFlagValues(t *testing.T) {
	cases := []struct {
		name   string
		source string
		format string
		since  time.Duration
		limit  int
		want   string
	}{
		{"source", "journal", "text", time.Hour, 10, "--source"},
		{"format", "engine", "yaml", time.Hour, 10, "--format"},
		{"since", "engine", "text", -time.Hour, 10, "--since"},
		{"limit", "engine", "text", time.Hour, 0, "--limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLogsFlags(tc.source, tc.format, tc.since, tc.limit)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %s", err, tc.want)
			}
		})
	}
	if err := validateLogsFlags("all", "json", time.Minute, 1); err != nil {
		t.Errorf("valid flags rejected: %v", err)
	}
}

func TestRemoteLogsDefaultsToTheEngineSourceAndAnHourWindow(t *testing.T) {
	var got remote.LogQuery
	stubLogsFetch(t, func(q remote.LogQuery) (remote.LogResult, error) {
		got = q
		return remote.LogResult{}, nil
	})
	withRemoteEnvironment(t, "prod")

	if err := cmdRemote([]string{"logs"}); err != nil {
		t.Fatal(err)
	}
	if got.Source != remote.LogSourceEngine {
		t.Errorf("source = %q, want engine by default", got.Source)
	}
	if got.Environment != "prod" {
		t.Errorf("environment = %q, want the resolved environment", got.Environment)
	}
	if got.Limit != 200 {
		t.Errorf("limit = %d, want the default 200", got.Limit)
	}
	if window := time.Since(got.Start); window < 55*time.Minute || window > 65*time.Minute {
		t.Errorf("window = %s, want about an hour", window)
	}
}

func TestRemoteLogsPassesTheFlagsThrough(t *testing.T) {
	var got remote.LogQuery
	stubLogsFetch(t, func(q remote.LogQuery) (remote.LogResult, error) {
		got = q
		return remote.LogResult{}, nil
	})
	withRemoteEnvironment(t, "prod")

	if err := cmdRemote([]string{"logs", "--source", "boot", "--since", "15m",
		"--limit", "5", "--instance", "i-42"}); err != nil {
		t.Fatal(err)
	}
	if got.Source != remote.LogSourceBoot {
		t.Errorf("source = %q, want boot", got.Source)
	}
	if got.Instance != "i-42" {
		t.Errorf("instance = %q, want i-42", got.Instance)
	}
	if got.Limit != 5 {
		t.Errorf("limit = %d, want 5", got.Limit)
	}
	if window := time.Since(got.Start); window < 10*time.Minute || window > 20*time.Minute {
		t.Errorf("window = %s, want about 15 minutes", window)
	}
}

func TestLogsTextOutputIsTimestampedAndUnlabelledForOneOrigin(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{Events: []remote.LogEvent{
			logEvent("a", 0, "engine", "i-1", "loading weights"),
			logEvent("b", time.Second, "engine", "i-1", "serving"),
		}}, nil
	})

	var buf bytes.Buffer
	if err := runLogsOnce(context.Background(), remote.Config{}, remote.LogQuery{}, "text",
		time.Hour, &buf); err != nil {
		t.Fatal(err)
	}
	want := "2026-08-09 11:30:00  loading weights\n2026-08-09 11:30:01  serving\n"
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestLogsTextOutputLabelsMixedOrigins(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{Events: []remote.LogEvent{
			logEvent("a", 0, "boot", "i-1", "downloading weights"),
			logEvent("b", time.Second, "engine", "i-1", "serving"),
		}}, nil
	})

	var buf bytes.Buffer
	if err := runLogsOnce(context.Background(), remote.Config{}, remote.LogQuery{}, "text",
		time.Hour, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "boot/i-1  downloading weights") ||
		!strings.Contains(buf.String(), "engine/i-1  serving") {
		t.Errorf("output =\n%s\nwant each line attributed to its source and instance", buf.String())
	}
}

func TestLogsReportsOmittedEvents(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{
			Events:  []remote.LogEvent{logEvent("a", 0, "engine", "i-1", "serving")},
			Omitted: 12,
		}, nil
	})

	var buf bytes.Buffer
	if err := runLogsOnce(context.Background(), remote.Config{}, remote.LogQuery{}, "text",
		time.Hour, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "12 earlier events omitted") {
		t.Errorf("output =\n%s\nwant the omission reported", buf.String())
	}
}

func TestLogsReportsAnEmptyWindowWithoutFailing(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{}, nil
	})

	var buf bytes.Buffer
	err := runLogsOnce(context.Background(), remote.Config{},
		remote.LogQuery{Environment: "prod", Source: "engine"}, "text", 30*time.Minute, &buf)
	if err != nil {
		t.Fatalf("an empty window is not a failure: %v", err)
	}
	if !strings.Contains(buf.String(), "no engine logs for environment \"prod\" in the last 30m0s") {
		t.Errorf("output = %q, want it to say nothing was logged in the window", buf.String())
	}
}

func TestLogsJSONOutputCarriesTheFields(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{Events: []remote.LogEvent{
			logEvent("a", 0, "engine", "i-1", "serving"),
		}}, nil
	})

	var buf bytes.Buffer
	if err := runLogsOnce(context.Background(), remote.Config{}, remote.LogQuery{}, "json",
		time.Hour, &buf); err != nil {
		t.Fatal(err)
	}
	var events []remote.LogEvent
	if err := json.Unmarshal(buf.Bytes(), &events); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if len(events) != 1 {
		t.Fatalf("decoded %d events, want 1", len(events))
	}
	if events[0].Source != "engine" || events[0].Instance != "i-1" || events[0].Message != "serving" {
		t.Errorf("event = %+v, want source, instance and message carried", events[0])
	}
	if events[0].Timestamp.IsZero() {
		t.Error("event carries no timestamp")
	}
}

func TestLogsJSONOutputIsAnEmptyArrayWhenNothingMatched(t *testing.T) {
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{}, nil
	})

	var buf bytes.Buffer
	if err := runLogsOnce(context.Background(), remote.Config{}, remote.LogQuery{}, "json",
		time.Hour, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("output = %q, want an empty array", buf.String())
	}
}

func TestFollowPrintsEachEventOnceAndStopsWhenCancelled(t *testing.T) {
	prev := logsFollowInterval
	logsFollowInterval = time.Millisecond
	t.Cleanup(func() { logsFollowInterval = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := logEvent("a", 0, "engine", "i-1", "loading weights")
	second := logEvent("b", time.Second, "engine", "i-1", "serving")

	polls := 0
	var windows []time.Time
	stubLogsFetch(t, func(q remote.LogQuery) (remote.LogResult, error) {
		polls++
		windows = append(windows, q.Start)
		switch polls {
		case 1:
			return remote.LogResult{Events: []remote.LogEvent{first}}, nil
		case 2:
			// The overlap re-reads the event already printed, alongside a new one.
			return remote.LogResult{Events: []remote.LogEvent{first, second}}, nil
		default:
			cancel()
			return remote.LogResult{Events: []remote.LogEvent{first, second}}, nil
		}
	})

	var buf bytes.Buffer
	if err := followLogsLoop(ctx, remote.Config{}, remote.LogQuery{}, "text", &buf); err != nil {
		t.Fatalf("a cancelled follow is a clean exit, got: %v", err)
	}
	if n := strings.Count(buf.String(), "loading weights"); n != 1 {
		t.Errorf("printed the first event %d times, want exactly once:\n%s", n, buf.String())
	}
	if n := strings.Count(buf.String(), "serving"); n != 1 {
		t.Errorf("printed the second event %d times, want exactly once:\n%s", n, buf.String())
	}
	if len(windows) < 2 || !windows[1].After(windows[0]) {
		t.Errorf("windows = %v, want each poll to start from the last event seen", windows)
	}
}

// Once a follow has seen a second origin it keeps labelling, so a poll that
// happens to return one instance's lines does not silently drop the prefix the
// lines before it carried.
func TestFollowKeepsLabellingOnceOriginsAreMixed(t *testing.T) {
	prev := logsFollowInterval
	logsFollowInterval = time.Millisecond
	t.Cleanup(func() { logsFollowInterval = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	polls := 0
	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		polls++
		switch polls {
		case 1:
			return remote.LogResult{Events: []remote.LogEvent{
				logEvent("a", 0, "boot", "i-1", "downloading"),
				logEvent("b", time.Second, "engine", "i-1", "serving"),
			}}, nil
		case 2:
			return remote.LogResult{Events: []remote.LogEvent{
				logEvent("c", 2*time.Second, "engine", "i-1", "still serving"),
			}}, nil
		default:
			cancel()
			return remote.LogResult{}, nil
		}
	})

	var buf bytes.Buffer
	if err := followLogsLoop(ctx, remote.Config{}, remote.LogQuery{}, "text", &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "engine/i-1  still serving") {
		t.Errorf("output =\n%s\nwant the later single-origin batch still labelled", buf.String())
	}
}

func TestFollowReturnsARealError(t *testing.T) {
	prev := logsFollowInterval
	logsFollowInterval = time.Millisecond
	t.Cleanup(func() { logsFollowInterval = prev })

	stubLogsFetch(t, func(remote.LogQuery) (remote.LogResult, error) {
		return remote.LogResult{}, errFetchFailed
	})

	var buf bytes.Buffer
	err := followLogsLoop(context.Background(), remote.Config{}, remote.LogQuery{}, "text", &buf)
	if err == nil {
		t.Fatal("a failure that is not a cancellation should be reported")
	}
}
