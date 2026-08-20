package main

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// TestPflagParseForms pins the parse forms the flag-package migration had to
// keep, on the commands whose flag sets moved onto the tree. Every case stops
// at parse time or the first validation, so no case reaches the network or a
// real config: an unknown flag proves pflag's error spelling, and a
// downstream error proves the form before it was parsed.
func TestPflagParseForms(t *testing.T) {
	t.Run("attached value", func(t *testing.T) {
		isolateConfig(t)
		// No model and no alias, so this reaches selection validation — which
		// only happens if --provider=openrouter was parsed off one token.
		if err := cmdAdd([]string{"--provider=openrouter"}); err == nil ||
			!strings.Contains(err.Error(), "needs a model or an alias") {
			t.Errorf("add --provider=openrouter = %v, want the selection error", err)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		for name, call := range map[string]func() error{
			"add":           func() error { return cmdAdd([]string{"-p", "ollama", "--nope"}) },
			"apply":         func() error { return cmdApply([]string{"--nope"}) },
			"serve":         func() error { return cmdServe([]string{"--nope"}) },
			"fleet metrics": func() error { return cmdFleet([]string{"metrics", "--nope"}) },
			"remote start":  func() error { return cmdRemoteStart([]string{"--nope"}) },
			"daemon":        func() error { return cmdDaemon([]string{"--nope"}) },
		} {
			if err := call(); err == nil || !strings.Contains(err.Error(), "unknown flag: --nope") {
				t.Errorf("%s --nope = %v, want unknown-flag error", name, err)
			}
		}
	})

	t.Run("unknown shorthand", func(t *testing.T) {
		if err := cmdApply([]string{"-z"}); err == nil ||
			!strings.Contains(err.Error(), "unknown shorthand flag") {
			t.Errorf("apply -z = %v, want unknown-shorthand error", err)
		}
	})

	t.Run("flag after positional", func(t *testing.T) {
		// sortFlagsBeforeArgs used to move these before the positional; pflag
		// takes them in place. An unknown flag raised *after* the positional is
		// the proof that parsing continued past it.
		for name, call := range map[string]func() error{
			"fleet metrics": func() error { return cmdFleet([]string{"metrics", "someNode", "--nope"}) },
			"remote env":    func() error { return cmdRemoteEnv([]string{"somePath", "--nope"}) },
		} {
			if err := call(); err == nil || !strings.Contains(err.Error(), "unknown flag: --nope") {
				t.Errorf("%s <positional> --nope = %v, want unknown-flag error", name, err)
			}
		}
	})

	t.Run("daemon loopback conflict", func(t *testing.T) {
		// The separated forms must reach the same check from either order.
		for _, args := range [][]string{
			{"-l", "--api-addr", daemon.DefaultAPIAddr},
			{"--api-addr", daemon.DefaultAPIAddr, "-l"},
		} {
			if err := cmdDaemon(args); err == nil ||
				!strings.Contains(err.Error(), "both given") {
				t.Errorf("daemon %v = %v, want the loopback conflict", args, err)
			}
		}
	})

	t.Run("attached conflict", func(t *testing.T) {
		// The attached and the separated forms must reach the same check.
		err := cmdDaemon([]string{"--loopback", "--api-addr=" + daemon.DefaultAPIAddr})
		if err == nil || !strings.Contains(err.Error(), "both given") {
			t.Errorf("daemon --api-addr=... = %v, want the loopback conflict", err)
		}
	})
}
