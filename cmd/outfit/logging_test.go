//go:build !windows

package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// captureStderrFile redirects os.Stderr to a file for the test's duration and
// returns a reader for what has been written so far. Unlike captureStderr it
// does not wait for the function to return — the daemon under test runs until
// it is signalled, and its log has to be readable while it does.
func captureStderrFile(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		f.Close()
	})
	return func() string {
		data, _ := os.ReadFile(path)
		return string(data)
	}
}

// waitForLog polls the captured log until it contains want.
func waitForLog(t *testing.T, read func() string, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if out := read(); strings.Contains(out, want) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("log never contained %q; log so far:\n%s", want, read())
	return ""
}

// freeAddr returns a loopback address nothing is listening on. The daemon's
// own address cannot be discovered from the log in these tests: at warn the
// startup record is silenced, which is the very thing under test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// runDaemon starts `outfit daemon` with args against a stub engine, returning
// the API's base URL, the log reader, and a stop function. The daemon takes no
// Outfit — it is a worker driven by its API — so the token comes from the
// environment and nothing here names a model.
func runDaemon(t *testing.T, args ...string) (string, func() string, func()) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "sekrit")
	stubEngineDaemon(t, filepath.Join(t.TempDir(), "args"))
	t.Chdir(t.TempDir())

	addr := freeAddr(t)
	read := captureStderrFile(t)
	done := make(chan error, 1)
	argv := append([]string{"--api-addr", addr}, args...)
	go func() { done <- cmdDaemon(argv) }()

	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		// Authenticated, so the readiness probe is an ordinary served request
		// rather than a rejection the level tests would then have to discount.
		req, err := http.NewRequest("GET", base+"/v1/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer sekrit")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never answered on %s; log:\n%s", addr, read())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop := func() {
		interruptSelf(t)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("daemon exited with %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("daemon did not exit on SIGINT")
		}
	}
	return base, read, stop
}

func TestCmdDaemon_SummarisesRequestsByDefault(t *testing.T) {
	base, read, stop := runDaemon(t)
	defer stop()

	if code, _ := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); code != 200 {
		t.Fatalf("status = %d", code)
	}
	out := waitForLog(t, read, "api request")
	if !strings.Contains(out, "path=/v1/status") || !strings.Contains(out, "status=200") {
		t.Errorf("summary missing path or status:\n%s", out)
	}
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("a served request was not summarised at info:\n%s", out)
	}
}

func TestCmdDaemon_LogLevelSilencesSuccessesNotRejections(t *testing.T) {
	base, read, stop := runDaemon(t, "--log-level", "warn")
	defer stop()

	// A poll a fleet client would make, repeatedly: silent at warn.
	for range 3 {
		if code, _ := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); code != 200 {
			t.Fatalf("status = %d", code)
		}
	}
	if out := read(); strings.Contains(out, "status=200") {
		t.Errorf("successful polls were logged at warn:\n%s", out)
	}
	// The rejection is not silenced with them.
	if code, _ := apiDo(t, "GET", base+"/v1/status", "wrong", ""); code != http.StatusUnauthorized {
		t.Fatal("expected a 401")
	}
	out := waitForLog(t, read, "status=401")
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("the rejection was not recorded at warn:\n%s", out)
	}
	if strings.Contains(out, "wrong") || strings.Contains(out, "sekrit") {
		t.Errorf("the log discloses a token:\n%s", out)
	}
}

func TestCmdDaemon_LogLevelFlagBeatsEnvironment(t *testing.T) {
	// The variable says be silent; the flag says be loud. The flag wins.
	t.Setenv(daemon.LevelEnvVar, "error")
	base, read, stop := runDaemon(t, "--log-level", "info")
	defer stop()

	if code, _ := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); code != 200 {
		t.Fatalf("status = %d", code)
	}
	waitForLog(t, read, "status=200")
}

func TestCmdDaemon_EnvironmentSetsTheLevel(t *testing.T) {
	t.Setenv(daemon.LevelEnvVar, "error")
	base, read, stop := runDaemon(t)
	defer stop()

	if code, _ := apiDo(t, "GET", base+"/v1/status", "sekrit", ""); code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Nothing at error severity happened, so nothing is logged.
	if out := read(); strings.Contains(out, "api request") {
		t.Errorf("OUTFIT_LOG_LEVEL=error did not silence a served request:\n%s", out)
	}
}

func TestCmdDaemon_UnknownLogLevelFailsBeforeListening(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(daemon.TokenEnvVar, "sekrit")
	t.Setenv(daemon.LevelEnvVar, "")
	t.Chdir(t.TempDir())

	err := cmdDaemon([]string{"--api-addr", "127.0.0.1:0", "--log-level", "chatty"})
	if err == nil {
		t.Fatal("an unrecognised log level was accepted")
	}
	if !strings.Contains(err.Error(), "chatty") {
		t.Errorf("error does not name the offending value: %v", err)
	}
	for _, name := range daemon.LevelNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name accepted level %q: %v", name, err)
		}
	}
}

func TestCmdServe_UnknownLogLevelIsRejectedWithoutAPI(t *testing.T) {
	// The flag is accepted with or without --api, so a value it would ignore
	// still has to be a value it understands.
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	err := cmdServe([]string{"--log-level", "chatty", "--dry-run", outfitPath})
	if err == nil || !strings.Contains(err.Error(), "chatty") {
		t.Fatalf("serve --log-level chatty = %v, want a rejection naming it", err)
	}
}

func TestCmdServe_DryRunOutputIsUnaffectedByTheLevel(t *testing.T) {
	// Narration is not a log record: --dry-run prints the same thing however
	// the level is set.
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nPRESET ./preset.ini\nALIAS qwen\n")
	plain := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Error(err)
		}
	})
	quiet := captureStdout(t, func() {
		if err := cmdServe([]string{"--log-level", "error", "--dry-run", outfitPath}); err != nil {
			t.Error(err)
		}
	})
	if plain != quiet {
		t.Errorf("the level changed narration:\n plain: %q\n quiet: %q", plain, quiet)
	}
	if !strings.Contains(plain, "llama-server") {
		t.Errorf("dry run printed no command: %q", plain)
	}
}
