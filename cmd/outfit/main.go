// Command outfit configures a coding agent ("harness") to use a model provider,
// by deep-merging provider settings into that harness's config. opencode and the
// Pi coding agent are supported; the harness is chosen at runtime with
// --harness/-H or OUTFIT_HARNESS, or a stored default set via `outfit harness
// --set`, and defaults to opencode.
//
// Providers are defined in providers.yaml, which is embedded
// into the binary at build time. For opencode the config is parsed as JSONC so
// comments and existing settings outside the managed provider block are
// preserved; for Pi the managed provider is merged into ~/.pi/agent/models.json.
//
// Usage:
//
//	outfit list
//	outfit show   [--harness <name>]  # show what the harness has configured
//	outfit add    --provider <name> [--model <id>] [--alias <name>]
//	outfit remove --provider <name> [--model <id>] [--alias <name>]
//	outfit apply   [path]  # apply an Outfit file (defaults to ./Outfit)
//	outfit unapply [path]  # remove what an Outfit file selects
//	outfit alias   [path] [-n <name>]  # name an Outfit; -l lists them
//	outfit unalias <name>  # drop a registered name
//	outfit serve  [path]   # run the PROVIDER's server (llama.cpp or oMLX)
//	outfit export [-p name] # print the current config as an Outfit
//	outfit init-providers [path] # write the embedded providers.yaml out
//	outfit harness [-H name] [-O[=path]]  # launch the harness, optionally applying an
//	                                      # Outfit first (--get shows it; --set stores the default)
//	outfit completion <bash|zsh|powershell> # print the tab-completion script
//	outfit remote bootstrap|start|stop|status|metrics|deploy|env|ls # control the remote GPU
//	                                      # inference instance (bootstrap does the
//	                                      # once-per-account shared setup; deploy
//	                                      # sets what it serves; env returns the
//	                                      # running endpoint's env vars; ls lists
//	                                      # registered environments)
//
// Short flags: -p (provider), -m (model), -a (alias),
// -c (context), -o (output), -u (base-url), -H (harness), -O (outfit).
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
// ALIAS — and every command that takes a path takes that name instead. The
// registry is machine-local state in outfit's own config, not part of any
// Outfit, so an Outfit stays as portable as it was.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/discovery"
	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// version is the binary's version. It defaults to "dev" and is overridden at
// build time via -ldflags "-X main.version=...", set by the Makefile and
// goreleaser.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "add":
		return cmdAdd(rest)
	case "remove":
		return cmdRemove(rest)
	case "list":
		return cmdList(rest)
	case "show":
		return cmdShow(rest)
	case "apply":
		return cmdApply(rest)
	case "unapply":
		return cmdUnapply(rest)
	case "alias":
		return cmdAlias(rest)
	case "unalias":
		return cmdUnalias(rest)
	case "serve":
		return cmdServe(rest)
	case "export":
		return cmdExport(rest)
	case "init-providers":
		return cmdInitProviders(rest)
	case "harness":
		return cmdHarness(rest)
	case "completion":
		return cmdCompletion(rest)
	case "__complete":
		// Hidden: the completion script's way of asking what could come next.
		return cmdComplete(rest)
	case "remote":
		return cmdRemote(rest)
	case "version", "-v", "--version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `outfit — configure coding-agent model providers

Usage:
  outfit list
  outfit show   [--harness <name>]         (providers/models the harness has configured)
  outfit add    --provider <name> [--model <id>] [--alias <name>] [--context <size>] [--output <size>]
  outfit remove --provider <name> [--model <id>] [--alias <name>]
  outfit apply  [path] [--output <size>]   (defaults to ./Outfit)
  outfit unapply [path]                    (remove what an Outfit selects)
  outfit alias  [path] [-n <name>] [-l]    (name an Outfit; -l lists them)
  outfit unalias <name>                    (drop a registered name)
  outfit serve  [path] [--dry-run] [--daemon] [--api]  (run the PROVIDER's inference server)
  outfit export [--provider <name>]
  outfit init-providers [path]      (defaults to ./providers.yaml)
  outfit harness [<outfit>] [-H <name>] [--outfit[=<path>]] [args...]
                                    (launch the harness; available: %s)
  outfit completion <shell>         (tab completion: bash, zsh, powershell)
   outfit remote <bootstrap|start|stop|status|metrics|deploy|env|ls> [path]
                                    (control the remote GPU instance; bootstrap
                                     does the once-per-account shared setup;
                                     deploy sets what it serves, from the Outfit;
                                     env returns the running endpoint's env vars;
                                     ls lists registered environments)
  outfit version                    (or -v/--version)

Flags:
  -p, --provider       provider name (see `+"`outfit list`"+`)
  -m, --model          model id to set as default / to add or remove
  -a, --alias          friendly name for the model (the harness key); for
                       llama.cpp the server's reported model name under serve,
                       and the preset section serve selects
  -c, --context        context window size for the added model(s); accepts
                       human suffixes (128k, 1m) or an absolute count (200000)
  -o, --output         max output tokens for the added model(s); same format as
                       --context. Defaults to a quarter of --context when unset
  -u, --base-url       override the provider API base URL
                       (or set OUTFIT_BASE_URL)
  -H, --harness        which harness to configure (or set OUTFIT_HARNESS);
                       overrides the stored default
  -O, --outfit         (harness only) apply this Outfit before launching; given
                       bare it applies ./`+outfit.DefaultFile+`, so attach the path
                       when naming one: --outfit=<path>
      --providers      path to a providers.yaml override
                       (or set OUTFIT_PROVIDERS)

add: deep-merges the provider into the active harness's config, preserving
     everything else. Specify a model (or an alias). --context sets the model's
     context window; --output sets the max output tokens (opencode requires it
     alongside a context, defaulting to a quarter of the context).
remove: removes the provider, or just the named model when a model/alias is
        given.
apply: applies an Outfit file — a declarative, Dockerfile-style description of
       one provider selection — as if you had run the equivalent add.
unapply: removes what an Outfit file selects, as if you had run the equivalent
       remove. The inverse of apply.
alias: registers an Outfit under a short name, which then stands in wherever an
       Outfit path goes (apply, unapply, serve, harness). The name defaults to
       the Outfit's own ALIAS; --name/-n picks another, --force/-F re-points a
       name already registered, and --list/-l shows them all. A path on disk
       always wins over a name, so registering one changes nothing that works.
unalias: drops a registered name. The Outfit file itself is left alone.
serve: runs the inference server the Outfit's PROVIDER names — llamacpp
       (llama-server) or omlx (Apple Silicon). With a PRESET it turns the
       matching section into the command, reading it in that engine's flag
       vocabulary; otherwise it derives one from the Outfit's own instructions.
       Prints the command before running it; --dry-run/-n prints without
       launching the server.
export: prints the active harness's config as an Outfit (outfit export > Outfit).
init-providers: writes the binary's built-in providers.yaml to the working
       directory (or [path]) so you can customise the catalogue and point
       outfit at it with --providers/OUTFIT_PROVIDERS. Refuses to overwrite an
       existing file unless --force is given.
harness: launches the active harness, forwarding any trailing args to it. A
       leading argument that names an Outfit — a registered alias or a path — is
       applied first and not forwarded; put -- before the harness's own args to
       keep them, and a leading -- opts out of this entirely.
       --outfit/-O applies an Outfit first, as if you had run apply before it.
       --get prints the active harness instead of launching it; --set <name>
       stores the default harness and exits. Honours -H/--harness and
       OUTFIT_HARNESS.
show: lists the providers and models actually configured in the active harness's
      config (where list shows the catalogue of what you could configure), and
      the aliases you have registered.
completion: prints a tab-completion script for bash, zsh, or powershell. Add
      source <(outfit completion bash) to your ~/.bashrc (zsh: swap in zsh) and
      TAB then completes commands, flags, providers, harnesses, and your
      registered aliases.
remote: runs the model on a cloud GPU that exists only while you use it, from
       the same Outfit. bootstrap does the once-per-account shared setup (Image
       Builder, the lifecycle Lambdas, shared bucket/roles/VPC) with a consent
       gate; deploy creates the environment the Outfit's REMOTE names — its own
       address, API key and allowed CIDR — and says what it serves (PROVIDER
       picks the engine, just as it does for serve); start boots it and, with
       --env/-e, prints the exports your agent needs; env returns the running
       endpoint's environment variables without starting it; status reports its
       state; stop shuts it down rather than waiting for the idle timer; ls
       lists the registered environments. The endpoint's URLs come from the
       Outfit's REMOTE — a bare name selects an environment under
       ~/.config/outfit/remotes/<name>/, a path names a file — falling back to
       the default environment.
`, strings.Join(harness.Names(), ", "))
}

// parseSelection parses the flags shared by add and remove into a Selection,
// plus the separately-returned harness name (the harness is never part of a
// Selection, so it cannot leak into an Outfit).
func parseSelection(name string, args []string) (outfit.Selection, string, error) {
	var s outfit.Selection
	var harnessName string
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&s.Provider, "provider", "", "provider name")
	fs.StringVar(&s.Provider, "p", "", "provider name (shorthand)")
	fs.StringVar(&s.Model, "model", "", "model id")
	fs.StringVar(&s.Model, "m", "", "model id (shorthand)")
	fs.StringVar(&s.Alias, "alias", "", "friendly name for the model (overrides the harness key)")
	fs.StringVar(&s.Alias, "a", "", "friendly name for the model (shorthand)")
	fs.StringVar(&s.Context, "context", "", "context window size (e.g. 128k, 1m, 200000)")
	fs.StringVar(&s.Context, "c", "", "context window size (shorthand)")
	fs.StringVar(&s.Output, "output", "", "max output tokens (defaults to a quarter of --context)")
	fs.StringVar(&s.Output, "o", "", "max output tokens (shorthand)")
	fs.StringVar(&s.Providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&s.BaseURL, "base-url", "", "override the provider API base URL")
	fs.StringVar(&s.BaseURL, "u", "", "API base URL override (shorthand)")
	fs.StringVar(&harnessName, "harness", "", "which harness to configure")
	fs.StringVar(&harnessName, "H", "", "which harness to configure (shorthand)")
	if err := fs.Parse(args); err != nil {
		return s, "", err
	}
	if s.Provider == "" {
		return s, "", fmt.Errorf("--provider/-p is required (see `outfit list`)")
	}
	return s, harnessName, nil
}

func cmdAdd(args []string) error {
	sel, harnessName, err := parseSelection("add", args)
	if err != nil {
		return err
	}
	h, _, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}
	return applySelection(sel, h, "")
}

// applySelection writes a single provider selection into the active harness's
// config. It is the shared core of `add` and `apply`: both resolve a selection
// (from flags or an Outfit file) and hand it here.
// envDir is the directory of the Outfit the selection came from, which is where
// a `.env` holding its API key belongs; it is empty when no Outfit is involved
// (an `outfit add` from flags), leaving only the process environment.
func applySelection(sel outfit.Selection, h harness.Harness, envDir string) error {
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
		env, err := remoteEnvName(sel.Remote, envDir)
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
		baseURL, err := remoteBaseURL(sel.Remote, envDir)
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

	summary, err := h.Apply(p, sel, contextSize, outputSize, opencode.EnvResolver(envDir))
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
// Outfit command the same shorthand. usage shows the caller's own way of naming
// a path (subcommands take it positionally, `harness` takes it as a flag) in
// the not-found hint. It returns the parsed selection alongside the resolved
// path, which callers use to locate files referenced relative to the Outfit.
//
// It prints when an alias decided the path, which is against this file's
// convention that only cmdX functions write to stdout. The alternative is to
// repeat that reporting at all four call sites, where one omission would leave
// the user guessing which file was read.
func readOutfit(usage, path string) (outfit.Selection, string, error) {
	if path == "" {
		path = outfit.DefaultFile
	}
	if aliased, ok, err := resolveAlias(path); err != nil {
		return outfit.Selection{}, path, err
	} else if ok {
		fmt.Printf("Using alias %q (%s)\n\n", path, aliased)
		path = aliased
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, outfit.DefaultFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == outfit.DefaultFile {
			return outfit.Selection{}, path, fmt.Errorf("no %s found in the current directory (pass a path or an alias: %s; see `outfit alias --list`)", outfit.DefaultFile, usage)
		}
		return outfit.Selection{}, path, fmt.Errorf("reading %s: %w", path, err)
	}
	sel, err := outfit.Parse(data)
	if err != nil {
		return outfit.Selection{}, path, fmt.Errorf("%s: %w", path, err)
	}
	return sel, path, nil
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
	if _, err := os.Stat(path); err != nil {
		return "", false, fmt.Errorf("alias %q points at %s, which is gone — re-point it with `outfit alias -n %s <path>`, or drop it with `outfit unalias %s`", arg, path, arg, arg)
	}
	return path, true, nil
}

// cmdApply reads an Outfit file and applies it. The path defaults to ./Outfit
// when none is given, so a bare `outfit apply` works in a directory that
// holds one.
func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	var providers, output, harnessName string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&output, "output", "", "max output tokens (overrides the Outfit's OUTPUT)")
	fs.StringVar(&output, "o", "", "max output tokens (shorthand)")
	fs.StringVar(&harnessName, "harness", "", "which harness to configure")
	fs.StringVar(&harnessName, "H", "", "which harness to configure (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	h, _, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
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
	return applySelection(sel, h, filepath.Dir(outfitPath))
}

// cmdUnapply reads an Outfit file and removes what it selects — the inverse of
// apply, as remove is to add. The path defaults to ./Outfit when none is given,
// so a bare `outfit unapply` works in a directory that holds one.
func cmdUnapply(args []string) error {
	fs := flag.NewFlagSet("unapply", flag.ContinueOnError)
	var providers, harnessName string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&harnessName, "harness", "", "which harness to configure")
	fs.StringVar(&harnessName, "H", "", "which harness to configure (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	h, _, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	sel, outfitPath, err := readOutfit("outfit unapply <file>", path)
	if err != nil {
		return err
	}
	sel.Providers = providers
	return removeSelection(sel, h, filepath.Dir(outfitPath))
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
func cmdAlias(args []string) error {
	fs := flag.NewFlagSet("alias", flag.ContinueOnError)
	var name string
	var force, list bool
	fs.StringVar(&name, "name", "", "register under this name instead of the Outfit's ALIAS")
	fs.StringVar(&name, "n", "", "name to register under (shorthand)")
	fs.BoolVar(&force, "force", false, "re-point a name that is already registered")
	fs.BoolVar(&force, "F", false, "re-point an existing name (shorthand)")
	fs.BoolVar(&list, "list", false, "list the registered aliases")
	fs.BoolVar(&list, "l", false, "list the registered aliases (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if list {
		var b strings.Builder
		if err := writeAliases(&b, true); err != nil {
			return err
		}
		fmt.Print(b.String())
		return nil
	}

	var arg string
	if rest := fs.Args(); len(rest) > 0 {
		arg = rest[0]
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
	// resolves against the Outfit's own directory under `serve`.
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
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
		fmt.Printf("Added alias %q for %s (stored in %s).\n\n", name, abs, config.Path())
		fmt.Println("Use it anywhere an Outfit path goes:")
		fmt.Printf("  outfit apply %s\n", name)
		fmt.Printf("  outfit serve %s\n", name)
		fmt.Printf("  outfit harness %s\n", name)
	}
	return nil
}

// cmdUnalias drops a registered alias. The Outfit it pointed at is untouched —
// only the name goes away.
func cmdUnalias(args []string) error {
	fs := flag.NewFlagSet("unalias", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
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
		fmt.Fprintf(b, "Aliases (from %s):\n\n", config.Path())
	} else {
		b.WriteString("\nAliases:\n")
	}
	for _, name := range names {
		path, _ := f.Alias(name)
		line := fmt.Sprintf("  %-*s  %s", width, name, path)
		if _, err := os.Stat(path); err != nil {
			line += " (missing)"
		}
		b.WriteString(line + "\n")
	}
	return nil
}

// cmdExport reconstructs an Outfit from the active harness's config and prints
// it to stdout, so an existing setup can be captured (outfit export > Outfit).
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	var provider, providers, harnessName string
	fs.StringVar(&provider, "provider", "", "provider to export")
	fs.StringVar(&provider, "p", "", "provider to export (shorthand)")
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&harnessName, "harness", "", "which harness to read")
	fs.StringVar(&harnessName, "H", "", "which harness to read (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

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

func cmdRemove(args []string) error {
	sel, harnessName, err := parseSelection("remove", args)
	if err != nil {
		return err
	}
	h, _, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}
	return removeSelection(sel, h, "")
}

// removeSelection removes a single provider selection from the active harness's
// config. It is the shared core of `remove` and `unapply`: both resolve a
// selection (from flags or an Outfit file) and hand it here. It is the inverse
// of applySelection, so it names the provider the same way — for a remote Outfit
// that is the environment name, not the PROVIDER value — to remove exactly what
// apply wrote. envDir is the Outfit's directory, needed to read a path-form
// REMOTE's environment; it is empty for a flag-based remove.
func removeSelection(sel outfit.Selection, h harness.Harness, envDir string) error {
	if sel.Remote != "" {
		env, err := remoteEnvName(sel.Remote, envDir)
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

// Set records the flag. The flag package passes "true" for the valueless form
// (see IsBoolFlag); that stands for the default Outfit, which is the empty path
// readOutfit resolves to ./Outfit.
func (f *outfitPathFlag) Set(v string) error {
	f.set = true
	if v == "true" {
		v = ""
	}
	f.path = v
	return nil
}

// IsBoolFlag lets --outfit be given without a value.
func (f *outfitPathFlag) IsBoolFlag() bool { return true }

// cmdHarness launches the active harness, or with --set/--get manages and
// reports the stored preference. The harness is resolved with the same
// precedence as add/apply (--harness/-H > OUTFIT_HARNESS > preference >
// default), and any trailing args after the flags are passed to the harness.
// With --outfit/-O it applies an Outfit first, so a single command dresses the
// harness and then runs it.
func cmdHarness(args []string) error {
	fs := flag.NewFlagSet("harness", flag.ContinueOnError)
	var set, harnessName, providers string
	var get bool
	var outfitPath outfitPathFlag
	fs.StringVar(&set, "set", "", "store this harness as the default and exit")
	fs.BoolVar(&get, "get", false, "print the active harness instead of launching it")
	fs.StringVar(&harnessName, "harness", "", "which harness to launch")
	fs.StringVar(&harnessName, "H", "", "which harness to launch (shorthand)")
	fs.Var(&outfitPath, "outfit", "apply this Outfit before launching (bare: ./"+outfit.DefaultFile+")")
	fs.Var(&outfitPath, "O", "apply this Outfit before launching (shorthand)")
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if set != "" {
		if err := harness.SavePreference(set); err != nil {
			return err
		}
		fmt.Printf("Default harness set to %q (stored in %s).\n", set, harness.PreferencePath())
		return nil
	}

	h, source, err := harness.Resolve(harnessName)
	if err != nil {
		return err
	}

	// --get reports the harness rather than running anything, so it applies
	// nothing either.
	if get {
		pref, _ := harness.LoadPreference()
		fmt.Printf("Active harness: %s (from %s)\n", h.Name(), source)
		if pref == "" {
			fmt.Printf("Stored preference: none (defaults to %s)\n", harness.Default)
		} else {
			fmt.Printf("Stored preference: %s\n", pref)
		}
		fmt.Printf("Available: %s\n", strings.Join(harness.Names(), ", "))
		return nil
	}

	// Take the first positional argument as the Outfit to wear when it names
	// one — a registered alias, a path, or a directory holding one. Everything
	// else is forwarded to the harness untouched, so this can only claim an
	// argument the harness could not have used anyway. An explicit `--` opts
	// out, for an alias that collides with one of the harness's own
	// subcommands.
	rest := fs.Args()
	if !outfitPath.set && !flagsTerminated(args, rest) && len(rest) > 0 && namesAnOutfitOrAlias(rest[0]) {
		outfitPath.set, outfitPath.path = true, rest[0]
		// Reslice rather than rebuild: rest shares its backing array with args,
		// so appending to it would write over the caller's arguments.
		rest = rest[1:]
		// The `--` that separated outfit's Outfit from the harness's own args is
		// ours to drop; any other `--` belongs to the harness and is forwarded.
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
	}

	// A .env beside the applied Outfit is where its keys live, so the launched
	// agent is given the same ones. Without an Outfit there is no such file and
	// only the environment (plus any provider key outfit resolves) is passed on.
	var envDir string
	var sel outfit.Selection
	if outfitPath.set {
		var err error
		sel, envDir, err = applyBeforeLaunch(outfitPath, providers, h, rest)
		if err != nil {
			return err
		}
	}

	// For a remote Outfit, fetch the live API key and base URL from the
	// running endpoint so the harness can reach it without the user having
	// to export anything by hand.
	var remoteResp *remote.Response
	if sel.Remote != "" {
		cfg, cfgErr := resolveRemoteConfigForOutfit(sel.Remote, envDir)
		if cfgErr == nil {
			if resp, envErr := remote.Env(context.Background(), cfg); envErr == nil {
				remoteResp = resp
			}
		}
	}

	// Launch the harness, forwarding stdio and any trailing args.
	bin := h.Command()
	cmd := exec.Command(bin, rest...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = harnessEnv(providers, opencode.EnvResolver(envDir), remoteResp)
	// A worn Outfit brings its whole local environment to the launched agent:
	// its adjacent .env fills any gaps left above, and its ENV instructions
	// override everything. These shape only the child's environment — outfit
	// never mutates its own — and follow the same precedence the remote commands
	// use: ENV > process environment > .env.
	if outfitPath.set {
		cmd.Env = overlayLocalEnv(cmd.Env, sel, envDir)
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found — install the %s harness or add it to your PATH", bin, h.Name())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The harness ran and chose its own exit code; surface it verbatim.
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
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
// agent can be given the same keys the apply resolved, and the caller can
// detect if the Outfit targets a remote endpoint.
func applyBeforeLaunch(f outfitPathFlag, providers string, h harness.Harness, rest []string) (outfit.Selection, string, error) {
	// The flag's value has to be attached, so `--outfit ./dev/Outfit` (or
	// `--outfit q3`) would otherwise apply ./Outfit and quietly hand the path
	// or alias to the harness.
	if f.path == "" && len(rest) > 0 && namesAnOutfitOrAlias(rest[0]) {
		return outfit.Selection{}, "", fmt.Errorf("--outfit takes its path attached: --outfit=%s (a bare --outfit applies ./%s)", rest[0], outfit.DefaultFile)
	}
	sel, path, err := readOutfit("outfit harness --outfit=<file>", f.path)
	if err != nil {
		return outfit.Selection{}, "", err
	}
	// As for apply, --providers overrides the catalogue the selection resolves
	// against (an Outfit never names one).
	sel.Providers = providers
	fmt.Printf("Applying %s\n\n", path)
	envDir := filepath.Dir(path)
	if err := applySelection(sel, h, envDir); err != nil {
		return outfit.Selection{}, "", err
	}
	fmt.Println()
	return sel, envDir, nil
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

// cmdShow prints the providers and models currently configured in the active
// harness's config. It takes the same --harness/-H override (and the same
// flag > env > preference > default precedence) as every other command, so you
// can inspect any harness without changing the stored default. Where `list`
// shows the catalogue of what you could configure, `show` shows what is
// actually configured right now.
func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	var harnessName string
	fs.StringVar(&harnessName, "harness", "", "which harness to read")
	fs.StringVar(&harnessName, "H", "", "which harness to read (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

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

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var providers string
	var showModels bool
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.BoolVar(&showModels, "models", false, "also fetch each provider's current models live from its endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(providers))
	if err != nil {
		return err
	}

	// An optional positional argument narrows the listing to one provider, which
	// is the natural way to ask for a single provider's live models.
	names := cat.SortedProviderNames()
	if rest := fs.Args(); len(rest) > 0 {
		if _, ok := cat.Providers[rest[0]]; !ok {
			return fmt.Errorf("unknown provider %q (see `outfit list`)", rest[0])
		}
		names = []string{rest[0]}
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
		harnesses := "opencode"
		if p.Pi != nil {
			harnesses = "opencode, pi"
		}
		fmt.Fprintf(&b, "    harnesses: %s\n", harnesses)
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

// cmdInitProviders writes the binary's embedded providers.yaml to the working
// directory (or an explicit path) as a starting point for a custom catalogue.
// It refuses to clobber an existing file unless --force is given, so a stray
// run can't destroy a catalogue the user has been editing.
func cmdInitProviders(args []string) error {
	fs := flag.NewFlagSet("init-providers", flag.ContinueOnError)
	var force bool
	fs.BoolVar(&force, "force", false, "overwrite an existing file")
	fs.BoolVar(&force, "F", false, "overwrite an existing file (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := defaultProvidersFile
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}

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
