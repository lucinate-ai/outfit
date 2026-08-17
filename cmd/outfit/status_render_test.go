package main

import (
	"strings"
	"testing"
)

// The shared status view is where `remote status` and `fleet status` agree on
// the facts they both carry; this pins the wording so the two cannot diverge.
func TestStatusFactServingText(t *testing.T) {
	f := statusFact{
		State: "running", Model: "qwen", Runner: "llamacpp", Version: "1.2.0",
		UptimeSeconds: 30, LastActiveAt: "2026-01-02T00:00:00Z", IdleSeconds: 30,
	}
	got := f.servingText()
	for _, want := range []string{"llamacpp  qwen", "(up 30s)", "(last active 30s ago)", "(1.2.0)"} {
		if !strings.Contains(got, want) {
			t.Errorf("servingText %q missing %q", got, want)
		}
	}
}

func TestStatusFactServingTextLeavesOutAbsentFacts(t *testing.T) {
	// No uptime, no activity, no version: nothing is invented.
	if got := (statusFact{Model: "qwen"}).servingText(); got != "qwen" {
		t.Errorf("servingText = %q, want just the model", got)
	}
	// A runner with no model shows the runner alone.
	if got := (statusFact{Runner: "llamacpp"}).servingText(); got != "llamacpp" {
		t.Errorf("servingText = %q, want just the runner", got)
	}
	if got := (statusFact{}).servingText(); got != "" {
		t.Errorf("an empty fact should have no serving text, got %q", got)
	}
}
