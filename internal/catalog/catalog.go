// Package catalog holds the provider/model-family catalogue (providers.yaml)
// and turns a provider selection into an opencode provider block.
package catalog

import (
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProvidersEnv names the environment variable that points at a providers.yaml
// override.
const ProvidersEnv = "OUTFIT_PROVIDERS"

// baseURLEnv names the environment variable that overrides the provider's API
// base URL, regardless of which provider is selected. The --base-url flag takes
// precedence over it; both win over the catalogue's static and per-provider
// (optionsFromEnv) base URLs.
const baseURLEnv = "OUTFIT_BASE_URL"

// providersYAML is the externalised provider/model-family catalogue, embedded
// into the binary at build time but maintained as a plain file.
//
//go:embed providers.yaml
var providersYAML []byte

// Catalog is the parsed providers.yaml.
type Catalog struct {
	Providers map[string]*Provider `yaml:"providers"`
}

// Provider describes how to construct an opencode provider block.
type Provider struct {
	Description    string             `yaml:"description"`
	Name           string             `yaml:"name"`
	NPM            string             `yaml:"npm"`
	APIKeyEnv      string             `yaml:"apiKeyEnv"`
	APIKeyRequired bool               `yaml:"apiKeyRequired"`
	APIKeyPrefix   string             `yaml:"apiKeyPrefix"`
	Options        map[string]any     `yaml:"options"`
	OptionsFromEnv map[string]string  `yaml:"optionsFromEnv"`
	Families       map[string]*Family `yaml:"families"`
	// Pi marks the provider as usable by the Pi harness and carries its
	// Pi-specific settings. Nil when the provider has no `pi:` block, in which
	// case BuildPiProvider reports it as unsupported.
	Pi *PiConfig `yaml:"pi"`
}

// PiConfig is a provider's Pi-harness settings, from the catalogue `pi:` block.
type PiConfig struct {
	// API is the Pi protocol type: openai-completions, openai-responses,
	// anthropic-messages, or google-generative-ai.
	API string `yaml:"api"`
	// BaseURL is the Pi endpoint. Optional; falls back to options.baseURL.
	BaseURL string `yaml:"baseUrl"`
}

// Family is a named set of models within a provider.
type Family struct {
	Description  string                    `yaml:"description"`
	DefaultModel string                    `yaml:"defaultModel"`
	Models       map[string]map[string]any `yaml:"models"`
}

// ResolveCatalogPath determines which catalogue file to use: the flag value if
// given, otherwise the OUTFIT_PROVIDERS env var, otherwise "" (embedded).
func ResolveCatalogPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	return os.Getenv(ProvidersEnv)
}

// Load parses the embedded catalogue.
func Load() (*Catalog, error) {
	return LoadFrom("")
}

// EmbeddedYAML returns a copy of the raw providers.yaml embedded into the
// binary, so callers can write it out as a starting point for a custom
// catalogue (see `outfit init-providers`).
func EmbeddedYAML() []byte {
	out := make([]byte, len(providersYAML))
	copy(out, providersYAML)
	return out
}

// LoadFrom parses the catalogue from path, falling back to the embedded
// catalogue when path is empty.
func LoadFrom(path string) (*Catalog, error) {
	data := providersYAML
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading provider catalogue %s: %w", path, err)
		}
		data = b
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing provider catalogue: %w", err)
	}
	return &c, nil
}

// SortedProviderNames returns provider keys in stable order.
func (c *Catalog) SortedProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for n := range c.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SortedFamilyNames returns a provider's family names in stable order.
func (p *Provider) SortedFamilyNames() []string {
	names := make([]string, 0, len(p.Families))
	for n := range p.Families {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ModelKeys returns a family's model keys in stable order.
func (f *Family) ModelKeys() []string {
	keys := make([]string, 0, len(f.Models))
	for k := range f.Models {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MatchFamily returns the name of the provider family whose model set exactly
// matches keys, or "" if none does. It lets `outfit export` name a family
// instead of listing the individual models it expands to.
func MatchFamily(p *Provider, keys []string) string {
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	for _, name := range p.SortedFamilyNames() {
		fk := p.Families[name].ModelKeys()
		if len(fk) != len(want) {
			continue
		}
		matched := true
		for _, k := range fk {
			if !want[k] {
				matched = false
				break
			}
		}
		if matched {
			return name
		}
	}
	return ""
}

// BuildProviderBlock turns a provider plus an optional family and/or explicit
// model into an opencode provider block, returning the block and the
// fully-qualified default model (provider/model), or "" if none was selected.
// resolve looks up env vars (typically from .env, then the environment).
//
// baseURLOverride, when non-empty, sets options.baseURL for any provider. It
// comes from the --base-url flag; when empty, the OUTFIT_BASE_URL env var is
// consulted via resolve. Either wins over the catalogue's static baseURL and
// any per-provider optionsFromEnv mapping.
func BuildProviderBlock(id string, p *Provider, familyName, modelOverride, baseURLOverride string, resolve func(string) string) (block map[string]any, defaultModel string, err error) {
	block = map[string]any{}
	if p.Name != "" {
		block["name"] = p.Name
	}
	if p.NPM != "" {
		block["npm"] = p.NPM
	}

	options := map[string]any{}
	for k, v := range p.Options {
		options[k] = v
	}
	for optKey, envVar := range p.OptionsFromEnv {
		if v := resolve(envVar); v != "" {
			options[optKey] = v
		}
	}
	if baseURLOverride == "" {
		baseURLOverride = resolve(baseURLEnv)
	}
	if baseURLOverride != "" {
		options["baseURL"] = baseURLOverride
	}
	if p.APIKeyEnv != "" {
		key := resolve(p.APIKeyEnv)
		switch {
		case key == "" && p.APIKeyRequired:
			return nil, "", fmt.Errorf("%s is not set; add it to your .env or environment", p.APIKeyEnv)
		case key != "" && p.APIKeyPrefix != "" && !strings.HasPrefix(key, p.APIKeyPrefix):
			return nil, "", fmt.Errorf("%s does not look right (expected it to start with %q)", p.APIKeyEnv, p.APIKeyPrefix)
		case key != "":
			options["apiKey"] = key
		}
	}
	if len(options) > 0 {
		block["options"] = options
	}

	models := map[string]any{}
	var defaultModelKey string
	if familyName != "" {
		fam, ok := p.Families[familyName]
		if !ok {
			return nil, "", fmt.Errorf("provider %q has no model family %q (see `outfit list`)", id, familyName)
		}
		for k, v := range fam.Models {
			models[k] = v
		}
		defaultModelKey = fam.DefaultModel
	}
	if modelOverride != "" {
		if _, ok := models[modelOverride]; !ok {
			models[modelOverride] = map[string]any{"name": modelOverride}
		}
		defaultModelKey = modelOverride
	}
	if len(models) > 0 {
		block["models"] = models
	}

	if defaultModelKey != "" {
		defaultModel = id + "/" + defaultModelKey
	}
	return block, defaultModel, nil
}

// piPlaceholderAPIKey is the dummy apiKey written for keyless local providers so
// Pi treats them as authed and lists their models. Pi resolves it as a literal
// (no leading "$"), and llama.cpp/Ollama-style servers ignore the value.
const piPlaceholderAPIKey = "local"

// PiProvider is a provider entry for Pi's ~/.pi/agent/models.json, produced by
// BuildPiProvider. JSON tags match Pi's schema; empty fields are omitted.
type PiProvider struct {
	BaseURL string    `json:"baseUrl,omitempty"`
	API     string    `json:"api,omitempty"`
	APIKey  string    `json:"apiKey,omitempty"`
	Models  []PiModel `json:"models"`
}

// PiModel is one model within a PiProvider. ContextWindow and MaxTokens are set
// by the Pi harness from the selection's CONTEXT and OUTPUT, so they are omitted
// when zero.
type PiModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	MaxTokens     int    `json:"maxTokens,omitempty"`
}

// BuildPiProvider turns a provider plus an optional family and/or explicit model
// into a Pi provider entry, returning the entry and the chosen default model key
// (provider-relative), or "" if none was selected. resolve looks up env vars
// (used only for the OUTFIT_BASE_URL override).
//
// Unlike opencode, the API key is written as a "$ENV_VAR" interpolation rather
// than the resolved secret, matching Pi's idiom. A provider without a `pi:`
// block in the catalogue is not Pi-compatible and yields an error.
//
// baseURLOverride mirrors BuildProviderBlock: when non-empty it wins; otherwise
// OUTFIT_BASE_URL is consulted, then the catalogue's pi.baseUrl, then
// options.baseURL.
func BuildPiProvider(id string, p *Provider, familyName, modelOverride, baseURLOverride string, resolve func(string) string) (PiProvider, string, error) {
	if p.Pi == nil {
		return PiProvider{}, "", fmt.Errorf("provider %q is not supported by the pi harness (no pi config in the catalogue)", id)
	}

	prov := PiProvider{API: p.Pi.API}

	if baseURLOverride == "" {
		baseURLOverride = resolve(baseURLEnv)
	}
	switch {
	case baseURLOverride != "":
		prov.BaseURL = baseURLOverride
	case p.Pi.BaseURL != "":
		prov.BaseURL = p.Pi.BaseURL
	default:
		prov.BaseURL, _ = p.Options["baseURL"].(string)
	}

	if p.APIKeyEnv != "" {
		prov.APIKey = "$" + p.APIKeyEnv
	} else {
		// Pi only surfaces a provider's models in /model once auth is configured;
		// with no apiKey at all the models load but stay unavailable. Keyless local
		// servers (llama.cpp, Ollama, …) ignore the key, so write a dummy literal —
		// the same placeholder pattern Pi's own docs use for Ollama — to make the
		// models selectable.
		prov.APIKey = piPlaceholderAPIKey
	}

	// Collect models in stable order so the written file is deterministic.
	names := map[string]string{}
	var keys []string
	var defaultModelKey string
	if familyName != "" {
		fam, ok := p.Families[familyName]
		if !ok {
			return PiProvider{}, "", fmt.Errorf("provider %q has no model family %q (see `outfit list`)", id, familyName)
		}
		for _, k := range fam.ModelKeys() {
			keys = append(keys, k)
			if n, _ := fam.Models[k]["name"].(string); n != "" {
				names[k] = n
			}
		}
		defaultModelKey = fam.DefaultModel
	}
	if modelOverride != "" {
		if _, seen := names[modelOverride]; !seen {
			found := false
			for _, k := range keys {
				if k == modelOverride {
					found = true
					break
				}
			}
			if !found {
				keys = append(keys, modelOverride)
			}
		}
		defaultModelKey = modelOverride
	}
	for _, k := range keys {
		prov.Models = append(prov.Models, PiModel{ID: k, Name: names[k]})
	}

	return prov, defaultModelKey, nil
}
