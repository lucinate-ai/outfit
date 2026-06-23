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
//	outfit apply  [path]   # apply an Outfit file (defaults to ./Outfit)
//	outfit export [-p name] # print the current config as an Outfit
//
// Short flags: -p (provider), -f (model-family), -m (model), -c (context),
// -b (base-url).
//
// The API base URL can be overridden for any provider with --base-url/-b or the
// OUTFIT_BASE_URL environment variable; the flag wins over the env var, and
// either wins over the catalogue's defaults.
//
// An Outfit is a declarative, Dockerfile-style file describing one provider
// selection, applied with `outfit apply`; see outfit.go.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

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
	case "export":
		return cmdExport(rest)
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
  outfit add    --provider <name> [--model-family <family>] [--model <id>] [--context <size>]
  outfit remove --provider <name> [--model-family <family>] [--model <id>]
  outfit apply  [path]              (defaults to ./Outfit)
  outfit export [--provider <name>]

Flags:
  -p, --provider       provider name (see `+"`outfit list`"+`)
  -f, --model-family   model family to add or remove
  -m, --model          model id to set as default / to add or remove
  -c, --context        context window size for the added model(s); accepts
                       human suffixes (128k, 1m) or an absolute count (200000)
  -b, --base-url       override the provider API base URL
                       (or set OUTFIT_BASE_URL)
      --providers      path to a providers.yaml override
                       (or set OUTFIT_PROVIDERS)

add: deep-merges the provider into the opencode config, preserving everything
     else. Specify a family and/or an explicit model. --context sets the
     model's limit.context window.
remove: removes the provider, or just the named models when a family/model is
        given. Clears the default model if it pointed at something removed.
apply: applies an Outfit file — a declarative, Dockerfile-style description of
       one provider selection — as if you had run the equivalent add.
export: prints the current config as an Outfit (outfit export > Outfit).
`)
}

// selection holds the common flags shared by add and remove.
type selection struct {
	provider  string
	family    string
	model     string
	context   string
	providers string
	baseURL   string
}

func parseSelection(name string, args []string) (selection, error) {
	var s selection
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&s.provider, "provider", "", "provider name")
	fs.StringVar(&s.provider, "p", "", "provider name (shorthand)")
	fs.StringVar(&s.family, "model-family", "", "model family")
	fs.StringVar(&s.family, "f", "", "model family (shorthand)")
	fs.StringVar(&s.model, "model", "", "model id")
	fs.StringVar(&s.model, "m", "", "model id (shorthand)")
	fs.StringVar(&s.context, "context", "", "context window size (e.g. 128k, 1m, 200000)")
	fs.StringVar(&s.context, "c", "", "context window size (shorthand)")
	fs.StringVar(&s.providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&s.baseURL, "base-url", "", "override the provider API base URL")
	fs.StringVar(&s.baseURL, "b", "", "API base URL override (shorthand)")
	if err := fs.Parse(args); err != nil {
		return s, err
	}
	if s.provider == "" {
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
func applySelection(sel selection) error {
	if sel.family == "" && sel.model == "" {
		return fmt.Errorf("a provider selection needs a model family and/or a model")
	}

	cat, err := loadCatalogFrom(resolveCatalogPath(sel.providers))
	if err != nil {
		return err
	}
	p, ok := cat.Providers[sel.provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (see `outfit list`)", sel.provider)
	}

	block, defaultModel, err := buildProviderBlock(sel.provider, p, sel.family, sel.model, sel.baseURL, resolveEnv)
	if err != nil {
		return err
	}

	var contextSize int
	if sel.context != "" {
		contextSize, err = parseContextSize(sel.context)
		if err != nil {
			return err
		}
		models, _ := block["models"].(map[string]any)
		if len(models) == 0 {
			return fmt.Errorf("--context/-c needs a model: specify --model-family/-f and/or --model/-m")
		}
		applyContextSize(models, contextSize)
	}

	configFile, err := resolveConfigFile()
	if err != nil {
		return err
	}
	if err := writeConfig(configFile, sel.provider, block, defaultModel); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n\n", configFile)
	line := fmt.Sprintf("Configured provider %q", sel.provider)
	if sel.family != "" {
		line += fmt.Sprintf(" with family %q", sel.family)
	}
	fmt.Println(line + ".")
	if defaultModel != "" {
		fmt.Printf("Default model: %s\n", defaultModel)
	}
	if contextSize > 0 {
		fmt.Printf("Context window: %d tokens\n", contextSize)
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

// cmdApply reads an Outfit file and applies it. The path defaults to ./Outfit
// when none is given, so a bare `outfit apply` works in a directory that
// holds one.
func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	var providers string
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := DefaultOutfitFile
	if rest := fs.Args(); len(rest) > 0 {
		path = rest[0]
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == DefaultOutfitFile {
			return fmt.Errorf("no %s found in the current directory (pass a path: outfit apply <file>)", DefaultOutfitFile)
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}
	sel, err := parseOutfit(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	sel.providers = providers
	return applySelection(sel)
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

	configFile, err := resolveConfigFile()
	if err != nil {
		return err
	}
	states, defaultModel, err := loadConfigState(configFile)
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

	sel := selection{provider: provider, baseURL: st.baseURL}
	if prefix := provider + "/"; strings.HasPrefix(defaultModel, prefix) {
		sel.model = strings.TrimPrefix(defaultModel, prefix)
	}

	cat, catErr := loadCatalogFrom(resolveCatalogPath(providers))

	// Prefer naming a family when the configured models match one, and drop a
	// MODEL line that would only restate that family's default.
	if catErr == nil {
		if p, ok := cat.Providers[provider]; ok {
			if fam := matchFamily(p, st.modelKeys); fam != "" {
				sel.family = fam
				if p.Families[fam].DefaultModel == sel.model {
					sel.model = ""
				}
			}
			// Drop a baseURL that only restates the catalogue's default — keep it
			// only when it is a genuine override worth recording.
			if def, _ := p.Options["baseURL"].(string); sel.baseURL == def {
				sel.baseURL = ""
			}
		}
	}

	// Ensure the Outfit still selects something if we recognised neither a
	// family nor a default model.
	if sel.family == "" && sel.model == "" && len(st.modelKeys) > 0 {
		sel.model = st.modelKeys[0]
	}

	// Reconstruct the context window when the exported models agree on one.
	sel.context = exportContext(sel, st)

	fmt.Print(formatOutfit(sel))
	return nil
}

// exportContext returns the context window to record for an export, as a token
// count string, when the models the Outfit selects all share a single value.
// It returns "" when no context was set or the models disagree (e.g. a config
// hand-edited to differ), so export never invents or guesses a value.
func exportContext(sel selection, st providerState) string {
	var keys []string
	switch {
	case sel.family != "":
		keys = st.modelKeys // a matched family covers exactly these models
	case sel.model != "":
		keys = []string{sel.model}
	}
	distinct := map[int]bool{}
	for _, k := range keys {
		if c, ok := st.contexts[k]; ok {
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

	cat, err := loadCatalogFrom(resolveCatalogPath(sel.providers))
	if err != nil {
		return err
	}

	// Resolve the model keys to remove. With no family/model, the whole
	// provider is removed.
	var modelKeys []string
	if sel.model != "" {
		modelKeys = append(modelKeys, sel.model)
	}
	if sel.family != "" {
		p, ok := cat.Providers[sel.provider]
		if !ok {
			return fmt.Errorf("unknown provider %q (see `outfit list`)", sel.provider)
		}
		fam, ok := p.Families[sel.family]
		if !ok {
			return fmt.Errorf("provider %q has no model family %q (see `outfit list`)", sel.provider, sel.family)
		}
		modelKeys = append(modelKeys, fam.modelKeys()...)
	}

	configFile, err := resolveConfigFile()
	if err != nil {
		return err
	}
	removed, err := removeConfig(configFile, sel.provider, modelKeys)
	if err != nil {
		return err
	}

	if removed == 0 {
		fmt.Printf("Nothing to remove for provider %q in %s.\n", sel.provider, configFile)
		return nil
	}
	fmt.Printf("Updated %s\n\n", configFile)
	if len(modelKeys) == 0 {
		fmt.Printf("Removed provider %q.\n", sel.provider)
	} else {
		fmt.Printf("Removed %d model(s) from provider %q.\n", removed, sel.provider)
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

	cat, err := loadCatalogFrom(resolveCatalogPath(providers))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("Available providers:\n")
	for _, name := range cat.sortedProviderNames() {
		p := cat.Providers[name]
		fmt.Fprintf(&b, "\n  %s — %s\n", name, p.Description)
		if p.APIKeyEnv != "" {
			req := ""
			if p.APIKeyRequired {
				req = " (required)"
			}
			fmt.Fprintf(&b, "    api key: %s%s\n", p.APIKeyEnv, req)
		}
		for _, f := range p.sortedFamilyNames() {
			fam := p.Families[f]
			fmt.Fprintf(&b, "    family %s — %s (default: %s)\n", f, fam.Description, fam.DefaultModel)
		}
	}
	fmt.Print(b.String())
	return nil
}
