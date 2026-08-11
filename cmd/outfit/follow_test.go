package main

import (
	"context"
	"errors"
	"testing"
)

// followUntilInterrupted is the wiring both `remote logs -f` and `fleet logs
// -f` reach, and nothing exercised it: each command's tests drive the polling
// loop directly, so the wrapper that installs the signal handler and decides
// what a cancelled follow returns was never entered.

func TestFollowUntilInterruptedRunsTheLoop(t *testing.T) {
	ran := false
	err := followUntilInterrupted(func(ctx context.Context) error {
		ran = true
		if ctx == nil {
			t.Error("the loop should be handed a context")
		}
		if ctx.Err() != nil {
			t.Errorf("the context should be live when the loop starts, got %v", ctx.Err())
		}
		return nil
	})
	if err != nil {
		t.Errorf("returned %v, want nil", err)
	}
	if !ran {
		t.Error("the loop was never run")
	}
}

func TestFollowUntilInterruptedReturnsTheLoopsError(t *testing.T) {
	want := errors.New("the node fell over")
	err := followUntilInterrupted(func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Errorf("returned %v, want the loop's own error — a real failure must not be swallowed", err)
	}
}

func TestFollowUntilInterruptedCancelsTheContextOnReturn(t *testing.T) {
	// The deferred cancel must fire, or a loop that spawned work on the
	// context would leak it past the command's return.
	var held context.Context
	if err := followUntilInterrupted(func(ctx context.Context) error {
		held = ctx
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if held.Err() == nil {
		t.Error("the context should be cancelled once the loop returns")
	}
}
