// Command outfit configures the current user's global opencode installation
// to use a model provider, by deep-merging provider settings into the opencode
// config under ${XDG_CONFIG_HOME:-$HOME/.config}/opencode.
//
// Providers and model families are defined in providers.yaml, which is embedded
// into the binary at build time. The config is parsed as JSONC so comments and
// existing settings outside the managed provider block are preserved.
//
// Usage:
//
//	outfit list
//	outfit add    --provider <name> [--model-family <family>] [--model <id>]
//	outfit remove --provider <name> [--model-family <family>] [--model <id>]
//	outfit apply   [path]  # apply an Outfit file (defaults to ./Outfit)
//	outfit unapply [path]  # remove what an Outfit file selects
//	outfit serve  [path]   # run llama-server for the Outfit (PRESET or MODEL)
//	outfit export [-p name] # print the current config as an Outfit
//	outfit init-providers [path] # write the embedded providers.yaml out
//
// Short flags: -p (provider), -f (model-family), -m (model), -a (alias),
// -c (context), -o (output), -u (base-url).
//
// The API base URL can be overridden for any provider with --base-url/-u or the
// OUTFIT_BASE_URL environment variable; the flag wins over the env var, and
// either wins over the catalogue's defaults.
//
// An Outfit is a declarative, Dockerfile-style file describing one provider
// selection, applied with `outfit apply` and reverted with `outfit unapply`;
// see the internal/outfit package.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/preset"
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
	case "apply":
		return cmdApply(rest)
	case "unapply":
		return cmdUnapply(rest)
	case "serve":
		return cmdServe(rest)
	case "export":
		return cmdExport(rest)
	case "init-providers":
		return cmdInitProviders(rest)
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
	fmt.Fprint(os.Stderr, `outfit — configure opencode model providers

Usage:
  outfit list
  outfit add    --provider <name> [--model-family <family>] [--model <id>] [--context <size>] [--output <size>]
  outfit remove --provider <name> [--model-family <family>] [--model <id>]
  outfit apply  [path] [--output <size>]   (defaults to ./Outfit)
  outfit unapply [path]                    (remove what an Outfit selects)
  outfit serve  [path] [--dry-run]         (run llama-server from the PRESET)
  outfit export [--provider <name>]
  outfit init-providers [path]      (defaults to ./providers.yaml)
  outfit version                    (or -v/--version)

Flags:
  -p, --provider       provider name (see `+"`outfit list`"+`)
  -f, --model-family   model family to add or remove
  -m, --model          model id to set as default / to add or remove
  -a, --alias          friendly name for the model (the harness key); for
                       llama.cpp the server's reported model name under serve
  -c, --context        context window size for the added model(s); accepts
                       human suffixes (128k, 1m) or an absolute count (200000)
  -o, --output         max output tokens for the added model(s); same format as
                       --context. Defaults to a quarter of --context when unset
  -u, --base-url       override the provider API base URL
                       (or set OUTFIT_BASE_URL)
      --providers      path to a providers.yaml override
                       (or set OUTFIT_PROVIDERS)

add: deep-merges the provider into the opencode config, preserving everything
     else. Specify a family and/or an explicit model. --context sets the
     model's limit.context window; --output sets limit.output (opencode
     requires it alongside a context, defaulting to a quarter of the context).
remove: removes the provider, or just the named models when a family/model is
        given. Clears the default model if it pointed at something removed.
apply: applies an Outfit file — a declarative, Dockerfile-style description of
       one provider selection — as if you had run the equivalent add.
unapply: removes what an Outfit file selects, as if you had run the equivalent
       remove. The inverse of apply.
serve: runs llama-server for the Outfit. With a PRESET (a llama.cpp .ini) it
       turns the matching section into the command; otherwise it derives one
       from MODEL/ALIAS/CONTEXT/BASEURL. Prints the command before running it;
       --dry-run/-n prints without launching the server.
export: prints the current config as an Outfit (outfit export > Outfit).
init-providers: writes the binary's built-in providers.yaml to the working
       directory (or [path]) so you can customise the catalogue and point
       outfit at it with --providers/OUTFIT_PROVIDERS. Refuses to overwrite an
       existing file unless --force is given.
`)
}

func parseSelection(name string, args []string) (outfit.Selection, error) {
	var s outfit.Selection
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&s.Provider, "provider", "", "provider name")
	fs.StringVar(&s.Provider, "p", "", "provider name (shorthand)")
	fs.StringVar(&s.Family, "model-family", "", "model family")
	fs.StringVar(&s.Family, "f", "", "model family (shorthand)")
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
	if err := fs.Parse(args); err != nil {
		return s, err
	}
	if s.Provider == "" {
		return s, fmt.Errorf("--provider/-p is required (see `outfit list`)")
	}
	return s, nil
}

func cmdAdd(args []string) error {
	sel, err := parseSelection("add", args)
	if err != nil {
		return err
	}
	return applySelection(sel)
}

// applySelection writes a single provider selection into the opencode config.
// It is the shared core of `add` and `apply`: both resolve a selection (from
// flags or an Outfit file) and hand it here.
func applySelection(sel outfit.Selection) error {
	if sel.Family == "" && sel.Model == "" && sel.Alias == "" {
		return fmt.Errorf("a provider selection needs a model family, a model, or an alias")
	}

	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(sel.Providers))
	if err != nil {
		return err
	}
	p, ok := cat.Providers[sel.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (see `outfit list`)", sel.Provider)
	}

	// The harness keys a model by its friendly ALIAS when given, otherwise by the
	// provider-native MODEL itself. For a single-model llama.cpp server the key is
	// only a label (the server serves whatever it loaded), so an ALIAS keeps that
	// label readable; for an API provider, leaving ALIAS unset keeps the real id.
	modelKey := sel.Alias
	if modelKey == "" {
		modelKey = sel.Model
	}

	block, defaultModel, err := catalog.BuildProviderBlock(sel.Provider, p, sel.Family, modelKey, sel.BaseURL, opencode.ResolveEnv)
	if err != nil {
		return err
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
		models, _ := block["models"].(map[string]any)
		if len(models) == 0 {
			return fmt.Errorf("--context/-c needs a model: specify --model-family/-f and/or --model/-m")
		}
		contextsize.Apply(models, contextSize, outputSize)
	}

	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return err
	}
	if err := opencode.WriteConfig(configFile, sel.Provider, block, defaultModel); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n\n", configFile)
	line := fmt.Sprintf("Configured provider %q", sel.Provider)
	if sel.Family != "" {
		line += fmt.Sprintf(" with family %q", sel.Family)
	}
	fmt.Println(line + ".")
	if defaultModel != "" {
		fmt.Printf("Default model: %s\n", defaultModel)
	}
	if contextSize > 0 {
		fmt.Printf("Context window: %d tokens\n", contextSize)
		fmt.Printf("Max output: %d tokens\n", outputSize)
	}
	if opts, ok := block["options"].(map[string]any); ok {
		if _, ok := opts["apiKey"]; ok {
			fmt.Printf("API key injected from %s.\n", p.APIKeyEnv)
		}
		if b, ok := opts["baseURL"]; ok {
			fmt.Printf("Base URL: %v\n", b)
		}
	}
	fmt.Println("\nRun 'opencode' from any directory to use the configuration.")
	return nil
}

// readOutfit reads and parses the Outfit at path, defaulting to ./Outfit when
// path is empty so a bare command works in a directory that holds one. cmd
// names the calling subcommand for the not-found hint. It returns the parsed
// selection alongside the resolved path, which callers use to locate files
// referenced relative to the Outfit.
func readOutfit(cmd, path string) (outfit.Selection, string, error) {
	if path == "" {
		path = outfit.DefaultFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == outfit.DefaultFile {
			return outfit.Selection{}, path, fmt.Errorf("no %s found in the current directory (pass a path: outfit %s <file>)", outfit.DefaultFile, cmd)
		}
		return outfit.Selection{}, path, fmt.Errorf("reading %s: %w", path, err)
	}
	sel, err := outfit.Parse(data)
	if err != nil {
		return outfit.Selection{}, path, fmt.Errorf("%s: %w", path, err)
	}
	return sel, path, nil
}

// cmdApply reads an Outfit file and applies it. The path defaults to ./Outfit
// when none is given, so a bare `outfit apply` works in a directory that
// holds one.
func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	var providers, output string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&output, "output", "", "max output tokens (overrides the Outfit's OUTPUT)")
	fs.StringVar(&output, "o", "", "max output tokens (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	sel, _, err := readOutfit("apply", path)
	if err != nil {
		return err
	}
	sel.Providers = providers
	// A command-line --output/-o overrides the Outfit's OUTPUT instruction.
	if output != "" {
		sel.Output = output
	}
	return applySelection(sel)
}

// cmdUnapply reads an Outfit file and removes what it selects — the inverse of
// apply, as remove is to add. The path defaults to ./Outfit when none is given,
// so a bare `outfit unapply` works in a directory that holds one.
func cmdUnapply(args []string) error {
	fs := flag.NewFlagSet("unapply", flag.ContinueOnError)
	var providers string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	sel, _, err := readOutfit("unapply", path)
	if err != nil {
		return err
	}
	sel.Providers = providers
	return removeSelection(sel)
}

// llamaServerBinary is the llama.cpp server executable that `serve` launches.
// It is a package var so tests can point it at a stub instead of a real build.
var llamaServerBinary = "llama-server"

// cmdServe reads an Outfit and runs llama-server for it. With a PRESET it turns
// the matching preset section into the command; without one it derives the
// command from the Outfit's own MODEL/ALIAS/CONTEXT/BASEURL. Either way it
// prints the command before running it. The Outfit path defaults to ./Outfit.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var dryRun bool
	fs.BoolVar(&dryRun, "dry-run", false, "print the llama-server command without running it")
	fs.BoolVar(&dryRun, "n", false, "print the command without running it (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var path string
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}
	sel, outfitPath, err := readOutfit("serve", path)
	if err != nil {
		return err
	}

	var argv []string
	if sel.Preset != "" {
		// A relative PRESET is resolved against the Outfit's directory, so an
		// Outfit and its preset can travel together.
		presetPath := sel.Preset
		if !filepath.IsAbs(presetPath) {
			presetPath = filepath.Join(filepath.Dir(outfitPath), presetPath)
		}
		data, err := os.ReadFile(presetPath)
		if err != nil {
			return fmt.Errorf("reading preset %s: %w", presetPath, err)
		}
		pre, err := preset.Parse(data)
		if err != nil {
			return fmt.Errorf("%s: %w", presetPath, err)
		}
		// The preset's sections are named by the friendly ALIAS, not the
		// provider-native MODEL, so that is what selects one.
		sec, err := pre.Select(sel.Alias)
		if err != nil {
			return fmt.Errorf("%s: %w", presetPath, err)
		}
		// Anything the Outfit states (CONTEXT, BASEURL, ALIAS, MODEL) overrides
		// the preset's own values.
		overrides, err := outfitServeParams(sel)
		if err != nil {
			return err
		}
		argv = pre.Command(llamaServerBinary, sec, overrides)
		fmt.Printf("Using preset %s (model %s)\n\n", presetPath, sec.Name)
	} else {
		if sel.Model == "" {
			return fmt.Errorf("serve needs a PRESET or a MODEL (an HF repo like org/model:quant, or a path to a .gguf)")
		}
		params, err := outfitServeParams(sel)
		if err != nil {
			return err
		}
		argv = append([]string{llamaServerBinary}, preset.Flags(params)...)
		fmt.Printf("Serving %s from %s\n\n", sel.Model, outfitPath)
	}

	fmt.Printf("%s\n\n", preset.FormatCommand(argv))
	if dryRun {
		return nil
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found — install llama.cpp (e.g. brew install llama.cpp) or check the path", argv[0])
		}
		return err
	}
	return nil
}

// outfitServeParams turns the llama-server settings an Outfit states into preset
// params: the provider-native MODEL supplies the model source (hf for a Hugging
// Face repo, model for a .gguf path); ALIAS, CONTEXT, and BASEURL fill in the
// rest. They seed a preset-less command and, with a preset, override its values.
func outfitServeParams(sel outfit.Selection) ([]preset.Param, error) {
	var params []preset.Param
	if sel.Model != "" {
		if isModelPath(sel.Model) {
			params = append(params, preset.Param{Key: "model", Value: sel.Model})
		} else {
			params = append(params, preset.Param{Key: "hf", Value: sel.Model})
		}
	}
	if sel.Alias != "" {
		params = append(params, preset.Param{Key: "alias", Value: sel.Alias})
	}
	if sel.Context != "" {
		n, err := contextsize.Parse(sel.Context)
		if err != nil {
			return nil, err
		}
		params = append(params, preset.Param{Key: "ctx-size", Value: strconv.Itoa(n)})
	}
	if sel.BaseURL != "" {
		host, port, err := hostPortFromURL(sel.BaseURL)
		if err != nil {
			return nil, err
		}
		if host != "" {
			params = append(params, preset.Param{Key: "host", Value: host})
		}
		if port != "" {
			params = append(params, preset.Param{Key: "port", Value: port})
		}
	}
	return params, nil
}

// isModelPath reports whether a MODEL value is a local file rather than a
// Hugging Face repo: an absolute or explicitly-relative path, a home-relative
// path, or anything ending in .gguf. Everything else is treated as org/model.
func isModelPath(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), ".gguf") {
		return true
	}
	return strings.HasPrefix(model, "/") ||
		strings.HasPrefix(model, "./") ||
		strings.HasPrefix(model, "../") ||
		strings.HasPrefix(model, "~")
}

// hostPortFromURL extracts the host and port from a BASEURL so serve can bind
// llama-server to the same endpoint the harness will call. A bare host:port
// with no scheme is accepted too.
func hostPortFromURL(raw string) (host, port string, err error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid BASEURL %q: %w", raw, err)
	}
	return u.Hostname(), u.Port(), nil
}

// cmdExport reconstructs an Outfit from the current opencode config and prints
// it to stdout, so an existing setup can be captured (outfit export > Outfit).
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	var provider, providers string
	fs.StringVar(&provider, "provider", "", "provider to export")
	fs.StringVar(&provider, "p", "", "provider to export (shorthand)")
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return err
	}
	states, defaultModel, err := opencode.LoadConfigState(configFile)
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

	// Prefer naming a family when the configured models match one, and drop a
	// MODEL line that would only restate that family's default.
	if catErr == nil {
		if p, ok := cat.Providers[provider]; ok {
			if fam := catalog.MatchFamily(p, st.ModelKeys); fam != "" {
				sel.Family = fam
				if p.Families[fam].DefaultModel == sel.Model {
					sel.Model = ""
				}
			}
			// Drop a baseURL that only restates the catalogue's default — keep it
			// only when it is a genuine override worth recording.
			if def, _ := p.Options["baseURL"].(string); sel.BaseURL == def {
				sel.BaseURL = ""
			}
		}
	}

	// Ensure the Outfit still selects something if we recognised neither a
	// family nor a default model.
	if sel.Family == "" && sel.Model == "" && len(st.ModelKeys) > 0 {
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
func exportLimit(sel outfit.Selection, st opencode.ProviderState, values map[string]int) string {
	var keys []string
	switch {
	case sel.Family != "":
		keys = st.ModelKeys // a matched family covers exactly these models
	case sel.Model != "":
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
	sel, err := parseSelection("remove", args)
	if err != nil {
		return err
	}
	return removeSelection(sel)
}

// removeSelection removes a single provider selection from the opencode config.
// It is the shared core of `remove` and `unapply`: both resolve a selection
// (from flags or an Outfit file) and hand it here. It is the inverse of
// applySelection.
func removeSelection(sel outfit.Selection) error {
	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(sel.Providers))
	if err != nil {
		return err
	}

	// Resolve the model keys to remove. With no family/model, the whole
	// provider is removed.
	var modelKeys []string
	if sel.Alias != "" {
		modelKeys = append(modelKeys, sel.Alias)
	}
	if sel.Model != "" {
		modelKeys = append(modelKeys, sel.Model)
	}
	if sel.Family != "" {
		p, ok := cat.Providers[sel.Provider]
		if !ok {
			return fmt.Errorf("unknown provider %q (see `outfit list`)", sel.Provider)
		}
		fam, ok := p.Families[sel.Family]
		if !ok {
			return fmt.Errorf("provider %q has no model family %q (see `outfit list`)", sel.Provider, sel.Family)
		}
		modelKeys = append(modelKeys, fam.ModelKeys()...)
	}

	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return err
	}
	removed, err := opencode.RemoveConfig(configFile, sel.Provider, modelKeys)
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

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var providers string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cat, err := catalog.LoadFrom(catalog.ResolveCatalogPath(providers))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("Available providers:\n")
	for _, name := range cat.SortedProviderNames() {
		p := cat.Providers[name]
		fmt.Fprintf(&b, "\n  %s — %s\n", name, p.Description)
		if p.APIKeyEnv != "" {
			req := ""
			if p.APIKeyRequired {
				req = " (required)"
			}
			fmt.Fprintf(&b, "    api key: %s%s\n", p.APIKeyEnv, req)
		}
		for _, f := range p.SortedFamilyNames() {
			fam := p.Families[f]
			fmt.Fprintf(&b, "    family %s — %s (default: %s)\n", f, fam.Description, fam.DefaultModel)
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
