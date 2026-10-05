package main

import (
	"strings"
	"testing"
	"time"
)

// The keep rides the stats read as an absolute deadline, and every surface that
// draws the active figure draws the keep as a relative figure after it — "active
// 2m 5s ago  keep for 2h" — on the same line. The control plane is the single
// home of the "is it still in the future?" judgement (it drops the field there),
// so these renderers only ever ask "does the read carry a deadline that is still
// ahead?" and draw it when it is, and nothing when it is not.

// keepNow pins the one-shot metrics clock and returns an RFC3339 deadline d
// after it, so a test can assert the exact relative figure that deadline
// renders.
func keepNow(t *testing.T, d time.Duration) string {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	metricsNow = func() time.Time { return at }
	t.Cleanup(func() { metricsNow = time.Now })
	return at.Add(d).UTC().Format(time.RFC3339)
}

// aLineContaining returns the first output line carrying every phrase, or "".
func aLineContaining(out string, phrases ...string) string {
	for _, line := range strings.Split(out, "\n") {
		ok := true
		for _, p := range phrases {
			if !strings.Contains(line, p) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	return ""
}

// A kept, active endpoint draws the keep after the active figure, on the same
// line — the point of sharing the line is that "when did it last do anything"
// and "how long is it kept" are read at a glance, not on two rows.
func TestCloudMetricsBarKeepsOnTheActiveLine(t *testing.T) {
	deadline := keepNow(t, 2*time.Hour)
	statsServer(t, `{
		"environment": "dev",
		"state": "running",
		"instanceType": "g6e.xlarge",
		"modelId": "unsloth/Qwen3.6-27B",
		"cpu": {"utilization": 24},
		"lastActiveAt": "2025-12-31T23:58:15Z",
		"idleSeconds": 125,
		"retainUntil": "`+deadline+`"
	}`)

	out := captureStdout(t, func() {
		if err := cmdMetrics([]string{"--env", "default", "--format=bar"}); err != nil {
			t.Fatalf("cmdMetrics: %v", err)
		}
	})
	if line := aLineContaining(out, "2m 5s ago", "keep for 2h"); line == "" {
		t.Errorf("bar format did not put the keep on the active line:\n%s", out)
	}
	// Beside the active figure, before the bars: a fact about the endpoint, not
	// a utilisation reading.
	if line := aLineContaining(out, "keep for 2h"); line != "" {
		if strings.Index(out, "CPU") < strings.Index(line, "keep for 2h") {
			t.Errorf("keep is not before the bars:\n%s", out)
		}
	}
}

// The table format draws the same combined line as a key-value row.
func TestCloudMetricsTableKeepsOnTheActiveRow(t *testing.T) {
	deadline := keepNow(t, 2*time.Hour)
	statsServer(t, `{
		"environment": "dev",
		"state": "running",
		"instanceType": "g6e.xlarge",
		"modelId": "unsloth/Qwen3.6-27B",
		"cpu": {"utilization": 24},
		"lastActiveAt": "2025-12-31T23:58:15Z",
		"idleSeconds": 125,
		"retainUntil": "`+deadline+`"
	}`)

	out := captureStdout(t, func() {
		if err := cmdMetrics([]string{"--env", "default", "--format=table"}); err != nil {
			t.Fatalf("cmdMetrics: %v", err)
		}
	})
	if line := aLineContaining(out, "active:", "2m 5s ago", "keep for 2h"); line == "" {
		t.Errorf("table format did not put the keep on the active row:\n%s", out)
	}
}

// The relative figure drops zero units: hours, minutes, and a mix each render
// their own short form.
func TestKeepDurationRendersRelatively(t *testing.T) {
	// A stopped, unactive endpoint shows the keep alone, so each duration is
	// read straight off the bar.
	for d, want := range map[time.Duration]string{
		2 * time.Hour:    "keep for 2h",
		24 * time.Minute: "keep for 24m",
		90 * time.Minute: "keep for 1h 30m",
		// A seconds remainder never surfaces: it floors into the minutes.
		2*time.Hour + 5*time.Minute + 30*time.Second: "keep for 2h 5m",
		24*time.Minute + 30*time.Second:              "keep for 24m",
		// Sub-minute still renders, as a minute.
		45 * time.Second: "keep for 1m",
	} {
		t.Run(want, func(t *testing.T) {
			deadline := keepNow(t, d)
			statsServer(t, `{
				"environment": "dev",
				"state": "stopped",
				"runner": "llamacpp",
				"modelId": "unsloth/Qwen3.6-27B",
				"retainUntil": "`+deadline+`"
			}`)
			out := captureStdout(t, func() {
				if err := cmdMetrics([]string{"--env", "default", "--format=bar"}); err != nil {
					t.Fatalf("cmdMetrics: %v", err)
				}
			})
			if !strings.Contains(out, want) {
				t.Errorf("%s: %s format missing %q:\n%s", d, "bar", want, out)
			}
		})
	}
}

// A stopped environment can still be kept: the deadline is the control plane's,
// not the engine's, so the keep survives the non-running short-circuit in both
// formats.
func TestCloudMetricsStoppedKeptStillShowsKeep(t *testing.T) {
	deadline := keepNow(t, 4*time.Hour)
	for format := range map[string]bool{"bar": true, "table": true} {
		t.Run(format, func(t *testing.T) {
			statsServer(t, `{
				"environment": "dev",
				"state": "stopped",
				"runner": "llamacpp",
				"modelId": "unsloth/Qwen3.6-27B",
				"retainUntil": "`+deadline+`"
			}`)
			out := captureStdout(t, func() {
				if err := cmdMetrics([]string{"--env", "default", "--format=" + format}); err != nil {
					t.Fatalf("cmdMetrics: %v", err)
				}
			})
			if !strings.Contains(out, "keep for 4h") {
				t.Errorf("%s format dropped the keep for a stopped, kept endpoint:\n%s", format, out)
			}
		})
	}
}

// No deadline on the read, no keep: the renderer does not invent one, and it
// leaves the active figure (now just "active") in place.
func TestCloudMetricsOmitsKeepWhenAbsent(t *testing.T) {
	for _, format := range []string{"bar", "table"} {
		t.Run(format, func(t *testing.T) {
			statsServer(t, `{
				"environment": "dev",
				"state": "running",
				"cpu": {"utilization": 24},
				"lastActiveAt": "2025-12-31T23:58:15Z",
				"idleSeconds": 125
			}`)
			out := captureStdout(t, func() {
				if err := cmdMetrics([]string{"--env", "default", "--format=" + format}); err != nil {
					t.Fatalf("cmdMetrics: %v", err)
				}
			})
			if strings.Contains(out, "keep for") {
				t.Errorf("%s format claimed a keep the read does not carry:\n%s", format, out)
			}
			if !strings.Contains(out, "2m 5s ago") {
				t.Errorf("%s format lost the active figure:\n%s", format, out)
			}
		})
	}
}

// The deadline rides the node's stats, so `fleet metrics` draws the keep after
// the active figure whatever the node's state. A faked daemon carrying the field
// stands in for a kept cloud environment on the same render path.
func TestFleetMetricsShowsKeep(t *testing.T) {
	deadline := keepNow(t, 2*time.Hour)
	fleetNodeWithMetrics(t, map[string]any{
		"state":        "running",
		"runner":       "llamacpp",
		"modelId":      "org/qwen",
		"cpu":          map[string]any{"utilization": 30.0},
		"lastActiveAt": "2025-12-31T23:58:15Z",
		"idleSeconds":  125,
		"retainUntil":  deadline,
	})

	out := captureStdout(t, func() {
		if err := cmdMetrics(nil); err != nil {
			t.Fatalf("cmdFleet metrics: %v", err)
		}
	})
	if line := aLineContaining(out, "2m 5s ago", "keep for 2h"); line == "" {
		t.Errorf("fleet metrics did not put the keep on the active line:\n%s", out)
	}
}

// A node whose read carries no deadline draws no keep, so a local daemon node —
// which never has one — is simply quieter on that figure.
func TestFleetMetricsOmitsKeepWhenAbsent(t *testing.T) {
	fleetNodeWithMetrics(t, map[string]any{
		"state":        "running",
		"runner":       "llamacpp",
		"modelId":      "org/qwen",
		"cpu":          map[string]any{"utilization": 30.0},
		"lastActiveAt": "2025-12-31T23:58:15Z",
		"idleSeconds":  125,
	})

	out := captureStdout(t, func() {
		if err := cmdMetrics(nil); err != nil {
			t.Fatalf("cmdFleet metrics: %v", err)
		}
	})
	if strings.Contains(out, "keep for") {
		t.Errorf("fleet metrics claimed a keep a local node never carries:\n%s", out)
	}
}
