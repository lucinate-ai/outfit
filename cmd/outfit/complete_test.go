package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// complete runs the hidden completion helper and returns its candidate lines
// and the trailing directive.
func complete(t *testing.T, words ...string) (candidates []string, directive string) {
	t.Helper()
	out := captureStdout(t, func() {
		if err := cmdComplete(words); err != nil {
			t.Fatalf("cmdComplete(%v): %v", words, err)
		}
	})
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, ":"):
			directive = line
		default:
			candidates = append(candidates, line)
		}
	}
	return candidates, directive
}

// hasAll reports whether every want is among got.
func hasAll(got []string, want ...string) bool {
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// TestComplete_CommandNames checks that the first word offers the commands, and
// that the completion helper itself stays hidden.
func TestComplete_CommandNames(t *testing.T) {
	isolateConfig(t)

	got, directive := complete(t, "")
	if !hasAll(got, "alias", "unalias", "apply", "unapply", "serve", "harness", "show", "completion") {
		t.Errorf("commands missing from %v", got)
	}
	for _, name := range got {
		if name == "__complete" {
			t.Error("the hidden __complete command should not be offered")
		}
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}

	// No words at all is the same question.
	if got, _ := complete(t); !hasAll(got, "alias", "unalias") {
		t.Errorf("commands missing with no words: %v", got)
	}
}

// TestCompletionCoversDispatch is the drift guard: a command added to run()'s
// switch has to be completable too.
func TestCompletionCoversDispatch(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// Only the dispatch switch in run() matches "case" lines holding bare
	// string literals at one tab of indentation.
	re := regexp.MustCompile(`(?m)^\tcase "([^"]+)"(?:, "[^"]+")*:`)
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		if name == "__complete" || strings.HasPrefix(name, "-") {
			continue
		}
		found++
		if _, ok := commands[name]; !ok {
			t.Errorf("command %q is dispatched but not completable (add it to commands in complete.go)", name)
		}
	}
	if found < 10 {
		t.Fatalf("only found %d dispatched commands; the scan is not matching run()'s switch", found)
	}
}

// TestComplete_UnaliasOffersAliasNames checks the case the feature exists for,
// including that a path makes no sense there.
func TestComplete_UnaliasOffersAliasNames(t *testing.T) {
	isolateConfig(t)

	got, directive := complete(t, "unalias", "")
	if len(got) != 0 {
		t.Errorf("candidates with an empty registry: %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}

	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")
	registerOutfit(t, "PROVIDER llamacpp\nALIAS gemma\n")

	got, directive = complete(t, "unalias", "")
	if !hasAll(got, "gemma", "qwen") {
		t.Errorf("aliases missing from %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q (a path cannot be unaliased)", directive, directiveNoFile)
	}

	// Only one name is taken.
	if got, _ := complete(t, "unalias", "qwen", ""); len(got) != 0 {
		t.Errorf("a second argument was offered candidates: %v", got)
	}
}

// TestComplete_OutfitCommandsOfferAliasesAndPaths checks the commands that take
// either.
func TestComplete_OutfitCommandsOfferAliasesAndPaths(t *testing.T) {
	isolateConfig(t)
	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")

	for _, cmd := range []string{"apply", "unapply", "serve", "alias", "harness"} {
		got, directive := complete(t, cmd, "")
		if !hasAll(got, "qwen") {
			t.Errorf("%s: aliases missing from %v", cmd, got)
		}
		if directive != directiveFile {
			t.Errorf("%s: directive = %q, want %q", cmd, directive, directiveFile)
		}
	}
}

// TestComplete_HarnessStopsAfterTheOutfit checks that outfit offers nothing for
// the arguments that belong to the launched agent.
func TestComplete_HarnessStopsAfterTheOutfit(t *testing.T) {
	isolateConfig(t)
	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")

	got, directive := complete(t, "harness", "qwen", "")
	if len(got) != 0 {
		t.Errorf("candidates offered for the harness's own args: %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}

	// A flag and its value do not count as the Outfit.
	if got, _ := complete(t, "harness", "-H", "pi", ""); !hasAll(got, "qwen") {
		t.Errorf("a detached flag value was mistaken for the Outfit: %v", got)
	}
}

// TestComplete_FlagNames checks that a leading dash offers the command's own
// flags, not another command's.
func TestComplete_FlagNames(t *testing.T) {
	isolateConfig(t)

	got, directive := complete(t, "alias", "-")
	if !hasAll(got, "--name", "-n", "--force", "-F", "--list", "-l") {
		t.Errorf("alias flags missing from %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}

	if got, _ := complete(t, "serve", "-"); !hasAll(got, "--dry-run", "-n") {
		t.Errorf("serve flags missing from %v", got)
	}

	if got, _ := complete(t, "daemon", "-"); !hasAll(got, "--loopback", "-l") {
		t.Errorf("daemon flags missing the loopback shorthand from %v", got)
	}
}

// TestComplete_FlagValues checks the values that can be enumerated.
func TestComplete_FlagValues(t *testing.T) {
	isolateConfig(t)

	for _, words := range [][]string{
		{"harness", "--set", ""},
		{"harness", "-H", ""},
		{"apply", "--harness", ""},
	} {
		if got, _ := complete(t, words...); !hasAll(got, "opencode", "pi") {
			t.Errorf("%v: harnesses missing from %v", words, got)
		}
	}

	if got, _ := complete(t, "add", "--provider", ""); !hasAll(got, "llamacpp", "openrouter") {
		t.Errorf("providers missing from %v", got)
	}
	if _, directive := complete(t, "apply", "--providers", ""); directive != directiveFile {
		t.Errorf("--providers should complete paths, got %q", directive)
	}
	if got, _ := complete(t, "completion", ""); !hasAll(got, "bash", "zsh", "powershell") {
		t.Errorf("shells missing from %v", got)
	}
}

// TestComplete_EqualsForm checks the attached-value form, which is how bash
// hands over `--outfit=<TAB>` — the only way that flag can take a path at all.
func TestComplete_EqualsForm(t *testing.T) {
	isolateConfig(t)
	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")

	got, directive := complete(t, "harness", "--outfit", "=", "")
	if !hasAll(got, "qwen") {
		t.Errorf("aliases missing from %v", got)
	}
	if directive != directiveFile {
		t.Errorf("directive = %q, want %q", directive, directiveFile)
	}

	// And the same for a flag whose value is a provider.
	if got, _ := complete(t, "add", "--provider", "=", ""); !hasAll(got, "llamacpp") {
		t.Errorf("providers missing from %v", got)
	}
}

// TestComplete_ModelValues checks that --model/-m has no static candidates —
// the catalogue no longer enumerates models — but that the flag still consumes
// its value so a following flag completes normally rather than being read as the
// model.
func TestComplete_ModelValues(t *testing.T) {
	isolateConfig(t)

	// amazon-bedrock has no models endpoint (no base URL), so discovery makes no
	// network call and offers nothing — the offline, no-candidates path.
	got, directive := complete(t, "add", "-p", "amazon-bedrock", "-m", "")
	if len(got) != 0 {
		t.Errorf("expected no candidates for a non-discoverable provider, got %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}

	// -m consumes its value: the harness flag after it still completes.
	if got, _ := complete(t, "add", "-p", "amazon-bedrock", "-m", "some-model", "-H", ""); !hasAll(got, "opencode", "pi") {
		t.Errorf("a flag after --model did not complete; -m may not be consuming its value: %v", got)
	}
}

// TestComplete_ModelDiscovery checks that --model/-m offers the models a
// provider's endpoint currently serves, sourced live from a stub.
func TestComplete_ModelDiscovery(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"disc-a"},{"id":"disc-b"}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.yaml")
	mustWrite(t, provPath, "providers:\n  stub:\n    description: Stub\n    npm: \"@ai-sdk/openai-compatible\"\n    options:\n      baseURL: "+srv.URL+"\n")

	got, directive := complete(t, "add", "--providers", provPath, "-p", "stub", "-m", "")
	if !hasAll(got, "disc-a", "disc-b") {
		t.Errorf("discovered models not offered: %v", got)
	}
	if directive != directiveNoFile {
		t.Errorf("directive = %q, want %q", directive, directiveNoFile)
	}
}

// TestComplete_AttachedEqualsForm checks the same flag=value case as it arrives
// from zsh and PowerShell, which pass `--outfit=qw` as a single word rather than
// splitting it on "=" the way bash does.
func TestComplete_AttachedEqualsForm(t *testing.T) {
	isolateConfig(t)
	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")

	got, directive := complete(t, "harness", "--outfit=")
	if !hasAll(got, "qwen") {
		t.Errorf("aliases missing from %v", got)
	}
	if directive != directiveFile {
		t.Errorf("directive = %q, want %q", directive, directiveFile)
	}

	// A partially-typed value is still the flag's value, not a new flag.
	if got, _ := complete(t, "harness", "--outfit=qw"); !hasAll(got, "qwen") {
		t.Errorf("aliases missing for a partial attached value: %v", got)
	}
	// The short flag attaches its value the same way.
	if got, _ := complete(t, "add", "-p=llamacpp", "-f="); len(got) == 0 {
		t.Error("no families offered for -p=<name> -f=")
	}
}

// TestComplete_NeverErrors checks the one hard rule: whatever the state of the
// machine, completion prints candidates or nothing — never a failure.
func TestComplete_NeverErrors(t *testing.T) {
	home := isolateConfig(t)

	// A corrupt config must not stop alias-less completion working.
	configPath := filepath.Join(home, ".config", "outfit", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, configPath, "{not json")

	for _, words := range [][]string{
		nil,
		{""},
		{"-"},
		{"nonsense", ""},
		{"unalias", ""},
		{"apply", ""},
		{"harness", "--outfit", "=", ""},
		{"add", "--providers", "/nope/providers.yaml", "-p", ""},
		{"alias", "--name"},
	} {
		out := captureStdout(t, func() {
			if err := cmdComplete(words); err != nil {
				t.Errorf("cmdComplete(%v) = %v, want nil", words, err)
			}
		})
		if !strings.Contains(out, ":") {
			t.Errorf("cmdComplete(%v) printed no directive: %q", words, out)
		}
	}
}

// completionOf returns the script printed for a shell.
func completionOf(t *testing.T, shell string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := cmdCompletion([]string{shell}); err != nil {
			t.Fatalf("cmdCompletion %s: %v", shell, err)
		}
	})
}

// TestCompletionCommand checks that every supported shell prints a script that
// wires itself up and calls the __complete helper.
func TestCompletionCommand(t *testing.T) {
	markers := map[string][]string{
		"bash":       {"_outfit()", "complete -F _outfit outfit", "__complete"},
		"zsh":        {"#compdef outfit", "compdef _outfit outfit", "__complete"},
		"powershell": {"Register-ArgumentCompleter", "-CommandName outfit", "__complete"},
	}
	for shell, wants := range markers {
		out := completionOf(t, shell)
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s script is missing %q:\n%s", shell, want, out)
			}
		}
	}

	// Every advertised shell has a script.
	for _, shell := range completionShells {
		if completionScripts[shell] == "" {
			t.Errorf("shell %q is advertised but has no embedded script", shell)
		}
	}

	if err := cmdCompletion(nil); err == nil {
		t.Error("expected an error with no shell named")
	}
	if err := cmdCompletion([]string{"fish"}); err == nil {
		t.Error("expected an error for an unsupported shell")
	}
}

// TestCompletionScriptsAreValid runs each script through its own shell's syntax
// checker, when that shell is installed. Skipped shells are just not exercised
// on this machine — CI has bash and zsh.
func TestCompletionScriptsAreValid(t *testing.T) {
	cases := []struct {
		shell   string
		bin     string
		ext     string
		checker func(bin, path string) *exec.Cmd
	}{
		{"bash", "bash", "bash", func(bin, path string) *exec.Cmd {
			return exec.Command(bin, "-n", path)
		}},
		{"zsh", "zsh", "zsh", func(bin, path string) *exec.Cmd {
			return exec.Command(bin, "-n", path)
		}},
		{"powershell", "pwsh", "ps1", func(bin, path string) *exec.Cmd {
			// Parse the file without running it; any parse error fails the test.
			script := "$errs=$null;" +
				"[System.Management.Automation.Language.Parser]::ParseFile('" +
				path + "',[ref]$null,[ref]$errs)|Out-Null;" +
				"if($errs.Count){exit 1}"
			return exec.Command(bin, "-NoProfile", "-Command", script)
		}},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			bin, err := exec.LookPath(c.bin)
			if err != nil {
				t.Skipf("%s not installed", c.bin)
			}
			path := filepath.Join(t.TempDir(), "outfit."+c.ext)
			mustWrite(t, path, completionOf(t, c.shell))
			if out, err := c.checker(bin, path).CombinedOutput(); err != nil {
				t.Errorf("%s rejected the completion script: %v\n%s", c.bin, err, out)
			}
		})
	}
}

// TestComplete_LogLevelValues checks that --log-level completes to the levels
// the parser accepts, on both commands that take it. The flag and its value
// are separate table entries, so this covers being offered the flag at all as
// well as what follows it — a value listed without the flag would complete
// only for someone who already knew it existed.
func TestComplete_LogLevelValues(t *testing.T) {
	isolateConfig(t)

	for _, cmd := range []string{"daemon", "serve"} {
		got, directive := complete(t, cmd, "--log-level", "")
		if !hasAll(got, daemon.LevelNames()...) {
			t.Errorf("%s --log-level: %v does not offer %v", cmd, got, daemon.LevelNames())
		}
		if directive != directiveNoFile {
			t.Errorf("%s --log-level: directive = %q, want %q", cmd, directive, directiveNoFile)
		}
		if flags, _ := complete(t, cmd, "-"); !hasAll(flags, "--log-level") {
			t.Errorf("%s: --log-level is not offered among its flags: %v", cmd, flags)
		}
	}
}
