package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A file that exists has not necessarily been written. The daemon tests poll
// for the stub engine's argv and the engine log, and a read that returned as
// soon as the file existed could see it empty or partial and fail an
// assertion about its contents. These pin the two halves of the fix: the
// helper waits for content, and the stub never exposes an empty argv file.

func TestWaitForFileContaining_SkipsAnEmptyAndAPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	go func() {
		_ = os.WriteFile(path, nil, 0o600) // created, nothing written yet
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(path, []byte("engine up\n"), 0o600) // the first line only
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(path, []byte("engine up\nengine down\n"), 0o600)
	}()

	got := waitForFileContaining(t, path, "engine up", "engine down")

	if got != "engine up\nengine down\n" {
		t.Errorf("returned before the wanted text was there: %q", got)
	}
}

func TestWaitForFile_DoesNotReturnAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "args")
	go func() {
		_ = os.WriteFile(path, nil, 0o600)
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(path, []byte("--metrics\n"), 0o600)
	}()

	if got := waitForFile(t, path); got != "--metrics\n" {
		t.Errorf("got %q, want the written argv", got)
	}
}

func TestStubEngineDaemon_ArgsFileNeverAppearsEmpty(t *testing.T) {
	// With a plain redirection the empty file was visible on most runs, so a
	// handful of runs is enough to catch a regression; each costs a process spawn.
	for i := 0; i < 10; i++ {
		argsFile := filepath.Join(t.TempDir(), "args")
		stubEngineDaemon(t, argsFile)
		cmd := exec.Command(llamaServerBinary, "--hf-repo", "org/model:Q4_K_M", "--metrics")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		// Read as fast as possible from the moment the engine starts, so a file
		// that is created before it is written would be caught in between.
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(argsFile)
			if err == nil {
				if len(data) == 0 {
					_ = cmd.Process.Kill()
					t.Fatalf("run %d: the argv file existed while empty", i)
				}
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatalf("run %d: the argv file never appeared", i)
			}
		}
		// Killed rather than asked to stop: the stub only acts on TERM between
		// its sleeps, and nothing here depends on how it exits.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
