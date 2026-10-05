package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rootExec dispatches the full root command tree with the given args and
// returns what was written to standard output and any error, the way the real
// binary would see it. Unlike the cmd* seams, this exercises the whole tree —
// Find, the root's Args validator, and each command's dispatch — so it is the
// seam for pinning how a top-level word routes.
func rootExec(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(args)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	exeErr := root.Execute()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), exeErr
}

// launched reports whether the stubbed harness binary was invoked (its args
// file exists), and if so returns the args it received.
func launched(t *testing.T, argsFile string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// TestRoot_MovedSpellingsNameTheirNewHome pins the error each removed
// top-level spelling gives: it must name the command that replaced it, and a
// word that never existed keeps Cobra's ordinary unknown-command error.
func TestRoot_MovedSpellingsNameTheirNewHome(t *testing.T) {
	isolateConfig(t)

	cases := map[string]string{
		"add":            "harness add",
		"remove":         "harness remove",
		"apply":          "harness apply",
		"unapply":        "harness unapply",
		"show":           "harness show",
		"export":         "harness export",
		"list":           "provider list",
		"init-providers": "provider init",
	}
	for old, newHome := range cases {
		_, err := rootExec(t, old)
		if err == nil {
			t.Errorf("%s: expected an error, got none", old)
			continue
		}
		want := fmt.Sprintf("%q moved: run spinloop %s", old, newHome)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %q, want it to contain %q", old, err.Error(), want)
		}
	}

	// A word that never existed is not a moved spelling, so it keeps the
	// ordinary unknown-command error rather than a moved-to message.
	_, err := rootExec(t, "frobnicate")
	if err == nil || !strings.Contains(err.Error(), `unknown command "frobnicate" for "spinloop"`) {
		t.Errorf("bogus word: error = %v, want the unknown-command error", err)
	}
}

// TestRoot_MovedSubcommandSpellingsNameTheirNewHome pins the same signpost one
// level down, for a subcommand a group used to have: "fleet metrics" and
// "cloud status" and their siblings must each name the top-level verb that
// replaced them, rather than cobra's ordinary unknown-command error.
func TestRoot_MovedSubcommandSpellingsNameTheirNewHome(t *testing.T) {
	isolateConfig(t)

	for spelling, newHome := range movedSubcommands {
		words := strings.Fields(spelling)
		_, err := rootExec(t, words...)
		if err == nil {
			t.Errorf("%s: expected an error, got none", spelling)
			continue
		}
		want := fmt.Sprintf("%q moved: run spinloop %s", spelling, newHome)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %q, want it to contain %q", spelling, err.Error(), want)
		}
	}
}

// TestRoot_HarnessSubcommandsDispatch pins that each of harness's six
// subcommands runs the subcommand and never launches the agent — the dispatch
// the grouping depends on.
func TestRoot_HarnessSubcommandsDispatch(t *testing.T) {
	home := isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)
	ocPath := filepath.Join(home, ".config", "opencode", "opencode.json")

	// add: writes the config.
	if _, err := rootExec(t, "harness", "add", "-p", "ollama", "-m", "llama3.2"); err != nil {
		t.Fatalf("harness add: %v", err)
	}
	if m := readConfigMap(t, ocPath); m["model"] != "ollama/llama3.2" {
		t.Errorf("harness add did not write the config: %v", m["model"])
	}

	// show: reports the config, does not launch.
	out, err := rootExec(t, "harness", "show")
	if err != nil {
		t.Fatalf("harness show: %v", err)
	}
	if !strings.Contains(out, "Harness:") || !strings.Contains(out, "ollama") {
		t.Errorf("harness show did not report the config:\n%s", out)
	}

	// export: renders a Spinloop, does not launch.
	out, err = rootExec(t, "harness", "export")
	if err != nil {
		t.Fatalf("harness export: %v", err)
	}
	if !strings.Contains(out, "PROVIDER ollama") {
		t.Errorf("harness export did not render a Spinloop:\n%s", out)
	}

	// remove: clears what add wrote.
	if _, err := rootExec(t, "harness", "remove", "-p", "ollama", "-m", "llama3.2"); err != nil {
		t.Fatalf("harness remove: %v", err)
	}
	if m := readConfigMap(t, ocPath); m["model"] == "ollama/llama3.2" {
		t.Errorf("harness remove left the model in place: %v", m["model"])
	}

	// apply: writes what the Spinloop selects.
	spath := filepath.Join(t.TempDir(), "Spinloop")
	mustWrite(t, spath, "PROVIDER llamacpp\nMODEL gemma\n")
	if _, err := rootExec(t, "harness", "apply", spath); err != nil {
		t.Fatalf("harness apply: %v", err)
	}
	if m := readConfigMap(t, ocPath); m["model"] != "llamacpp/gemma" {
		t.Errorf("harness apply did not write the selection: %v", m["model"])
	}

	// unapply: removes what apply wrote.
	if _, err := rootExec(t, "harness", "unapply", spath); err != nil {
		t.Fatalf("harness unapply: %v", err)
	}
	if m := readConfigMap(t, ocPath); m["model"] == "llamacpp/gemma" {
		t.Errorf("harness unapply left the model in place: %v", m["model"])
	}

	// None of the six may have launched the agent.
	if args, launched := launched(t, argsFile); launched {
		t.Errorf("a harness subcommand launched the agent with %q", args)
	}
}

// TestRoot_HarnessGroupDoesNotLaunch pins that the harness group does nothing
// on its own: a bare `harness` shows its help, and a first word that is not a
// subcommand gets the unknown-subcommand error. Neither launches the agent.
func TestRoot_HarnessGroupDoesNotLaunch(t *testing.T) {
	isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	// A bare harness shows the group's help and launches nothing.
	out, err := rootExec(t, "harness")
	if err != nil {
		t.Fatalf("bare harness: %v", err)
	}
	if !strings.Contains(out, "Available Commands") {
		t.Errorf("bare harness should list its subcommands:\n%s", out)
	}
	if args, launched := launched(t, argsFile); launched {
		t.Errorf("a bare harness launched the agent with %q", args)
	}

	// A first word that is not a subcommand is an unknown subcommand, not a launch.
	if _, err := rootExec(t, "harness", "run", "hello"); err == nil ||
		!strings.Contains(err.Error(), `unknown command "run" for "spinloop harness"`) {
		t.Errorf("harness run: error = %v, want the unknown-subcommand error", err)
	}
	if args, launched := launched(t, argsFile); launched {
		t.Errorf("harness run launched the agent with %q; only open launches", args)
	}
}

// TestRoot_HarnessOpenLaunches pins that `open` is the launch: a bare open
// launches with nothing forwarded, and a non-Spinloop first word is forwarded.
func TestRoot_HarnessOpenLaunches(t *testing.T) {
	isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	// A bare open launches with nothing forwarded.
	if _, err := rootExec(t, "harness", "open"); err != nil {
		t.Fatalf("harness open: %v", err)
	}
	if args, launched := launched(t, argsFile); !launched {
		t.Fatal("harness open did not launch the agent")
	} else if strings.TrimSpace(args) != "" {
		t.Errorf("harness open forwarded %q, want nothing", args)
	}

	// A first word that is not a Spinloop is forwarded to the harness.
	argsFile2 := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile2)
	if _, err := rootExec(t, "harness", "open", "run", "hello"); err != nil {
		t.Fatalf("harness open run: %v", err)
	}
	if args, launched := launched(t, argsFile2); !launched {
		t.Fatal("harness open run did not launch the agent")
	} else if strings.TrimSpace(args) != "run\nhello" {
		t.Errorf("harness open run forwarded %q, want \"run\\nhello\"", args)
	}
}

// TestRoot_CodeLaunches pins that `code` dispatches to the launch at the root:
// a bare code launches with nothing forwarded, and a non-Spinloop first word is
// forwarded — the same behaviour `harness open` has.
func TestRoot_CodeLaunches(t *testing.T) {
	isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	// A bare code launches with nothing forwarded.
	if _, err := rootExec(t, "code"); err != nil {
		t.Fatalf("code: %v", err)
	}
	if args, launched := launched(t, argsFile); !launched {
		t.Fatal("code did not launch the agent")
	} else if strings.TrimSpace(args) != "" {
		t.Errorf("code forwarded %q, want nothing", args)
	}

	// A first word that is not a Spinloop is forwarded to the harness.
	argsFile2 := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile2)
	if _, err := rootExec(t, "code", "run", "hello"); err != nil {
		t.Fatalf("code run: %v", err)
	}
	if args, launched := launched(t, argsFile2); !launched {
		t.Fatal("code run did not launch the agent")
	} else if strings.TrimSpace(args) != "run\nhello" {
		t.Errorf("code run forwarded %q, want \"run\\nhello\"", args)
	}
}

// TestRoot_HarnessOpenHelpDoesNotLaunch pins that -h/--help on open shows
// open's own help instead of launching the agent: Cobra does not intercept the
// help flag while flag parsing is off, so open's body must. A -- before it
// still forwards it to the harness.
func TestRoot_HarnessOpenHelpDoesNotLaunch(t *testing.T) {
	isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	for _, flag := range []string{"-h", "--help"} {
		out, err := rootExec(t, "harness", "open", flag)
		if err != nil {
			t.Fatalf("harness open %s: %v", flag, err)
		}
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, "harness open") {
			t.Errorf("harness open %s did not show open's help:\n%s", flag, out)
		}
		if args, launched := launched(t, argsFile); launched {
			t.Errorf("harness open %s launched the agent with %q; help must not launch", flag, args)
		}
	}

	// A -- before the help flag forwards it to the harness instead of showing help.
	argsFile2 := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile2)
	if _, err := rootExec(t, "harness", "open", "--", "--help"); err != nil {
		t.Fatalf("harness open -- --help: %v", err)
	}
	if args, launched := launched(t, argsFile2); !launched {
		t.Fatal("harness open -- --help did not forward to the harness")
	} else if strings.TrimSpace(args) != "--help" {
		t.Errorf("harness open -- --help forwarded %q, want \"--help\"", args)
	}
}

// TestRoot_HarnessConfigGetsAndSets pins that `config` reports and stores the
// default harness instead of launching: a bare config (and --get) report, and
// --set stores.
func TestRoot_HarnessConfigGetsAndSets(t *testing.T) {
	isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	// A bare config reports the harness and does not launch.
	out, err := rootExec(t, "harness", "config")
	if err != nil {
		t.Fatalf("harness config: %v", err)
	}
	if !strings.Contains(out, "Active harness:") {
		t.Errorf("harness config did not report the harness:\n%s", out)
	}
	if _, launched := launched(t, argsFile); launched {
		t.Error("harness config launched the agent")
	}

	// --get is the same report.
	out, err = rootExec(t, "harness", "config", "--get")
	if err != nil {
		t.Fatalf("harness config --get: %v", err)
	}
	if !strings.Contains(out, "Active harness:") {
		t.Errorf("harness config --get did not report the harness:\n%s", out)
	}

	// --set stores the preference and does not launch.
	out, err = rootExec(t, "harness", "config", "--set", "pi")
	if err != nil {
		t.Fatalf("harness config --set: %v", err)
	}
	if !strings.Contains(out, `Default harness set to "pi"`) {
		t.Errorf("harness config --set did not store the preference:\n%s", out)
	}
	if _, launched := launched(t, argsFile); launched {
		t.Error("harness config --set launched the agent")
	}
}

// TestRoot_FlagBeforeSubcommandWord pins that a harness flag placed before the
// subcommand word is still honoured, and the subcommand still runs — the
// launch's own flag parsing does not swallow the dispatch.
func TestRoot_FlagBeforeSubcommandWord(t *testing.T) {
	home := isolateConfig(t)
	stubHarnessBinary(t, "opencode", filepath.Join(t.TempDir(), "args"))

	// -H before the subcommand word selects the harness the subcommand configures.
	if _, err := rootExec(t, "harness", "-H", "pi", "add", "-p", "ollama", "-m", "llama3.1"); err != nil {
		t.Fatalf("harness -H pi add: %v", err)
	}
	m := readPiModels(t, home)
	if !hasProvider(m, "ollama") {
		t.Errorf("harness -H pi add did not write the Pi config: %v", m)
	}
}

// TestRoot_ProviderGroupBare pins that the provider group shows its help with
// its subcommands listed, and does nothing else.
func TestRoot_ProviderGroupBare(t *testing.T) {
	isolateConfig(t)

	out, err := rootExec(t, "provider")
	if err != nil {
		t.Fatalf("bare provider: %v", err)
	}
	if !strings.Contains(out, "Available Commands") {
		t.Errorf("bare provider should list its subcommands:\n%s", out)
	}
	if !strings.Contains(out, "list") || !strings.Contains(out, "init") {
		t.Errorf("bare provider should name list and init:\n%s", out)
	}
}

// hasProvider reports whether a Pi models.json map carries a provider entry.
func hasProvider(models map[string]any, name string) bool {
	providers, ok := models["providers"].(map[string]any)
	if !ok {
		// models.json may be the providers map itself.
		providers = models
	}
	_, present := providers[name]
	return present
}
