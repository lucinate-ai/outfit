package main

// Tab completion. `outfit completion <shell>` prints one of the embedded
// scripts below; each calls the hidden `outfit __complete` with every word up
// to the cursor, and this file works out what could come next. The scripts are
// deliberately thin — bash, zsh, and PowerShell differ only in how they hand
// over the words and insert a result, never in the candidates — so this file
// stays the one source of truth for what completes to what.
//
// The command and flag tables here mirror run()'s switch and each cmdX's
// FlagSet. That duplication is deliberate — the flag package offers no way to
// enumerate a FlagSet before it is parsed — and TestCompletionCoversDispatch
// guards the half of it that matters.

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/harness"
)

//go:embed completion.bash
var bashCompletion string

//go:embed completion.zsh
var zshCompletion string

//go:embed completion.ps1
var powershellCompletion string

// completionScripts maps a shell name to its embedded completion script.
var completionScripts = map[string]string{
	"bash":       bashCompletion,
	"zsh":        zshCompletion,
	"powershell": powershellCompletion,
}

// completionShells lists the supported shells in a stable order, for the
// `completion` argument's own completion and for error messages.
var completionShells = []string{"bash", "powershell", "zsh"}

// Directives tell the shell script whether to offer paths alongside whatever
// candidates were printed.
const (
	directiveNoFile = ":nofile"
	directiveFile   = ":file"
)

// candidateKind says what belongs in a given slot — a flag's value, or a
// command's positional argument.
type candidateKind int

const (
	kindNone      candidateKind = iota // no candidates, no paths (also: bool flags)
	kindFile                           // paths only
	kindAlias                          // registered aliases, or a path
	kindAliasOnly                      // registered aliases; a path would be meaningless
	kindHarness
	kindProvider
	kindFamily
	kindModel
	kindShell
)

// command describes one subcommand's completable surface.
type command struct {
	// flags is every flag the command accepts, as typed.
	flags []string
	// values maps a value-taking flag to what its value is. A flag absent from
	// this map is a boolean and consumes no following word.
	values map[string]candidateKind
	// positional is what the command's first positional argument accepts, and
	// positionals how many arguments that applies to.
	positional  candidateKind
	positionals int
}

// selectionFlags are the flags add and remove share (see parseSelection).
var selectionFlags = []string{
	"--provider", "-p", "--model-family", "-f", "--model", "-m",
	"--alias", "-a", "--context", "-c", "--output", "-o",
	"--providers", "--base-url", "-u", "--harness", "-H",
}

// selectionValues maps those of them that take a value.
var selectionValues = map[string]candidateKind{
	"--provider": kindProvider, "-p": kindProvider,
	"--model-family": kindFamily, "-f": kindFamily,
	"--model": kindModel, "-m": kindModel,
	"--alias": kindNone, "-a": kindNone,
	"--context": kindNone, "-c": kindNone,
	"--output": kindNone, "-o": kindNone,
	"--providers": kindFile,
	"--base-url":  kindNone, "-u": kindNone,
	"--harness": kindHarness, "-H": kindHarness,
}

// commands is the completable command surface. Keep it in step with run()'s
// switch; TestCompletionCoversDispatch fails when a command is added there and
// forgotten here.
var commands = map[string]command{
	"add":    {flags: selectionFlags, values: selectionValues},
	"remove": {flags: selectionFlags, values: selectionValues},
	"list": {
		flags:  []string{"--providers"},
		values: map[string]candidateKind{"--providers": kindFile},
	},
	"show": {
		flags:  []string{"--harness", "-H"},
		values: map[string]candidateKind{"--harness": kindHarness, "-H": kindHarness},
	},
	"apply": {
		flags: []string{"--providers", "--output", "-o", "--harness", "-H"},
		values: map[string]candidateKind{
			"--providers": kindFile, "--output": kindNone, "-o": kindNone,
			"--harness": kindHarness, "-H": kindHarness,
		},
		positional: kindAlias, positionals: 1,
	},
	"unapply": {
		flags: []string{"--providers", "--harness", "-H"},
		values: map[string]candidateKind{
			"--providers": kindFile, "--harness": kindHarness, "-H": kindHarness,
		},
		positional: kindAlias, positionals: 1,
	},
	"alias": {
		flags:      []string{"--name", "-n", "--force", "-F", "--list", "-l"},
		values:     map[string]candidateKind{"--name": kindNone, "-n": kindNone},
		positional: kindAlias, positionals: 1,
	},
	"unalias": {positional: kindAliasOnly, positionals: 1},
	"serve": {
		flags:      []string{"--dry-run", "-n"},
		positional: kindAlias, positionals: 1,
	},
	"export": {
		flags: []string{"--provider", "-p", "--providers", "--harness", "-H"},
		values: map[string]candidateKind{
			"--provider": kindProvider, "-p": kindProvider,
			"--providers": kindFile, "--harness": kindHarness, "-H": kindHarness,
		},
	},
	"init-providers": {
		flags:      []string{"--force", "-F"},
		positional: kindFile, positionals: 1,
	},
	"harness": {
		flags: []string{"--set", "--get", "--harness", "-H", "--outfit", "-O", "--providers"},
		values: map[string]candidateKind{
			"--set": kindHarness, "--harness": kindHarness, "-H": kindHarness,
			// --outfit's value is optional, so it is not consumed as a separate
			// word; naming it here is what completes `--outfit=<TAB>`.
			"--outfit": kindAlias, "-O": kindAlias,
			"--providers": kindFile,
		},
		// Only the first argument can name an Outfit; the rest are the
		// harness's own, and outfit has nothing to say about them.
		positional: kindAlias, positionals: 1,
	},
	"completion": {positional: kindShell, positionals: 1},
	"version":    {},
	"help":       {},
}

// cmdCompletion prints the shell completion script for the named shell.
func cmdCompletion(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("completion needs a shell (supported: %s)", strings.Join(completionShells, ", "))
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		return fmt.Errorf("unsupported shell %q (supported: %s)", args[0], strings.Join(completionShells, ", "))
	}
	fmt.Print(script)
	return nil
}

// cmdComplete prints the completion candidates for a partially-typed command
// line, one per line, followed by a directive telling the shell whether paths
// belong in the list too. args is every word after `outfit` up to and including
// the one under the cursor, which may be empty.
//
// It never returns an error and never writes to stderr: a completion helper
// that failed would spew over the user's prompt. A config it cannot read, a
// catalogue it cannot load, and nonsense input all mean "no candidates".
func cmdComplete(args []string) error {
	candidates, directive := completions(args)
	for _, c := range candidates {
		fmt.Println(c)
	}
	fmt.Println(directive)
	return nil
}

// completions works out what could follow the words typed so far.
func completions(args []string) ([]string, string) {
	if len(args) == 0 {
		return commandNames(), directiveNoFile
	}
	cur, words := args[len(args)-1], args[:len(args)-1]
	if len(words) == 0 {
		return commandNames(), directiveNoFile
	}
	cmd, ok := commands[words[0]]
	if !ok {
		return nil, directiveNoFile
	}

	// A flag and its value can arrive attached as one word (`--outfit=<partial>`,
	// how zsh and PowerShell pass it) or split into three (`--outfit`, `=`, ``,
	// how bash tokenizes because "=" is a word-break). Handle the attached form
	// here; the split form falls through to the prev-word check below. Either
	// way this is the only route to completing --outfit/-O, whose value has to
	// be attached.
	if flag, _, ok := strings.Cut(cur, "="); ok && strings.HasPrefix(flag, "-") {
		if kind, found := cmd.values[flag]; found {
			return candidatesFor(kind, words)
		}
	}
	prev := words[len(words)-1]
	if prev == "=" && len(words) > 1 {
		prev = words[len(words)-2]
	}
	if kind, ok := cmd.values[prev]; ok {
		return candidatesFor(kind, words)
	}

	if strings.HasPrefix(cur, "-") {
		return cmd.flags, directiveNoFile
	}
	if positionalsUsed(cmd, words[1:]) >= cmd.positionals {
		return nil, directiveNoFile
	}
	return candidatesFor(cmd.positional, words)
}

// positionalsUsed counts the arguments already given to a command, skipping
// flags and the words they consume as values.
func positionalsUsed(cmd command, words []string) int {
	n, skip := 0, false
	for _, w := range words {
		switch {
		case skip:
			skip = false
		case w == "=":
			// Part of a --flag=value triple; the value follows and is not a
			// positional either.
			skip = true
		case strings.HasPrefix(w, "-"):
			// A flag with an attached value consumes nothing further.
			if _, takesValue := cmd.values[w]; takesValue && !strings.Contains(w, "=") {
				skip = true
			}
		default:
			n++
		}
	}
	return n
}

// commandNames returns the commands worth completing, in stable order. The
// hidden __complete is not among them.
func commandNames() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// candidatesFor returns the candidates for one slot. words is everything typed
// before the cursor, which a few kinds consult for context.
func candidatesFor(kind candidateKind, words []string) ([]string, string) {
	switch kind {
	case kindFile:
		return nil, directiveFile
	case kindAlias:
		return aliasNames(), directiveFile
	case kindAliasOnly:
		return aliasNames(), directiveNoFile
	case kindHarness:
		return harness.Names(), directiveNoFile
	case kindProvider:
		cat, err := loadCatalogFor(words)
		if err != nil {
			return nil, directiveNoFile
		}
		return cat.SortedProviderNames(), directiveNoFile
	case kindFamily:
		p := providerOn(words)
		if p == nil {
			return nil, directiveNoFile
		}
		return p.SortedFamilyNames(), directiveNoFile
	case kindModel:
		p := providerOn(words)
		if p == nil {
			return nil, directiveNoFile
		}
		return modelNames(p, flagValue(words, "--model-family", "-f")), directiveNoFile
	case kindShell:
		return completionShells, directiveNoFile
	default:
		return nil, directiveNoFile
	}
}

// aliasNames returns the registered alias names, or none when the config
// cannot be read.
func aliasNames() []string {
	f, err := config.Load()
	if err != nil {
		return nil
	}
	return f.AliasNames()
}

// loadCatalogFor loads the provider catalogue, honouring a --providers override
// already on the command line.
func loadCatalogFor(words []string) (*catalog.Catalog, error) {
	return catalog.LoadFrom(catalog.ResolveCatalogPath(flagValue(words, "--providers")))
}

// providerOn returns the provider named by --provider/-p on the command line so
// far, or nil when there is none (or the catalogue will not load).
func providerOn(words []string) *catalog.Provider {
	name := flagValue(words, "--provider", "-p")
	if name == "" {
		return nil
	}
	cat, err := loadCatalogFor(words)
	if err != nil {
		return nil
	}
	return cat.Providers[name]
}

// modelNames returns the model keys of one family, or of every family when none
// is named.
func modelNames(p *catalog.Provider, family string) []string {
	if fam, ok := p.Families[family]; ok {
		return fam.ModelKeys()
	}
	var models []string
	for _, name := range p.SortedFamilyNames() {
		models = append(models, p.Families[name].ModelKeys()...)
	}
	sort.Strings(models)
	return models
}

// flagValue finds the value already given to one of names, in either the
// attached (--flag=value, which bash splits into three words) or detached
// (--flag value) form. It returns "" when the flag is not on the line.
func flagValue(words []string, names ...string) string {
	matches := func(w string) bool {
		for _, name := range names {
			if w == name {
				return true
			}
		}
		return false
	}
	for i, w := range words {
		if prefix, value, ok := strings.Cut(w, "="); ok && matches(prefix) {
			return value
		}
		if !matches(w) {
			continue
		}
		// --flag = value (bash's split), or --flag value.
		if i+2 < len(words) && words[i+1] == "=" {
			return words[i+2]
		}
		if i+1 < len(words) {
			return words[i+1]
		}
	}
	return ""
}
