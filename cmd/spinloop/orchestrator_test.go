package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// The command's help reads per the cli-ux conventions: a lowercase
// imperative, the flags the command's contract is built from, and the
// items file's default in place.
func TestOrchestratorHelpReadsPerTheCliConventions(t *testing.T) {
	isolateConfig(t)
	// The one-line description is a lowercase imperative phrase.
	short := orchestratorCmd().Short
	if short == "" || (rune(short[0]) >= 'A' && rune(short[0]) <= 'Z') || strings.HasSuffix(short, ".") {
		t.Errorf("the short description should be a lowercase phrase, got %q", short)
	}
	out := captureStdout(t, func() {
		_ = cmdOrchestrator([]string{"--help"})
	})
	for _, want := range []string{
		"--gateway",
		"--items",
		"./work.yaml",
		"--token-env",
		"OPENAI_API_KEY",
		"-f, --fleet",
		"-H, --harness",
		"--create-item-dirs",
		"--listen",
		"-l, --loopback",
		"--api-token",
		"--api-token-file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the help should carry %q, got:\n%s", want, out)
		}
	}
}

// Neither a flag nor a file names a gateway: the refusal names both ways to
// give one, before anything is resolved.
func TestCmdOrchestrator_NeitherFlagNorFileNamesBothWays(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator(nil)
	if err == nil {
		t.Fatal("a run with no gateway should fail")
	}
	for _, want := range []string{"--gateway", "fleet.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %q, got %v", want, err)
		}
	}
}

// A fleet file with no gateway section is refused, naming the file.
func TestCmdOrchestrator_AFleetFileWithoutAGatewaySectionNamesIt(t *testing.T) {
	isolateConfig(t)
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	t.Chdir(dir)
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator(nil)
	if err == nil || !strings.Contains(err.Error(), "fleet.yaml") || !strings.Contains(err.Error(), "--gateway") {
		t.Errorf("a file without a gateway section should fail naming the file and the flag, got %v", err)
	}
}

// The section's token variable is honoured where the token flag was not
// given, and an explicit flag wins over the section.
func TestCmdOrchestrator_TheSectionsTokenVariableIsHonoured(t *testing.T) {
	isolateConfig(t)
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	t.Chdir(dir)
	fleetFileIn(t, dir, "gateway:\n  name: the-fleet\n  url: http://gw:4000\n  tokenEnv: ORCH_FILE_TOKEN\nnodes:\n"+node.entry("gpu-box"))
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	t.Setenv("ORCH_FILE_TOKEN", "")
	t.Setenv("GW_ORCH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	err := cmdOrchestrator(nil)
	if err == nil || !strings.Contains(err.Error(), "ORCH_FILE_TOKEN") {
		t.Errorf("without a token flag the section's variable should be in force, got %v", err)
	}
	err = cmdOrchestrator([]string{"--token-env", "GW_ORCH_TOKEN"})
	if err == nil || !strings.Contains(err.Error(), "GW_ORCH_TOKEN") {
		t.Errorf("an explicit token flag should win over the section, got %v", err)
	}
}

// The fleet file in the working directory names the gateway: with no
// gateway flag, the run works against the section's address, and the banner
// names it. The token sits only in the .env beside the file, the way a
// gateway-routed launch reads it. The item names a tag no node carries, so
// nothing is launched.
func TestCmdOrchestrator_AFleetFileNamesTheGateway(t *testing.T) {
	isolateConfig(t)
	t.Setenv("ORCH_FILE_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	t.Chdir(dir)
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	mustWrite(t, filepath.Join(dir, ".env"), "ORCH_FILE_TOKEN=the-token\n")
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n  tags:\n    - gpu=a100\n")

	srv, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	// The file names the gateway the server just bound, so the command finds
	// it without a flag.
	fleetFileIn(t, dir, "gateway:\n  name: the-fleet\n  url: http://"+ln.Addr().String()+"\n  tokenEnv: ORCH_FILE_TOKEN\nnodes:\n"+node.entry("gpu-box"))

	stdout := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdOrchestrator([]string{"-l"}) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(stdout)
		if strings.Contains(string(data), "in the backlog") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(stdout)
	if !strings.Contains(string(data), "http://"+ln.Addr().String()) || !strings.Contains(string(data), "in the backlog") {
		t.Fatalf("the banner should name the gateway the fleet file gave, got:\n%s", data)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the interrupt should end the run without an error, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the interrupt did not end the run")
	}
}

// A token variable set nowhere is refused, naming the gateway and the
// variable, before an item is worked.
func TestCmdOrchestrator_AnUnsetTokenVariableNamesTheGatewayAndTheVariable(t *testing.T) {
	isolateConfig(t)
	t.Setenv("GW_ORCH_TOKEN", "")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "--token-env", "GW_ORCH_TOKEN"})
	if err == nil {
		t.Fatal("an unset token variable should fail the run")
	}
	for _, want := range []string{"http://gw:4000", "GW_ORCH_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure should name %q, got %v", want, err)
		}
	}
}

// A harness without a single-task form is refused at startup, naming it,
// rather than as every item it would work.
func TestCmdOrchestrator_AHarnessWithoutASingleTaskFormNamesIt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "--harness", "lucinate"})
	if err == nil || !strings.Contains(err.Error(), "lucinate") {
		t.Errorf("a harness without a single-task form should fail naming it, got %v", err)
	}
}

// A file that is not a list of items stops the command, naming the file,
// working no item.
func TestCmdOrchestrator_ANUnparseableItemsFileNamesIt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "not: [a list\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000"})
	if err == nil || !strings.Contains(err.Error(), "work.yaml") {
		t.Errorf("an unparseable items file should fail naming the file, got %v", err)
	}
}

// A gateway that does not answer ends the run, naming the gateway, working
// no item: no state is recorded for the backlog.
func TestCmdOrchestrator_AGatewayThatDoesNotAnswerEndsTheRunNamingIt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "-l"})
	if err == nil {
		t.Fatal("a gateway that does not answer should end the run")
	}
	if !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Errorf("the failure should name the gateway, got %v", err)
	}
	if _, err := os.Stat("work.yaml.state.json"); !os.IsNotExist(err) {
		t.Errorf("a run that worked no item records no state, got %v", err)
	}
}

// A gateway that refuses the token ends the run, naming the gateway and its
// answer, working no item.
func TestCmdOrchestrator_AGatewayThatRefusesTheTokenEndsTheRunNamingIt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-wrong-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"missing or invalid bearer token"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", srv.URL, "-l"})
	if err == nil {
		t.Fatal("a gateway that refuses the token should end the run")
	}
	if !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), "401") {
		t.Errorf("the failure should name the gateway and its answer, got %v", err)
	}
}

// The command works against a real gateway on loopback: the banner names the
// file and the gateway, the loop reads the topology, and the interrupt ends
// it cleanly. The item names a tag no node carries, so nothing is launched.
func TestCmdOrchestrator_WorksAgainstAGatewayOnLoopback(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n  tags:\n    - gpu=a100\n")

	srv, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// The banner goes to stdout; redirect it the way the gateway test does.
	stdout := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdOrchestrator([]string{"--gateway", "http://" + ln.Addr().String(), "-l"}) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(stdout)
		if strings.Contains(string(data), "in the backlog") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(stdout)
	if !strings.Contains(string(data), "work.yaml") || !strings.Contains(string(data), "in the backlog") {
		t.Fatalf("the banner should name the items file and the backlog, got:\n%s", data)
	}

	// Give the loop a few passes to read the topology and find no match.
	time.Sleep(200 * time.Millisecond)
	if st, err := os.ReadFile("work.yaml.state.json"); err == nil && strings.Contains(string(st), `"running"`) {
		t.Fatalf("an item no node matches should not be launched, state:\n%s", st)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the interrupt should end the run without an error, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the interrupt did not end the run")
	}
}

// --loopback and --listen are two answers to one question: the conflict is
// named before anything is resolved or served.
func TestCmdOrchestrator_LoopbackConflictsWithAnExplicitListen(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--loopback", "--listen", "127.0.0.1:4010"})
	if err == nil || !strings.Contains(err.Error(), "--loopback") || !strings.Contains(err.Error(), "--listen") {
		t.Errorf("the conflict should name both flags, got %v", err)
	}
}

// A non-loopback bind without a token is refused before serving, naming the
// address and the ways a token may be supplied.
func TestCmdOrchestrator_RefusesATokenlessNonLoopbackBind(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	t.Setenv("SPINLOOP_API_TOKEN", "")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "--listen", "0.0.0.0:4010"})
	if err == nil {
		t.Fatal("a tokenless non-loopback bind should be refused")
	}
	for _, want := range []string{"0.0.0.0:4010", "--api-token-file", "SPINLOOP_API_TOKEN", "--api-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %q, got %v", want, err)
		}
	}
}

// The API's token is resolved the daemon's way: the environment variable, a
// file, or the literal flag, two of which at once is a conflict.
func TestCmdOrchestrator_TheAPITokenIsResolvedTheDaemonsWay(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")

	// The environment: a non-loopback bind with it gets past the refusal,
	// and the run ends on the gateway, not the token.
	t.Setenv("SPINLOOP_API_TOKEN", "the-api-token")
	err := cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Fatalf("a token from the environment should get past the refusal, got %v", err)
	}

	// A file: likewise.
	t.Setenv("SPINLOOP_API_TOKEN", "")
	tokenFile := filepath.Join(dir, "token")
	mustWrite(t, tokenFile, "the-api-token\n")
	err = cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "--api-token-file", tokenFile})
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Fatalf("a token from a file should get past the refusal, got %v", err)
	}

	// Two sources at once is a conflict.
	err = cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "-l", "--api-token", "a", "--api-token-file", tokenFile})
	if err == nil || !strings.Contains(err.Error(), "--api-token") || !strings.Contains(err.Error(), "--api-token-file") {
		t.Fatalf("two token sources at once should be a conflict, got %v", err)
	}
}

// On loopback the work list API serves without a token: a caller needs
// nothing while the run works, the startup line names the address, and the
// clean interrupt takes the server down with the run, the state saved.
func TestCmdOrchestrator_LoopbackServesTheWorkListWithoutAToken(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	t.Setenv("SPINLOOP_API_TOKEN", "")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	t.Chdir(dir)
	// The item names a tag no node carries, so nothing is launched.
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n  tags:\n    - gpu=a100\n")

	srv, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// The banner goes to stdout; redirect it the way the gateway test does.
	stdout := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdOrchestrator([]string{"--gateway", "http://" + ln.Addr().String(), "-l"}) }()

	// The startup line names the address the work list answers on.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(stdout)
		if strings.Contains(string(data), "Work list on") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(stdout)
	if !strings.Contains(string(data), "Work list on 127.0.0.1:4010") {
		t.Fatalf("the startup line should name the work list's address, got:\n%s", data)
	}
	// Stdout is redirected to a file, not a terminal, so the startup work
	// list is the plain tab-separated line spinloop work list prints off a
	// pipe: the item's id and its state, backlog since no node carries the
	// tag it names.
	if !strings.Contains(string(data), "a\tbacklog") {
		t.Fatalf("the startup output should carry the work list, item %q backlog, got:\n%s", "a", data)
	}

	// A tokenless caller reads the work list while the run works.
	resp, err := http.Get("http://127.0.0.1:4010/v1/items")
	if err != nil {
		t.Fatalf("the work list API should answer on loopback: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"a"`) || !strings.Contains(string(body), `"backlog"`) {
		t.Fatalf("a tokenless caller should read the work list, got %d: %s", resp.StatusCode, body)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the interrupt should end the run without an error, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the interrupt did not end the run")
	}

	// The server is down with the run, the state saved.
	if resp, err := http.Get("http://127.0.0.1:4010/health"); err == nil {
		resp.Body.Close()
		t.Error("the interrupt should take the work list API down with the run")
	}
	if _, err := os.Stat("work.yaml.state.json"); err != nil {
		t.Errorf("the interrupt should leave the state saved: %v", err)
	}
}

// A restart's recovered state shows at startup: an item the state beside
// the file already records done shows done in the startup work list, not
// backlog.
func TestCmdOrchestrator_StartupShowsARestartsRecoveredState(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	t.Setenv("SPINLOOP_API_TOKEN", "")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	t.Chdir(dir)
	// Both items name a tag no node carries, so neither is launched: the
	// backlog one stays backlog, and the recorded one keeps its record.
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n  tags:\n    - gpu=a100\n"+
		"- id: b\n  instructions: do\n  dir: .\n  tags:\n    - gpu=a100\n")
	if err := orchestrator.SaveStateFile("work.yaml", map[string]orchestrator.ItemState{
		"a": {State: orchestrator.StateDone, Node: "gpu-box", EndedAt: "2026-01-01T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}

	srv, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	stdout := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdOrchestrator([]string{"--gateway", "http://" + ln.Addr().String(), "-l"}) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(stdout)
		if strings.Contains(string(data), "Work list on") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(stdout)
	if !strings.Contains(string(data), "a\tdone") {
		t.Errorf("the startup work list should show item %q's recorded state done, got:\n%s", "a", data)
	}
	if !strings.Contains(string(data), "b\tbacklog") {
		t.Errorf("the startup work list should show item %q with no record as backlog, got:\n%s", "b", data)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the interrupt should end the run without an error, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the interrupt did not end the run")
	}
}

// An unrecognised --dispatch stops the command before it works an item,
// naming the flag, the value, and the accepted set.
func TestCmdOrchestrator_AnUnrecognisedDispatchBackendNamesIt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "-l", "--dispatch", "vm"})
	if err == nil {
		t.Fatal("an unrecognised --dispatch should stop the command")
	}
	if !strings.Contains(err.Error(), "--dispatch") || !strings.Contains(err.Error(), "vm") ||
		!strings.Contains(err.Error(), "bare") || !strings.Contains(err.Error(), "docker") {
		t.Errorf("the failure should name the flag, the value, and the accepted set, got %v", err)
	}
	if _, err := os.Stat("work.yaml.state.json"); !os.IsNotExist(err) {
		t.Errorf("a run that worked no item records no state, got %v", err)
	}
}

// harness.yaml's dispatch: is the run's choice where --dispatch is not
// given: naming a value the command does not recognise stops it, naming
// harness.yaml's own field rather than the flag.
func TestCmdOrchestrator_HarnessYamlDispatchIsUsedWhenFlagNotGiven(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "dispatch: vm\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "-l"})
	if err == nil {
		t.Fatal("an unrecognised harness.yaml dispatch should stop the command")
	}
	if !strings.Contains(err.Error(), "harness.yaml") || !strings.Contains(err.Error(), "vm") {
		t.Errorf("the failure should name harness.yaml's own field and the value, got %v", err)
	}
}

// An explicit --dispatch wins over harness.yaml's dispatch:, even where
// harness.yaml names a value the command would otherwise refuse.
func TestCmdOrchestrator_ExplicitDispatchFlagWinsOverHarnessYaml(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "dispatch: vm\n")
	// bare, explicitly, reaches the (unreachable) gateway's own failure —
	// not harness.yaml's unrecognised value, which --dispatch bare never
	// consults.
	err := cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "-l", "--dispatch", "bare"})
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Errorf("an explicit --dispatch should win over harness.yaml's, got %v", err)
	}
}

// --dispatch docker checks the daemon once, at startup: an unreachable one
// stops the command before it works an item, naming the fix.
func TestCmdOrchestrator_DispatchDockerChecksTheDaemonAtStartup(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	// No `docker` on PATH stands in for a daemon that does not answer: both
	// are the docker backend's own reachability check failing before any
	// item is worked.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "-l", "--dispatch", "docker"})
	if err == nil {
		t.Fatal("an unreachable docker daemon should stop the command")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("the failure should name the fix, got %v", err)
	}
	if _, err := os.Stat("work.yaml.state.json"); !os.IsNotExist(err) {
		t.Errorf("a run that worked no item records no state, got %v", err)
	}
}

// A harness.yaml whose env collides with the token's own variable stops
// the command before it works an item, whether the file is the default
// beside the items file or one --harness-config names.
func TestCmdOrchestrator_HarnessYamlEnvCollisionNamesTheVariable(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "env:\n  OPENAI_API_KEY: nope\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "-l"})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("the failure should name the colliding variable, got %v", err)
	}
	if _, err := os.Stat("work.yaml.state.json"); !os.IsNotExist(err) {
		t.Errorf("a run that worked no item records no state, got %v", err)
	}
}

// --harness-config names a file read in preference to harness.yaml beside
// the items file.
func TestCmdOrchestrator_HarnessConfigFlagOverridesTheDefault(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "env:\n  OPENAI_API_KEY: nope\n")
	mustWrite(t, "other.yaml", "env:\n  FOO: bar\n")
	err := cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "-l", "--harness-config", "other.yaml"})
	// The default harness.yaml's collision would have stopped the command
	// before it ever reached the gateway; reaching the (unreachable)
	// gateway's own failure instead shows the flag's file was the one read.
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Errorf("the flag's file should be read instead of the colliding default, got %v", err)
	}
}

// harness.yaml's own harness: is used where --harness is not given.
func TestCmdOrchestrator_HarnessYamlHarnessIsUsedWhenFlagNotGiven(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "harness: not-a-real-harness\n")
	err := cmdOrchestrator([]string{"--gateway", "http://gw:4000", "-l"})
	if err == nil {
		t.Fatal("an unrecognised harness.yaml harness should stop the command")
	}
	if !strings.Contains(err.Error(), "not-a-real-harness") {
		t.Errorf("the failure should name harness.yaml's own value, got %v", err)
	}
}

// An explicit --harness wins over harness.yaml's harness:, even where
// harness.yaml names a value the command would otherwise refuse.
func TestCmdOrchestrator_ExplicitHarnessFlagWinsOverHarnessYaml(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OPENAI_API_KEY", "the-token")
	dir := t.TempDir()
	t.Chdir(dir)
	mustWrite(t, "work.yaml", "- id: a\n  instructions: do\n  dir: .\n")
	mustWrite(t, "harness.yaml", "harness: not-a-real-harness\n")
	// opencode, explicitly, reaches the (unreachable) gateway's own
	// failure — not harness.yaml's unrecognised value, which an explicit
	// --harness never consults.
	err := cmdOrchestrator([]string{"--gateway", "http://127.0.0.1:1", "-l", "--harness", "opencode"})
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Errorf("an explicit --harness should win over harness.yaml's, got %v", err)
	}
}
