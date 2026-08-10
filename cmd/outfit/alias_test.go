package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// registerOutfit writes an Outfit in a fresh directory and registers it under
// the name its ALIAS gives, returning the directory and the Outfit's path.
func registerOutfit(t *testing.T, body string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, outfit.DefaultFile)
	mustWrite(t, path, body)
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})
	return dir, path
}

// storedAlias returns the path registered under name, failing if there is none.
func storedAlias(t *testing.T, name string) string {
	t.Helper()
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	path, ok := f.Alias(name)
	if !ok {
		t.Fatalf("alias %q is not registered (have %v)", name, f.AliasNames())
	}
	return path
}

// TestCmdAlias_DefaultsToOutfitALIAS checks that a bare `outfit alias` names the
// Outfit after its own ALIAS, and stores the absolute path to the file itself.
func TestCmdAlias_DefaultsToOutfitALIAS(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 128k\n")
	t.Chdir(dir)

	out := captureStdout(t, func() {
		if err := cmdAlias(nil); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})
	if !strings.Contains(out, `Added alias "qwen3.6-27b"`) {
		t.Errorf("unexpected output:\n%s", out)
	}

	// t.Chdir resolves through any symlinks in the temp path, so compare
	// against the working directory rather than dir itself.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, outfit.DefaultFile)
	if got := storedAlias(t, "qwen3.6-27b"); got != want {
		t.Errorf("stored path = %q, want the absolute Outfit file %q", got, want)
	}
}

// TestCmdAlias_NameOverrideAndDirectoryArg checks that --name wins over the
// Outfit's ALIAS, and that a directory argument registers the Outfit inside it.
func TestCmdAlias_NameOverrideAndDirectoryArg(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS qwen3.6-27b\n")

	captureStdout(t, func() {
		if err := cmdAlias([]string{"-n", "q3", dir}); err != nil {
			t.Fatalf("cmdAlias -n: %v", err)
		}
	})

	if got, want := storedAlias(t, "q3"), filepath.Join(dir, outfit.DefaultFile); got != want {
		t.Errorf("stored path = %q, want %q", got, want)
	}
	if f, _ := config.Load(); len(f.AliasNames()) != 1 {
		t.Errorf("--name should register one alias, got %v", f.AliasNames())
	}
}

// TestCmdAlias_RequiresANameWhenOutfitHasNoALIAS checks that outfit never
// invents a name the Outfit does not state.
func TestCmdAlias_RequiresANameWhenOutfitHasNoALIAS(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nMODEL gemma\n")

	err := cmdAlias([]string{dir})
	if err == nil {
		t.Fatal("expected an error for an Outfit with no ALIAS")
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error %q does not point at --name", err)
	}
}

// TestCmdAlias_RefusesToRePointWithoutForce checks that an established name is
// not silently moved, and that --force moves it.
func TestCmdAlias_RefusesToRePointWithoutForce(t *testing.T) {
	isolateConfig(t)

	_, first := registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")

	second := t.TempDir()
	mustWrite(t, filepath.Join(second, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS q3\n")

	err := cmdAlias([]string{second})
	if err == nil {
		t.Fatal("expected an error re-pointing an existing alias")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error %q does not mention --force", err)
	}
	if got := storedAlias(t, "q3"); got != first {
		t.Errorf("alias moved without --force: %q", got)
	}

	out := captureStdout(t, func() {
		if err := cmdAlias([]string{"--force", second}); err != nil {
			t.Fatalf("cmdAlias --force: %v", err)
		}
	})
	if !strings.Contains(out, "Re-pointed") {
		t.Errorf("unexpected --force output:\n%s", out)
	}
	if got, want := storedAlias(t, "q3"), filepath.Join(second, outfit.DefaultFile); got != want {
		t.Errorf("stored path = %q, want %q", got, want)
	}
}

// TestCmdAlias_ReRegisteringSamePathIsIdempotent checks that running the command
// twice in the same directory is a no-op rather than an error.
func TestCmdAlias_ReRegisteringSamePathIsIdempotent(t *testing.T) {
	isolateConfig(t)

	dir, path := registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")

	out := captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("second cmdAlias: %v", err)
		}
	})
	if !strings.Contains(out, "already points here") {
		t.Errorf("unexpected repeat output:\n%s", out)
	}
	if got := storedAlias(t, "q3"); got != path {
		t.Errorf("stored path = %q, want %q", got, path)
	}
}

// TestCmdAlias_RejectsBadNames checks that a name can never be confused with a
// path or a flag.
func TestCmdAlias_RejectsBadNames(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS q3\n")

	for _, name := range []string{"a/b", ".", "..", "a b"} {
		if err := cmdAlias([]string{"-n", name, dir}); err == nil {
			t.Errorf("cmdAlias -n %q = nil, want an error", name)
		}
	}
	if f, _ := config.Load(); len(f.AliasNames()) != 0 {
		t.Errorf("a rejected name was registered: %v", f.AliasNames())
	}
}

// TestCmdAlias_RejectsUnparseableOutfit checks that an Outfit is validated at
// registration, so a broken file is caught here rather than days later.
func TestCmdAlias_RejectsUnparseableOutfit(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nNONSENSE x\n")

	if err := cmdAlias([]string{"-n", "q3", dir}); err == nil {
		t.Fatal("expected an error registering an Outfit that does not parse")
	}
	if f, _ := config.Load(); len(f.AliasNames()) != 0 {
		t.Errorf("a broken Outfit was registered: %v", f.AliasNames())
	}
}

// TestCmdAlias_List checks the listing: the empty state, sorted entries, and
// the marker on an alias whose Outfit has since gone.
func TestCmdAlias_List(t *testing.T) {
	isolateConfig(t)

	out := captureStdout(t, func() {
		if err := cmdAlias([]string{"--list"}); err != nil {
			t.Fatalf("cmdAlias --list: %v", err)
		}
	})
	if !strings.Contains(out, "No aliases registered") {
		t.Errorf("unexpected empty-state output:\n%s", out)
	}

	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")
	_, gone := registerOutfit(t, "PROVIDER llamacpp\nALIAS gemma\n")

	out = captureStdout(t, func() {
		if err := cmdAlias([]string{"-l"}); err != nil {
			t.Fatalf("cmdAlias -l: %v", err)
		}
	})
	if strings.Index(out, "gemma") > strings.Index(out, "qwen") {
		t.Errorf("aliases are not sorted:\n%s", out)
	}
	if strings.Contains(out, "(missing)") {
		t.Errorf("nothing is missing yet:\n%s", out)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if err := cmdAlias([]string{"--list"}); err != nil {
			t.Fatalf("cmdAlias --list: %v", err)
		}
	})
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "gemma") && !strings.Contains(line, "(missing)") {
			t.Errorf("a dangling alias is not marked:\n%s", out)
		}
		if strings.Contains(line, "qwen") && strings.Contains(line, "(missing)") {
			t.Errorf("a live alias is marked missing:\n%s", out)
		}
	}
}

// TestCmdUnalias checks that a name can be dropped, that the Outfit survives it,
// and that the argument is validated.
func TestCmdUnalias(t *testing.T) {
	isolateConfig(t)

	_, path := registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")

	out := captureStdout(t, func() {
		if err := cmdUnalias([]string{"q3"}); err != nil {
			t.Fatalf("cmdUnalias: %v", err)
		}
	})
	if !strings.Contains(out, `Removed alias "q3"`) {
		t.Errorf("unexpected output:\n%s", out)
	}
	if f, _ := config.Load(); len(f.AliasNames()) != 0 {
		t.Errorf("alias survived unalias: %v", f.AliasNames())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("unalias removed the Outfit file itself: %v", err)
	}

	if err := cmdUnalias([]string{"q3"}); err == nil {
		t.Error("expected an error for an unknown alias")
	}
	if err := cmdUnalias(nil); err == nil {
		t.Error("expected an error with no name")
	}
	if err := cmdUnalias([]string{"a", "b"}); err == nil {
		t.Error("expected an error with two names")
	}
}

// TestAlias_PreservesHarnessPreference is the anti-clobber test at the level the
// user meets it: outfit's config holds both settings, and writing either must
// leave the other alone.
func TestAlias_PreservesHarnessPreference(t *testing.T) {
	isolateConfig(t)

	captureStdout(t, func() {
		if err := cmdHarness([]string{"--set", "pi"}); err != nil {
			t.Fatalf("cmdHarness --set: %v", err)
		}
	})
	registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")

	out := captureStdout(t, func() {
		if err := cmdHarness([]string{"--get"}); err != nil {
			t.Fatalf("cmdHarness --get: %v", err)
		}
	})
	if !strings.Contains(out, "Stored preference: pi") {
		t.Errorf("registering an alias clobbered the harness preference:\n%s", out)
	}

	// ...and the other way round.
	captureStdout(t, func() {
		if err := cmdHarness([]string{"--set", "opencode"}); err != nil {
			t.Fatalf("cmdHarness --set: %v", err)
		}
	})
	storedAlias(t, "q3")
}

// TestApply_ByAlias checks the point of the feature: a registered name works
// from a directory that has nothing to do with the Outfit.
func TestApply_ByAlias(t *testing.T) {
	home := isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS q3\n")
	t.Chdir(t.TempDir()) // somewhere else entirely

	// The alias line goes to stderr, so `outfit remote env` can be eval'd.
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := cmdApply([]string{"q3"}); err != nil {
				t.Fatalf("cmdApply by alias: %v", err)
			}
		})
	})
	if !strings.Contains(stderr, `Using alias "q3"`) {
		t.Errorf("the alias was not reported:\n%s", stderr)
	}
	if strings.Contains(stdout, "Using alias") {
		t.Errorf("the alias line belongs on stderr:\n%s", stdout)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/q3" {
		t.Errorf("default model = %v, want llamacpp/q3", m["model"])
	}
}

// TestUnapply_ByAlias checks that the inverse resolves a name too — apply and
// unapply share readOutfit, but the pair is what the user actually relies on.
func TestUnapply_ByAlias(t *testing.T) {
	home := isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS q3\n")
	t.Chdir(t.TempDir())

	captureStdout(t, func() {
		if err := cmdApply([]string{"q3"}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
		if err := cmdUnapply([]string{"q3"}); err != nil {
			t.Fatalf("cmdUnapply by alias: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	models := m["provider"].(map[string]any)["llamacpp"].(map[string]any)["models"].(map[string]any)
	if len(models) != 0 {
		t.Errorf("models survived unapply by alias: %v", models)
	}
	if m["model"] != nil && m["model"] != "" {
		t.Errorf("default model survived unapply by alias: %v", m["model"])
	}
}

// TestServe_ByAliasResolvesRelativePreset checks that an aliased Outfit still
// finds the preset sitting next to it — the reason the registry stores the
// Outfit file rather than the directory.
func TestServe_ByAliasResolvesRelativePreset(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS q3\nPRESET ./preset.ini\n")
	mustWrite(t, filepath.Join(dir, "preset.ini"), "[q3]\nmodel = /models/q3.gguf\nngl = 99\n")
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	t.Chdir(t.TempDir()) // the preset is not resolvable from here by name alone

	out := captureStdout(t, func() {
		// The path comes last: flag parsing stops at the first positional, so
		// `serve q3 --dry-run` would hand --dry-run to llama-server.
		if err := cmdServe([]string{"--dry-run", "q3"}); err != nil {
			t.Fatalf("cmdServe by alias: %v", err)
		}
	})
	if !strings.Contains(out, filepath.Join(dir, "preset.ini")) {
		t.Errorf("the preset next to the aliased Outfit was not found:\n%s", out)
	}
	if !strings.Contains(out, "--n-gpu-layers 99") {
		t.Errorf("preset flags missing from the command:\n%s", out)
	}
}

// TestReadOutfit_PathBeatsAlias checks the precedence rule: registering an alias
// can never change what an already-working command does.
func TestReadOutfit_PathBeatsAlias(t *testing.T) {
	home := isolateConfig(t)

	// An alias named "dev", pointing somewhere far away.
	away := t.TempDir()
	mustWrite(t, filepath.Join(away, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS away\n")
	captureStdout(t, func() {
		if err := cmdAlias([]string{"-n", "dev", away}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	// A directory of the same name, here.
	here := t.TempDir()
	if err := os.Mkdir(filepath.Join(here, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(here, "dev", outfit.DefaultFile), "PROVIDER llamacpp\nALIAS local\n")
	t.Chdir(here)

	out := captureStdout(t, func() {
		if err := cmdApply([]string{"dev"}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	if !strings.Contains(out, "names both a path here and a registered alias") {
		t.Errorf("the shadowing was not reported:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/local" {
		t.Errorf("default model = %v, want llamacpp/local (the path, not the alias)", m["model"])
	}
}

// TestReadOutfit_DanglingAlias checks that an alias whose Outfit has moved says
// so, and names both ways out.
func TestReadOutfit_DanglingAlias(t *testing.T) {
	isolateConfig(t)

	_, path := registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	err := cmdApply([]string{"q3"})
	if err == nil {
		t.Fatal("expected an error for a dangling alias")
	}
	for _, want := range []string{"is gone", path, "outfit alias", "outfit unalias"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestReadOutfit_UnknownNameFallsThroughToPath checks that a name nobody
// registered still fails the way it always has.
func TestReadOutfit_UnknownNameFallsThroughToPath(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())

	err := cmdApply([]string{"nope"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "reading nope") {
		t.Errorf("error = %q, want the plain read failure", err)
	}
}

// TestCmdShow_ListsAliases checks that `outfit show` reports the registry too,
// including when no provider is configured.
func TestCmdShow_ListsAliases(t *testing.T) {
	isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")

	out := captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "No providers configured") {
		t.Fatalf("expected an unconfigured harness:\n%s", out)
	}
	if !strings.Contains(out, "Aliases:") || !strings.Contains(out, "q3") {
		t.Errorf("aliases missing from an empty show:\n%s", out)
	}

	captureStdout(t, func() {
		if err := cmdApply([]string{"q3"}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	out = captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "Aliases:") || !strings.Contains(out, "q3") {
		t.Errorf("aliases missing from a configured show:\n%s", out)
	}
}

// TestEnvAlias_SuppliesTheOutfit checks the point of OUTFIT_ALIAS: a command
// given no path acts on the aliased Outfit from anywhere, and says so.
func TestEnvAlias_SuppliesTheOutfit(t *testing.T) {
	home := isolateConfig(t)

	_, path := registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS q3\n")
	t.Chdir(t.TempDir()) // no Outfit here
	t.Setenv("OUTFIT_ALIAS", "q3")

	// Like the alias-argument note, this belongs on stderr so `outfit remote
	// env` stays eval-able.
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := cmdApply(nil); err != nil {
				t.Fatalf("cmdApply with OUTFIT_ALIAS: %v", err)
			}
		})
	})
	if !strings.Contains(stderr, `Using OUTFIT_ALIAS "q3"`) || !strings.Contains(stderr, path) {
		t.Errorf("the variable was not reported with its path:\n%s", stderr)
	}
	if strings.Contains(stdout, "OUTFIT_ALIAS") {
		t.Errorf("the OUTFIT_ALIAS line belongs on stderr:\n%s", stdout)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/q3" {
		t.Errorf("default model = %v, want llamacpp/q3", m["model"])
	}
}

// TestEnvAlias_ArgumentWins checks that OUTFIT_ALIAS cannot change what a
// command that names its own Outfit does — by path or by alias.
func TestEnvAlias_ArgumentWins(t *testing.T) {
	home := isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS fromenv\n")
	_, argued := registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS argued\n")
	t.Chdir(t.TempDir())
	t.Setenv("OUTFIT_ALIAS", "fromenv")

	opencodeJSON := filepath.Join(home, ".config", "opencode", "opencode.json")

	captureStdout(t, func() {
		if err := cmdApply([]string{argued}); err != nil {
			t.Fatalf("cmdApply by path: %v", err)
		}
	})
	if m := readConfigMap(t, opencodeJSON); m["model"] != "llamacpp/argued" {
		t.Errorf("a path argument lost to OUTFIT_ALIAS: model = %v", m["model"])
	}

	captureStdout(t, func() {
		if err := cmdApply([]string{"argued"}); err != nil {
			t.Fatalf("cmdApply by alias: %v", err)
		}
	})
	if m := readConfigMap(t, opencodeJSON); m["model"] != "llamacpp/argued" {
		t.Errorf("an alias argument lost to OUTFIT_ALIAS: model = %v", m["model"])
	}
}

// TestEnvAlias_BeatsTheDefaultOutfit checks the other half of the precedence:
// the variable names which Outfit is the default, so a ./Outfit in the working
// directory does not displace it.
func TestEnvAlias_BeatsTheDefaultOutfit(t *testing.T) {
	home := isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS fromenv\n")

	here := t.TempDir()
	mustWrite(t, filepath.Join(here, outfit.DefaultFile), "PROVIDER llamacpp\nMODEL gemma\nALIAS local\n")
	t.Chdir(here)
	t.Setenv("OUTFIT_ALIAS", "fromenv")

	captureStdout(t, func() {
		if err := cmdApply(nil); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/fromenv" {
		t.Errorf("default model = %v, want llamacpp/fromenv (the variable, not ./Outfit)", m["model"])
	}
}

// TestEnvAlias_NotShadowedByAFile checks the rule that differs from an
// argument: a file of the same name in the working directory is a coincidence,
// so it must not decide where an exported variable points.
func TestEnvAlias_NotShadowedByAFile(t *testing.T) {
	home := isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS q3\n")

	here := t.TempDir()
	mustWrite(t, filepath.Join(here, "q3"), "PROVIDER llamacpp\nMODEL gemma\nALIAS local\n")
	t.Chdir(here)
	t.Setenv("OUTFIT_ALIAS", "q3")

	out := captureStdout(t, func() {
		if err := cmdApply(nil); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	if strings.Contains(out, "names both a path here") {
		t.Errorf("the variable was shadowed by a same-named file:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/q3" {
		t.Errorf("default model = %v, want llamacpp/q3 (the registry, not the file)", m["model"])
	}
}

// TestEnvAlias_Errors checks that a variable that cannot be resolved says so
// naming itself, rather than failing as a missing file in the current
// directory — a stale export in a shell profile has to be diagnosable from the
// error alone.
func TestEnvAlias_Errors(t *testing.T) {
	isolateConfig(t)

	_, path := registerOutfit(t, "PROVIDER llamacpp\nALIAS gone\n")
	registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")
	t.Chdir(t.TempDir())

	t.Run("unset behaves as before", func(t *testing.T) {
		err := cmdApply(nil)
		if err == nil || !strings.Contains(err.Error(), "no Outfit found in the current directory") {
			t.Fatalf("err = %v, want the usual missing-Outfit failure", err)
		}
		if !strings.Contains(err.Error(), "OUTFIT_ALIAS") {
			t.Errorf("the hint does not offer OUTFIT_ALIAS: %v", err)
		}
	})

	t.Run("empty is ignored", func(t *testing.T) {
		t.Setenv("OUTFIT_ALIAS", "")
		err := cmdApply(nil)
		if err == nil || !strings.Contains(err.Error(), "no Outfit found in the current directory") {
			t.Fatalf("err = %v, want an empty value to be ignored", err)
		}
	})

	t.Run("path-shaped", func(t *testing.T) {
		t.Setenv("OUTFIT_ALIAS", "./somewhere/Outfit")
		err := cmdApply(nil)
		if err == nil {
			t.Fatal("expected an error for a path-shaped value")
		}
		if !strings.Contains(err.Error(), "OUTFIT_ALIAS") || !strings.Contains(err.Error(), "path separator") {
			t.Errorf("error %q does not explain the value is not a name", err)
		}
	})

	t.Run("unregistered", func(t *testing.T) {
		t.Setenv("OUTFIT_ALIAS", "nope")
		err := cmdApply(nil)
		if err == nil {
			t.Fatal("expected an error for an unregistered value")
		}
		for _, want := range []string{"OUTFIT_ALIAS", "not a registered alias", "outfit alias --list"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("dangling", func(t *testing.T) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OUTFIT_ALIAS", "gone")
		err := cmdApply(nil)
		if err == nil {
			t.Fatal("expected an error for a dangling value")
		}
		for _, want := range []string{"OUTFIT_ALIAS", "is gone", path, "outfit unalias"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

// TestEnvAlias_HarnessAppliesOnlyWhenAsked checks the boundary the variable
// draws: it decides *which* Outfit is the default, never *whether* a command
// acts on one. A bare `outfit harness` applies nothing; -O asks for the default
// Outfit and therefore picks the variable up.
func TestEnvAlias_HarnessAppliesOnlyWhenAsked(t *testing.T) {
	home := isolateConfig(t)
	stubHarnessBinary(t, "opencode", filepath.Join(t.TempDir(), "args"))

	registerOutfit(t, "PROVIDER llamacpp\nMODEL gemma\nALIAS q3\n")
	t.Chdir(t.TempDir())
	t.Setenv("OUTFIT_ALIAS", "q3")

	opencodeJSON := filepath.Join(home, ".config", "opencode", "opencode.json")

	captureStdout(t, func() {
		if err := cmdHarness(nil); err != nil {
			t.Fatalf("cmdHarness: %v", err)
		}
	})
	if _, err := os.Stat(opencodeJSON); !os.IsNotExist(err) {
		t.Errorf("a bare launch applied an Outfit (stat %s: %v)", opencodeJSON, err)
	}

	captureStdout(t, func() {
		if err := cmdHarness([]string{"-O"}); err != nil {
			t.Fatalf("cmdHarness -O: %v", err)
		}
	})
	if m := readConfigMap(t, opencodeJSON); m["model"] != "llamacpp/q3" {
		t.Errorf("-O did not apply the variable's Outfit: model = %v", m["model"])
	}
}

// TestEnvAlias_IgnoredByAlias checks that registering opts out: a bare
// `outfit alias` means the Outfit in this directory, so honouring the variable
// could only re-register what is already registered.
func TestEnvAlias_IgnoredByAlias(t *testing.T) {
	isolateConfig(t)

	registerOutfit(t, "PROVIDER llamacpp\nALIAS fromenv\n")

	here := t.TempDir()
	mustWrite(t, filepath.Join(here, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS local\n")
	t.Chdir(here)
	t.Setenv("OUTFIT_ALIAS", "fromenv")

	captureStdout(t, func() {
		if err := cmdAlias(nil); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := storedAlias(t, "local"), filepath.Join(cwd, outfit.DefaultFile); got != want {
		t.Errorf("stored path = %q, want the Outfit here %q", got, want)
	}
}

// TestEnvAlias_ReachesServe checks a caller other than apply, so the choke
// point is shown to serve every command rather than only the one under test.
func TestEnvAlias_ReachesServe(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS q3\nPRESET ./preset.ini\n")
	mustWrite(t, filepath.Join(dir, "preset.ini"), "[q3]\nmodel = /models/q3.gguf\nngl = 99\n")
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	t.Chdir(t.TempDir())
	t.Setenv("OUTFIT_ALIAS", "q3")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run"}); err != nil {
			t.Fatalf("cmdServe with OUTFIT_ALIAS: %v", err)
		}
	})
	if !strings.Contains(out, filepath.Join(dir, "preset.ini")) {
		t.Errorf("the preset next to the variable's Outfit was not found:\n%s", out)
	}
}

// TestEnvAlias_ReachesRemote checks the case that first caught this out: a
// `remote` subcommand with no argument only consults an Outfit when one is
// there to consult, and OUTFIT_ALIAS names one as surely as a ./Outfit does.
// Without this the command fell through to the per-user default config and
// reported the endpoint as unconfigured.
func TestEnvAlias_ReachesRemote(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	hit := make(chan string, 1)
	server := httptest.NewServer(hitRecorder("aliased", hit))
	defer server.Close()

	// An Outfit whose REMOTE sits beside it, registered and then left behind.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, outfit.DefaultFile), "PROVIDER openai-compatible\nALIAS q3\nREMOTE ./remote.json\n")
	cfg, _ := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, Region: "eu-west-1"})
	mustWrite(t, filepath.Join(dir, "remote.json"), string(cfg))
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	t.Chdir(t.TempDir()) // no ./Outfit, so only the variable can find it
	t.Setenv("OUTFIT_ALIAS", "q3")

	if err := cmdRemoteStop(nil); err != nil {
		t.Fatalf("cmdRemoteStop with OUTFIT_ALIAS: %v", err)
	}
	select {
	case name := <-hit:
		if name != "aliased" {
			t.Errorf("stop reached the %q server, want the aliased Outfit's", name)
		}
	default:
		t.Error("no server was reached — the variable's REMOTE was not used")
	}
}

// TestEnvAlias_ReachesDaemonOutfit checks the same gate on the daemon: with no
// path and no ./Outfit it starts idle, but a variable naming one means there is
// an Outfit to serve from after all.
func TestEnvAlias_ReachesDaemonOutfit(t *testing.T) {
	isolateConfig(t)

	_, path := registerOutfit(t, "PROVIDER llamacpp\nALIAS q3\n")
	t.Chdir(t.TempDir())

	if _, _, ok, err := resolveDaemonOutfit(""); err != nil || ok {
		t.Fatalf("resolveDaemonOutfit() = ok %v, err %v; want no Outfit with the variable unset", ok, err)
	}

	t.Setenv("OUTFIT_ALIAS", "q3")
	sel, got, ok, err := resolveDaemonOutfit("")
	if err != nil {
		t.Fatalf("resolveDaemonOutfit with OUTFIT_ALIAS: %v", err)
	}
	if !ok || got != path {
		t.Errorf("resolveDaemonOutfit() = %q, ok %v; want the variable's Outfit %q", got, ok, path)
	}
	if sel.Alias != "q3" {
		t.Errorf("resolved the wrong Outfit: ALIAS = %q", sel.Alias)
	}
}
