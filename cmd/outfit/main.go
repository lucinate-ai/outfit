// Command outfit configures a coding agent ("harness") to use a model provider,
// by deep-merging provider settings into that harness's config. The supported
// harnesses are opencode, the Pi coding agent, and lucinate; the harness is
// chosen at runtime with --harness/-H or OUTFIT_HARNESS, or a stored default
// set via `outfit harness --set`, and defaults to opencode. `outfit --help`
// and each command's own help describe the surface; the commands are built as
// a Cobra tree (see commands.go), so the help is what the tree says.
//
// Providers are defined in providers.yaml, which is embedded
// into the binary at build time. For opencode the config is parsed as JSONC so
// comments and existing settings outside the managed provider block are
// preserved; for Pi the managed provider is merged into
// ~/.pi/agent/models.json; for lucinate into ~/.lucinate/connections.json.
//
// The API base URL can be overridden for any provider with --base-url/-u or the
// OUTFIT_BASE_URL environment variable; the flag wins over the env var, and
// either wins over the catalogue's defaults.
//
// An Outfit is a declarative, Dockerfile-style file describing one provider
// selection, applied with `outfit apply` and reverted with `outfit unapply`;
// see the internal/outfit package. The harness is deliberately not part of an
// Outfit, so the same Outfit applies to any harness.
//
// `outfit alias` registers an Outfit under a short name — by default its own
// ALIAS — and every command that takes a path takes that name instead, or reads
// one from OUTFIT_ALIAS when given no path at all. The registry is machine-local
// state in outfit's own config, not part of any Outfit, so an Outfit stays as
// portable as it was.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/discovery"
	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/lucinate"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/outfitsrc"
	"github.com/lucinate-ai/outfit/internal/remote"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// version is the binary's version. It defaults to "dev" and is overridden at
// build time via -ldflags "-X main.version=...", set by the Makefile and
// goreleaser.
var version = "dev"

func main() {
	// A completion attempt must never spew over the prompt. The engine's
	// error paths write straight to the process's standard error, so when
	// this process is the engine there is no standard error: whatever state
	// the machine is in, the answer is "no candidates". A bare `outfit
	// __complete`, as a script sends it when nothing has been typed, is a
	// question about the first word.
	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		if f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			os.Stderr = f
		}
		if len(os.Args) == 2 {
			os.Args = append(os.Args, "")
		}
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root := newRootCmd()
	root.SetArgs(args)
	return root.Execute()
}

// registerSelectionFlags registers the flags add and remove share, into a
// Selection plus the separately-bound harness name (the harness is never part
// of a Selection, so it cannot leak into an Outfit).
func registerSelectionFlags(fs *pflag.FlagSet, s *outfit.Selection, harnessName *string) {
	fs.StringVarP(&s.Provider, "provider", "p", "", "provider name")
	fs.StringVarP(&s.Model, "model", "m", "", "model id")
	fs.StringVarP(&s.Alias, "alias", "a", "", "friendly name for the model (overrides the harness key)")
	fs.StringVarP(&s.Context, "context", "c", "", "context window size (e.g. 128k, 1m, 200000)")
	fs.StringVarP(&s.Output, "output", "o", "", "max output tokens (defaults to a quarter of --context)")
	fs.StringVar(&s.Providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&s.BaseURL, "base-url", "u", "", "override the provider API base URL")
	fs.StringVarP(harnessName, "harness", "H", "", "which harness to configure")
}

// addCmd deep-merges the named provider into the active harness's config.
func addCmd() *cobra.Command {
	var s outfit.Selection
	var harnessName string
	c := &cobra.Command{
		Use:           "add",
		Short:         "deep-merge a provider into the active harness's config",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if s.Provider == "" {
				return fmt.Errorf("--provider/-p is required (see `outfit list`)")
			}
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			return applySelection(s, h, "", opencode.EnvResolver(""))
		},
	}
	fs := c.Flags()
	registerSelectionFlags(fs, &s, &harnessName)
	fs.SetInterspersed(false)
	compRegister(c, "provider", compProviders)
	compRegister(c, "model", compModels)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// removeCmd removes the named provider, or just the named model.
func removeCmd() *cobra.Command {
	var s outfit.Selection
	var harnessName string
	c := &cobra.Command{
		Use:           "remove",
		Short:         "remove a provider, or a model, from the harness's config",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if s.Provider == "" {
				return fmt.Errorf("--provider/-p is required (see `outfit list`)")
			}
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			return removeSelection(s, h, "")
		},
	}
	fs := c.Flags()
	registerSelectionFlags(fs, &s, &harnessName)
	fs.SetInterspersed(false)
	compRegister(c, "provider", compProviders)
	compRegister(c, "model", compModels)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// envFileDir returns the local directory a `.env` beside outfitPath would
// live in, for opencode.EnvResolver — or "" when outfitPath is empty (no
// Outfit in play) or a URL, since a URL-sourced Outfit has no local directory
// to look beside.
func envFileDir(outfitPath string) string {
	if outfitPath == "" || outfitsrc.IsURL(outfitPath) {
		return ""
	}
	return filepath.Dir(outfitPath)
}

// applySelection writes a single provider selection into the active harness's
// config. It is the shared core of `add` and `apply`: both resolve a selection
// (from flags or an Outfit file) and hand it here.
// outfitPath is the Outfit the selection came from — its own path, not a
// pre-computed directory, so a relative REMOTE resolves correctly whether the
// Outfit is local or URL-sourced (see resolveRemotePath); it is empty when no
// Outfit is involved (an `outfit add` from flags). resolve looks up API key
// variables — normally opencode.EnvResolver of the Outfit's local directory,
// but `outfit harness` widens it with the key it fetched from a remote
// endpoint, which it is about to put in the launched agent's environment.
func applySelection(sel outfit.Selection, h harness.Harness, outfitPath string, resolve func(string) string) error {
	if sel.Model == "" && sel.Alias == "" {
		return fmt.Errorf("a provider selection needs a model or an alias")
	}

	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(sel.Providers))
	if err != nil {
		return err
	}
	p, ok := cat.Providers[sel.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (see `outfit list`)", sel.Provider)
	}

	// The catalogue provider p is resolved above by the PROVIDER value, which
	// stays the engine definition. From here on sel.Provider is the harness-facing
	// name: for a remote endpoint that is the environment name, so the model reads
	// as <env>/<model> and each environment keeps its own block rather than
	// several engines-of-the-same-kind overwriting one. The name comes from
	// removeSelection too, so apply and unapply stay symmetric.
	if sel.Remote != "" {
		env, err := remoteEnvName(sel.Remote, outfitPath)
		if err != nil {
			return err
		}
		if env != "" {
			sel.Provider = env
			// The provider is now keyed on the environment; label it so it reads
			// distinctly from a local engine of the same kind in a model picker
			// (e.g. "llama.cpp (dev-2)" rather than another bare "llama.cpp").
			sel.DisplayName = catalog.RemoteProviderLabel(p.Name, env)
		}
	}

	// An Outfit for a remote endpoint states no BASEURL: the address belongs to
	// the deployment, which records it in the remote config REMOTE names. Take
	// it from there — but only when the Outfit stated none, so a hand-written
	// BASEURL still wins.
	// The harness reports the base URL it wrote, so this needs no announcement
	// of its own beyond naming where it came from.
	if sel.BaseURL == "" && sel.Remote != "" {
		baseURL, err := remoteBaseURL(sel.Remote, outfitPath)
		if err != nil {
			return err
		}
		if baseURL != "" {
			fmt.Printf("Taking the base URL from %s.\n", sel.Remote)
			sel.BaseURL = baseURL
		}
	}

	var contextSize, outputSize int
	if sel.Output != "" && sel.Context == "" {
		return fmt.Errorf("--output/-o needs --context/-c: opencode requires a context window before an output limit")
	}
	if sel.Context != "" {
		contextSize, err = contextsize.Parse(sel.Context)
		if err != nil {
			return err
		}
		if sel.Output != "" {
			outputSize, err = contextsize.Parse(sel.Output)
			if err != nil {
				return err
			}
			if outputSize > contextSize {
				return fmt.Errorf("output limit (%d) cannot exceed the context window (%d)", outputSize, contextSize)
			}
		} else {
			outputSize = contextsize.DefaultOutput(contextSize)
		}
	}

	summary, err := h.Apply(p, sel, contextSize, outputSize, resolve)
	if err != nil {
		return err
	}

	fmt.Printf("Updated %s\n\n", summary.ConfigPath)
	fmt.Printf("Configured provider %q.\n", sel.Provider)
	if summary.DefaultModel != "" {
		fmt.Printf("Default model: %s\n", summary.DefaultModel)
	}
	if contextSize > 0 {
		fmt.Printf("Context window: %d tokens\n", contextSize)
		fmt.Printf("Max output: %d tokens\n", outputSize)
	}
	for _, note := range summary.Notes {
		fmt.Println(note)
	}
	return nil
}

// readOutfit reads and parses the Outfit at path, defaulting to ./Outfit when
// path is empty so a bare command works in a directory that holds one. When
// path names a directory, the default Outfit file inside it is used, so a
// caller can pass either the file itself or the directory that holds it. path
// may also be a name registered with `outfit alias`, which is what gives every
// Outfit command the same shorthand, or an http(s) URL, fetched instead of
// read from local disk — a URL ending in "/" gets outfit.DefaultFile appended,
// the URL equivalent of the directory case. usage shows the caller's own way
// of naming a path (subcommands take it positionally, `harness` takes it as a
// flag) in the not-found hint. It returns the parsed selection alongside the
// resolved path, which callers use to locate files referenced relative to the
// Outfit — via outfitsrc, so a relative reference resolves against a URL the
// same way it resolves against a local directory.
//
// With no path at all, OUTFIT_ALIAS names the Outfit before ./Outfit is tried,
// so one export dresses a whole shell. It ranks below an explicit argument and
// above the default: the variable decides *which* Outfit is the default, never
// whether a command acts on one. `outfit alias` opts out by naming
// outfit.DefaultFile itself, so registering never resolves through the
// registry it is writing to.
//
// It prints when an alias decided the path, which is against this file's
// convention that only cmdX functions report anything. The alternative is to
// repeat that reporting at all four call sites, where one omission would leave
// the user guessing which file was read. The line goes to stderr because
// `outfit remote env` writes shell exports to stdout for `eval`, which a stray
// prose line would break.
func readOutfit(usage, path string) (outfit.Selection, string, error) {
	if path == "" {
		named, aliased, err := outfitFromEnv()
		if err != nil {
			return outfit.Selection{}, path, err
		}
		if aliased != "" {
			fmt.Fprintf(os.Stderr, "Using %s %q (%s)\n\n", outfitAliasEnv, named, aliased)
			path = aliased
		} else {
			path = outfit.DefaultFile
		}
	}
	if aliased, ok, err := resolveAlias(path); err != nil {
		return outfit.Selection{}, path, err
	} else if ok {
		fmt.Fprintf(os.Stderr, "Using alias %q (%s)\n\n", path, aliased)
		path = aliased
	}

	var data []byte
	if outfitsrc.IsURL(path) {
		if strings.HasSuffix(path, "/") {
			path += outfit.DefaultFile
		}
		fetched, err := outfitsrc.Fetch(path)
		if err != nil {
			return outfit.Selection{}, path, err
		}
		data = fetched
	} else {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			path = filepath.Join(path, outfit.DefaultFile)
		}
		read, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && path == outfit.DefaultFile {
				return outfit.Selection{}, path, fmt.Errorf("no %s found in the current directory (pass a path or an alias: %s; or set %s; see `outfit alias --list`)", outfit.DefaultFile, usage, outfitAliasEnv)
			}
			return outfit.Selection{}, path, fmt.Errorf("reading %s: %w", path, err)
		}
		data = read
	}

	sel, err := outfit.Parse(data)
	if err != nil {
		return outfit.Selection{}, path, fmt.Errorf("%s: %w", path, err)
	}
	return sel, path, nil
}

// outfitAliasEnv names a registered alias for every command that takes an
// Outfit path but was given none.
const outfitAliasEnv = "OUTFIT_ALIAS"

// outfitFromEnv resolves OUTFIT_ALIAS, returning the name it holds alongside
// the Outfit it points at, or two empty strings when it is unset or empty.
//
// It deliberately does not reuse resolveAlias. That function is written for an
// argument, which is usually a path: a value it cannot resolve falls through to
// path handling, and a file on disk of the same spelling wins. Neither fits a
// variable. A variable can only ever have been set to name an alias, so a
// name it cannot resolve is a mistake to report rather than a filename to try,
// and a same-named file in whichever directory the user is standing in is a
// coincidence — letting it shadow the variable would make the export work in
// some directories and not others.
func outfitFromEnv() (string, string, error) {
	// The CLI layer's one Viper reads the variable (see cli_viper.go):
	// OUTFIT_ALIAS has no flag spelling, so only the env and its absence.
	name := cliViper.GetString("alias")
	if name == "" {
		return "", "", nil
	}
	if err := config.ValidAliasName(name); err != nil {
		return "", "", fmt.Errorf("%s: %w", outfitAliasEnv, err)
	}
	f, err := config.Load()
	if err != nil {
		return "", "", err
	}
	path, ok := f.Alias(name)
	if !ok {
		return "", "", fmt.Errorf("%s names %q, which is not a registered alias — see `outfit alias --list`, or unset %s", outfitAliasEnv, name, outfitAliasEnv)
	}
	// A URL target is not probed here: that would mean a network call on
	// every command that resolves no explicit path, just to confirm what a
	// real fetch will report anyway. A stale URL surfaces its own error when
	// something actually reads it.
	if !outfitsrc.IsURL(path) {
		if _, err := os.Stat(path); err != nil {
			return "", "", fmt.Errorf("%s names %q, which points at %s, which is gone — re-point it with `outfit alias -n %s <path>`, or drop it with `outfit unalias %s`", outfitAliasEnv, name, path, name, name)
		}
	}
	return name, path, nil
}

// resolveAlias looks arg up in the alias registry, returning the Outfit it
// names. It reports ok=false — leaving the caller to treat arg as a path —
// when arg is not a registered name, or when it also names something on disk.
//
// The name-shaped guard is not an optimisation: it means a path-shaped argument
// never causes a config read at all, so the commands that have nothing to do
// with outfit's own config (serve, most of all) keep working the same way when
// that config is absent, unreadable, or someone else's.
//
// A path on disk beats a registered alias, which is the opposite of how shell
// aliases work and deliberate: every existing invocation passes a path, so
// registering an alias must never change what an already-working command does.
// That rule is about arguments only — OUTFIT_ALIAS is resolved by
// outfitFromEnv, which says why it is not shadowed the same way.
func resolveAlias(arg string) (string, bool, error) {
	if !config.NameShaped(arg) {
		return "", false, nil
	}
	f, err := config.Load()
	if err != nil {
		return "", false, err
	}
	path, ok := f.Alias(arg)
	if !ok {
		return "", false, nil
	}
	if namesAnOutfit(arg) {
		fmt.Printf("Note: %q names both a path here and a registered alias; using the path.\n\n", arg)
		return "", false, nil
	}
	// A URL target is not probed here — see the matching note in
	// outfitFromEnv. A stale URL surfaces its own error when something
	// actually fetches it, rather than costing every resolution a network call.
	if !outfitsrc.IsURL(path) {
		if _, err := os.Stat(path); err != nil {
			return "", false, fmt.Errorf("alias %q points at %s, which is gone — re-point it with `outfit alias -n %s <path>`, or drop it with `outfit unalias %s`", arg, path, arg, arg)
		}
	}
	return path, true, nil
}

// applyCmd reads an Outfit file and applies it. The path defaults to ./Outfit
// when none is given, so a bare `outfit apply` works in a directory that
// holds one.
func applyCmd() *cobra.Command {
	var providers, output, harnessName string
	c := &cobra.Command{
		Use:           "apply",
		Short:         "apply an Outfit file (defaults to ./Outfit)",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			var path string
			if len(args) > 0 {
				path = args[0]
			}
			sel, outfitPath, err := readOutfit("outfit apply <file>", path)
			if err != nil {
				return err
			}
			sel.Providers = providers
			// A command-line --output/-o overrides the Outfit's OUTPUT instruction.
			if output != "" {
				sel.Output = output
			}
			return applySelection(sel, h, outfitPath, opencode.EnvResolver(envFileDir(outfitPath)))
		},
	}
	fs := c.Flags()
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&output, "output", "o", "", "max output tokens (overrides the Outfit's OUTPUT)")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to configure")
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "providers", compFiles)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// unapplyCmd reads an Outfit file and removes what it selects — the inverse of
// apply, as remove is to add. The path defaults to ./Outfit when none is given,
// so a bare `outfit unapply` works in a directory that holds one.
func unapplyCmd() *cobra.Command {
	var providers, harnessName string
	c := &cobra.Command{
		Use:           "unapply",
		Short:         "remove what an Outfit file selects",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			var path string
			if len(args) > 0 {
				path = args[0]
			}
			sel, outfitPath, err := readOutfit("outfit unapply <file>", path)
			if err != nil {
				return err
			}
			sel.Providers = providers
			return removeSelection(sel, h, outfitPath)
		},
	}
	fs := c.Flags()
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to configure")
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "providers", compFiles)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// cmdAlias registers an Outfit under a short name, so it can be used anywhere a
// path goes: `outfit apply <name>`, `outfit serve <name>`, `outfit harness
// <name>`. The name defaults to the Outfit's own ALIAS instruction.
//
// Note the two senses of "alias", which are related but not the same thing: the
// ALIAS keyword inside an Outfit names the model to the harness (and to
// llama-server under `serve`), while an alias in this registry names the Outfit
// file to outfit. Taking one from the other is a convenience, not an identity —
// --name/-n decouples them.
func aliasCmd() *cobra.Command {
	var name string
	var force, list bool
	c := &cobra.Command{
		Use:           "alias",
		Short:         "register an Outfit under a short name",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runAlias(args, name, force, list)
		},
	}
	fs := c.Flags()
	fs.StringVarP(&name, "name", "n", "", "register under this name instead of the Outfit's ALIAS")
	fs.BoolVarP(&force, "force", "F", false, "re-point a name that is already registered")
	fs.BoolVarP(&list, "list", "l", false, "list the registered aliases")
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	return c
}

// runAlias is the body of `outfit alias`.
func runAlias(args []string, name string, force, list bool) error {
	if list {
		var b strings.Builder
		if err := writeAliases(&b, true); err != nil {
			return err
		}
		fmt.Print(b.String())
		return nil
	}

	// Naming the default explicitly rather than leaving it to readOutfit is what
	// keeps OUTFIT_ALIAS out of this command: a bare `outfit alias` means the
	// Outfit in this directory, and honouring the variable could only
	// re-register what is already registered.
	arg := outfit.DefaultFile
	if len(args) > 0 {
		arg = args[0]
	}
	// Parse the Outfit even when --name is given: registering a file that does
	// not parse is a mistake worth catching now, not days later under `serve`.
	sel, path, err := readOutfit("outfit alias [path]", arg)
	if err != nil {
		return err
	}
	if name == "" {
		name = sel.Alias
	}
	if name == "" {
		return fmt.Errorf("%s has no ALIAS to name it by — pass one with --name/-n (outfit alias -n <name> [path])", path)
	}
	if err := config.ValidAliasName(name); err != nil {
		return err
	}
	// Store an absolute path so the alias resolves from any working directory,
	// and the Outfit file rather than its directory so a relative PRESET still
	// resolves against the Outfit's own directory under `serve`. A URL is
	// already absolute in the sense that matters — it resolves the same from
	// any working directory — so it is stored verbatim.
	abs := path
	if !outfitsrc.IsURL(path) {
		var err error
		abs, err = filepath.Abs(path)
		if err != nil {
			return err
		}
	}

	var previous string
	if err := config.Update(func(f *config.File) error {
		if existing, ok := f.Alias(name); ok {
			if existing == abs {
				previous = existing
				return nil
			}
			if !force {
				return fmt.Errorf("alias %q already points at %s; use --force to re-point it", name, existing)
			}
			previous = existing
		}
		f.SetAlias(name, abs)
		return nil
	}); err != nil {
		return err
	}

	switch {
	case previous == abs:
		fmt.Printf("Alias %q already points here (%s).\n", name, abs)
	case previous != "":
		fmt.Printf("Re-pointed alias %q to %s (was %s).\n", name, abs, previous)
	default:
		configPath, _ := config.Path()
		fmt.Printf("Added alias %q for %s (stored in %s).\n\n", name, abs, configPath)
		fmt.Println("Use it anywhere an Outfit path goes:")
		fmt.Printf("  outfit apply %s\n", name)
		fmt.Printf("  outfit serve %s\n", name)
		fmt.Printf("  outfit harness %s\n", name)
	}
	return nil
}

// unaliasCmd drops a registered alias. The Outfit it pointed at is untouched —
// only the name goes away.
func unaliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "unalias",
		Short:             "drop a registered name",
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasOnlySlot,
		RunE: func(c *cobra.Command, rest []string) error {
			resolve(c)
			return runUnalias(rest)
		},
	}
}

// runUnalias is the body of `outfit unalias`.
func runUnalias(rest []string) error {
	switch {
	case len(rest) == 0:
		return fmt.Errorf("unalias needs an alias name (see `outfit alias --list`)")
	case len(rest) > 1:
		return fmt.Errorf("unalias takes a single alias name, got %d", len(rest))
	}
	name := rest[0]

	var previous string
	if err := config.Update(func(f *config.File) error {
		path, ok := f.Alias(name)
		if !ok {
			return fmt.Errorf("unknown alias %q (see `outfit alias --list`)", name)
		}
		previous = path
		f.RemoveAlias(name)
		return nil
	}); err != nil {
		return err
	}

	fmt.Printf("Removed alias %q (was %s).\n", name, previous)
	return nil
}

// writeAliases renders the alias registry into b: every registered name with
// the Outfit it points at, marking any whose file has since gone. It is shared
// by `outfit alias --list` and `outfit show`. header controls the heading and
// the empty-state line, which `show` leaves out — there it is one section among
// several, and an empty registry is not worth a paragraph.
func writeAliases(b *strings.Builder, header bool) error {
	f, err := config.Load()
	if err != nil {
		return err
	}
	names := f.AliasNames()
	if len(names) == 0 {
		if header {
			b.WriteString("No aliases registered. Add one with `outfit alias` in a directory holding an Outfit.\n")
		}
		return nil
	}

	width := 0
	for _, name := range names {
		if len(name) > width {
			width = len(name)
		}
	}
	if header {
		configPath, _ := config.Path()
		fmt.Fprintf(b, "Aliases (from %s):\n\n", configPath)
	} else {
		b.WriteString("\nAliases:\n")
	}
	for _, name := range names {
		path, _ := f.Alias(name)
		line := fmt.Sprintf("  %-*s  %s", width, name, path)
		// A URL target is printed as-is: listing makes no network call, so
		// its liveness is never checked either way, only a local target's is.
		if !outfitsrc.IsURL(path) {
			if _, err := os.Stat(path); err != nil {
				line += " (missing)"
			}
		}
		b.WriteString(line + "\n")
	}
	return nil
}

// exportCmd reconstructs an Outfit from the active harness's config and prints
// it to stdout, so an existing setup can be captured (outfit export > Outfit).
func exportCmd() *cobra.Command {
	var provider, providers, harnessName string
	c := &cobra.Command{
		Use:           "export",
		Short:         "print the harness's config as an Outfit",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runExport(provider, providers, harnessName)
		},
	}
	fs := c.Flags()
	fs.StringVarP(&provider, "provider", "p", "", "provider to export")
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to read")
	fs.SetInterspersed(false)
	c.ValidArgsFunction = noPositionals
	compRegister(c, "provider", compProviders)
	compRegister(c, "providers", compFiles)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// runExport is the body of `outfit export`.
func runExport(provider, providers, harnessName string) error {
	h, _, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}
	configFile, err := h.ConfigPath()
	if err != nil {
		return err
	}
	states, defaultModel, err := h.State()
	if err != nil {
		return err
	}
	if len(states) == 0 {
		return fmt.Errorf("no providers configured in %s", configFile)
	}

	names := make([]string, 0, len(states))
	for n := range states {
		names = append(names, n)
	}
	sort.Strings(names)

	// Pick which provider to export: the flag, else the default model's
	// provider, else the sole configured provider.
	if provider == "" && len(names) == 1 {
		provider = names[0]
	}
	if provider == "" && defaultModel != "" {
		provider = strings.SplitN(defaultModel, "/", 2)[0]
	}
	if provider == "" {
		return fmt.Errorf("multiple providers configured; choose one with -p (have: %s)", strings.Join(names, ", "))
	}
	st, ok := states[provider]
	if !ok {
		return fmt.Errorf("provider %q is not configured in %s (have: %s)", provider, configFile, strings.Join(names, ", "))
	}

	sel := outfit.Selection{Provider: provider, BaseURL: st.BaseURL}
	if prefix := provider + "/"; strings.HasPrefix(defaultModel, prefix) {
		sel.Model = strings.TrimPrefix(defaultModel, prefix)
	}

	cat, catErr := catalog.LoadFrom(catalog.ResolveCatalogPath(providers))

	// Drop a baseURL that only restates the catalogue's default — keep it only
	// when it is a genuine override worth recording.
	if catErr == nil {
		if p, ok := cat.Providers[provider]; ok {
			if def, _ := p.Options["baseURL"].(string); sel.BaseURL == def {
				sel.BaseURL = ""
			}
		}
	}

	// Ensure the Outfit still selects a model when the default model did not
	// name one for this provider.
	if sel.Model == "" && len(st.ModelKeys) > 0 {
		sel.Model = st.ModelKeys[0]
	}

	// Reconstruct the context and output limits when the exported models agree
	// on a single value for each.
	sel.Context = exportLimit(sel, st, st.Contexts)
	sel.Output = exportLimit(sel, st, st.Outputs)

	fmt.Print(outfit.Format(sel))
	return nil
}

// exportLimit returns a per-model limit (limit.context or limit.output,
// depending on the values map passed) to record for an export, as a token count
// string, when the models the Outfit selects all share a single value. It
// returns "" when none was set or the models disagree (e.g. a config hand-edited
// to differ), so export never invents or guesses a value.
func exportLimit(sel outfit.Selection, st harness.ProviderState, values map[string]int) string {
	var keys []string
	if sel.Model != "" {
		keys = []string{sel.Model}
	}
	distinct := map[int]bool{}
	for _, k := range keys {
		if c, ok := values[k]; ok {
			distinct[c] = true
		}
	}
	if len(distinct) != 1 {
		return ""
	}
	for c := range distinct {
		return strconv.Itoa(c)
	}
	return ""
}

// removeSelection removes a single provider selection from the active harness's
// config. It is the shared core of `remove` and `unapply`: both resolve a
// selection (from flags or an Outfit file) and hand it here. It is the inverse
// of applySelection, so it names the provider the same way — for a remote Outfit
// that is the environment name, not the PROVIDER value — to remove exactly what
// apply wrote. outfitPath is the Outfit's own path, needed to read a path-form
// REMOTE's environment; it is empty for a flag-based remove.
func removeSelection(sel outfit.Selection, h harness.Harness, outfitPath string) error {
	if sel.Remote != "" {
		env, err := remoteEnvName(sel.Remote, outfitPath)
		if err != nil {
			return err
		}
		if env != "" {
			sel.Provider = env
		}
	}

	// Resolve the model keys to remove. With no model or alias, the whole
	// provider is removed.
	var modelKeys []string
	if sel.Alias != "" {
		modelKeys = append(modelKeys, sel.Alias)
	}
	if sel.Model != "" {
		modelKeys = append(modelKeys, sel.Model)
	}

	configFile, err := h.ConfigPath()
	if err != nil {
		return err
	}
	removed, err := h.Remove(sel.Provider, modelKeys)
	if err != nil {
		return err
	}

	if removed == 0 {
		fmt.Printf("Nothing to remove for provider %q in %s.\n", sel.Provider, configFile)
		return nil
	}
	fmt.Printf("Updated %s\n\n", configFile)
	if len(modelKeys) == 0 {
		fmt.Printf("Removed provider %q.\n", sel.Provider)
	} else {
		fmt.Printf("Removed %d model(s) from provider %q.\n", removed, sel.Provider)
	}
	return nil
}

// outfitPathFlag is the harness command's --outfit/-O flag: the Outfit to apply
// before launching, whose value is optional. Given bare it means the default
// Outfit, exactly as a bare `outfit apply` does; --outfit=<path> names one.
// The value has to be attached because everything positional after the flags
// belongs to the harness.
type outfitPathFlag struct {
	set  bool
	path string
}

func (f *outfitPathFlag) String() string { return f.path }

func (f *outfitPathFlag) Type() string { return "string" }

// Set records the flag. pflag passes the flag's NoOptDefVal for the valueless
// form ("true"); that stands for the default Outfit, which is the empty path
// readOutfit resolves as OUTFIT_ALIAS > ./Outfit.
func (f *outfitPathFlag) Set(v string) error {
	f.set = true
	if v == "true" {
		v = ""
	}
	f.path = v
	return nil
}

// cmdHarness is the seam the suite calls the harness command through, the
// way the tree does: fresh command, the body's parse, the launch.
func cmdHarness(args []string) error {
	return execCmd(harnessCmd(), args)
}

// harnessEnv is the environment for the agent outfit launches: this process's,
// plus any provider API key outfit can resolve that the environment does not
// already carry. Neither harness stores the secret itself — opencode
// substitutes {env:VAR} and Pi resolves $VAR, both when they run — so without
// this a key kept only in outfit's .env would never reach the agent, and the
// user would have to export it by hand.
//
// A variable already set in the environment is left alone, so an explicit
// export always wins. A catalogue that cannot be loaded is not fatal: launching
// the agent matters more than the keys, and it will report its own auth error.
//
// remoteResp carries the live API key and base URL from a running remote
// endpoint. When present, OPENAI_API_KEY and OPENAI_BASE_URL are injected so
// the harness can reach the remote without the user exporting them manually.
func harnessEnv(providersPath string, resolve func(string) string, remoteResp *remote.Response) []string {
	env := os.Environ()
	if remoteResp != nil {
		if os.Getenv("OPENAI_API_KEY") == "" {
			env = append(env, "OPENAI_API_KEY="+remoteResp.APIKey)
		}
		if os.Getenv("OPENAI_BASE_URL") == "" {
			env = append(env, "OPENAI_BASE_URL="+remoteResp.BaseURL)
		}
	}
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(providersPath))
	if err != nil {
		return env
	}
	seen := map[string]bool{}
	for _, name := range cat.SortedProviderNames() {
		key := cat.Providers[name].APIKeyEnv
		if key == "" || seen[key] || os.Getenv(key) != "" {
			continue
		}
		seen[key] = true
		if value := resolve(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// lucinateLaunchKey resolves the API key to inject as LUCINATE_OPENAI_API_KEY
// for the lucinate harness. The active provider is the worn Outfit's when one is
// worn, otherwise the provider behind lucinate's default connection — the model
// lucinate will boot into. It prefers a value already exported, falling back to
// what resolve (the .env) can supply. Returns false when there is no active
// provider, it has no key variable, or nothing resolves.
func lucinateLaunchKey(providersPath string, resolve func(string) string, sel outfit.Selection, worn bool) (string, bool) {
	providerID := ""
	if worn {
		providerID = sel.Provider
	}
	if providerID == "" {
		if id, ok := lucinate.DefaultProviderID(); ok {
			providerID = id
		}
	}
	if providerID == "" {
		return "", false
	}
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(providersPath))
	if err != nil {
		return "", false
	}
	p := cat.Providers[providerID]
	if p == nil || p.APIKeyEnv == "" {
		return "", false
	}
	if v := os.Getenv(p.APIKeyEnv); v != "" {
		return v, true
	}
	if v := resolve(p.APIKeyEnv); v != "" {
		return v, true
	}
	return "", false
}

// setEnvIfBlank sets key in a child environment unless it already holds a
// non-empty value. It differs from setEnvIfAbsent in treating an exported but
// empty variable as unset — for an address or a key, "" is not a deliberate
// choice worth preserving, it is a gap, and this is the rule harnessEnv already
// applies to the remote endpoint's values.
func setEnvIfBlank(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		if kv != prefix {
			return env // set to something; leave it alone
		}
		env[i] = prefix + value
		return env
	}
	return append(env, prefix+value)
}

// setEnvIfAbsent appends key=value to a child environment unless the key is
// already present, so an explicit setting is never overridden.
func setEnvIfAbsent(env []string, key, value string) []string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return env
		}
	}
	return append(env, prefix+value)
}

// overlayLocalEnv layers the worn Outfit's local environment onto the agent's
// env: the whole `.env` beside the Outfit fills variables the base does not
// already carry, then the Outfit's ENV instructions override whatever is there.
// dir is the Outfit's directory. base already holds outfit's process
// environment and any provider key it resolved, so the `.env` only fills genuine
// gaps and ENV alone can override an exported variable — the precedence is
// ENV > process environment > `.env`, the same rule the remote commands follow.
// A `.env` that cannot be read is not fatal; the agent launches without it.
func overlayLocalEnv(base []string, sel outfit.Selection, dir string) []string {
	out := append([]string(nil), base...)
	at := map[string]int{}
	for i, kv := range out {
		if k, _, ok := strings.Cut(kv, "="); ok {
			at[k] = i
		}
	}
	set := func(key, value string) {
		if i, ok := at[key]; ok {
			out[i] = key + "=" + value
			return
		}
		at[key] = len(out)
		out = append(out, key+"="+value)
	}

	// The .env only fills gaps, so a variable already in base (process
	// environment or a resolved provider key) is left untouched.
	if vars, err := opencode.ParseEnvFile(filepath.Join(dir, ".env")); err == nil {
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := at[k]; !ok {
				set(k, vars[k])
			}
		}
	}

	// ENV overrides everything, even an exported variable.
	for _, e := range sel.Env {
		set(e.Key, e.Value)
	}
	return out
}

// flagsTerminated reports whether flag parsing stopped at an explicit bare
// `--`. The flag package consumes that terminator without reporting it, so the
// only reliable trace is the last argument it swallowed — scanning args for a
// `--` before the first non-flag token would misread a detached flag value
// (`outfit harness -H pi -- run`).
func flagsTerminated(args, rest []string) bool {
	n := len(args) - len(rest)
	return n > 0 && args[n-1] == "--"
}

// namesAnOutfitOrAlias reports whether arg is a way of naming an Outfit: a path
// to one (or a directory holding one), or a registered alias. It is the
// predicate for taking `harness`'s first positional argument as an Outfit
// rather than forwarding it to the harness.
//
// A config that cannot be read is treated as "no aliases" rather than an error:
// the argument is then forwarded and the harness still launches. Someone who
// meant an alias gets the real parse error from `outfit apply <name>`, where it
// is actionable.
func namesAnOutfitOrAlias(arg string) bool {
	if namesAnOutfit(arg) {
		return true
	}
	if !config.NameShaped(arg) {
		return false
	}
	f, err := config.Load()
	if err != nil {
		return false
	}
	_, ok := f.Alias(arg)
	return ok
}

// applyBeforeLaunch applies the Outfit named by --outfit/-O to the harness that
// is about to be launched — exactly the work `outfit apply` does, so one command
// can dress the harness and then run it. rest is what will be forwarded to the
// harness, inspected only to catch a path that was meant for the flag.
// It returns the applied Outfit's directory and selection, so the launched
// agent can be given the same keys the apply resolved, along with the remote
// endpoint's live environment when the Outfit names one.
//
// The remote key is fetched before the apply, not after, so the apply resolves
// against the environment the agent will actually run with. Fetching it
// afterwards left the apply warning that no key was set while the launch was
// about to supply one.
func applyBeforeLaunch(f outfitPathFlag, providers string, h harness.Harness, rest []string, route routeOptions) (outfit.Selection, string, *remote.Response, *fleet.Choice, error) {
	// The flag's value has to be attached, so `--outfit ./dev/Outfit` (or
	// `--outfit q3`) would otherwise apply ./Outfit and quietly hand the path
	// or alias to the harness.
	if f.path == "" && len(rest) > 0 && namesAnOutfitOrAlias(rest[0]) {
		return outfit.Selection{}, "", nil, nil, fmt.Errorf("--outfit takes its path attached: --outfit=%s (a bare --outfit applies ./%s)", rest[0], outfit.DefaultFile)
	}
	sel, path, err := readOutfit("outfit harness --outfit=<file>", f.path)
	if err != nil {
		return outfit.Selection{}, "", nil, nil, err
	}
	// As for apply, --providers overrides the catalogue the selection resolves
	// against (an Outfit never names one).
	sel.Providers = providers
	envDir := envFileDir(path)
	localResolve := opencode.EnvResolver(envDir)
	// Routing runs before the apply, and before anything is printed about
	// applying, for the reason the remote fetch does: a launch that cannot find
	// a node must leave the harness config exactly as it was.
	choice, err := routeThroughFleet(sel, path, route)
	if err != nil {
		return outfit.Selection{}, "", nil, nil, err
	}
	if choice != nil {
		// The chosen node's address is what the apply writes, in the slot a
		// REMOTE endpoint's address is written to.
		sel.BaseURL = choice.BaseURL
	}
	fmt.Printf("Applying %s\n\n", path)
	// Before the apply, so a launch that cannot authenticate stops without
	// having rewritten the harness config.
	remoteResp, err := fetchRemoteEnv(sel, path, localResolve)
	if err != nil {
		return outfit.Selection{}, "", nil, nil, err
	}
	resolve := remoteLaunchResolver(localResolve, remoteResp)
	if choice != nil && choice.APIKey != "" {
		resolve = fleetLaunchResolver(resolve, choice.APIKey)
	}
	if err := applySelection(sel, h, path, resolve); err != nil {
		return outfit.Selection{}, "", nil, nil, err
	}
	fmt.Println()
	return sel, envDir, remoteResp, choice, nil
}

// fleetLaunchResolver extends a lookup with the engine key of the node a launch
// was routed to. The apply then writes a config knowing the key will be there,
// exactly as the remote path does — the missing-key warning is left for when a
// key really is missing.
func fleetLaunchResolver(base func(string) string, key string) func(string) string {
	return func(name string) string {
		if v := base(name); v != "" {
			return v
		}
		if name == remoteAPIKeyEnv {
			return key
		}
		return ""
	}
}

// remoteEnvTimeout bounds the call that fetches a remote endpoint's key, so a
// control plane that never answers delays the launch rather than blocking it.
const remoteEnvTimeout = 30 * time.Second

// fetchRemoteEnv returns the live base URL and API key of the endpoint an
// Outfit's REMOTE names, or nil when it states no REMOTE. The endpoint is
// started with a key that only the control plane knows, so this is the one
// place it can come from; `outfit harness` puts it in the environment of the
// agent it launches.
//
// Whether a failure is fatal depends on whether the key is needed. With nothing
// else supplying one, launching is pointless — the endpoint refuses every
// request — so the command stops and says what to do about it. When the key is
// already to hand (exported, in the `.env` beside the Outfit, or set by an ENV
// instruction) the fetch was only a convenience, so it warns and carries on.
func fetchRemoteEnv(sel outfit.Selection, outfitPath string, resolve func(string) string) (*remote.Response, error) {
	if sel.Remote == "" {
		return nil, nil
	}
	// The call crosses the network, and a cold control plane is not instant.
	fmt.Fprintf(os.Stderr, "Fetching the endpoint's environment from %s...\n", sel.Remote)
	cfg, err := resolveRemoteConfigForOutfit(sel.Remote, outfitPath)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), remoteEnvTimeout)
		defer cancel()
		var resp *remote.Response
		if resp, err = remote.Env(ctx, cfg); err == nil {
			return resp, nil
		}
	}
	if localKey(sel, resolve) == "" {
		return nil, fmt.Errorf(
			"could not fetch the API key for %s: %w\n"+
				"Start the endpoint with `outfit remote start %s` if it is stopped, or export %s yourself",
			sel.Remote, err, sel.Remote, remoteAPIKeyEnv)
	}
	fmt.Fprintf(os.Stderr,
		"Warning: could not fetch the API key for %s (%v).\nCarrying on with the %s already set here.\n",
		sel.Remote, err, remoteAPIKeyEnv)
	return nil, nil
}

// localKey returns the API key the launch can supply without the control
// plane: an ENV instruction in the Outfit, which overrides everything at launch
// (see overlayLocalEnv), otherwise whatever the environment or the adjacent
// `.env` holds.
func localKey(sel outfit.Selection, resolve func(string) string) string {
	for _, e := range sel.Env {
		if e.Key == remoteAPIKeyEnv {
			return e.Value
		}
	}
	return resolve(remoteAPIKeyEnv)
}

// remoteLaunchResolver extends an environment-variable lookup with the key
// fetched from a running remote endpoint. `outfit harness` gives that key to
// the agent it launches, so an apply on the same path should resolve it too:
// the config it writes is complete, and the missing-key warning is left for
// the case where the key really is missing. resp is nil when the Outfit names
// no remote, or the fetch failed, and the lookup is then unchanged.
func remoteLaunchResolver(base func(string) string, resp *remote.Response) func(string) string {
	if resp == nil || resp.APIKey == "" {
		return base
	}
	return func(name string) string {
		if v := base(name); v != "" {
			return v
		}
		if name == remoteAPIKeyEnv {
			return resp.APIKey
		}
		return ""
	}
}

// namesAnOutfit reports whether arg points at an Outfit file, or at a directory
// holding one — the two shapes readOutfit accepts on disk. It is the shared
// "this string denotes an Outfit here" predicate: it decides whether a path
// beats a registered alias of the same name, and whether `harness` takes its
// first positional argument rather than forwarding it.
func namesAnOutfit(arg string) bool {
	info, err := os.Stat(arg)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err := os.Stat(filepath.Join(arg, outfit.DefaultFile))
		return err == nil
	}
	return filepath.Base(arg) == outfit.DefaultFile
}

// showCmd prints the providers and models currently configured in the active
// harness's config. It takes the same --harness/-H override (and the same
// flag > env > preference > default precedence) as every other command, so you
// can inspect any harness without changing the stored default. Where `list`
// shows the catalogue of what you could configure, `show` shows what is
// actually configured right now.
func showCmd() *cobra.Command {
	var harnessName string
	c := &cobra.Command{
		Use:           "show",
		Short:         "show the providers and models the harness has configured",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runShow(harnessName)
		},
	}
	c.Flags().StringVarP(&harnessName, "harness", "H", "", "which harness to read")
	c.Flags().SetInterspersed(false)
	c.ValidArgsFunction = noPositionals
	compRegister(c, "harness", compHarnessNames)
	return c
}

// runShow is the body of `outfit show`.
func runShow(harnessName string) error {
	h, source, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}
	configFile, err := h.ConfigPath()
	if err != nil {
		return err
	}
	states, defaultModel, err := h.State()
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Harness: %s (from %s)\n", h.Name(), source)
	fmt.Fprintf(&b, "Config:  %s\n", configFile)
	if defaultModel != "" {
		fmt.Fprintf(&b, "Default model: %s\n", defaultModel)
	}

	if len(states) == 0 {
		b.WriteString("\nNo providers configured. Add one with `outfit add`.\n")
		if err := writeAliases(&b, false); err != nil {
			return err
		}
		fmt.Print(b.String())
		return nil
	}

	names := make([]string, 0, len(states))
	for n := range states {
		names = append(names, n)
	}
	sort.Strings(names)

	b.WriteString("\nConfigured providers:\n")
	for _, name := range names {
		st := states[name]
		fmt.Fprintf(&b, "\n  %s\n", name)
		if st.BaseURL != "" {
			fmt.Fprintf(&b, "    base url: %s\n", st.BaseURL)
		}
		if len(st.ModelKeys) == 0 {
			b.WriteString("    (no models)\n")
			continue
		}
		for _, key := range st.ModelKeys {
			line := "    model " + key
			var limits []string
			if c, ok := st.Contexts[key]; ok {
				limits = append(limits, fmt.Sprintf("context %d", c))
			}
			if o, ok := st.Outputs[key]; ok {
				limits = append(limits, fmt.Sprintf("output %d", o))
			}
			if len(limits) > 0 {
				line += " (" + strings.Join(limits, ", ") + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	if err := writeAliases(&b, false); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

func listCmd() *cobra.Command {
	var providers string
	var showModels bool
	c := &cobra.Command{
		Use:           "list",
		Short:         "list the provider catalogue",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runList(args, providers, showModels)
		},
	}
	fs := c.Flags()
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.BoolVar(&showModels, "models", false, "also fetch each provider's current models live from its endpoint")
	fs.SetInterspersed(false)
	// An optional positional narrows the listing to one provider.
	c.ValidArgsFunction = compProviders
	compRegister(c, "providers", compFiles)
	return c
}

// runList is the body of `outfit list`.
func runList(args []string, providers string, showModels bool) error {
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(providers))
	if err != nil {
		return err
	}

	// An optional positional argument narrows the listing to one provider, which
	// is the natural way to ask for a single provider's live models.
	names := cat.SortedProviderNames()
	if len(args) > 0 {
		if _, ok := cat.Providers[args[0]]; !ok {
			return fmt.Errorf("unknown provider %q (see `outfit list`)", args[0])
		}
		names = []string{args[0]}
	}

	// Only resolve keys and hit the network when --models is asked for; a plain
	// list stays entirely offline.
	var resolve func(string) string
	if showModels {
		resolve = opencode.EnvResolver("")
	}

	var b strings.Builder
	b.WriteString("Available providers:\n")
	for _, name := range names {
		p := cat.Providers[name]
		fmt.Fprintf(&b, "\n  %s — %s\n", name, p.Description)
		if p.APIKeyEnv != "" {
			req := ""
			if p.APIKeyRequired {
				req = " (required)"
			}
			fmt.Fprintf(&b, "    api key: %s%s\n", p.APIKeyEnv, req)
		}
		harnesses := []string{"opencode"}
		if p.Pi != nil {
			harnesses = append(harnesses, "pi")
		}
		if p.Lucinate != nil {
			harnesses = append(harnesses, "lucinate")
		}
		fmt.Fprintf(&b, "    harnesses: %s\n", strings.Join(harnesses, ", "))
		if showModels {
			if models, err := discovery.Models(p, "", resolve); err == nil && len(models) > 0 {
				for _, m := range models {
					fmt.Fprintf(&b, "    model %s\n", m)
				}
			} else {
				b.WriteString("    models: (none found)\n")
			}
		}
	}
	fmt.Print(b.String())
	return nil
}

// defaultProvidersFile is the filename cmdInitProviders writes to when no path
// is given. It matches the name the embedded catalogue carries and the one
// --providers/OUTFIT_PROVIDERS are typically pointed at.
const defaultProvidersFile = "providers.yaml"

// initProvidersCmd writes the binary's embedded providers.yaml to the working
// directory (or an explicit path) as a starting point for a custom catalogue.
// It refuses to clobber an existing file unless --force is given, so a stray
// run can't destroy a catalogue the user has been editing.
func initProvidersCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:           "init-providers",
		Short:         "write the built-in providers.yaml out",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			path := defaultProvidersFile
			if len(args) > 0 {
				path = args[0]
			}
			return runInitProviders(path, force)
		},
	}
	c.Flags().BoolVarP(&force, "force", "F", false, "overwrite an existing file")
	c.Flags().SetInterspersed(false)
	c.ValidArgsFunction = fileSlot
	return c
}

// runInitProviders is the body of `outfit init-providers`.
func runInitProviders(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass a different path or use --force to overwrite", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking %s: %w", path, err)
		}
	}

	if err := os.WriteFile(path, catalog.EmbeddedYAML(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Printf("Wrote %s\n\n", path)
	fmt.Printf("Edit it, then point outfit at it:\n")
	fmt.Printf("  outfit list --providers %s\n", path)
	fmt.Printf("  OUTFIT_PROVIDERS=%s outfit list\n", path)
	return nil
}

// Test seams: the suite calls these the way the tree does — each runs its
// command through execCmd rather than parsing a private FlagSet.
func cmdAdd(args []string) error           { return execCmd(addCmd(), args) }
func cmdRemove(args []string) error        { return execCmd(removeCmd(), args) }
func cmdList(args []string) error          { return execCmd(listCmd(), args) }
func cmdShow(args []string) error          { return execCmd(showCmd(), args) }
func cmdApply(args []string) error         { return execCmd(applyCmd(), args) }
func cmdUnapply(args []string) error       { return execCmd(unapplyCmd(), args) }
func cmdAlias(args []string) error         { return execCmd(aliasCmd(), args) }
func cmdUnalias(args []string) error       { return execCmd(unaliasCmd(), args) }
func cmdExport(args []string) error        { return execCmd(exportCmd(), args) }
func cmdInitProviders(args []string) error { return execCmd(initProvidersCmd(), args) }
