// Package catalog holds the provider/model-family catalogue (providers.yaml)
// and turns a provider selection into an opencode provider block.
package catalog

import (
	_ "embed"
	"fmt"
	"net/url"
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
	Description    string `yaml:"description"`
	Name           string `yaml:"name"`
	NPM            string `yaml:"npm"`
	APIKeyEnv      string `yaml:"apiKeyEnv"`
	APIKeyRequired bool   `yaml:"apiKeyRequired"`
	// APIKeyOptional marks a provider that also works unauthenticated (a local
	// server). It only affects the Pi harness: see BuildPiProvider.
	APIKeyOptional bool              `yaml:"apiKeyOptional"`
	APIKeyPrefix   string            `yaml:"apiKeyPrefix"`
	Options        map[string]any    `yaml:"options"`
	OptionsFromEnv map[string]string `yaml:"optionsFromEnv"`
	// OptionsRequired lists option keys that must resolve to a non-empty value
	// (from static options or optionsFromEnv) when the provider is applied.
	// It guards a caller-supplied option that has no usable default — such as a
	// Vertex AI project — on a provider that injects no API key. See
	// BuildProviderBlock.
	OptionsRequired []string `yaml:"optionsRequired"`
	// Pi marks the provider as usable by the Pi harness and carries its
	// Pi-specific settings. Nil when the provider has no `pi:` block, in which
	// case BuildPiProvider reports it as unsupported.
	Pi *PiConfig `yaml:"pi"`
	// Lucinate marks the provider as usable by the lucinate harness (an
	// OpenAI-compatible endpoint). Nil when the provider has no `lucinate:`
	// block, in which case BuildLucinateConnection reports it as unsupported.
	Lucinate *LucinateConfig `yaml:"lucinate"`
}

// PiConfig is a provider's Pi-harness settings, from the catalogue `pi:` block.
type PiConfig struct {
	// API is the Pi protocol type: openai-completions, openai-responses,
	// anthropic-messages, or google-generative-ai.
	API string `yaml:"api"`
	// BaseURL is the Pi endpoint. Optional; falls back to options.baseURL.
	BaseURL string `yaml:"baseUrl"`
}

// LucinateConfig is a provider's lucinate-harness settings, from the catalogue
// `lucinate:` block. Its presence marks the provider lucinate-capable; the
// (optional) base URL falls back to options.baseURL when unset.
type LucinateConfig struct {
	// BaseURL is the lucinate connection URL. Optional; falls back to
	// options.baseURL.
	BaseURL string `yaml:"baseUrl"`
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

// ResolveOptions returns the provider's options with its optionsFromEnv mapping
// applied over the static ones, which is the view *both* harness builders need:
// a per-provider variable like OLLAMA_BASE_URL is a user's runtime statement
// about where the server is, so it is not opencode-specific. resolve looks up
// env vars (typically a .env, then the environment).
//
// The returned map is a fresh copy, so callers may layer their own overrides
// over it without mutating the catalogue.
func (p *Provider) ResolveOptions(resolve func(string) string) map[string]any {
	options := make(map[string]any, len(p.Options)+len(p.OptionsFromEnv))
	for k, v := range p.Options {
		options[k] = v
	}
	for optKey, envVar := range p.OptionsFromEnv {
		if v := resolve(envVar); v != "" {
			options[optKey] = v
		}
	}
	return options
}

// RequireOptions checks that every option in OptionsRequired resolved to a
// non-empty value. A provider may require caller-supplied options that have no
// usable default (e.g. a Vertex AI project); unlike the API key, these are plain
// options, so apiKeyRequired does not cover them.
//
// Both builders call this. Pi's schema carries no general options map, so a
// provider that both requires an option and declares a `pi` block cannot express
// it there — failing loudly beats writing an entry that is missing something
// essential. The embedded catalogue never pairs the two (TestCatalogPiIntegrity
// enforces that), but a runtime catalogue supplied via --providers can.
func (p *Provider) RequireOptions(id string, options map[string]any) error {
	for _, optKey := range p.OptionsRequired {
		if v, ok := options[optKey]; !ok || v == nil || v == "" {
			if env := p.OptionsFromEnv[optKey]; env != "" {
				return fmt.Errorf("the %q option is required for provider %q; set %s in your .env or environment", optKey, id, env)
			}
			return fmt.Errorf("the %q option is required for provider %q; set it in the catalogue options", optKey, id)
		}
	}
	return nil
}

// BuildProviderBlock turns a provider plus an explicit model into an opencode
// provider block, returning the block and the fully-qualified default model
// (provider/model), or "" if none was selected. resolve looks up env vars
// (typically from .env, then the environment).
//
// baseURLOverride, when non-empty, sets options.baseURL for any provider. It
// comes from the --base-url flag; when empty, the OUTFIT_BASE_URL env var is
// consulted via resolve. Either wins over the catalogue's static baseURL and
// any per-provider optionsFromEnv mapping.
func BuildProviderBlock(id string, p *Provider, modelOverride, baseURLOverride string, resolve func(string) string) (block map[string]any, defaultModel string, err error) {
	block = map[string]any{}
	if p.Name != "" {
		block["name"] = p.Name
	}
	if p.NPM != "" {
		block["npm"] = p.NPM
	}

	options := p.ResolveOptions(resolve)
	if baseURLOverride == "" {
		baseURLOverride = resolve(baseURLEnv)
	}
	if baseURLOverride != "" {
		options["baseURL"] = baseURLOverride
	}
	if err := p.RequireOptions(id, options); err != nil {
		return nil, "", err
	}
	if p.APIKeyEnv != "" {
		key := resolve(p.APIKeyEnv)
		baseURL, _ := options["baseURL"].(string)
		switch {
		case key == "" && p.APIKeyRequired:
			return nil, "", fmt.Errorf("%s is not set; add it to your .env or environment", p.APIKeyEnv)
		case key != "" && p.APIKeyPrefix != "" && !strings.HasPrefix(key, p.APIKeyPrefix):
			return nil, "", fmt.Errorf("%s does not look right (expected it to start with %q)", p.APIKeyEnv, p.APIKeyPrefix)
		case key == "" && p.APIKeyOptional && IsLocalEndpoint(baseURL):
			// A local server that needs no key: leave the option out entirely
			// rather than point it at a variable nobody will set.
		default:
			// opencode substitutes {env:VAR} when it reads the config, so the
			// secret is never written to disk. The variable has to be set when
			// opencode runs; `outfit harness` passes on whatever it can resolve,
			// including from its own .env.
			options["apiKey"] = EnvRef(p.APIKeyEnv)
		}
	}
	if len(options) > 0 {
		block["options"] = options
	}

	models := map[string]any{}
	var defaultModelKey string
	if modelOverride != "" {
		models[modelOverride] = map[string]any{"name": modelOverride}
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

// RemoteProviderLabel is the display name for a harness provider that a remote
// environment has renamed, so it reads distinctly from a local engine of the
// same kind: the engine's display name qualified by the environment, e.g.
// "llama.cpp (dev-2)". With no engine name it is the environment alone, which is
// still unique — no local provider shares it.
func RemoteProviderLabel(engine, env string) string {
	if engine == "" {
		return env
	}
	return fmt.Sprintf("%s (%s)", engine, env)
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

// BuildPiProvider turns a provider plus an explicit model into a Pi provider
// entry, returning the entry and the chosen default model key
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
func BuildPiProvider(id string, p *Provider, modelOverride, baseURLOverride string, resolve func(string) string) (PiProvider, string, error) {
	if p.Pi == nil {
		return PiProvider{}, "", fmt.Errorf("provider %q is not supported by the pi harness (no pi config in the catalogue)", id)
	}

	prov := PiProvider{API: p.Pi.API}

	options := p.ResolveOptions(resolve)
	if baseURLOverride == "" {
		baseURLOverride = resolve(baseURLEnv)
	}
	// Precedence: the explicit override (--base-url, then OUTFIT_BASE_URL), then
	// the provider's own endpoint variable, then the catalogue's Pi endpoint,
	// then its opencode one. The per-provider variable sits above both catalogue
	// values because it is the user speaking about their own machine, and below
	// the explicit override because that is the user speaking more specifically.
	var envBaseURL string
	if v := p.OptionsFromEnv["baseURL"]; v != "" {
		envBaseURL = resolve(v)
	}
	switch {
	case baseURLOverride != "":
		prov.BaseURL = baseURLOverride
	case envBaseURL != "":
		prov.BaseURL = envBaseURL
	case p.Pi.BaseURL != "":
		prov.BaseURL = p.Pi.BaseURL
	default:
		prov.BaseURL, _ = p.Options["baseURL"].(string)
	}
	if prov.BaseURL != "" {
		options["baseURL"] = prov.BaseURL
	}
	if err := p.RequireOptions(id, options); err != nil {
		return PiProvider{}, "", err
	}

	// Pi only surfaces a provider's models in /model once auth is configured;
	// with no apiKey at all the models load but stay unavailable. Keyless local
	// servers (llama.cpp, Ollama, …) ignore the key, so write a dummy literal —
	// the same placeholder pattern Pi's own docs use for Ollama — to make the
	// models selectable.
	//
	// A "$VAR" reference is written whenever the provider has a key env var,
	// because Pi resolves it at run time: the key need not be set when the
	// Outfit is applied, and exporting it later is enough. The exception is an
	// apiKeyOptional provider, pointed at a local endpoint, whose var is unset:
	// one provider covers both a keyless local server and an authenticated
	// remote one, and for the local server a reference to a variable set
	// nowhere would hide the models. That exception is deliberately conditioned
	// on the endpoint rather than only on the key, because a placeholder
	// written for a remote endpoint could not be repaired by exporting the key
	// afterwards — Pi would keep sending the placeholder.
	switch {
	case p.APIKeyEnv != "" && p.APIKeyOptional && resolve(p.APIKeyEnv) == "" && IsLocalEndpoint(prov.BaseURL):
		prov.APIKey = piPlaceholderAPIKey
	case p.APIKeyEnv != "":
		prov.APIKey = "$" + p.APIKeyEnv
	default:
		prov.APIKey = piPlaceholderAPIKey
	}

	var defaultModelKey string
	if modelOverride != "" {
		prov.Models = append(prov.Models, PiModel{ID: modelOverride})
		defaultModelKey = modelOverride
	}

	return prov, defaultModelKey, nil
}

// LucinateConnection is the resolved plumbing for a lucinate OpenAI-compatible
// connection: the endpoint URL and the selected model. lucinate reads the API
// key from LUCINATE_OPENAI_API_KEY at run time, so — like Pi's $VAR idiom — no
// secret is carried here.
type LucinateConnection struct {
	BaseURL string
	Model   string
}

// BuildLucinateConnection turns a provider plus an explicit model into the
// plumbing for a lucinate connection, returning the connection and the chosen
// model key, or "" if none was selected. A provider without a `lucinate:` block
// in the catalogue is not lucinate-compatible and yields an error.
//
// baseURLOverride mirrors BuildPiProvider: when non-empty it wins; otherwise
// OUTFIT_BASE_URL is consulted, then the provider's own endpoint variable, then
// the catalogue's lucinate.baseUrl, then options.baseURL. resolve looks up env
// vars (used for the override and the per-provider baseURL variable).
func BuildLucinateConnection(id string, p *Provider, modelOverride, baseURLOverride string, resolve func(string) string) (LucinateConnection, string, error) {
	if p.Lucinate == nil {
		return LucinateConnection{}, "", fmt.Errorf("provider %q is not supported by the lucinate harness (no lucinate config in the catalogue)", id)
	}

	options := p.ResolveOptions(resolve)
	if baseURLOverride == "" {
		baseURLOverride = resolve(baseURLEnv)
	}
	// Same precedence as BuildPiProvider: explicit override, then the provider's
	// own endpoint variable, then the catalogue's lucinate endpoint, then its
	// opencode one.
	var envBaseURL string
	if v := p.OptionsFromEnv["baseURL"]; v != "" {
		envBaseURL = resolve(v)
	}
	var conn LucinateConnection
	switch {
	case baseURLOverride != "":
		conn.BaseURL = baseURLOverride
	case envBaseURL != "":
		conn.BaseURL = envBaseURL
	case p.Lucinate.BaseURL != "":
		conn.BaseURL = p.Lucinate.BaseURL
	default:
		conn.BaseURL, _ = p.Options["baseURL"].(string)
	}
	if conn.BaseURL != "" {
		options["baseURL"] = conn.BaseURL
	}
	if err := p.RequireOptions(id, options); err != nil {
		return LucinateConnection{}, "", err
	}

	conn.Model = modelOverride
	return conn, modelOverride, nil
}

// EnvRef renders an environment-variable reference in the form opencode
// substitutes when it reads its config, so a secret never lands on disk.
func EnvRef(name string) string {
	return "{env:" + name + "}"
}

// IsLocalEndpoint reports whether a base URL points at this machine, where a
// server commonly needs no API key. An empty or unparseable URL counts as
// local, so callers only treat an endpoint as remote on real evidence.
func IsLocalEndpoint(baseURL string) bool {
	if baseURL == "" {
		return true
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	switch u.Hostname() {
	case "", "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	return false
}
