package fleet

import (
	"strings"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/cloud"
)

// Every line a phase renders is computed from the phase and the time it is
// drawn at, so a phase that holds while the clock moves draws a different line
// each time. The clock here is fixed and moved by hand: nothing in a phase is
// read from the wall clock, so the same phase at two times is the whole test.
func TestRenderPhaseIsComputedAtDrawTime(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	// A capacity wait counts down towards the attempt it is waiting for.
	wait := StartPhase{Kind: PhaseWaitingCapacity, Since: base, RetryAt: base.Add(120 * time.Second), Detail: "no-capacity"}
	if got, want := RenderPhase(wait, base), "waiting for capacity — retrying in 2m 0s"; got != want {
		t.Errorf("RenderPhase(wait, base) = %q, want %q", got, want)
	}
	if got, want := RenderPhase(wait, base.Add(73*time.Second)), "waiting for capacity — retrying in 47s"; got != want {
		t.Errorf("the wait did not count down: %q, want %q", got, want)
	}
	// Past its due time, with the next attempt not yet reported.
	if got, want := RenderPhase(wait, base.Add(130*time.Second)), "waiting for capacity — retrying now"; got != want {
		t.Errorf("RenderPhase past the due time = %q, want %q", got, want)
	}
	// A refusal whose reply named no delay says what it is waiting for and
	// nothing about when, rather than a wrong number.
	noDue := StartPhase{Kind: PhaseWaitingCapacity, Since: base, Detail: "no-capacity"}
	if got, want := RenderPhase(noDue, base.Add(time.Minute)), "waiting for capacity"; got != want {
		t.Errorf("RenderPhase with no due time = %q, want %q", got, want)
	}

	// A boot counts up from when it began.
	boot := StartPhase{Kind: PhaseBooting, Since: base, Detail: "starting"}
	if got, want := RenderPhase(boot, base.Add(4*time.Minute+12*time.Second)), "booting (4m 12s)"; got != want {
		t.Errorf("RenderPhase(boot) = %q, want %q", got, want)
	}
	if got, want := RenderPhase(boot, base.Add(2*time.Hour)), "booting (2h 0m 0s)"; got != want {
		t.Errorf("the boot did not count up: %q, want %q", got, want)
	}

	// An attempt in flight, and a dropped connection carrying the transport
	// error it dropped with.
	if got, want := RenderPhase(StartPhase{Kind: PhaseAttempting, Since: base}, base), "waking the instance…"; got != want {
		t.Errorf("RenderPhase(attempting) = %q, want %q", got, want)
	}
	dropped := StartPhase{Kind: PhaseReconnecting, Since: base, RetryAt: base.Add(5 * time.Second), Detail: "EOF"}
	if got, want := RenderPhase(dropped, base.Add(time.Second)), "connection dropped (EOF) — retrying in 4s"; got != want {
		t.Errorf("RenderPhase(reconnecting) = %q, want %q", got, want)
	}
}

// A seeding reply is not a boot: no instance exists yet, and the line counts
// up from the first seeding reply. The attempts that follow it poll the same
// fetch, so they must not restart the clock.
func TestStartPhasesSeeding(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	var got []StartPhase
	progress, onState := StartPhases(func(p StartPhase) { got = append(got, p) })

	onState(cloud.StateInFlight)
	onState(stateSeeding)
	progress("seeding the weights (seed llamacpp--org-model--Q4_K_M); retrying in 60s")
	// The next attempt supersedes nothing: it polls the fetch the seeding
	// reply reported, and the reply that follows reports the same fetch.
	onState(cloud.StateInFlight)
	onState(stateSeeding)

	want := []StartPhaseKind{PhaseAttempting, PhaseSeeding}
	kinds := make([]StartPhaseKind, len(got))
	for i, p := range got {
		kinds[i] = p.Kind
	}
	if len(kinds) != len(want) {
		t.Fatalf("phases = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("phases = %v, want %v", kinds, want)
		}
	}
	if got[1].Detail != stateSeeding {
		t.Errorf("seeding phase detail = %q, want %q", got[1].Detail, stateSeeding)
	}

	// The rendered line counts up from the first seeding reply at two times.
	seed := StartPhase{Kind: PhaseSeeding, Since: base, Detail: stateSeeding}
	if line := RenderPhase(seed, base.Add(30*time.Second)); line != "seeding the weights (30s)" {
		t.Errorf("RenderPhase(seed, +30s) = %q", line)
	}
	if line := RenderPhase(seed, base.Add(13*time.Minute)); line != "seeding the weights (13m 0s)" {
		t.Errorf("RenderPhase(seed, +13m) = %q", line)
	}
	// A phase drawn without a clock renders its name alone, as a boot does.
	if line := RenderPhase(StartPhase{Kind: PhaseSeeding, Detail: stateSeeding}, base); line != "seeding the weights" {
		t.Errorf("RenderPhase(seed, no Since) = %q", line)
	}
}

// The fetch finishing is a handoff, not a continuation: the reply that
// reports the instance coming up enters a boot with its own clock, so the
// line counts the boot up from the handoff rather than carrying the
// fetch's elapsed time over into it.
func TestStartPhasesHandsTheSeedingOffToTheBoot(t *testing.T) {
	var got []StartPhase
	progress, onState := StartPhases(func(p StartPhase) { got = append(got, p) })

	onState(cloud.StateInFlight)
	onState(stateSeeding)
	progress("seeding the weights (seed llamacpp--org-model--Q4_K_M); retrying in 60s")
	// The weights are in: the next reply reports the instance coming up.
	onState(cloud.StateInFlight)
	onState("starting")
	progress("instance starting; retrying in 5s")

	want := []StartPhaseKind{PhaseAttempting, PhaseSeeding, PhaseBooting}
	kinds := make([]StartPhaseKind, len(got))
	for i, p := range got {
		kinds[i] = p.Kind
	}
	if len(kinds) != len(want) {
		t.Fatalf("phases = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("phases = %v, want %v", kinds, want)
		}
	}
	if got[2].Detail != "starting" {
		t.Errorf("boot phase detail = %q, want the control plane's state", got[2].Detail)
	}
	// The boot's clock starts at the handoff: a little after it began, the
	// line counts the boot, not the fetch that preceded it.
	if line := RenderPhase(got[2], got[2].Since.Add(7*time.Second)); line != "booting (7s)" {
		t.Errorf("rendered boot at +7s = %q, want the boot counting up from the handoff", line)
	}
}

// The mapping from cloud.Start's two callbacks onto phases: an attempt goes
// out, is refused for capacity with a due time for the next one, and the
// attempt that follows retires the refusal rather than leaving it standing
// while the instance boots — the defect this phase stream exists for.
func TestStartPhasesRetiresACapacityWait(t *testing.T) {
	var got []StartPhase
	progress, onState := StartPhases(func(p StartPhase) { got = append(got, p) })

	onState(cloud.StateInFlight)
	onState("no-capacity")
	progress("instance no-capacity; retrying in 120s")
	onState(cloud.StateInFlight)

	kinds := make([]StartPhaseKind, len(got))
	for i, p := range got {
		kinds[i] = p.Kind
	}
	want := []StartPhaseKind{PhaseAttempting, PhaseWaitingCapacity, PhaseWaitingCapacity, PhaseAttempting}
	if len(kinds) != len(want) {
		t.Fatalf("phases = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("phases = %v, want %v", kinds, want)
		}
	}
	// The retry-after reaches this caller only on the progress line, and it
	// lands on the wait the state established.
	due := got[2].RetryAt.Sub(got[2].Since)
	if due < 119*time.Second || due > 121*time.Second {
		t.Errorf("the capacity wait's due time is %s after it began, want about 2m", due)
	}
	if last := got[len(got)-1]; last.Kind == PhaseWaitingCapacity {
		t.Error("the capacity wait outlived the attempt that superseded it")
	}
}

// A reply reporting the instance coming up enters the boot, and the polls that
// follow are that same boot: its elapsed time counts from the first reply that
// reported it, rather than restarting on each poll.
func TestStartPhasesHoldsTheBootAcrossPolls(t *testing.T) {
	var got []StartPhase
	progress, onState := StartPhases(func(p StartPhase) { got = append(got, p) })

	onState(cloud.StateInFlight)
	onState("starting")
	progress("instance starting; retrying in 5s")
	onState(cloud.StateInFlight)
	onState("starting")

	if len(got) != 2 {
		t.Fatalf("phases = %+v, want the attempt and the boot only", got)
	}
	if got[1].Kind != PhaseBooting || got[1].Detail != "starting" {
		t.Errorf("second phase = %+v, want a boot reporting the control plane's state", got[1])
	}
}

// A dropped connection is its own phase, carrying the transport error and the
// wait before the retry, and the attempt that follows retires it.
func TestStartPhasesReportsADroppedConnection(t *testing.T) {
	var got []StartPhase
	progress, onState := StartPhases(func(p StartPhase) { got = append(got, p) })

	onState(cloud.StateInFlight)
	progress("connection dropped (unexpected EOF); retrying in 5s")

	last := got[len(got)-1]
	if last.Kind != PhaseReconnecting {
		t.Fatalf("last phase = %+v, want a reconnect", last)
	}
	if last.Detail != "unexpected EOF" {
		t.Errorf("detail = %q, want the transport error", last.Detail)
	}
	if line := RenderPhase(last, last.Since.Add(time.Second)); !strings.Contains(line, "retrying in 4s") {
		t.Errorf("rendered = %q, want the wait counting down", line)
	}
}
