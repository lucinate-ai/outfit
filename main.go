// Command oc-config configures the current user's global opencode installation
// to use a model provider, by deep-merging provider settings into the opencode
// config under ${XDG_CONFIG_HOME:-$HOME/.config}/opencode.
//
// Providers and model families are defined in providers.yaml, which is embedded
// into the binary at build time. The config is parsed as JSONC so comments and
// existing settings outside the managed provider block are preserved.
//
// Usage:
//
//	oc-config list
//	oc-config add    --provider <name> [--model-family <family>] [--model <id>]
//	oc-config remove --provider <name> [--model-family <family>] [--model <id>]
//
// Short flags: -p (provider), -f (model-family), -m (model), -b (base-url).
//
// The API base URL can be overridden for any provider with --base-url/-b or the
// OC_CONFIG_BASE_URL environment variable; the flag wins over the env var, and
// either wins over the catalogue's defaults.
package main

import (
	"flag"
	"fmt"
	"os"
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
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `oc-config — configure opencode model providers

Usage:
  oc-config list
  oc-config add    --provider <name> [--model-family <family>] [--model <id>]
  oc-config remove --provider <name> [--model-family <family>] [--model <id>]

Flags:
  -p, --provider       provider name (see `+"`oc-config list`"+`)
  -f, --model-family   model family to add or remove
  -m, --model          model id to set as default / to add or remove
  -b, --base-url       override the provider API base URL
                       (or set OC_CONFIG_BASE_URL)
  -c, --providers      path to a providers.yaml override
                       (or set OC_CONFIG_PROVIDERS)

add: deep-merges the provider into the opencode config, preserving everything
     else. Specify a family and/or an explicit model.
remove: removes the provider, or just the named models when a family/model is
        given. Clears the default model if it pointed at something removed.
`)
}

// selection holds the common flags shared by add and remove.
type selection struct {
	provider  string
	family    string
	model     string
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
	fs.StringVar(&s.providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&s.providers, "c", "", "providers.yaml override (shorthand)")
	fs.StringVar(&s.baseURL, "base-url", "", "override the provider API base URL")
	fs.StringVar(&s.baseURL, "b", "", "API base URL override (shorthand)")
	if err := fs.Parse(args); err != nil {
		return s, err
	}
	if s.provider == "" {
		return s, fmt.Errorf("--provider/-p is required (see `oc-config list`)")
	}
	return s, nil
}

func cmdAdd(args []string) error {
	sel, err := parseSelection("add", args)
	if err != nil {
		return err
	}
	if sel.family == "" && sel.model == "" {
		return fmt.Errorf("specify --model-family/-f and/or --model/-m")
	}

	cat, err := loadCatalogFrom(resolveCatalogPath(sel.providers))
	if err != nil {
		return err
	}
	p, ok := cat.Providers[sel.provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (see `oc-config list`)", sel.provider)
	}

	block, defaultModel, err := buildProviderBlock(sel.provider, p, sel.family, sel.model, sel.baseURL, resolveEnv)
	if err != nil {
		return err
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
			return fmt.Errorf("unknown provider %q (see `oc-config list`)", sel.provider)
		}
		fam, ok := p.Families[sel.family]
		if !ok {
			return fmt.Errorf("provider %q has no model family %q (see `oc-config list`)", sel.provider, sel.family)
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
	fs.StringVar(&providers, "c", "", "providers.yaml override (shorthand)")
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
