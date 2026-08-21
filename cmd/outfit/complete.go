package main

// Tab completion. `outfit completion <shell>` prints the script the CLI
// framework generates for that shell; the script hands every word typed so
// far to the hidden `outfit __complete` (the framework's engine) and inserts
// whatever comes back, so the candidates never differ between shells. This
// file supplies the half the engine cannot see: what a flag's value and each
// positional slot completes to. Command names, subcommands, and flag names in
// both forms complete automatically from the tree — there is no second table
// of the surface that could drift, and TestCompletionCoversTree walks the
// tree to enforce that.

import (
	"fmt"
	"os"
	"strings"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/discovery"
	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// completionShells lists the supported shells in a stable order, for the
// `completion` argument's own completion and for error messages.
var completionShells = []string{"bash", "powershell", "zsh"}

// The engine's directive spelling: :0 lets the shell offer paths alongside
// the candidates, :4 says no.
const (
	directiveFile   = ":0"
	directiveNoFile = ":4"
)

// completionCmd prints the completion script the framework generates for the
// current command tree. Because a command named `completion` exists, Cobra
// leaves its own default (which would also support fish) alone.
func completionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "completion <shell>",
		Short: "print a tab completion script for bash, zsh, or powershell",
		Long: `Prints the tab completion script for the given shell. Once it is in place,
TAB completes commands, flags, providers, harnesses, and your registered
aliases.

The script must be sourced by a running shell, or saved where a shell reads
it at start-up:

  bash:
    For the current session:
      $ source <(outfit completion bash)
    Or for every new session, save the script once where bash reads
    completions (on macOS, ~/.bashrc can source it instead):
      $ outfit completion bash > /etc/bash_completion.d/outfit

  zsh:
    Save the script once as a completion function:
      $ mkdir -p ~/.zfunc && outfit completion zsh > ~/.zfunc/_outfit
    And have ~/.zshrc load it:
      fpath=(~/.zfunc $fpath)
      autoload -Uz compinit && compinit

  powershell:
    For the current session:
      > outfit completion powershell | Out-String | Invoke-Expression
    Or put that line in the PowerShell profile to load it every session:
      > Add-Content $PROFILE "outfit completion powershell | Out-String | Invoke-Expression"
`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completionShells, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			if len(args) != 1 {
				return fmt.Errorf("completion needs a shell (supported: %s)", strings.Join(completionShells, ", "))
			}
			switch args[0] {
			case "bash":
				return c.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return c.Root().GenZshCompletion(os.Stdout)
			case "powershell":
				return c.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell %q (supported: %s)", args[0], strings.Join(completionShells, ", "))
			}
		},
	}
	return c
}

// cmdCompletion is the seam the suite calls the `completion` command through.
// It dispatches through a full root: the printed script is generated for the
// whole tree, which only the root can see.
func cmdCompletion(args []string) error {
	return execCmd(newRootCmd(), append([]string{"completion"}, args...))
}

// The completion functions below are the value half of the surface. Each
// swallows its own failure and returns (nil, NoFileComp): a broken config or
// catalogue means "no candidates", never an error, so a completion attempt
// can never spew over the user's prompt.

// aliasNames returns the registered alias names, or none when the config
// cannot be read.
func aliasNames() []string {
	f, err := config.Load()
	if err != nil {
		return nil
	}
	return f.AliasNames()
}

// flagStr reads a value the user already typed for a flag off the command's
// parsed flags. By the time the engine calls a completion function it has
// parsed the words before the cursor, so a --providers override on the line
// is already in here.
func flagStr(c *cobra.Command, name string) string {
	if f := c.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// compProviders offers the catalogue's provider names, honouring a
// --providers override already on the command line.
func compProviders(c *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(flagStr(c, "providers")))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cat.SortedProviderNames(), cobra.ShellCompDirectiveNoFileComp
}

// compModels offers the models the --provider on the line currently serves,
// sourced live from its endpoint. Any failure (offline, timeout, no key)
// yields no candidates.
func compModels(c *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	name := flagStr(c, "provider")
	if name == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(flagStr(c, "providers")))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	p, ok := cat.Providers[name]
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	models, err := discovery.Models(p, "", opencode.EnvResolver(""))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return models, cobra.ShellCompDirectiveNoFileComp
}

// compHarnessNames offers the harness names.
func compHarnessNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return harness.Names(), cobra.ShellCompDirectiveNoFileComp
}

// compLogLevel offers the levels the log-level parser accepts.
func compLogLevel(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return daemon.LevelNames(), cobra.ShellCompDirectiveNoFileComp
}

// compFiles offers no static candidates and lets the shell fall back to
// filesystem paths.
func compFiles(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveDefault
}

// compNoValues is the completion for a value flag with nothing to enumerate:
// no candidates, no paths.
func compNoValues(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// Positional slots. The engine hands a command's ValidArgsFunction the
// positional arguments already on the line, which is how a fixed-arity slot
// knows it has been filled.

// noPositionals is a command with no positional arguments at all.
func noPositionals(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// aliasSlot is the Outfit slot: registered names plus a path, for the first
// positional only.
func aliasSlot(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return aliasNames(), cobra.ShellCompDirectiveDefault
}

// aliasOnlySlot is the unalias slot: registered names; a path is meaningless.
func aliasOnlySlot(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return aliasNames(), cobra.ShellCompDirectiveNoFileComp
}

// fileSlot is a single path argument.
func fileSlot(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveDefault
}

// keepSlot is `remote keep <duration> [outfit]`: a duration first, then the
// optional Outfit.
func keepSlot(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return nil, cobra.ShellCompDirectiveNoFileComp
	case 1:
		return aliasNames(), cobra.ShellCompDirectiveDefault
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// harnessValueFlags are the harness flags that take a detached value;
// --outfit/-O takes an optional attached value and consumes no word, and
// --get/--no-wake take none.
var harnessValueFlags = map[string]bool{
	"--set":          true,
	"--harness":      true,
	"-H":             true,
	"--providers":    true,
	"--fleet":        true,
	"--node":         true,
	"--prefer":       true,
	"--wake-timeout": true,
}

// harnessSlot completes one word of a harness command line. The command runs
// with Cobra's flag parsing off, so the engine calls this for everything and
// hands over the words before the cursor unparsed: the Outfit may be a
// leading positional or an attached --outfit/-O value; a detached flag right
// before the cursor is asking for its own value; beyond the Outfit every word
// belongs to the launched harness and outfit offers nothing.
func harnessSlot(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// --flag=<partial>, the form zsh and PowerShell pass as one word.
	if eq := strings.IndexByte(toComplete, '='); eq > 0 && toComplete[0] == '-' {
		switch toComplete[:eq] {
		case "--outfit", "-O":
			return aliasNames(), cobra.ShellCompDirectiveDefault
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Completing a flag name: the names the engine collected from the
	// command's flag set stand as the offering.
	if strings.HasPrefix(toComplete, "-") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) > 0 {
		last := args[len(args)-1]
		if strings.HasPrefix(last, "-") && !strings.Contains(last, "=") {
			// A detached flag right before the cursor is asking for its value.
			switch last {
			case "--set", "--harness", "-H":
				return harness.Names(), cobra.ShellCompDirectiveNoFileComp
			case "--providers", "--fleet":
				return nil, cobra.ShellCompDirectiveDefault
			case "--node", "--prefer", "--wake-timeout":
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
		}
	}
	// Count the positionals already on the line, skipping flags and the
	// words they consume (-- the bash-split --outfit = "" triple included).
	n, skip, terminated := 0, false, false
	for _, w := range args {
		switch {
		case skip:
			skip = false
		case w == "=":
			skip = true
		case w == "--":
			terminated = true
		case strings.HasPrefix(w, "-"):
			if eq := strings.IndexByte(w, '='); eq > 0 {
				// An attached value is consumed within the word.
			} else if harnessValueFlags[w] {
				skip = true
			}
		default:
			n++
		}
	}
	if terminated || n > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return aliasNames(), cobra.ShellCompDirectiveDefault
}

// compStatic marks a flag whose value completion is named by hand, so
// defaultFlagCompletions leaves it alone.
const compStatic = "outfit.completion"

// compRegister names a flag's value completion. Registering under the long
// name covers the shorthand too: both spellings resolve to the same flag.
func compRegister(c *cobra.Command, name string, f cobra.CompletionFunc) {
	if fl := c.Flags().Lookup(name); fl != nil {
		if fl.Annotations == nil {
			fl.Annotations = map[string][]string{}
		}
		fl.Annotations[compStatic] = []string{}
	}
	c.RegisterFlagCompletionFunc(name, f)
}

// defaultFlagCompletions sweeps the tree and gives every value-taking flag
// no command has named the completion it would have had under the old table
// anyway: no candidates, no paths. Under the old hand table an unknown value
// flag was mistaken for a boolean and its value completed like a positional
// argument; a command tree cannot make that mistake.
func defaultFlagCompletions(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.NoOptDefVal == "" {
				if _, ok := f.Annotations[compStatic]; !ok {
					c.RegisterFlagCompletionFunc(f.Name, compNoValues)
				}
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
