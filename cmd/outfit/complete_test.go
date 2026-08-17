package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// complete runs the hidden `outfit __complete` the way a generated script
// does — through the real tree on the framework's engine — and returns the
// candidate lines (descriptions stripped) and the trailing directive. It
// fails the test if the engine exits nonzero.
func complete(t *testing.T, words ...string) (candidates []string, directive string) {
	return completeCore(t, false, words...)
}

// completeQuiet additionally fails on any word on the process's standard
// error. The protocol says the command stays silent for the states the spec
// locks — broken config, unloadable catalogue, broken alias — wherever that
// comes from. main() makes it hold for a real process on top of that, even
// where the engine itself would log (a nonsense word on the line).
func completeQuiet(t *testing.T, words ...string) (candidates []string, directive string) {
	return completeCore(t, true, words...)
}

func completeCore(t *testing.T, quiet bool, words ...string) (candidates []string, directive string) {
	t.Helper()
	if len(words) == 0 {
		// A bare `outfit __complete` asks about the first word.
		words = []string{""}
	}
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"__complete"}, words...))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStderr := os.Stderr
	os.Stderr = w
	execErr := root.Execute()
	w.Close()
	os.Stderr = prevStderr
	errOut, _ := io.ReadAll(r)
	if quiet {
		if s := strings.TrimSpace(string(errOut)); s != "" {
			t.Errorf("outfit __complete %v wrote to stderr: %q", words, s)
		}
	}
	if execErr != nil {
		t.Fatalf("outfit __complete %v = %v, want nil", words, execErr)
	}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, ":"):
			directive = line
		default:
			candidates = append(candidates, strings.SplitN(line, "\t", 2)[0])
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
// that the completion engine itself stays hidden.
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

// TestCompletionCoversTree is the drift guard: completion is derived from the
// command tree, so the guard walks the tree itself. (a) Every visible command
// is offered where its parent completes. (b) Every flag every command
// registers completes, in both its long and short forms.
func TestCompletionCoversTree(t *testing.T) {
	isolateConfig(t)

	visible := func(c *cobra.Command) bool {
		return !c.Hidden && c.Name() != "help"
	}

	// (a) Command names and subcommands.
	root := newRootCmd()
	var commands, subcommands int
	var names []string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			if !visible(sub) {
				continue
			}
			subcommands++
			got, directive := complete(t, append(path, "")...)
			if !slices.Contains(got, sub.Name()) {
				t.Errorf("subcommand %q of %v is offered nowhere: %v", sub.Name(), path, got)
			}
			if directive != directiveNoFile {
				t.Errorf("%v %s: directive = %q, want %q (subcommands take no paths)", path, sub.Name(), directive, directiveNoFile)
			}
			walk(sub, append(path, sub.Name()))
		}
	}
	walk(root, nil)
	for _, c := range root.Commands() {
		if !visible(c) {
			continue
		}
		commands++
		names = append(names, c.Name())
	}
	got, _ := complete(t, "")
	for _, name := range names {
		if !slices.Contains(got, name) {
			t.Errorf("command %q is in the tree but not offered at the first word", name)
		}
	}
	if commands+subcommands < 20 {
		t.Fatalf("walked only %d commands and %d subcommands; the guard is not matching the tree", commands, subcommands)
	}

	// (b) Flags, long and short.
	var walkFlags func(c *cobra.Command, path []string)
	walkFlags = func(c *cobra.Command, path []string) {
		want := []string{}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			want = append(want, "--"+f.Name)
			if f.Shorthand != "" {
				want = append(want, "-"+f.Shorthand)
			}
		})
		got, _ := complete(t, append(path, "-")...)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%v: flag %s is registered but not completed: %v", path, w, got)
			}
		}
		for _, sub := range c.Commands() {
			if visible(sub) {
				walkFlags(sub, append(path, sub.Name()))
			}
		}
	}
	walkFlags(root, nil)
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

	slots := [][]string{
		{"apply", ""}, {"unapply", ""}, {"serve", ""}, {"alias", ""}, {"harness", ""},
		{"fleet", "route", ""}, {"remote", "deploy", ""}, {"remote", "start", ""},
	}
	for _, words := range slots {
		got, directive := complete(t, words...)
		if !hasAll(got, "qwen") {
			t.Errorf("%v: aliases missing from %v", words, got)
		}
		if directive != directiveFile {
			t.Errorf("%v: directive = %q, want %q", words, directive, directiveFile)
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

	// An explicit -- forwards everything, so nothing is offered.
	if got, _ := complete(t, "harness", "--", ""); len(got) != 0 {
		t.Errorf("candidates offered after an explicit --: %v", got)
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

// TestCompletionHelpTeachesSetup checks that `completion --help` says how to
// install the script: the script is useless to a user who is not told to
// source it or where to put it.
func TestCompletionHelpTeachesSetup(t *testing.T) {
	cmd, _, err := newRootCmd().Find([]string{"completion"})
	if err != nil {
		t.Fatalf("finding completion: %v", err)
	}
	for _, marker := range []string{
		"source <(outfit completion bash)",
		"outfit completion zsh > ~/.zfunc/_outfit",
		"outfit completion powershell | Out-String | Invoke-Expression",
	} {
		if !strings.Contains(cmd.Long, marker) {
			t.Errorf("completion help missing %q:\n%s", marker, cmd.Long)
		}
	}
}

// TestComplete_EqualsForm checks the attached-value form, which is how a
// single-word shell hands over `--outfit=<TAB>` — the only way that flag can
// take a path at all.
func TestComplete_EqualsForm(t *testing.T) {
	isolateConfig(t)
	registerOutfit(t, "PROVIDER llamacpp\nALIAS qwen\n")

	// bash splits --outfit= into words; the harness slot sees the triple and
	// still completes the value.
	got, directive := complete(t, "harness", "--outfit", "=", "")
	if !hasAll(got, "qwen") {
		t.Errorf("aliases missing from %v", got)
	}
	if directive != directiveFile {
		t.Errorf("directive = %q, want %q", directive, directiveFile)
	}

	// And the same for a flag whose value is a provider.
	if got, _ := complete(t, "add", "--provider="); !hasAll(got, "llamacpp") {
		t.Errorf("providers missing for --provider=: %v", got)
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
// from zsh and PowerShell, which pass `--outfit=qw` as a single word rather
// than splitting it on "=" the way bash does.
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
	// And the bare attached short form.
	if got, _ := complete(t, "harness", "-O="); !hasAll(got, "qwen") {
		t.Errorf("aliases missing for -O=: %v", got)
	}
}

// TestComplete_NeverErrors checks the one hard rule: whatever the state of the
// machine, completion prints candidates or nothing — never a failure, never a
// word on the error stream.
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
		{"add", "-p", "amazon-bedrock", "-m", ""},
		{"unalias", ""},
		{"apply", ""},
		{"harness", "--outfit", "=", ""},
		{"harness", "--nope="},
		{"alias", "--name", ""},
		{"add", "-p", "amazon-bedrock", "-m", "x", "--unknown-flag", ""},
	} {
		// complete() fails the test on any nonzero exit; the directive line
		// must still be there.
		if _, directive := complete(t, words...); directive == "" {
			t.Errorf("outfit __complete %v printed no directive", words)
		}
	}

	// The states the spec locks are quiet even in-process: a broken config,
	// an unloadable catalogue, and an unknown flag all mean no candidates,
	// never a word on the error stream.
	for _, words := range [][]string{
		{"unalias", ""},
		{"apply", ""},
		{"add", "--providers", "/nope/providers.yaml", "-p", ""},
		{"harness", "--outfit", "=", ""},
	} {
		completeQuiet(t, words...)
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

// TestCompletionCommand checks that every supported shell prints a script
// generated for the current command tree, calling the hidden __complete
// engine, and that anything else fails naming the supported shells.
func TestCompletionCommand(t *testing.T) {
	markers := map[string][]string{
		"bash":       {"# bash completion V2 for outfit", "__start_outfit", "__complete"},
		"zsh":        {"#compdef outfit", "compdef _outfit outfit", "__complete"},
		"powershell": {"# powershell completion for outfit", "Register-ArgumentCompleter", "__complete"},
	}
	for shell, wants := range markers {
		out := completionOf(t, shell)
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s script is missing %q:\n%s", shell, want, out)
			}
		}
	}

	if err := cmdCompletion(nil); err == nil {
		t.Error("expected an error with no shell named")
	} else if !strings.Contains(err.Error(), "bash") ||
		!strings.Contains(err.Error(), "powershell") ||
		!strings.Contains(err.Error(), "zsh") {
		t.Errorf("error without a shell should name the supported shells: %v", err)
	}
	if err := cmdCompletion([]string{"fish"}); err == nil {
		t.Error("expected an error for an unsupported shell")
	} else if !strings.Contains(err.Error(), "bash") ||
		!strings.Contains(err.Error(), "powershell") ||
		!strings.Contains(err.Error(), "zsh") {
		t.Errorf("error for fish should name the supported shells: %v", err)
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
// the parser accepts, on both commands that take it.
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
