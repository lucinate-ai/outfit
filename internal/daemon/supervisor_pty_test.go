//go:build !windows

package daemon

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSupervisorPTYCapture is the end of the chain the unit tests cover in
// pieces: the capture presents the engine's stdout as a terminal, so the
// output an engine gates on a terminal — a model download's progress among
// them — reaches the engine log at all, and the line the engine redraws in
// place is recorded as its state rather than as raw overwrites.
func TestSupervisorPTYCapture(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "engine.log")
	s := NewSupervisor(logPath)
	// The first line is written only because the engine sees a terminal on
	// its stdout; the bar is drawn the way the engines draw them, carriage
	// return and all; the last line never redraws.
	engine := stubEngine(t, `if [ -t 1 ]; then echo 'a line only a terminal gets'; fi
printf '\rDownloading m.gguf   1%%\r'
printf '\rDownloading m.gguf 100%%\r'
printf '\n'
echo 'a plain line'
echo "no_color=${NO_COLOR:-unset}"
# A coloured line, the way an engine that colours by terminal presence
# would write one to stderr — and only when the engine was not told off.
if [ -z "${NO_COLOR:-}" ]; then printf '\033[31mred\033[0m\n' 1>&2; fi
echo 'a stderr line' 1>&2`)

	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	// The state flips only after the pump's final record and the log file's
	// close, so the log is whole by the time the state says stopped.
	waitForState(t, s, StateStopped)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"a line only a terminal gets\n",
		"Downloading m.gguf   1%\n",
		"Downloading m.gguf 100%\n",
		"a plain line\n",
		// The stderr side of the capture goes to the log unaltered, as
		// before the pseudo-terminal existed.
		"a stderr line\n",
		// The engine was told its output is going to a file.
		"no_color=1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\033") {
		t.Errorf("an escape reached the log:\n%q", got)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("a carriage return reached the log:\n%q", got)
	}
}

// TestSupervisorPTYCaptureEndsWithALingeringChild covers the bound on the
// drain after the engine exits: a child that outlives the engine keeps the
// slave open, so the master never reports end of stream, and the capture
// closes it once ptyDrainTimeout has passed. The output written before the
// exit is still in the log.
func TestSupervisorPTYCaptureEndsWithALingeringChild(t *testing.T) {
	previous := ptyDrainTimeout
	ptyDrainTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ptyDrainTimeout = previous })

	logPath := filepath.Join(t.TempDir(), "engine.log")
	s := NewSupervisor(logPath)
	engine := stubEngine(t, `echo 'before the exit'
sleep 3 &
exit 0`)
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateStopped)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "before the exit\n") {
		t.Errorf("the log is missing the output written before the exit:\n%s", data)
	}
}

// TestSupervisorFallsBackWithoutAPTY covers the capture's fallback: where no
// pseudo-terminal can be opened, the engine's stdout goes to the log file as
// written — the carriage returns among them — and the engine is told its
// output is going to a file the same way. The spec's no-pseudo-terminal
// scenario, run with the opening stood in for so the branch is reachable
// whatever platform the test runs on.
func TestSupervisorFallsBackWithoutAPTY(t *testing.T) {
	previous := attachPTY
	attachPTY = func() (*os.File, *os.File, error) {
		return nil, nil, errors.New("no pseudo-terminal on this platform")
	}
	t.Cleanup(func() { attachPTY = previous })

	logPath := filepath.Join(t.TempDir(), "engine.log")
	s := NewSupervisor(logPath)
	engine := stubEngine(t, `if [ -t 1 ]; then echo 'a line only a terminal gets'; fi
printf '\rDownloading m.gguf   1%%\r'
printf '\rDownloading m.gguf 100%%\r'
printf '\n'
echo 'a plain line'
echo "no_color=${NO_COLOR:-unset}"`)

	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateStopped)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// The engine never saw a terminal on its stdout: the output it gates on
	// one is absent, and the bar's carriage returns stand in the log as
	// written — the redraw's own double among them, the normaliser being
	// what the fallback has no part in.
	if strings.Contains(got, "a line only a terminal gets") {
		t.Errorf("the engine saw a terminal it was not given:\n%s", got)
	}
	if !strings.Contains(got, "\rDownloading m.gguf   1%\r\rDownloading m.gguf 100%\r\n") {
		t.Errorf("the fallback altered the engine's stdout:\n%q", got)
	}
	if !strings.Contains(got, "a plain line\n") {
		t.Errorf("the log is missing a plain line:\n%s", got)
	}
	// The fallback is still the capture: the engine was told its output is
	// going to a file.
	if !strings.Contains(got, "no_color=1") {
		t.Errorf("the fallback changed the engine's environment:\n%s", got)
	}
	if strings.Contains(got, "\033") {
		t.Errorf("an escape reached the log:\n%q", got)
	}
}

// TestSupervisorForwardingIsVerbatim covers the capture's other branch: with
// no log path, the engine's output goes to the supervisor's own stdio
// verbatim — no pseudo-terminal, no normaliser, and no log file — the
// off-the-terminal serve case.
func TestSupervisorForwardingIsVerbatim(t *testing.T) {
	// Deterministic whatever the test runner's own environment says: the
	// forwarding path must leave the engine's colouring to the engine, and
	// an empty NO_COLOR is the "unset" answer for the question below.
	t.Setenv("NO_COLOR", "")
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW

	s := NewSupervisor("")
	engine := stubEngine(t, `printf 'raw \r bytes\n'
echo "no_color=${NO_COLOR:-unset}"
echo 'stderr as written' 1>&2`)
	if err := s.Start([]string{engine}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, StateStopped)
	outW.Close()
	errW.Close()
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := io.ReadAll(errR)
	if err != nil {
		t.Fatal(err)
	}

	// The carriage return survives: nothing between the engine and the
	// supervisor's stdio touched the bytes.
	if !strings.Contains(string(out), "raw \r bytes\n") {
		t.Errorf("the forwarding path altered the engine's stdout: %q", out)
	}
	// And the engine's own answer to the colour question is untouched:
	// the forwarding path told the engine nothing, because on a terminal
	// the engine's colour is wanted.
	if !strings.Contains(string(out), "no_color=unset\n") {
		t.Errorf("the forwarding path changed the engine's environment: %q", out)
	}
	if !strings.Contains(string(errOut), "stderr as written\n") {
		t.Errorf("the forwarding path altered the engine's stderr: %q", errOut)
	}
}
