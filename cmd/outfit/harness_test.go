package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/outfit"
)

// readPiModels reads ~/.pi/agent/models.json (HOME must be set) for assertions.
func readPiModels(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("models.json not valid JSON: %v", err)
	}
	return m
}

// isolateConfig points HOME and XDG_CONFIG_HOME at fresh temp dirs and clears
// OUTFIT_HARNESS, so harness resolution and the Pi/opencode/preference files are
// all sandboxed.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OUTFIT_HARNESS", "")
	return home
}

func TestHarness_GetAndSet(t *testing.T) {
	isolateConfig(t)

	// Default before any preference is stored. --get reports the active harness
	// rather than launching it (a bare `harness` execs the agent binary, which
	// would hang an interactive TUI under test).
	out := captureStdout(t, func() {
		if err := cmdHarness([]string{"--get"}); err != nil {
			t.Fatalf("cmdHarness --get: %v", err)
		}
	})
	if !strings.Contains(out, "Active harness: opencode") || !strings.Contains(out, "Stored preference: none") {
		t.Errorf("unexpected default harness output:\n%s", out)
	}

	// Set a preference.
	out = captureStdout(t, func() {
		if err := cmdHarness([]string{"--set", "pi"}); err != nil {
			t.Fatalf("cmdHarness --set: %v", err)
		}
	})
	if !strings.Contains(out, `Default harness set to "pi"`) {
		t.Errorf("unexpected --set output:\n%s", out)
	}

	// It is now the active harness.
	out = captureStdout(t, func() {
		if err := cmdHarness([]string{"--get"}); err != nil {
			t.Fatalf("cmdHarness --get: %v", err)
		}
	})
	if !strings.Contains(out, "Active harness: pi") || !strings.Contains(out, "Stored preference: pi") {
		t.Errorf("preference not reflected:\n%s", out)
	}

	// Unknown harness is rejected.
	if err := cmdHarness([]string{"--set", "bogus"}); err == nil {
		t.Error("expected error setting an unknown harness")
	}
}

// stubHarnessBinary puts a script named after the harness binary first on PATH,
// recording its argv to argsFile, so a launch can be tested without a real agent
// installed. It returns nothing; the caller reads argsFile.
func stubHarnessBinary(t *testing.T, name, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestHarness_AppliesOutfitBeforeLaunch checks that --outfit applies the named
// Outfit — the same work `outfit apply` does — and then launches the harness
// with the trailing args.
func TestHarness_AppliesOutfitBeforeLaunch(t *testing.T) {
	home := isolateConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)

	outfitPath := filepath.Join(t.TempDir(), "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 128k\n")

	out := captureStdout(t, func() {
		if err := cmdHarness([]string{"--outfit=" + outfitPath, "run", "hello"}); err != nil {
			t.Fatalf("cmdHarness --outfit: %v", err)
		}
	})
	if !strings.Contains(out, "Applying "+outfitPath) {
		t.Errorf("apply not reported:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/gemma" {
		t.Errorf("Outfit was not applied before launch: default model = %v", m["model"])
	}

	// The harness still ran, with the trailing args forwarded to it.
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("harness was not launched: %v", err)
	}
	if strings.TrimSpace(string(got)) != "run\nhello" {
		t.Errorf("forwarded args = %q, want \"run\\nhello\"", got)
	}
}

// TestHarness_OutfitDefaultsToCurrentDirectory checks that a bare --outfit takes
// the default Outfit in the working directory, as a bare `outfit apply` does.
func TestHarness_OutfitDefaultsToCurrentDirectory(t *testing.T) {
	home := isolateConfig(t)
	stubHarnessBinary(t, "opencode", filepath.Join(t.TempDir(), "args"))

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\n")
	t.Chdir(dir)

	captureStdout(t, func() {
		if err := cmdHarness([]string{"-O"}); err != nil {
			t.Fatalf("cmdHarness -O: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/gemma" {
		t.Errorf("./Outfit was not applied: default model = %v", m["model"])
	}
}

// TestHarness_OutfitErrors covers the ways --outfit can be given wrongly: no
// Outfit to default to, an unreadable path, and a path left detached from the
// flag (which would otherwise be forwarded to the harness).
func TestHarness_OutfitErrors(t *testing.T) {
	isolateConfig(t)
	stubHarnessBinary(t, "opencode", filepath.Join(t.TempDir(), "args"))

	dir := t.TempDir()
	t.Chdir(dir)

	err := cmdHarness([]string{"--outfit"})
	if err == nil || !strings.Contains(err.Error(), "outfit harness --outfit=<file>") {
		t.Errorf("expected a hint naming the flag, got %v", err)
	}

	if err := cmdHarness([]string{"--outfit=" + filepath.Join(dir, "nope")}); err == nil {
		t.Error("expected an error for a missing Outfit")
	}

	// A detached path is a typo, not a harness arg: the flag takes no value, so
	// the Outfit would silently be the default one.
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\n")
	other := filepath.Join(t.TempDir(), "Outfit")
	mustWrite(t, other, "PROVIDER llamacpp\nMODEL other\n")
	err = cmdHarness([]string{"--outfit", other})
	if err == nil || !strings.Contains(err.Error(), "--outfit="+other) {
		t.Errorf("expected the detached-path hint, got %v", err)
	}
}

// TestHarness_GetDoesNotApply checks that --get stays an inspection command:
// it reports the harness and applies nothing, even alongside --outfit.
func TestHarness_GetDoesNotApply(t *testing.T) {
	home := isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\n")
	t.Chdir(dir)

	out := captureStdout(t, func() {
		if err := cmdHarness([]string{"--get", "-O"}); err != nil {
			t.Fatalf("cmdHarness --get -O: %v", err)
		}
	})
	if !strings.Contains(out, "Active harness: opencode") {
		t.Errorf("unexpected --get output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("--get should not have applied the Outfit")
	}
}

// launchedArgs runs cmdHarness against a stubbed binary and returns what the
// harness was launched with, plus everything outfit printed on the way.
func launchedArgs(t *testing.T, args []string) (forwarded, out string) {
	t.Helper()
	argsFile := filepath.Join(t.TempDir(), "args")
	stubHarnessBinary(t, "opencode", argsFile)
	out = captureStdout(t, func() {
		if err := cmdHarness(args); err != nil {
			t.Fatalf("cmdHarness %v: %v", args, err)
		}
	})
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("harness was not launched: %v", err)
	}
	return strings.TrimSpace(string(got)), out
}

// aliasFor registers an Outfit selecting model under name, returning the
// Outfit's path.
func aliasFor(t *testing.T, name, model string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Outfit")
	mustWrite(t, path, "PROVIDER llamacpp\nMODEL "+model+"\n")
	captureStdout(t, func() {
		if err := cmdAlias([]string{"-n", name, dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})
	return path
}

// TestHarness_LeadingAliasAppliesThenLaunches checks the shorthand the alias
// registry exists for: `outfit harness <name> -- <args>` wears the Outfit and
// hands the harness only its own arguments.
func TestHarness_LeadingAliasAppliesThenLaunches(t *testing.T) {
	home := isolateConfig(t)
	path := aliasFor(t, "q3", "gemma")

	forwarded, out := launchedArgs(t, []string{"q3", "--", "run", "hello"})
	if !strings.Contains(out, "Applying "+path) {
		t.Errorf("apply not reported:\n%s", out)
	}
	if forwarded != "run\nhello" {
		t.Errorf("forwarded args = %q, want \"run\\nhello\" (alias and -- consumed)", forwarded)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/gemma" {
		t.Errorf("the alias was not applied: default model = %v", m["model"])
	}
}

// TestHarness_LeadingAliasWithoutDashDash checks that the separator is optional.
func TestHarness_LeadingAliasWithoutDashDash(t *testing.T) {
	isolateConfig(t)
	aliasFor(t, "q3", "gemma")

	if forwarded, _ := launchedArgs(t, []string{"q3", "run"}); forwarded != "run" {
		t.Errorf("forwarded args = %q, want \"run\"", forwarded)
	}
}

// TestHarness_LeadingPositionalIsForwardedWhenNotAnOutfit is the regression
// guard for the whole feature: an ordinary harness argument must reach the
// harness untouched, `--` and all.
func TestHarness_LeadingPositionalIsForwardedWhenNotAnOutfit(t *testing.T) {
	home := isolateConfig(t)

	forwarded, _ := launchedArgs(t, []string{"run", "--", "--flag"})
	if forwarded != "run\n--\n--flag" {
		t.Errorf("forwarded args = %q, want them verbatim", forwarded)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("nothing should have been applied")
	}
}

// TestHarness_DashDashProtectsAnAliasNamedLikeASubcommand checks the escape
// hatch for a name that collides with one of the harness's own commands.
func TestHarness_DashDashProtectsAnAliasNamedLikeASubcommand(t *testing.T) {
	home := isolateConfig(t)
	aliasFor(t, "run", "gemma")

	if forwarded, _ := launchedArgs(t, []string{"--", "run"}); forwarded != "run" {
		t.Errorf("forwarded args = %q, want \"run\"", forwarded)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("an explicit -- should have opted out of applying the alias")
	}
}

// TestHarness_TerminatorAfterDetachedFlagValue checks that the terminator is
// still recognised when a flag value precedes it — the case a naive scan for
// the first non-flag token gets wrong.
func TestHarness_TerminatorAfterDetachedFlagValue(t *testing.T) {
	home := isolateConfig(t)
	aliasFor(t, "run", "gemma")

	if forwarded, _ := launchedArgs(t, []string{"-H", "opencode", "--", "run"}); forwarded != "run" {
		t.Errorf("forwarded args = %q, want \"run\"", forwarded)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("the terminator after a detached flag value was missed")
	}
}

// TestHarness_MalformedConfigStillLaunches checks the documented safety
// property: a config outfit cannot parse must not stop the harness launching.
// The leading word is simply forwarded, and a real alias error surfaces later
// under `outfit apply`, where it is actionable.
func TestHarness_MalformedConfigStillLaunches(t *testing.T) {
	home := isolateConfig(t)

	configPath := filepath.Join(home, ".config", "outfit", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, configPath, "{not json")

	// "run" is name-shaped and not on disk, so resolving it consults the config
	// — which is corrupt. That must degrade to "not an Outfit", forwarding it.
	forwarded, _ := launchedArgs(t, []string{"run", "hello"})
	if forwarded != "run\nhello" {
		t.Errorf("forwarded args = %q, want them forwarded verbatim", forwarded)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("a corrupt config should have applied nothing")
	}
}

// TestHarness_LeadingOutfitPathApplies checks that a path works positionally
// too, so `harness` reads like apply and serve.
func TestHarness_LeadingOutfitPathApplies(t *testing.T) {
	home := isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\n")

	if forwarded, _ := launchedArgs(t, []string{filepath.Join(dir, "Outfit"), "--", "run"}); forwarded != "run" {
		t.Errorf("forwarded args = %q, want \"run\"", forwarded)
	}
	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/gemma" {
		t.Errorf("the positional Outfit was not applied: default model = %v", m["model"])
	}
}

// TestHarness_ExplicitOutfitFlagBeatsLeadingPositional checks that --outfit
// keeps its meaning: with it, positional args are the harness's again.
func TestHarness_ExplicitOutfitFlagBeatsLeadingPositional(t *testing.T) {
	home := isolateConfig(t)
	aliasFor(t, "q3", "gemma")

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL other\n")

	forwarded, _ := launchedArgs(t, []string{"--outfit=" + filepath.Join(dir, "Outfit"), "q3"})
	if forwarded != "q3" {
		t.Errorf("forwarded args = %q, want \"q3\"", forwarded)
	}
	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "llamacpp/other" {
		t.Errorf("default model = %v, want llamacpp/other (the flag, not the positional)", m["model"])
	}
}

// TestHarness_DetachedAliasIsCaught checks that a detached alias is treated like
// a detached path: a typo to point out, not an Outfit to guess at.
func TestHarness_DetachedAliasIsCaught(t *testing.T) {
	isolateConfig(t)
	aliasFor(t, "q3", "gemma")

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL other\n")
	t.Chdir(dir)

	err := cmdHarness([]string{"-O", "q3"})
	if err == nil || !strings.Contains(err.Error(), "--outfit=q3") {
		t.Errorf("expected the detached-value hint, got %v", err)
	}
}

func TestCmdShow(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	// Nothing configured yet.
	out := captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "Harness: opencode (from default)") {
		t.Errorf("missing harness header:\n%s", out)
	}
	if !strings.Contains(out, "No providers configured") {
		t.Errorf("expected empty-config notice:\n%s", out)
	}

	// After an add, show lists the provider, its models, their limits, and the
	// default model.
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-m", "deepseek/deepseek-v4-flash", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	out = captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "Configured providers:") || !strings.Contains(out, "openrouter") {
		t.Errorf("provider not shown:\n%s", out)
	}
	if !strings.Contains(out, "context 128000") || !strings.Contains(out, "output 32000") {
		t.Errorf("model limits not shown:\n%s", out)
	}
	if !strings.Contains(out, "Default model: openrouter/") {
		t.Errorf("default model not shown:\n%s", out)
	}
}

// writeOpencodeConfig writes raw JSON to the opencode config under HOME so a
// test can stage a config `show` then reads back — including shapes that `add`
// would never produce, like a provider with no models.
func writeOpencodeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir opencode config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}
}

func TestCmdShow_BaseURLAndEmptyProvider(t *testing.T) {
	home := isolateConfig(t)

	// A provider carrying a base URL override, alongside one configured with no
	// models at all — a shape `add` won't produce but a hand-edited config can.
	writeOpencodeConfig(t, home, `{
	  "provider": {
	    "llamacpp": {
	      "options": {"baseURL": "http://127.0.0.1:9999/v1"},
	      "models": {"my-model": {"limit": {"context": 128000, "output": 32000}}}
	    },
	    "bare": {
	      "models": {}
	    }
	  }
	}`)

	out := captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "base url: http://127.0.0.1:9999/v1") {
		t.Errorf("base URL not shown:\n%s", out)
	}
	if !strings.Contains(out, "model my-model (context 128000, output 32000)") {
		t.Errorf("model line not shown:\n%s", out)
	}
	if !strings.Contains(out, "(no models)") {
		t.Errorf("empty provider not flagged:\n%s", out)
	}
}

func TestCmdShow_Errors(t *testing.T) {
	home := isolateConfig(t)

	// An unrecognised flag is surfaced rather than silently ignored.
	if err := cmdShow([]string{"--nope"}); err == nil {
		t.Error("expected error for an unknown flag")
	}

	// A malformed harness config is reported, not swallowed.
	writeOpencodeConfig(t, home, "{ this is not json")
	if err := cmdShow(nil); err == nil {
		t.Error("expected error reading a malformed config")
	}
}

func TestCmdShow_HarnessOverride(t *testing.T) {
	isolateConfig(t)

	// The opencode default is configured...
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-m", "deepseek/deepseek-v4-flash"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})

	// ...but -H pi reads the (empty) Pi config instead, naming the flag as the
	// source, without disturbing the stored default.
	out := captureStdout(t, func() {
		if err := cmdShow([]string{"-H", "pi"}); err != nil {
			t.Fatalf("cmdShow -H pi: %v", err)
		}
	})
	if !strings.Contains(out, "Harness: pi (from --harness flag)") {
		t.Errorf("harness override not honoured:\n%s", out)
	}
	if !strings.Contains(out, "No providers configured") {
		t.Errorf("Pi config should be empty:\n%s", out)
	}

	// An unknown harness is rejected.
	if err := cmdShow([]string{"-H", "bogus"}); err == nil {
		t.Error("expected error for an unknown harness")
	}
}

func TestCmdShow_PiPopulated(t *testing.T) {
	isolateConfig(t)

	// Configure a provider on Pi, which — unlike opencode — has no default-model
	// setting, so `show` must list the provider and its models without inventing
	// a "Default model:" line.
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-H", "pi", "-p", "ollama", "-m", "llama3.2", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd -H pi: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdShow([]string{"-H", "pi"}); err != nil {
			t.Fatalf("cmdShow -H pi: %v", err)
		}
	})
	if !strings.Contains(out, "Configured providers:") || !strings.Contains(out, "ollama") {
		t.Errorf("Pi provider not shown:\n%s", out)
	}
	if !strings.Contains(out, "context 128000") {
		t.Errorf("model context not shown:\n%s", out)
	}
	if strings.Contains(out, "Default model:") {
		t.Errorf("Pi has no default model; the line should be omitted:\n%s", out)
	}
}

func TestCmdAdd_PiHarnessViaFlag(t *testing.T) {
	home := isolateConfig(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-H", "pi", "-p", "openrouter", "-m", "deepseek/deepseek-v4-flash", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "models.json") || !strings.Contains(out, "Run 'pi'") {
		t.Errorf("expected Pi-flavoured output:\n%s", out)
	}

	prov := readPiModels(t, home)["providers"].(map[string]any)["openrouter"].(map[string]any)
	if prov["api"] != "openai-completions" {
		t.Errorf("api = %v", prov["api"])
	}
	if prov["baseUrl"] != "https://openrouter.ai/api/v1" {
		t.Errorf("baseUrl = %v", prov["baseUrl"])
	}
	// API key is an env interpolation; the resolved secret must not be written.
	if prov["apiKey"] != "$DEEPSEEK_API_KEY" {
		t.Errorf("apiKey = %v, want $DEEPSEEK_API_KEY", prov["apiKey"])
	}
	for _, m := range prov["models"].([]any) {
		if m.(map[string]any)["contextWindow"] != float64(128000) {
			t.Errorf("model %v missing context window", m)
		}
	}

	// opencode must be untouched (no opencode.json written under HOME/.config).
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("opencode config should not have been written for a Pi add")
	}
}

func TestCmdAdd_PiHarnessViaEnvAndPreference(t *testing.T) {
	home := isolateConfig(t)

	// Via OUTFIT_HARNESS.
	t.Setenv("OUTFIT_HARNESS", "pi")
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "ollama", "-m", "llama3.2"}); err != nil {
			t.Fatalf("cmdAdd via env: %v", err)
		}
	})
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["ollama"]; !ok {
		t.Error("ollama not written to Pi config via OUTFIT_HARNESS")
	}

	// Via stored preference (env cleared).
	t.Setenv("OUTFIT_HARNESS", "")
	if err := cmdHarness([]string{"--set", "pi"}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "llamacpp", "-m", "local-model"}); err != nil {
			t.Fatalf("cmdAdd via preference: %v", err)
		}
	})
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["llamacpp"]; !ok {
		t.Error("llamacpp not written to Pi config via stored preference")
	}
}

func TestCmdAdd_PiUnsupportedProvider(t *testing.T) {
	isolateConfig(t)
	if err := cmdAdd([]string{"-H", "pi", "-p", "amazon-bedrock", "-m", "anthropic.claude-3-5-sonnet"}); err == nil {
		t.Error("expected error adding a Pi-unsupported provider")
	}
}

func TestCmdAdd_PiUnsupportedVertex(t *testing.T) {
	isolateConfig(t)
	for _, p := range []string{"google-vertex", "google-vertex-anthropic"} {
		t.Setenv("GOOGLE_VERTEX_PROJECT", "my-proj") // a project is set, so the failure is Pi support, not a missing option
		if err := cmdAdd([]string{"-H", "pi", "-p", p, "-m", "some-model"}); err == nil {
			t.Errorf("%s: expected error adding a Pi-unsupported provider", p)
		}
	}
}

func TestCmdAdd_VertexRequiresProject(t *testing.T) {
	home := isolateConfig(t)

	// Without a project, the whole `add` path fails with a clear error.
	err := cmdAdd([]string{"-p", "google-vertex-anthropic", "-m", "claude-3-5-sonnet-v2@20241022"})
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_VERTEX_PROJECT") {
		t.Fatalf("expected a missing-project error naming GOOGLE_VERTEX_PROJECT, got %v", err)
	}

	// With the project set, the provider is written to the opencode config with
	// project and the default location, and no apiKey.
	t.Setenv("GOOGLE_VERTEX_PROJECT", "my-proj")
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "google-vertex-anthropic", "-m", "claude-3-5-sonnet-v2@20241022"}); err != nil {
			t.Fatalf("cmdAdd with project set: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "google-vertex-anthropic/claude-3-5-sonnet-v2@20241022" {
		t.Errorf("default model = %v", m["model"])
	}
	prov := m["provider"].(map[string]any)["google-vertex-anthropic"].(map[string]any)
	if _, hasNPM := prov["npm"]; hasNPM {
		t.Error("google-vertex-anthropic should carry no npm (opencode built-in)")
	}
	opts := prov["options"].(map[string]any)
	if opts["project"] != "my-proj" {
		t.Errorf("project = %v, want my-proj", opts["project"])
	}
	if opts["location"] != "global" {
		t.Errorf("location = %v, want the global default", opts["location"])
	}
	if _, hasKey := opts["apiKey"]; hasKey {
		t.Error("google-vertex-anthropic should carry no apiKey (ambient credentials)")
	}
}

func TestCmdExportRemove_PiRoundTrip(t *testing.T) {
	home := isolateConfig(t)
	t.Setenv("OUTFIT_HARNESS", "pi")

	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "ollama", "-m", "llama3.2", "-c", "200000"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	if !strings.Contains(out, "PROVIDER ollama") || !strings.Contains(out, "MODEL    llama3.2") {
		t.Errorf("unexpected Pi export:\n%s", out)
	}
	if !strings.Contains(out, "CONTEXT  200000") {
		t.Errorf("export did not recover the context window:\n%s", out)
	}

	// Remove the whole provider from the Pi config.
	out = captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "ollama"}); err != nil {
			t.Fatalf("cmdRemove: %v", err)
		}
	})
	if !strings.Contains(out, "Removed provider") {
		t.Errorf("unexpected remove output:\n%s", out)
	}
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["ollama"]; ok {
		t.Error("ollama should have been removed from the Pi config")
	}
}

// Neither harness stores the secret — opencode substitutes {env:VAR}, Pi
// resolves $VAR — so a key kept only in outfit's .env has to reach the agent
// outfit launches, or the user would have to export it by hand.
func TestHarnessEnv_CarriesResolvableKeys(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	fromDotEnv := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-from-dotenv"
		}
		return ""
	}

	var found string
	for _, kv := range harnessEnv("", fromDotEnv) {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") {
			found = kv
		}
	}
	if found != "OPENAI_API_KEY=sk-from-dotenv" {
		t.Errorf("launched agent's env has %q, want the key from .env", found)
	}
}

// An explicit export is the user's own decision and must win.
func TestHarnessEnv_DoesNotOverrideTheEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-exported")
	fromDotEnv := func(string) string { return "sk-from-dotenv" }

	for _, kv := range harnessEnv("", fromDotEnv) {
		if kv == "OPENAI_API_KEY=sk-from-dotenv" {
			t.Error("the .env value overrode an exported one")
		}
	}
}

// envValue returns the value the env slice carries for key, or "" and false.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	var value string
	var found bool
	// Later assignments win, matching how exec treats a duplicated key.
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			value, found = strings.TrimPrefix(kv, prefix), true
		}
	}
	return value, found
}

func writeDotEnv(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The adjacent .env fills a variable the base environment leaves unset.
func TestOverlayLocalEnv_DotEnvFillsAGap(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "AWS_PROFILE=dev\n")

	out := overlayLocalEnv([]string{"PATH=/usr/bin"}, outfit.Selection{}, dir)
	if got, ok := envValue(out, "AWS_PROFILE"); !ok || got != "dev" {
		t.Errorf("AWS_PROFILE = %q (present=%v), want the .env value", got, ok)
	}
}

// A variable already in the base environment beats the .env — the .env only
// fills gaps.
func TestOverlayLocalEnv_BaseBeatsDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "AWS_PROFILE=fromdotenv\n")

	out := overlayLocalEnv([]string{"AWS_PROFILE=exported"}, outfit.Selection{}, dir)
	if got, _ := envValue(out, "AWS_PROFILE"); got != "exported" {
		t.Errorf("AWS_PROFILE = %q, want the base value to win over the .env", got)
	}
}

// An ENV instruction overrides both the base environment and the .env.
func TestOverlayLocalEnv_EnvOverridesBoth(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "AWS_PROFILE=fromdotenv\n")
	sel := outfit.Selection{Env: []outfit.EnvVar{{Key: "AWS_PROFILE", Value: "fromenv"}}}

	out := overlayLocalEnv([]string{"AWS_PROFILE=exported"}, sel, dir)
	if got, _ := envValue(out, "AWS_PROFILE"); got != "fromenv" {
		t.Errorf("AWS_PROFILE = %q, want the ENV value to override both", got)
	}
}

// stubHarnessDumpingEnv puts a script named after the harness binary first on
// PATH that writes its whole environment to envFile, so a launch can assert what
// the agent actually receives.
func stubHarnessDumpingEnv(t *testing.T, name, envFile string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nenv > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestHarness_LaunchedAgentSeesLocalEnvironment is the end-to-end check that the
// worn Outfit's local environment reaches the launched agent with the right
// precedence: ENV > shell > .env.
func TestHarness_LaunchedAgentSeesLocalEnvironment(t *testing.T) {
	isolateConfig(t)
	envFile := filepath.Join(t.TempDir(), "childenv")
	stubHarnessDumpingEnv(t, "opencode", envFile)

	dir := t.TempDir()
	// FOO: set by ENV, .env and the shell — ENV must win.
	// BAR: only in the .env — it fills a gap and reaches the agent.
	// BAZ: in the shell and the .env — the shell wins.
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\nENV FOO=fromenv\n")
	mustWrite(t, filepath.Join(dir, ".env"), "FOO=fromdotenv\nBAR=frombar\nBAZ=fromdotenv\n")
	t.Setenv("FOO", "fromshell")
	t.Setenv("BAZ", "fromshell")

	captureStdout(t, func() {
		if err := cmdHarness([]string{filepath.Join(dir, "Outfit"), "run"}); err != nil {
			t.Fatalf("cmdHarness: %v", err)
		}
	})

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("agent did not launch: %v", err)
	}
	got, _ := envValue(strings.Split(string(data), "\n"), "FOO")
	if got != "fromenv" {
		t.Errorf("agent FOO = %q, want ENV to win", got)
	}
	if got, _ := envValue(strings.Split(string(data), "\n"), "BAR"); got != "frombar" {
		t.Errorf("agent BAR = %q, want the .env to fill the gap", got)
	}
	if got, _ := envValue(strings.Split(string(data), "\n"), "BAZ"); got != "fromshell" {
		t.Errorf("agent BAZ = %q, want the shell to win over the .env", got)
	}
}

// TestHarness_NoOutfitAppliesNoOverlay checks that without a worn Outfit the
// launch adds no whole-.env overlay and no ENV values — only the shell (plus any
// provider key outfit resolves) reaches the agent.
func TestHarness_NoOutfitAppliesNoOverlay(t *testing.T) {
	isolateConfig(t)
	envFile := filepath.Join(t.TempDir(), "childenv")
	stubHarnessDumpingEnv(t, "opencode", envFile)

	// A .env sits in the working directory, but with no Outfit worn its
	// non-key variables must not be forwarded.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".env"), "BAR=frombar\n")
	t.Chdir(dir)

	captureStdout(t, func() {
		if err := cmdHarness([]string{"run"}); err != nil {
			t.Fatalf("cmdHarness: %v", err)
		}
	})

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("agent did not launch: %v", err)
	}
	if got, ok := envValue(strings.Split(string(data), "\n"), "BAR"); ok {
		t.Errorf("agent BAR = %q, want no overlay when no Outfit is worn", got)
	}
}

// The overlay shapes only the returned slice; outfit's own process environment
// is never mutated, so nothing leaks past the launched agent.
func TestOverlayLocalEnv_DoesNotMutateProcessEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "OUTFIT_LEAK_CHECK=fromdotenv\n")
	sel := outfit.Selection{Env: []outfit.EnvVar{{Key: "OUTFIT_ENV_LEAK_CHECK", Value: "fromenv"}}}

	overlayLocalEnv(os.Environ(), sel, dir)

	if v := os.Getenv("OUTFIT_LEAK_CHECK"); v != "" {
		t.Errorf("the .env leaked into the process env: OUTFIT_LEAK_CHECK=%q", v)
	}
	if v := os.Getenv("OUTFIT_ENV_LEAK_CHECK"); v != "" {
		t.Errorf("an ENV value leaked into the process env: OUTFIT_ENV_LEAK_CHECK=%q", v)
	}
}
