package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Spinloop's ENV instructions reach the command that was given it, which is
// what lets a project's AWS profile or SPINLOOP_CLOUD_* overrides live in the
// Spinloop rather than in whatever shell is running the command.
func TestReadSpinloopEnv_AppliesENVInstructions(t *testing.T) {
	t.Setenv("SPINLOOP_READ_ENV_PROBE", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "Spinloop")
	body := "PROVIDER llamacpp\nENV SPINLOOP_READ_ENV_PROBE=from-the-spinloop\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyReadSpinloopEnv(path); err != nil {
		t.Fatalf("applyReadSpinloopEnv: %v", err)
	}
	if got := os.Getenv("SPINLOOP_READ_ENV_PROBE"); got != "from-the-spinloop" {
		t.Errorf("the ENV instruction did not reach the process: %q", got)
	}
}

// The .env beside the Spinloop fills what the environment has not already set,
// so a secret lives in a file rather than a command line.
func TestReadSpinloopEnv_AppliesTheAdjacentDotEnv(t *testing.T) {
	t.Setenv("SPINLOOP_READ_DOTENV_PROBE", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Spinloop"), []byte("PROVIDER llamacpp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SPINLOOP_READ_DOTENV_PROBE=from-the-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyReadSpinloopEnv(filepath.Join(dir, "Spinloop")); err != nil {
		t.Fatalf("applyReadSpinloopEnv: %v", err)
	}
	if got := os.Getenv("SPINLOOP_READ_DOTENV_PROBE"); got != "from-the-dotenv" {
		t.Errorf("the adjacent .env did not reach the process: %q", got)
	}
}

// Naming nothing applies nothing: a Spinloop in the working directory does not
// set variables for a command that was not told to read one.
func TestReadSpinloopEnv_EmptyPathAppliesNothing(t *testing.T) {
	t.Setenv("SPINLOOP_READ_IMPLICIT_PROBE", "")
	dir := t.TempDir()
	body := "PROVIDER llamacpp\nENV SPINLOOP_READ_IMPLICIT_PROBE=should-not-apply\n"
	if err := os.WriteFile(filepath.Join(dir, "Spinloop"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := applyReadSpinloopEnv(""); err != nil {
		t.Fatalf("applyReadSpinloopEnv(\"\"): %v", err)
	}
	if got := os.Getenv("SPINLOOP_READ_IMPLICIT_PROBE"); got != "" {
		t.Errorf("a Spinloop nobody named set %q — the verbs take one only when told", got)
	}
}

// A named Spinloop that cannot be read fails the command, rather than being
// skipped: the operator pointed at it.
func TestReadSpinloopEnv_UnreadableNamesItself(t *testing.T) {
	err := applyReadSpinloopEnv(filepath.Join(t.TempDir(), "nosuch", "Spinloop"))
	if err == nil {
		t.Fatal("a named Spinloop that cannot be read should fail")
	}
	if !strings.Contains(err.Error(), "Spinloop") {
		t.Errorf("the failure should name what it could not read, got %v", err)
	}
}

// The flag reaches the verbs through the real command line, and selects
// nothing: the target still has to be named.
func TestReadVerbsTakeTheSpinloopFlag(t *testing.T) {
	t.Setenv("SPINLOOP_READ_VERB_PROBE", "")
	dir := t.TempDir()
	body := "PROVIDER llamacpp\nENV SPINLOOP_READ_VERB_PROBE=applied\n"
	spinloopPath := filepath.Join(dir, "Spinloop")
	if err := os.WriteFile(spinloopPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFleetFile(t, oneNodeFleetBody)

	if err := cmdStatus([]string{"-O", spinloopPath}); err != nil {
		t.Errorf("status with a Spinloop: %v", err)
	}
	if got := os.Getenv("SPINLOOP_READ_VERB_PROBE"); got != "applied" {
		t.Errorf("the verb did not apply the Spinloop's ENV: %q", got)
	}
}
