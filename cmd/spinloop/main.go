// Command spinloop configures a coding agent ("harness") to use a model provider,
// by deep-merging provider settings into that harness's config. The supported
// harnesses are opencode, the Pi coding agent, and lucinate; the harness is
// chosen at runtime with --harness/-H or SPINLOOP_HARNESS, or a stored default
// set via `spinloop harness config --set`, and defaults to opencode.
// `spinloop --help`
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
// SPINLOOP_BASE_URL environment variable; the flag wins over the env var, and
// either wins over the catalogue's defaults.
//
// A Spinloop is a declarative, Dockerfile-style file describing one provider
// selection, applied with `spinloop harness apply` and reverted with `spinloop
// harness unapply`;
// see the internal/spinloop package. The harness is deliberately not part of an
// Spinloop, so the same Spinloop applies to any harness.
//
// `spinloop alias` registers a Spinloop under a short name — by default its own
// ALIAS — and every command that takes a path takes that name instead, or reads
// one from SPINLOOP_ALIAS when given no path at all. The registry is machine-local
// state in spinloop's own config, not part of any Spinloop, so a Spinloop stays as
// portable as it was.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spinloop-ai/spinloop/internal/catalog"
	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/config"
	"github.com/spinloop-ai/spinloop/internal/contextsize"
	"github.com/spinloop-ai/spinloop/internal/discovery"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/harness"
	"github.com/spinloop-ai/spinloop/internal/lucinate"
	"github.com/spinloop-ai/spinloop/internal/opencode"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
	"github.com/spinloop-ai/spinloop/internal/spinloopsrc"
)

// version is the binary's version. It defaults to "dev" and is overridden at
// build time via -ldflags "-X main.version=...", set by the Makefile and
// goreleaser.
var version = "dev"

func main() {
	// A completion attempt must never spew over the prompt. The engine's
	// error paths write straight to the process's standard error, so when
	// this process is the engine there is no standard error: whatever state
	// the machine is in, the answer is "no candidates". A bare `spinloop
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
	cloud.SetCLIVersion(version)
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
// of a Selection, so it cannot leak into a Spinloop).
func registerSelectionFlags(fs *pflag.FlagSet, s *spinloop.Selection, harnessName *string) {
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
	var s spinloop.Selection
	var harnessName string
	c := &cobra.Command{
		Use:   "add",
		Short: "deep-merge a provider into the active harness's config",
		Long: `deep-merges the provider into the active harness's config, preserving
everything else. Specify a model (or an alias). --context sets the model's
context window; --output sets the max output tokens (opencode requires it
alongside a context, defaulting to a quarter of the context).`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if s.Provider == "" {
				return fmt.Errorf("--provider/-p is required (see `spinloop provider list`)")
			}
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			return applySelection(s, h, "", "", "", opencode.EnvResolver(""))
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
	var s spinloop.Selection
	var harnessName string
	c := &cobra.Command{
		Use:   "remove",
		Short: "remove a provider, or a model, from the harness's config",
		Long: `removes the provider, or just the named model when a model/alias is
given.`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if s.Provider == "" {
				return fmt.Errorf("--provider/-p is required (see `spinloop provider list`)")
			}
			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			return removeSelection(s, h, "", "")
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

// envFileDir returns the local directory a `.env` beside spinloopPath would
// live in, for opencode.EnvResolver — or "" when spinloopPath is empty (no
// Spinloop in play) or a URL, since a URL-sourced Spinloop has no local directory
// to look beside.
func envFileDir(spinloopPath string) string {
	if spinloopPath == "" || spinloopsrc.IsURL(spinloopPath) {
		return ""
	}
	return filepath.Dir(spinloopPath)
}

// applySelection writes a single provider selection into the active harness's
// config. It is the shared core of `add` and `apply`: both resolve a selection
// (from flags or a Spinloop file) and hand it here.
// spinloopPath is the Spinloop the selection came from — its own path, not a
// pre-computed directory, so a relative PRESET resolves correctly whether the
// Spinloop is local or URL-sourced; it is empty when no Spinloop is involved
// (an `spinloop harness add` from flags). envName is the --env flag's value:
// the registered environment the selection is applied against, empty for a
// local apply. resolve looks up API key variables — normally
// opencode.EnvResolver of the Spinloop's local directory, but `spinloop
// harness` widens it with the key it fetched from a cloud endpoint, which it
// is about to put in the launched agent's environment. gatewayLabel waives the
// model-or-alias requirement below and renames the provider: non-empty only
// when the selection is routed at a gateway with no model or alias of its own,
// since a gateway resolves the model per request and needs no Spinloop to name
// one — see the rename below for what the label is used for.
func applySelection(sel spinloop.Selection, h harness.Harness, spinloopPath, envName, gatewayLabel string, resolve func(string) string) error {
	if sel.Model == "" && sel.Alias == "" && gatewayLabel == "" {
		return fmt.Errorf("a provider selection needs a model or an alias")
	}

	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(sel.Providers))
	if err != nil {
		return err
	}
	p, ok := cat.Providers[sel.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (see `spinloop provider list`)", sel.Provider)
	}

	// The catalogue provider p is resolved above by the PROVIDER value, which
	// stays the engine definition. From here on sel.Provider is the harness-facing
	// name: for an environment that is its name, so the model reads as
	// <env>/<model> and each environment keeps its own block rather than
	// several engines-of-the-same-kind overwriting one. The name comes from
	// removeSelection too, so apply and unapply stay symmetric. The flag is an
	// explicit name, so a missing registration is a mistake to report rather
	// than a config to wait for.
	var envCfg *cloud.Config
	if envName != "" {
		if !cloud.IsEnvName(envName) {
			return fmt.Errorf("%q is not an environment name: an environment name is a plain identifier, with no path", envName)
		}
		envPath, err := cloud.EnvConfigPath(envName)
		if err != nil {
			return err
		}
		if _, err := os.Stat(envPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("environment %q is not registered: run `spinloop cloud deploy --env %q` to create it", envName, envName)
			}
			return err
		}
		cfg, err := cloud.LoadEnvironment(envName, viperGetenv())
		if err != nil {
			return err
		}
		envCfg = &cfg
		sel.Provider = envName
		// The provider is now keyed on the environment; label it so it reads
		// distinctly from a local engine of the same kind in a model picker
		// (e.g. "llama.cpp (dev-2)" rather than another bare "llama.cpp").
		sel.DisplayName = catalog.CloudProviderLabel(p.Name, envName)
	} else if gatewayLabel != "" {
		// A gateway-routed selection with no model of its own carries the
		// catalogue's shared "openai-compatible" id, which every gateway a
		// fleet might name would otherwise collide under. Key it by the
		// gateway instead — mirroring the environment rename above — so a
		// second gateway gets its own block. The display name leads with
		// "Gateway" rather than the catalogue engine's own name: p.Name is
		// always the generic "OpenAI-compatible" here, which carries no
		// gateway in its text at all, so a user searching a harness's model
		// picker for "gateway" would find nothing.
		sel.Provider = gatewayProviderKey(gatewayLabel)
		sel.DisplayName = catalog.CloudProviderLabel("Gateway", gatewayLabel)
	}

	// A Spinloop applied against an environment states no BASEURL: the address
	// belongs to the deployment, which records it in the environment's
	// registered cloud.json. Take it from there — but only when the Spinloop
	// stated none, so a hand-written BASEURL still wins.
	// The harness reports the base URL it wrote, so this needs no announcement
	// of its own beyond naming where it came from.
	if sel.BaseURL == "" && envCfg != nil && envCfg.BaseURL != "" {
		fmt.Printf("Taking the base URL from the %s environment.\n", envName)
		sel.BaseURL = envCfg.BaseURL
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

	// A launch wears the selection: the harness's default model becomes the
	// one it launches into.
	summary, err := h.Apply(p, sel, contextSize, outputSize, true, resolve)
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

// readSpinloop reads and parses the Spinloop at path, defaulting to ./Spinloop when
// path is empty so a bare command works in a directory that holds one. When
// path names a directory, the default Spinloop file inside it is used, so a
// caller can pass either the file itself or the directory that holds it. path
// may also be a name registered with `spinloop alias`, which is what gives every
// Spinloop command the same shorthand, or an http(s) URL, fetched instead of
// read from local disk — a URL ending in "/" gets spinloop.DefaultFile appended,
// the URL equivalent of the directory case. usage shows the caller's own way
// of naming a path (subcommands take it positionally, `harness` takes it as a
// flag) in the not-found hint. It returns the parsed selection alongside the
// resolved path, which callers use to locate files referenced relative to the
// Spinloop — via spinloopsrc, so a relative reference resolves against a URL the
// same way it resolves against a local directory.
//
// With no path at all, SPINLOOP_ALIAS names the Spinloop before ./Spinloop is tried,
// so one export dresses a whole shell. It ranks below an explicit argument and
// above the default: the variable decides *which* Spinloop is the default, never
// whether a command acts on one. `spinloop alias` opts out by naming
// spinloop.DefaultFile itself, so registering never resolves through the
// registry it is writing to.
//
// It prints when an alias decided the path, which is against this file's
// convention that only cmdX functions report anything. The alternative is to
// repeat that reporting at all four call sites, where one omission would leave
// the user guessing which file was read. The line goes to stderr because
// `spinloop cloud env` writes shell exports to stdout for `eval`, which a stray
// prose line would break.
func readSpinloop(usage, path string) (spinloop.Selection, string, error) {
	if path == "" {
		named, aliased, err := spinloopFromEnv()
		if err != nil {
			return spinloop.Selection{}, path, err
		}
		if aliased != "" {
			fmt.Fprintf(os.Stderr, "Using %s %q (%s)\n\n", spinloopAliasEnv, named, aliased)
			path = aliased
		} else {
			path = spinloop.DefaultFile
		}
	}
	if aliased, ok, err := resolveAlias(path); err != nil {
		return spinloop.Selection{}, path, err
	} else if ok {
		fmt.Fprintf(os.Stderr, "Using alias %q (%s)\n\n", path, aliased)
		path = aliased
	}

	var data []byte
	if spinloopsrc.IsURL(path) {
		if strings.HasSuffix(path, "/") {
			path += spinloop.DefaultFile
		}
		fetched, err := spinloopsrc.Fetch(path)
		if err != nil {
			return spinloop.Selection{}, path, err
		}
		data = fetched
	} else {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			path = filepath.Join(path, spinloop.DefaultFile)
		}
		read, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && path == spinloop.DefaultFile {
				return spinloop.Selection{}, path, fmt.Errorf("no %s found in the current directory (pass a path or an alias: %s; or set %s; see `spinloop alias --list`)", spinloop.DefaultFile, usage, spinloopAliasEnv)
			}
			return spinloop.Selection{}, path, fmt.Errorf("reading %s: %w", path, err)
		}
		data = read
	}

	sel, err := spinloop.Parse(data)
	if err != nil {
		return spinloop.Selection{}, path, fmt.Errorf("%s: %w", path, err)
	}
	return sel, path, nil
}

// spinloopAliasEnv names a registered alias for every command that takes an
// Spinloop path but was given none.
const spinloopAliasEnv = "SPINLOOP_ALIAS"

// spinloopAliasInForce reports whether SPINLOOP_ALIAS will decide the Spinloop
// a valueless --spinloop wears. The variable is the user's own naming of a
// Spinloop, so for routing it counts as one named explicitly: a fleet.yaml in
// the working directory is not picked up for it.
func spinloopAliasInForce() bool {
	return cliViper.GetString("alias") != ""
}

// spinloopFromEnv resolves SPINLOOP_ALIAS, returning the name it holds alongside
// the Spinloop it points at, or two empty strings when it is unset or empty.
//
// It deliberately does not reuse resolveAlias. That function is written for an
// argument, which is usually a path: a value it cannot resolve falls through to
// path handling, and a file on disk of the same spelling wins. Neither fits a
// variable. A variable can only ever have been set to name an alias, so a
// name it cannot resolve is a mistake to report rather than a filename to try,
// and a same-named file in whichever directory the user is standing in is a
// coincidence — letting it shadow the variable would make the export work in
// some directories and not others.
func spinloopFromEnv() (string, string, error) {
	// The CLI layer's one Viper reads the variable (see cli_viper.go):
	// SPINLOOP_ALIAS has no flag spelling, so only the env and its absence.
	name := cliViper.GetString("alias")
	if name == "" {
		return "", "", nil
	}
	if err := config.ValidAliasName(name); err != nil {
		return "", "", fmt.Errorf("%s: %w", spinloopAliasEnv, err)
	}
	f, err := config.Load()
	if err != nil {
		return "", "", err
	}
	path, ok := f.Alias(name)
	if !ok {
		return "", "", fmt.Errorf("%s names %q, which is not a registered alias — see `spinloop alias --list`, or unset %s", spinloopAliasEnv, name, spinloopAliasEnv)
	}
	// A URL target is not probed here: that would mean a network call on
	// every command that resolves no explicit path, just to confirm what a
	// real fetch will report anyway. A stale URL surfaces its own error when
	// something actually reads it.
	if !spinloopsrc.IsURL(path) {
		if _, err := os.Stat(path); err != nil {
			return "", "", fmt.Errorf("%s names %q, which points at %s, which is gone — re-point it with `spinloop alias -n %s <path>`, or drop it with `spinloop unalias %s`", spinloopAliasEnv, name, path, name, name)
		}
	}
	return name, path, nil
}

// resolveAlias looks arg up in the alias registry, returning the Spinloop it
// names. It reports ok=false — leaving the caller to treat arg as a path —
// when arg is not a registered name, or when it also names something on disk.
//
// The name-shaped guard is not an optimisation: it means a path-shaped argument
// never causes a config read at all, so the commands that have nothing to do
// with spinloop's own config (serve, most of all) keep working the same way when
// that config is absent, unreadable, or someone else's.
//
// A path on disk beats a registered alias, which is the opposite of how shell
// aliases work and deliberate: every existing invocation passes a path, so
// registering an alias must never change what an already-working command does.
// That rule is about arguments only — SPINLOOP_ALIAS is resolved by
// spinloopFromEnv, which says why it is not shadowed the same way.
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
	if namesAnSpinloop(arg) {
		fmt.Printf("Note: %q names both a path here and a registered alias; using the path.\n\n", arg)
		return "", false, nil
	}
	// A URL target is not probed here — see the matching note in
	// spinloopFromEnv. A stale URL surfaces its own error when something
	// actually fetches it, rather than costing every resolution a network call.
	if !spinloopsrc.IsURL(path) {
		if _, err := os.Stat(path); err != nil {
			return "", false, fmt.Errorf("alias %q points at %s, which is gone — re-point it with `spinloop alias -n %s <path>`, or drop it with `spinloop unalias %s`", arg, path, arg, arg)
		}
	}
	return path, true, nil
}

// applyCmd reads a Spinloop file and applies it. The path defaults to
// ./Spinloop when none is given, so a bare `spinloop harness apply` works in a
// directory that holds one.
func applyCmd() *cobra.Command {
	var providers, output, harnessName, envName string
	c := &cobra.Command{
		Use:   "apply",
		Short: "apply a Spinloop file (defaults to ./Spinloop)",
		Long: `applies a Spinloop file — a declarative, Dockerfile-style description of
one provider selection — as if you had run the equivalent add. With --env,
applies it against a registered cloud environment: the provider is keyed on
the environment's name and, absent a BASEURL, takes its address from the
environment's registered cloud.json.`,
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
			sel, spinloopPath, err := readSpinloop("spinloop harness apply <file>", path)
			if err != nil {
				return err
			}
			sel.Providers = providers
			// A command-line --output/-o overrides the Spinloop's OUTPUT instruction.
			if output != "" {
				sel.Output = output
			}
			return applySelection(sel, h, spinloopPath, envName, "", opencode.EnvResolver(envFileDir(spinloopPath)))
		},
	}
	fs := c.Flags()
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&output, "output", "o", "", "max output tokens (overrides the Spinloop's OUTPUT)")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to configure")
	fs.StringVarP(&envName, "env", "e", "", "apply against this registered environment (its name keys the provider; its cloud.json supplies the base URL)")
	compRegister(c, "env", compEnvs)
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "providers", compFiles)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// unapplyCmd reads a Spinloop file and removes what it selects — the inverse
// of apply, as remove is to add. The path defaults to ./Spinloop when none is
// given, so a bare `spinloop harness unapply` works in a directory that holds
// one.
func unapplyCmd() *cobra.Command {
	var providers, harnessName, envName string
	c := &cobra.Command{
		Use:   "unapply",
		Short: "remove what a Spinloop file selects",
		Long: `removes what a Spinloop file selects, as if you had run the equivalent
remove. The inverse of apply: --env must name the same environment apply
applied the Spinloop against, or there is nothing to remove.`,
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
			sel, spinloopPath, err := readSpinloop("spinloop harness unapply <file>", path)
			if err != nil {
				return err
			}
			sel.Providers = providers
			return removeSelection(sel, h, spinloopPath, envName)
		},
	}
	fs := c.Flags()
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to configure")
	fs.StringVarP(&envName, "env", "e", "", "the registered environment the Spinloop was applied against")
	compRegister(c, "env", compEnvs)
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	compRegister(c, "providers", compFiles)
	compRegister(c, "harness", compHarnessNames)
	return c
}

// cmdAlias registers a Spinloop under a short name, so it can be used anywhere
// a path goes: `spinloop harness apply <name>`, `spinloop serve <name>`,
// `spinloop harness <name>`. The name defaults to the Spinloop's own ALIAS
// instruction.
//
// Note the two senses of "alias", which are related but not the same thing: the
// ALIAS keyword inside a Spinloop names the model to the harness (and to
// llama-server under `serve`), while an alias in this registry names the Spinloop
// file to spinloop. Taking one from the other is a convenience, not an identity —
// --name/-n decouples them.
func aliasCmd() *cobra.Command {
	var name string
	var force, list bool
	c := &cobra.Command{
		Use:   "alias",
		Short: "register a Spinloop under a short name",
		Long: `registers a Spinloop under a short name, which then stands in wherever a
Spinloop path goes. The path may be a local file or directory, or an http(s)
URL: a URL is fetched and parsed when the alias is registered, so a broken one
is caught now, and is stored as-is. The name defaults to the Spinloop's own
ALIAS; --name/-n picks another, --force/-F re-points a name already
registered, and --list/-l shows them all. A path on disk always wins over a
name, so registering one changes nothing that works. Set SPINLOOP_ALIAS to a
registered name and every command given no Spinloop uses it, before ./Spinloop
is tried; an argument still wins.`,
		Example: `  # register the Spinloop in this directory under its own ALIAS
  spinloop alias

  # register a Spinloop that is fetched from a URL
  spinloop alias -n team-default https://example.com/team/Spinloop`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runAlias(args, name, force, list)
		},
	}
	fs := c.Flags()
	fs.StringVarP(&name, "name", "n", "", "register under this name instead of the Spinloop's ALIAS")
	fs.BoolVarP(&force, "force", "F", false, "re-point a name that is already registered")
	fs.BoolVarP(&list, "list", "l", false, "list the registered aliases")
	fs.SetInterspersed(false)
	c.ValidArgsFunction = aliasSlot
	return c
}

// runAlias is the body of `spinloop alias`.
func runAlias(args []string, name string, force, list bool) error {
	if list {
		var b strings.Builder
		if err := writeAliases(&b, true); err != nil {
			return err
		}
		fmt.Print(b.String())
		return nil
	}

	// Naming the default explicitly rather than leaving it to readSpinloop is what
	// keeps SPINLOOP_ALIAS out of this command: a bare `spinloop alias` means the
	// Spinloop in this directory, and honouring the variable could only
	// re-register what is already registered.
	arg := spinloop.DefaultFile
	if len(args) > 0 {
		arg = args[0]
	}
	// Parse the Spinloop even when --name is given: registering a file that does
	// not parse is a mistake worth catching now, not days later under `serve`.
	sel, path, err := readSpinloop("spinloop alias [path]", arg)
	if err != nil {
		return err
	}
	if name == "" {
		name = sel.Alias
	}
	if name == "" {
		return fmt.Errorf("%s has no ALIAS to name it by — pass one with --name/-n (spinloop alias -n <name> [path])", path)
	}
	if err := config.ValidAliasName(name); err != nil {
		return err
	}
	// Store an absolute path so the alias resolves from any working directory,
	// and the Spinloop file rather than its directory so a relative PRESET still
	// resolves against the Spinloop's own directory under `serve`. A URL is
	// already absolute in the sense that matters — it resolves the same from
	// any working directory — so it is stored verbatim.
	abs := path
	if !spinloopsrc.IsURL(path) {
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
		fmt.Println("Use it anywhere a Spinloop path goes:")
		fmt.Printf("  spinloop harness apply %s\n", name)
		fmt.Printf("  spinloop serve %s\n", name)
		fmt.Printf("  spinloop harness open %s\n", name)
	}
	return nil
}

// unaliasCmd drops a registered alias. The Spinloop it pointed at is untouched —
// only the name goes away.
func unaliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "unalias",
		Short:             "drop a registered name",
		Long:              `drops a registered name. The Spinloop file itself is left alone.`,
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

// runUnalias is the body of `spinloop unalias`.
func runUnalias(rest []string) error {
	switch {
	case len(rest) == 0:
		return fmt.Errorf("unalias needs an alias name (see `spinloop alias --list`)")
	case len(rest) > 1:
		return fmt.Errorf("unalias takes a single alias name, got %d", len(rest))
	}
	name := rest[0]

	var previous string
	if err := config.Update(func(f *config.File) error {
		path, ok := f.Alias(name)
		if !ok {
			return fmt.Errorf("unknown alias %q (see `spinloop alias --list`)", name)
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
// the Spinloop it points at, marking any whose file has since gone. It is
// shared by `spinloop alias --list` and `spinloop harness show`. header
// controls the heading and the empty-state line, which `show` leaves out —
// there it is one section among
// several, and an empty registry is not worth a paragraph.
func writeAliases(b *strings.Builder, header bool) error {
	f, err := config.Load()
	if err != nil {
		return err
	}
	names := f.AliasNames()
	if len(names) == 0 {
		if header {
			b.WriteString("No aliases registered. Add one with `spinloop alias` in a directory holding a Spinloop.\n")
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
		if !spinloopsrc.IsURL(path) {
			if _, err := os.Stat(path); err != nil {
				line += " (missing)"
			}
		}
		b.WriteString(line + "\n")
	}
	return nil
}

// exportCmd reconstructs a Spinloop from the active harness's config and
// prints it to stdout, so an existing setup can be captured (spinloop harness
// export > Spinloop).
func exportCmd() *cobra.Command {
	var provider, providers, harnessName string
	c := &cobra.Command{
		Use:           "export",
		Short:         "print the harness's config as a Spinloop",
		Long:          `prints the active harness's config as a Spinloop (spinloop harness export > Spinloop).`,
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

// runExport is the body of `spinloop harness export`.
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

	sel := spinloop.Selection{Provider: provider, BaseURL: st.BaseURL}
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

	// Ensure the Spinloop still selects a model when the default model did not
	// name one for this provider.
	if sel.Model == "" && len(st.ModelKeys) > 0 {
		sel.Model = st.ModelKeys[0]
	}

	// Reconstruct the context and output limits when the exported models agree
	// on a single value for each.
	sel.Context = exportLimit(sel, st, st.Contexts)
	sel.Output = exportLimit(sel, st, st.Outputs)

	fmt.Print(spinloop.Format(sel))
	return nil
}

// exportLimit returns a per-model limit (limit.context or limit.output,
// depending on the values map passed) to record for an export, as a token count
// string, when the models the Spinloop selects all share a single value. It
// returns "" when none was set or the models disagree (e.g. a config hand-edited
// to differ), so export never invents or guesses a value.
func exportLimit(sel spinloop.Selection, st harness.ProviderState, values map[string]int) string {
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
// selection (from flags or a Spinloop file) and hand it here. It is the inverse
// of applySelection, so it names the provider the same way — for an
// environment that is its name, not the PROVIDER value — to remove exactly what
// apply wrote. envName is the --env flag's value, empty when the selection is
// local; it must match the --env apply wrote the block under, or the removal
// finds nothing and says so.
func removeSelection(sel spinloop.Selection, h harness.Harness, spinloopPath, envName string) error {
	if envName != "" {
		if !cloud.IsEnvName(envName) {
			return fmt.Errorf("%q is not an environment name: an environment name is a plain identifier, with no path", envName)
		}
		sel.Provider = envName
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

// spinloopPathFlag is the harness command's --spinloop/-O flag: the Spinloop
// to apply before launching, whose value is optional. Given bare it means the
// default Spinloop, exactly as a bare `spinloop harness apply` does;
// --spinloop=<path> names one.
// The value has to be attached because everything positional after the flags
// belongs to the harness.
type spinloopPathFlag struct {
	set  bool
	path string
}

func (f *spinloopPathFlag) String() string { return f.path }

func (f *spinloopPathFlag) Type() string { return "string" }

// Set records the flag. pflag passes the flag's NoOptDefVal for the valueless
// form ("true"); that stands for the default Spinloop, which is the empty path
// readSpinloop resolves as SPINLOOP_ALIAS > ./Spinloop.
func (f *spinloopPathFlag) Set(v string) error {
	f.set = true
	if v == "true" {
		v = ""
	}
	f.path = v
	return nil
}

// cmdOpen is the seam the suite calls the launch (harness open) through, the
// way the tree does: fresh command, the body's parse, the launch.
func cmdOpen(args []string) error {
	return execCmd(openCmd(), args)
}

// cmdConfig is the seam the suite calls the harness's config command through:
// report (--get, the default) or store (--set) the default harness.
func cmdConfig(args []string) error {
	return execCmd(configCmd(), args)
}

// harnessEnv is the environment for the agent spinloop launches: this process's,
// plus any provider API key spinloop can resolve that the environment does not
// already carry. Neither harness stores the secret itself — opencode
// substitutes {env:VAR} and Pi resolves $VAR, both when they run — so without
// this a key kept only in spinloop's .env would never reach the agent, and the
// user would have to export it by hand.
//
// A variable already set in the environment is left alone, so an explicit
// export always wins. A catalogue that cannot be loaded is not fatal: launching
// the agent matters more than the keys, and it will report its own auth error.
//
// cloudResp carries the live API key and base URL from a running cloud
// endpoint. When present, OPENAI_API_KEY and OPENAI_BASE_URL are injected so
// the harness can reach the cloud without the user exporting them manually.
func harnessEnv(providersPath string, resolve func(string) string, cloudResp *cloud.Response) []string {
	env := os.Environ()
	if cloudResp != nil {
		if os.Getenv("OPENAI_API_KEY") == "" {
			env = append(env, "OPENAI_API_KEY="+cloudResp.APIKey)
		}
		if os.Getenv("OPENAI_BASE_URL") == "" {
			env = append(env, "OPENAI_BASE_URL="+cloudResp.BaseURL)
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
// for the lucinate harness. The active provider is the worn Spinloop's when one is
// worn, otherwise the provider behind lucinate's default connection — the model
// lucinate will boot into. It prefers a value already exported, falling back to
// what resolve (the .env) can supply. Returns false when there is no active
// provider, it has no key variable, or nothing resolves.
func lucinateLaunchKey(providersPath string, resolve func(string) string, sel spinloop.Selection, worn bool) (string, bool) {
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
// applies to the cloud endpoint's values.
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

// overlayLocalEnv layers the worn Spinloop's local environment onto the agent's
// env: the whole `.env` beside the Spinloop fills variables the base does not
// already carry, then the Spinloop's ENV instructions override whatever is there.
// dir is the Spinloop's directory. base already holds spinloop's process
// environment and any provider key it resolved, so the `.env` only fills genuine
// gaps and ENV alone can override an exported variable — the precedence is
// ENV > process environment > `.env`, the same rule the cloud commands follow.
// A `.env` that cannot be read is not fatal; the agent launches without it.
func overlayLocalEnv(base []string, sel spinloop.Selection, dir string) []string {
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

// splitHarnessArgs scans args for `harness`'s own flags and, at most once, a
// leading positional naming a Spinloop, wherever either appears among the
// arguments meant for spinloop itself; everything from the first argument
// that is neither forwards to the harness untouched. This lets --env (or any
// other of the command's flags) sit before or after the Spinloop name —
// `spinloop harness dev-3 --env prod ...` and `spinloop harness --env prod
// dev-3 ...` parse alike — which fs.Parse cannot do on its own: with
// interspersed flags disabled it stops at the first positional, and with them
// enabled it errors out on the first argument meant for the harness that
// happens to look like a flag (e.g. --prompt).
//
// An explicit `--` stops the scan and is itself dropped, whether it is the
// very first argument (opting out of Spinloop-naming entirely) or follows
// some of spinloop's own flags or a consumed Spinloop name — the harness gets
// everything after it, verbatim. Once a Spinloop name has been consumed
// (spinloopPath.set), or once one is offered that does not name a Spinloop or
// a registered alias, the scan stops there and that argument is the first one
// forwarded — it is never silently dropped.
func splitHarnessArgs(fs *pflag.FlagSet, spinloopPath *spinloopPathFlag, args []string) ([]string, error) {
	i := 0
	for i < len(args) {
		tok := args[i]
		if tok == "--" {
			return args[i+1:], nil
		}
		if len(tok) > 1 && tok[0] == '-' {
			flag, attached, hasAttached := lookupHarnessFlag(fs, tok)
			if flag == nil {
				return args[i:], nil
			}
			value := attached
			if !hasAttached {
				if flag.NoOptDefVal != "" {
					value = flag.NoOptDefVal
				} else if i+1 < len(args) {
					i++
					value = args[i]
				} else {
					return nil, fmt.Errorf("flag needs an argument: %s", tok)
				}
			}
			if err := flag.Value.Set(value); err != nil {
				return nil, fmt.Errorf("invalid argument %q for %s: %w", value, tok, err)
			}
			flag.Changed = true
			i++
			continue
		}
		if spinloopPath.set || !namesAnSpinloopOrAlias(tok) {
			return args[i:], nil
		}
		spinloopPath.set, spinloopPath.path = true, tok
		i++
	}
	return nil, nil
}

// lookupHarnessFlag resolves one argument token to a flag registered on fs,
// splitting off any attached value: `--name=value`, `-xvalue`, or `-x=value`.
// It reports a nil flag for anything unregistered, so the caller can treat it
// as the start of the harness's own arguments rather than fail on it.
func lookupHarnessFlag(fs *pflag.FlagSet, tok string) (flag *pflag.Flag, attached string, hasAttached bool) {
	if strings.HasPrefix(tok, "--") {
		name := tok[2:]
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, attached, hasAttached = name[:eq], name[eq+1:], true
		}
		return fs.Lookup(name), attached, hasAttached
	}
	flag = fs.ShorthandLookup(tok[1:2])
	if flag == nil {
		return nil, "", false
	}
	if len(tok) > 2 {
		attached, hasAttached = strings.TrimPrefix(tok[2:], "="), true
	}
	return flag, attached, hasAttached
}

// namesAnSpinloopOrAlias reports whether arg is a way of naming a Spinloop: a path
// to one (or a directory holding one), or a registered alias. It is the
// predicate for taking `harness`'s first positional argument as a Spinloop
// rather than forwarding it to the harness.
//
// A config that cannot be read is treated as "no aliases" rather than an
// error: the argument is then forwarded and the harness still launches.
// Someone who meant an alias gets the real parse error from `spinloop harness
// apply <name>`, where it is actionable.
func namesAnSpinloopOrAlias(arg string) bool {
	if namesAnSpinloop(arg) {
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

// applyBeforeLaunch applies the Spinloop named by --spinloop/-O to the harness
// that is about to be launched — exactly the work `spinloop harness apply`
// does, so one command can dress the harness and then run it. rest is what
// will be forwarded to the harness, inspected only to catch a path that was
// meant for the flag.
// It returns the applied Spinloop's directory and selection, so the launched
// agent can be given the same keys the apply resolved, along with the cloud
// endpoint's live environment when --env names an environment.
//
// The cloud key is fetched before the apply, not after, so the apply resolves
// against the environment the agent will actually run with. Fetching it
// afterwards left the apply warning that no key was set while the launch was
// about to supply one.
func applyBeforeLaunch(f spinloopPathFlag, providers string, h harness.Harness, rest []string, route routeOptions) (spinloop.Selection, string, *cloud.Response, *fleet.Choice, error) {
	// The flag's value has to be attached, so `--spinloop ./dev/Spinloop` (or
	// `--spinloop q3`) would otherwise apply ./Spinloop and quietly hand the path
	// or alias to the harness.
	if f.path == "" && len(rest) > 0 && namesAnSpinloopOrAlias(rest[0]) {
		return spinloop.Selection{}, "", nil, nil, fmt.Errorf("--spinloop takes its path attached: --spinloop=%s (a bare --spinloop applies ./%s)", rest[0], spinloop.DefaultFile)
	}
	sel, path, err := readSpinloop("spinloop harness --spinloop=<file>", f.path)
	if err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	sel, envDir, cloudResp, choice, err := applyRoutedSpinloop(sel, path, providers, h, route, false)
	if err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	return sel, envDir, cloudResp, choice, nil
}

// applyFromEnvironment configures the harness with no Spinloop at all: a bare
// `spinloop harness --env <name>` reaches here. It is applyBeforeLaunch's
// counterpart for that case — same return shape, same launch continuation —
// except there is no Spinloop to read, so routing and the provider selection
// come entirely from the named environment's live deploy-config.
func applyFromEnvironment(providers string, h harness.Harness, route routeOptions) (spinloop.Selection, string, *cloud.Response, *fleet.Choice, error) {
	return applyRoutedSpinloop(spinloop.Selection{}, "", providers, h, route, true)
}

// gatewayProviderID is the catalogue provider a gateway-only launch
// configures — a generic OpenAI-compatible endpoint — when no Spinloop names
// one: the gateway resolves the model per request, so nothing here needs to
// name one either.
const gatewayProviderID = "openai-compatible"

// applyFromGateway configures the harness for a fleet's gateway with no
// Spinloop at all: a launch that names a fleet but wears nothing reaches here.
// It is applyFromEnvironment's counterpart for the other way a launch can
// know where the model is served without being told which model.
//
// A gateway matches no node against a model — it resolves one per request — so
// a launch through one needs no Spinloop to name it. Where the fleet names no
// gateway there is nothing to route by, and the launch fails saying so: a node
// can only be matched by the model a Spinloop names.
//
// The model list the harness is given, the provider's gateway-derived name and
// key, and the warn-rather-than-fail on an unreachable gateway are all
// applyRoutedSpinloop's already — this supplies only the selection that says
// "a gateway is the endpoint", which is the one thing a Spinloop would
// otherwise have carried.
func applyFromGateway(providers string, h harness.Harness, route routeOptions) (spinloop.Selection, string, *cloud.Response, *fleet.Choice, error) {
	fail := func(err error) (spinloop.Selection, string, *cloud.Response, *fleet.Choice, error) {
		return spinloop.Selection{}, "", nil, nil, err
	}
	cfg, err := fleet.Resolve(route.fleetPath)
	if err != nil {
		return fail(err)
	}
	if _, ok := cfg.GatewaySection(); !ok {
		return fail(fmt.Errorf(
			"--fleet needs a Spinloop: it is the Spinloop's model that decides which node can serve you"))
	}
	return applyRoutedSpinloop(spinloop.Selection{Provider: gatewayProviderID}, "", providers, h, route, false)
}

// applyRoutedSpinloop routes an already-read Spinloop and applies it to the
// harness that is about to be launched: routing first, so a launch that cannot
// find a node leaves the harness config exactly as it was, then the cloud
// fetch and the apply themselves. `spinloop harness` reads its Spinloop on the
// way in; `spinloop fleet harness` reads its own, because with none it fails on
// its own terms. Both then run this one path.
//
// autoConfigure is set only by a bare `spinloop harness --env <name>`: no
// Spinloop was read (sel is empty and path is ""), so once the environment's
// live response is in hand, its deploy-config — what is actually deployed
// there — supplies the provider selection instead, exactly as if a Spinloop
// had stated the same PROVIDER/ALIAS/CONTEXT. An environment reporting
// nothing deployed (or an env Lambda predating this) fails the launch rather
// than reaching applySelection's generic "needs a model or an alias" error,
// so the message names the actual cause and how to fix it.
func applyRoutedSpinloop(sel spinloop.Selection, path string, providers string, h harness.Harness, route routeOptions, autoConfigure bool) (spinloop.Selection, string, *cloud.Response, *fleet.Choice, error) {
	// As for apply, --providers overrides the catalogue the selection resolves
	// against (a Spinloop never names one).
	sel.Providers = providers
	// --env and a fleet are two different answers to where the model is served
	// from, and the rule against naming both is the fleet commands' too, so it
	// is enforced and worded in one place. The fleet passed is the one this
	// launch's own discovery settled on — which, unlike a fleet command's, may
	// come from the working directory.
	if err := checkOneTarget(route.envName, route.fleetFile()); err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	envDir := envFileDir(path)
	if path == "" {
		// No Spinloop at all — the gateway-only case. There is no Spinloop
		// directory to look beside, so the .env that travels with this
		// launch is the one beside the fleet file instead, matching what
		// the messages below say when they name a fallback.
		if target := route.fleetFile(); target != "" {
			envDir = filepath.Dir(target)
		}
	}
	localResolve := opencode.EnvResolver(envDir)
	// Routing runs before the apply, and before anything is printed about
	// applying, for the reason the cloud fetch does: a launch that cannot find
	// a node must leave the harness config exactly as it was.
	choice, err := routeThroughFleet(sel, path, route)
	if err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	if choice != nil {
		// The chosen node's address is what the apply writes, in the slot a
		// cloud endpoint's address is written to.
		sel.BaseURL = choice.BaseURL
	}
	// label names what is being applied in the messages below: the Spinloop's
	// own path normally, or the fleet file when there is no Spinloop at all —
	// the gateway-only case, where nothing was read from disk to apply.
	label := path
	if label == "" {
		label = route.fleetFile()
	}
	switch {
	case autoConfigure:
		// Nothing to apply yet — the deploy-config print below covers this
		// case once the environment's live response is in hand.
	case path != "":
		fmt.Printf("Applying %s\n\n", path)
	default:
		fmt.Printf("Applying the gateway named in %s\n\n", label)
	}
	// Before the apply, so a launch that cannot authenticate stops without
	// having rewritten the harness config.
	cloudResp, err := fetchCloudEnv(sel, route.envName, localResolve)
	if err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	if autoConfigure {
		if cloudResp == nil || !cloudResp.Deployed || cloudResp.Runner == "" || cloudResp.ServedName == "" {
			return spinloop.Selection{}, "", nil, nil, fmt.Errorf(
				"nothing is deployed to environment %q to configure the harness with: "+
					"run `spinloop cloud deploy <spinloop> --env %s` to deploy one, "+
					"or `spinloop cloud bootstrap` to update the control plane if %s already has something deployed",
				route.envName, route.envName, route.envName)
		}
		provider, err := providerForRunner(cloudResp.Runner)
		if err != nil {
			return spinloop.Selection{}, "", nil, nil, err
		}
		sel.Provider = provider
		sel.Alias = cloudResp.ServedName
		if cloudResp.ContextSize > 0 {
			sel.Context = strconv.Itoa(cloudResp.ContextSize)
		}
		fmt.Printf("Configuring from what is deployed to %s.\n\n", route.envName)
	}
	resolve := cloudLaunchResolver(localResolve, cloudResp)
	if choice != nil && choice.APIKey != "" {
		resolve = fleetLaunchResolver(resolve, choice.APIKey)
	}
	// gatewayLabel names the gateway to applySelection when the selection has
	// no model or alias of its own to route by: it waives the requirement
	// below and renames the provider, since a second gateway must not
	// overwrite the first's block under the catalogue's shared id.
	var gatewayLabel string
	if choice != nil && choice.Gateway {
		// A fleet file naming a gateway authenticates with a token the client
		// holds itself, resolved the way a key is resolved elsewhere: an ENV
		// instruction, else the process environment, else the .env beside the
		// Spinloop. The variable is the one the section names, or the
		// default where the section names none. Set nowhere, the launch cannot
		// authenticate, and a gateway that refuses every request is not one to
		// point an agent at.
		env := choice.GatewayTokenEnv
		if env == "" {
			env = fleet.DefaultGatewayTokenEnv
		}
		key := localKeyUnder(sel, localResolve, env)
		if key == "" {
			return spinloop.Selection{}, "", nil, nil, fmt.Errorf(
				"no token to reach the gateway %s: export %s, or set it in the .env beside %s",
				choice.BaseURL, env, label)
		}
		choice.APIKey = key
		resolve = fleetLaunchResolver(resolve, key)

		// With nothing to route by model, the gateway resolves the model per
		// request instead — so the harness has nothing to show unless its
		// model list is populated from what the gateway can currently reach.
		// A failure here is a convenience lost, not a launch blocked: the
		// gateway still routes correctly with an empty list.
		if sel.Model == "" && sel.Alias == "" {
			gatewayLabel = choice.Label
			ctx, cancel := context.WithTimeout(context.Background(), gatewayModelsTimeout)
			ids, mErr := fetchGatewayModels(ctx, choice.BaseURL, key)
			cancel()
			if mErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not list the gateway's models (%v). The harness will show none until it is applied again.\n", mErr)
			} else {
				sel.DiscoveredModels = ids
			}
		}
	}
	if err := applySelection(sel, h, path, route.envName, gatewayLabel, resolve); err != nil {
		return spinloop.Selection{}, "", nil, nil, err
	}
	fmt.Println()
	return sel, envDir, cloudResp, choice, nil
}

// gatewayModelsTimeout bounds fetchGatewayModels, so a gateway that never
// answers delays the launch rather than blocking it — the model list is a
// convenience for the harness's picker, not something routing depends on.
const gatewayModelsTimeout = 10 * time.Second

// fetchGatewayModels returns the model IDs a gateway's GET /models reports, in
// the order it reports them. token is sent as a bearer token; empty means the
// gateway is unauthenticated, which Listen only permits on loopback.
func fetchGatewayModels(ctx context.Context, baseURL, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", baseURL, resp.Status)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding %s's model list: %w", baseURL, err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// gatewayProviderKey is the provider key a gateway-routed selection with no
// model of its own is written under: "gateway-" plus the label slugified, so
// "localhost:4000" becomes "gateway-localhost-4000" and a hand-picked
// gateway.name reads almost verbatim. Every gateway a fleet might name gets
// its own key this way, rather than colliding under the catalogue's shared
// "openai-compatible" id.
func gatewayProviderKey(label string) string {
	if slug := slugify(label); slug != "" {
		return "gateway-" + slug
	}
	return "gateway"
}

// slugify lowercases s and collapses anything that is not a letter or digit
// into a single hyphen, trimming one from the end — enough to turn a gateway
// address or a hand-picked name into a safe provider-key segment.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// fleetLaunchResolver extends a lookup with the engine key of the node a launch
// was routed to. The apply then writes a config knowing the key will be there,
// exactly as the cloud path does — the missing-key warning is left for when a
// key really is missing.
func fleetLaunchResolver(base func(string) string, key string) func(string) string {
	return func(name string) string {
		if v := base(name); v != "" {
			return v
		}
		if name == cloudAPIKeyEnv {
			return key
		}
		return ""
	}
}

// cloudEnvTimeout bounds the call that fetches a cloud endpoint's key, so a
// control plane that never answers delays the launch rather than blocking it.
const cloudEnvTimeout = 30 * time.Second

// fetchCloudEnv returns the live base URL and API key of the endpoint the
// environment named by --env serves, or nil when the flag is absent. The
// endpoint is started with a key that only the control plane knows, so this is
// the one place it can come from; `spinloop harness` puts it in the
// environment of the agent it launches.
//
// Whether a failure is fatal depends on whether the key is needed. With nothing
// else supplying one, launching is pointless — the endpoint refuses every
// request — so the command stops and says what to do about it. When the key is
// already to hand (exported, in the `.env` beside the Spinloop, or set by an ENV
// instruction) the fetch was only a convenience, so it warns and carries on.
//
// An environment whose configuration is not registered is a different failure:
// the name points at nothing on this machine, so it is reported as-is rather
// than downgraded.
func fetchCloudEnv(sel spinloop.Selection, envName string, resolve func(string) string) (*cloud.Response, error) {
	if envName == "" {
		return nil, nil
	}
	if !cloud.IsEnvName(envName) {
		return nil, fmt.Errorf("%q is not an environment name: an environment name is a plain identifier, with no path", envName)
	}
	envPath, err := cloud.EnvConfigPath(envName)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(envPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("environment %q is not registered: run `spinloop cloud deploy --env %q` to create it", envName, envName)
		}
		return nil, err
	}
	// The call crosses the network, and a cold control plane is not instant.
	fmt.Fprintf(os.Stderr, "Fetching the endpoint's environment from %s...\n", envName)
	cfg, err := cloud.LoadEnvironment(envName, viperGetenv())
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), cloudEnvTimeout)
		defer cancel()
		var resp *cloud.Response
		if resp, err = cloud.Env(ctx, cfg); err == nil {
			return resp, nil
		}
	}
	if localKey(sel, resolve) == "" {
		return nil, fmt.Errorf(
			"could not fetch the API key for %s: %w\n"+
				"Start the endpoint with `spinloop cloud start --env %s` if it is stopped, or export %s yourself",
			envName, err, envName, cloudAPIKeyEnv)
	}
	fmt.Fprintf(os.Stderr,
		"Warning: could not fetch the API key for %s (%v).\nCarrying on with the %s already set here.\n",
		envName, err, cloudAPIKeyEnv)
	return nil, nil
}

// localKeyUnder returns the key the launch can supply without a control
// plane, under the variable name: an ENV instruction in the Spinloop that
// names it, which overrides everything at launch (see overlayLocalEnv),
// otherwise whatever the environment or the adjacent `.env` holds.
func localKeyUnder(sel spinloop.Selection, resolve func(string) string, name string) string {
	for _, e := range sel.Env {
		if e.Key == name {
			return e.Value
		}
	}
	return resolve(name)
}

// localKey resolves under the cloud API key's variable — the one a REMOTE
// endpoint authenticates under.
func localKey(sel spinloop.Selection, resolve func(string) string) string {
	return localKeyUnder(sel, resolve, cloudAPIKeyEnv)
}

// cloudLaunchResolver extends an environment-variable lookup with the key
// fetched from a running cloud endpoint. `spinloop harness` gives that key to
// the agent it launches, so an apply on the same path should resolve it too:
// the config it writes is complete, and the missing-key warning is left for
// the case where the key really is missing. resp is nil when the Spinloop names
// no cloud, or the fetch failed, and the lookup is then unchanged.
func cloudLaunchResolver(base func(string) string, resp *cloud.Response) func(string) string {
	if resp == nil || resp.APIKey == "" {
		return base
	}
	return func(name string) string {
		if v := base(name); v != "" {
			return v
		}
		if name == cloudAPIKeyEnv {
			return resp.APIKey
		}
		return ""
	}
}

// namesAnSpinloop reports whether arg points at a Spinloop file, or at a directory
// holding one — the two shapes readSpinloop accepts on disk. It is the shared
// "this string denotes a Spinloop here" predicate: it decides whether a path
// beats a registered alias of the same name, and whether `harness` takes its
// first positional argument rather than forwarding it.
func namesAnSpinloop(arg string) bool {
	info, err := os.Stat(arg)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err := os.Stat(filepath.Join(arg, spinloop.DefaultFile))
		return err == nil
	}
	return filepath.Base(arg) == spinloop.DefaultFile
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
		Use:   "show",
		Short: "show the providers and models the harness has configured",
		Long: `lists the providers and models actually configured in the active
harness's config (where list shows the catalogue of what you could
configure), and the aliases you have registered.`,
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

// runShow is the body of `spinloop harness show`.
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
		b.WriteString("\nNo providers configured. Add one with `spinloop harness add`.\n")
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
		Use:   "list",
		Short: "list the provider catalogue",
		Long: `lists the catalogue: the providers spinloop knows about and, with
--models, the models each could be configured with. (show, in contrast,
lists what the active harness's config actually has.)`,
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

// runList is the body of `spinloop provider list`.
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
			return fmt.Errorf("unknown provider %q (see `spinloop provider list`)", args[0])
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

// defaultProvidersFile is the filename provider init writes to when no path
// is given. It matches the name the embedded catalogue carries and the one
// --providers/SPINLOOP_PROVIDERS are typically pointed at.
const defaultProvidersFile = "providers.yaml"

// initProviderCmd writes the binary's embedded providers.yaml to the working
// directory (or an explicit path) as a starting point for a custom catalogue.
// It refuses to clobber an existing file unless --force is given, so a stray
// run can't destroy a catalogue the user has been editing. It is `provider
// init` — the old top-level init-providers, renamed under the group that
// already says provider.
func initProviderCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "write the built-in providers.yaml out",
		Long: `writes the binary's built-in providers.yaml to the working directory
(or [path]) so you can customise the catalogue and point spinloop at it with
--providers/SPINLOOP_PROVIDERS. Refuses to overwrite an existing file unless
--force is given.`,
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

// runInitProviders is the body of `spinloop provider init`.
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
	fmt.Printf("Edit it, then point spinloop at it:\n")
	fmt.Printf("  spinloop provider list --providers %s\n", path)
	fmt.Printf("  SPINLOOP_PROVIDERS=%s spinloop provider list\n", path)
	return nil
}

// Test seams: the suite calls these the way the tree does — each runs its
// command through execCmd rather than parsing a private FlagSet.
func cmdAdd(args []string) error          { return execCmd(addCmd(), args) }
func cmdRemove(args []string) error       { return execCmd(removeCmd(), args) }
func cmdList(args []string) error         { return execCmd(listCmd(), args) }
func cmdShow(args []string) error         { return execCmd(showCmd(), args) }
func cmdApply(args []string) error        { return execCmd(applyCmd(), args) }
func cmdUnapply(args []string) error      { return execCmd(unapplyCmd(), args) }
func cmdAlias(args []string) error        { return execCmd(aliasCmd(), args) }
func cmdUnalias(args []string) error      { return execCmd(unaliasCmd(), args) }
func cmdExport(args []string) error       { return execCmd(exportCmd(), args) }
func cmdInitProvider(args []string) error { return execCmd(initProviderCmd(), args) }
