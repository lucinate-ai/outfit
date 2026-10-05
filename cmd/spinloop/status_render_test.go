package main

import (
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// The shared status view is where `cloud status` and `fleet status` agree on
// the facts they both carry; this pins the wording so the two cannot diverge.
func TestStatusFactServingText(t *testing.T) {
	f := statusFact{
		State: "running", Model: "qwen", Runner: "llamacpp", Version: "1.2.0",
		UptimeSeconds: 30, LastActiveAt: "2026-01-02T00:00:00Z", IdleSeconds: 30,
	}
	got := f.servingText()
	for _, want := range []string{"llamacpp  qwen", "(up 30s)", "(active 30s ago)", "(1.2.0)"} {
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

// A node whose engine is up as a process but has not answered its health check
// is running by state and unusable in fact. Without the mark the row reads as
// serving the model, which is what made a failed request look unexplained.
func TestStatusFactMarksAnEngineThatHasNotAnswered(t *testing.T) {
	f := statusFact{
		State: "running", Model: "qwen", Runner: "llamacpp",
		UptimeSeconds: 90, Ready: daemon.ReadyNo,
	}
	got := f.servingText()
	if !strings.Contains(got, "(not ready)") {
		t.Errorf("servingText %q should mark the engine not ready", got)
	}
	if strings.Index(got, "(not ready)") > strings.Index(got, "(up 1m 30s)") {
		t.Errorf("the mark belongs before the timings it qualifies, got %q", got)
	}
}

// An engine that has answered is the ordinary case and carries no mark, and a
// daemon reporting no reading is unknown rather than not ready.
func TestStatusFactMarksNothingWhenReadyOrUnknown(t *testing.T) {
	for _, ready := range []string{daemon.ReadyYes, ""} {
		f := statusFact{State: "running", Model: "qwen", Ready: ready}
		if got := f.servingText(); got != "qwen" {
			t.Errorf("Ready=%q: servingText = %q, want just the model", ready, got)
		}
	}
}
