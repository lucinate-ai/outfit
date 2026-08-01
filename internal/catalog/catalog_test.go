package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noEnv is a resolver that finds nothing.
func noEnv(string) string { return "" }

// envMap returns a resolver backed by a map.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestCatalogIntegrity guards the embedded providers.yaml against typos and
// drift: a custom provider supplying a baseURL must declare an npm package, and
// an apiKeyPrefix is meaningless without an apiKeyEnv.
func TestCatalogIntegrity(t *testing.T) {
	cat, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Providers) == 0 {
		t.Fatal("no providers in catalogue")
	}

	for name, p := range cat.Providers {
		if p.Description == "" {
			t.Errorf("provider %q: missing description", name)
		}
		if p.APIKeyPrefix != "" && p.APIKeyEnv == "" {
			t.Errorf("provider %q: apiKeyPrefix set without apiKeyEnv", name)
		}
		if p.APIKeyRequired && p.APIKeyEnv == "" {
			t.Errorf("provider %q: apiKeyRequired set without apiKeyEnv", name)
		}
		// A custom (non built-in) provider supplying a baseURL must name an npm
		// package, per the opencode custom-provider docs.
		if _, hasBaseURL := p.Options["baseURL"]; hasBaseURL && p.NPM == "" {
			t.Errorf("provider %q: baseURL set without npm package", name)
		}
		// A required option must have a source: a static option or an env mapping.
		for _, opt := range p.OptionsRequired {
			_, inStatic := p.Options[opt]
			_, inEnv := p.OptionsFromEnv[opt]
			if !inStatic && !inEnv {
				t.Errorf("provider %q: required option %q has no source (options or optionsFromEnv)", name, opt)
			}
		}
	}
}

// TestCatalogPiIntegrity checks that every provider declaring a `pi:` block
// names a valid Pi API type, and that a Pi-capable provider can resolve a base
// URL (from pi.baseUrl or options.baseURL) so the written models.json is usable.
func TestCatalogPiIntegrity(t *testing.T) {
	validAPIs := map[string]bool{
		"openai-completions":   true,
		"openai-responses":     true,
		"anthropic-messages":   true,
		"google-generative-ai": true,
	}
	cat, _ := Load()
	piCapable := 0
	for name, p := range cat.Providers {
		if p.Pi == nil {
			continue
		}
		piCapable++
		if !validAPIs[p.Pi.API] {
			t.Errorf("provider %q: invalid pi.api %q", name, p.Pi.API)
		}
		if p.Pi.BaseURL == "" {
			if _, ok := p.Options["baseURL"].(string); !ok {
				t.Errorf("provider %q: pi block has no baseUrl and no options.baseURL fallback", name)
			}
		}
	}
	if piCapable == 0 {
		t.Error("expected at least one Pi-capable provider in the catalogue")
	}
}

func TestBuildPiProvider_OpenRouter(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["openrouter"]

	prov, model, err := BuildPiProvider("openrouter", p, "deepseek/deepseek-v4-pro", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if model != "deepseek/deepseek-v4-pro" {
		t.Errorf("default model = %q", model)
	}
	if prov.API != "openai-completions" {
		t.Errorf("api = %q", prov.API)
	}
	if prov.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("baseUrl = %q, want the catalogue pi endpoint", prov.BaseURL)
	}
	// API key is an env interpolation, never the resolved secret.
	if prov.APIKey != "$DEEPSEEK_API_KEY" {
		t.Errorf("apiKey = %q, want $DEEPSEEK_API_KEY interpolation", prov.APIKey)
	}
	if len(prov.Models) != 1 || prov.Models[0].ID != "deepseek/deepseek-v4-pro" {
		t.Fatalf("models = %+v, want a single deepseek/deepseek-v4-pro entry", prov.Models)
	}
}

func TestBuildPiProvider_BaseURLFallbackAndOverride(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"] // pi block has no baseUrl; falls back to options.baseURL

	prov, _, err := BuildPiProvider("ollama", p, "llama3.2", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("baseUrl = %q, want the options.baseURL fallback", prov.BaseURL)
	}

	// Flag override wins over everything.
	prov, _, err = BuildPiProvider("ollama", p, "llama3.2", "https://flag.example/v1", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.BaseURL != "https://flag.example/v1" {
		t.Errorf("baseUrl = %q, want the flag override", prov.BaseURL)
	}

	// OUTFIT_BASE_URL wins when no flag is given.
	prov, _, err = BuildPiProvider("ollama", p, "llama3.2", "", envMap(map[string]string{
		baseURLEnv: "https://from-env.example/v1",
	}))
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.BaseURL != "https://from-env.example/v1" {
		t.Errorf("baseUrl = %q, want the OUTFIT_BASE_URL value", prov.BaseURL)
	}
}

func TestBuildPiProvider_ModelOverrideNoKey(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["llamacpp"] // apiKeyOptional, and the var is unset here

	prov, model, err := BuildPiProvider("llamacpp", p, "my-local", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if model != "my-local" {
		t.Errorf("default model = %q, want my-local", model)
	}
	// A keyless provider gets a dummy literal apiKey so Pi lists its models;
	// without one Pi loads the provider but hides its models from /model.
	if prov.APIKey != piPlaceholderAPIKey {
		t.Errorf("apiKey = %q, want the %q placeholder for a keyless provider", prov.APIKey, piPlaceholderAPIKey)
	}
	if len(prov.Models) != 1 || prov.Models[0].ID != "my-local" {
		t.Errorf("models = %+v, want a single my-local entry", prov.Models)
	}
}

func TestBuildPiProvider_UnsupportedProvider(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["amazon-bedrock"] // no pi block
	if _, _, err := BuildPiProvider("amazon-bedrock", p, "some-model", "", noEnv); err == nil {
		t.Fatal("expected error for a provider with no pi config")
	}
}

func TestResolveCatalogPath(t *testing.T) {
	t.Setenv(ProvidersEnv, "/from/env.yaml")
	if got := ResolveCatalogPath("/from/flag.yaml"); got != "/from/flag.yaml" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := ResolveCatalogPath(""); got != "/from/env.yaml" {
		t.Errorf("env should be used when flag empty, got %q", got)
	}
	t.Setenv(ProvidersEnv, "")
	if got := ResolveCatalogPath(""); got != "" {
		t.Errorf("expected empty (embedded), got %q", got)
	}
}

func TestLoadCatalogFrom(t *testing.T) {
	// Embedded fallback.
	if _, err := LoadFrom(""); err != nil {
		t.Fatalf("embedded catalogue: %v", err)
	}

	// Override file.
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	os.WriteFile(path, []byte(`providers:
  mine:
    description: My custom provider
    npm: "@ai-sdk/openai-compatible"
    options:
      baseURL: http://localhost:9999/v1
`), 0o600)

	cat, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if _, ok := cat.Providers["mine"]; !ok {
		t.Error("custom provider not loaded from override file")
	}
	if _, ok := cat.Providers["openrouter"]; ok {
		t.Error("override should replace, not merge with, the embedded catalogue")
	}

	// Missing file is an error.
	if _, err := LoadFrom(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Error("expected error for missing override file")
	}
}

func TestBuildProviderBlock_OpenRouterKeyReferenced(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["openrouter"]

	block, model, err := BuildProviderBlock("openrouter", p, "deepseek/deepseek-v4-flash", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-abc",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if want := "openrouter/deepseek/deepseek-v4-flash"; model != want {
		t.Errorf("default model = %q, want %q", model, want)
	}
	opts := block["options"].(map[string]any)
	// The secret is never written to the config; opencode substitutes the
	// reference when it reads it.
	if opts["apiKey"] != "{env:DEEPSEEK_API_KEY}" {
		t.Errorf("apiKey = %v, want the env reference", opts["apiKey"])
	}
	if opts["apiKey"] == "sk-or-v1-abc" {
		t.Error("the resolved secret must not be written to the config")
	}
	if models := block["models"].(map[string]any); len(models) != 1 {
		t.Errorf("got %d models, want 1", len(models))
	}
}

func TestBuildProviderBlock_RequiredKeyMissing(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["openrouter"]
	if _, _, err := BuildProviderBlock("openrouter", p, "deepseek/deepseek-v4-flash", "", noEnv); err == nil {
		t.Fatal("expected error when required key is missing")
	}
}

func TestBuildProviderBlock_KeyPrefixMismatch(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["openrouter"]
	_, _, err := BuildProviderBlock("openrouter", p, "deepseek/deepseek-v4-flash", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "wrong-prefix-key",
	}))
	if err == nil || !strings.Contains(err.Error(), "start with") {
		t.Fatalf("expected prefix mismatch error, got %v", err)
	}
}

func TestBuildProviderBlock_BedrockNoKeyRegionFromEnv(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["amazon-bedrock"]

	block, model, err := BuildProviderBlock("amazon-bedrock", p, "anthropic.claude-3-5-sonnet", "", envMap(map[string]string{
		"AWS_REGION": "eu-west-2",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if _, ok := opts["apiKey"]; ok {
		t.Error("bedrock block should not carry an apiKey")
	}
	if opts["region"] != "eu-west-2" {
		t.Errorf("region = %v, want eu-west-2 (from env override)", opts["region"])
	}
	if !strings.HasPrefix(model, "amazon-bedrock/") {
		t.Errorf("model = %q, want amazon-bedrock/...", model)
	}
}

func TestBuildProviderBlock_VertexRequiredOptionMissing(t *testing.T) {
	cat, _ := Load()
	for _, id := range []string{"google-vertex", "google-vertex-anthropic"} {
		p := cat.Providers[id]
		_, _, err := BuildProviderBlock(id, p, "some-model", "", noEnv)
		if err == nil {
			t.Fatalf("%s: expected error when required option project is missing", id)
		}
		if !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "GOOGLE_VERTEX_PROJECT") {
			t.Errorf("%s: error %q should name the option and its env var", id, err)
		}
	}
}

func TestBuildProviderBlock_RequiredOptionSources(t *testing.T) {
	// A required option is satisfied by a static option value, with no env
	// mapping needed.
	fromStatic := &Provider{
		Description:     "custom",
		Options:         map[string]any{"project": "static-proj"},
		OptionsRequired: []string{"project"},
	}
	block, _, err := BuildProviderBlock("custom", fromStatic, "m", "", noEnv)
	if err != nil {
		t.Fatalf("a static required option should satisfy the requirement: %v", err)
	}
	if block["options"].(map[string]any)["project"] != "static-proj" {
		t.Errorf("project = %v, want static-proj", block["options"].(map[string]any)["project"])
	}

	// A required option with no source at all fails with the catalogue-side
	// message (there is no env var to name).
	unmapped := &Provider{
		Description:     "custom",
		OptionsRequired: []string{"project"},
	}
	_, _, err = BuildProviderBlock("custom", unmapped, "m", "", noEnv)
	if err == nil || !strings.Contains(err.Error(), "catalogue options") {
		t.Fatalf("an unmapped required option should fail with the catalogue message, got %v", err)
	}
}

func TestBuildProviderBlock_VertexProjectAndLocation(t *testing.T) {
	cat, _ := Load()
	for _, id := range []string{"google-vertex", "google-vertex-anthropic"} {
		p := cat.Providers[id]
		block, model, err := BuildProviderBlock(id, p, "some-model", "", envMap(map[string]string{
			"GOOGLE_VERTEX_PROJECT": "my-proj",
		}))
		if err != nil {
			t.Fatalf("%s: BuildProviderBlock: %v", id, err)
		}
		// Keyed by opencode's built-in id, so no npm is emitted.
		if _, ok := block["npm"]; ok {
			t.Errorf("%s: block should have no npm (opencode built-in)", id)
		}
		opts := block["options"].(map[string]any)
		if _, ok := opts["apiKey"]; ok {
			t.Errorf("%s: block should not carry an apiKey (ambient credentials)", id)
		}
		if opts["project"] != "my-proj" {
			t.Errorf("%s: project = %v, want my-proj (from env)", id, opts["project"])
		}
		if opts["location"] != "global" {
			t.Errorf("%s: location = %v, want the global default", id, opts["location"])
		}
		if !strings.HasPrefix(model, id+"/") {
			t.Errorf("%s: model = %q, want %s/...", id, model, id)
		}
	}
}

func TestBuildProviderBlock_VertexLocationOverride(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["google-vertex"]
	block, _, err := BuildProviderBlock("google-vertex", p, "gemini-2.0-flash", "", envMap(map[string]string{
		"GOOGLE_VERTEX_PROJECT":  "my-proj",
		"GOOGLE_VERTEX_LOCATION": "europe-west4",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["location"] != "europe-west4" {
		t.Errorf("location = %v, want europe-west4 (env override)", opts["location"])
	}
}

func TestBuildPiProvider_VertexUnsupported(t *testing.T) {
	cat, _ := Load()
	for _, id := range []string{"google-vertex", "google-vertex-anthropic"} {
		p := cat.Providers[id] // no pi block
		if _, _, err := BuildPiProvider(id, p, "some-model", "", noEnv); err == nil {
			t.Fatalf("%s: expected pi to reject a provider with no pi block", id)
		}
	}
}

func TestBuildProviderBlock_CustomProviderDefaults(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, model, err := BuildProviderBlock("ollama", p, "llama3.2", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if block["npm"] != "@ai-sdk/openai-compatible" {
		t.Errorf("npm = %v", block["npm"])
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "http://localhost:11434/v1" {
		t.Errorf("baseURL = %v, want default", opts["baseURL"])
	}
	if model != "ollama/llama3.2" {
		t.Errorf("model = %q", model)
	}
}

func TestBuildProviderBlock_ModelOverrideAddsEntry(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, model, err := BuildProviderBlock("ollama", p, "my-model", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if model != "ollama/my-model" {
		t.Errorf("model = %q, want ollama/my-model", model)
	}
	models := block["models"].(map[string]any)
	entry, ok := models["my-model"].(map[string]any)
	if !ok || entry["name"] != "my-model" {
		t.Errorf("expected generated model entry, got %v", models["my-model"])
	}
}

func TestBuildProviderBlock_NoModelIsNoDefault(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["llamacpp"]

	block, model, err := BuildProviderBlock("llamacpp", p, "", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if model != "" {
		t.Errorf("default model = %q, want empty when no model is named", model)
	}
	if _, ok := block["models"]; ok {
		t.Errorf("block should carry no models when none is named, got %v", block["models"])
	}
}

// TestBuildProviderBlock_BaseURLFlagOverrides checks that the --base-url value
// wins over both the catalogue's static baseURL and the per-provider
// optionsFromEnv mapping (here OLLAMA_BASE_URL).
func TestBuildProviderBlock_BaseURLFlagOverrides(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, _, err := BuildProviderBlock("ollama", p, "llama3.2", "https://flag.example/v1", envMap(map[string]string{
		"OLLAMA_BASE_URL": "https://per-provider.example/v1",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://flag.example/v1" {
		t.Errorf("baseURL = %v, want the --base-url flag value", opts["baseURL"])
	}
}

// TestBuildProviderBlock_BaseURLFromEnv checks that, with no flag, the general
// OUTFIT_BASE_URL env var overrides the catalogue's static baseURL.
func TestBuildProviderBlock_BaseURLFromEnv(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, _, err := BuildProviderBlock("ollama", p, "llama3.2", "", envMap(map[string]string{
		baseURLEnv: "https://from-env.example/v1",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://from-env.example/v1" {
		t.Errorf("baseURL = %v, want the OUTFIT_BASE_URL value", opts["baseURL"])
	}
}

// TestBuildProviderBlock_BaseURLFlagBeatsEnv checks the precedence: an explicit
// --base-url flag wins over OUTFIT_BASE_URL.
func TestBuildProviderBlock_BaseURLFlagBeatsEnv(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, _, err := BuildProviderBlock("ollama", p, "llama3.2", "https://flag.example/v1", envMap(map[string]string{
		baseURLEnv: "https://from-env.example/v1",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://flag.example/v1" {
		t.Errorf("baseURL = %v, want the flag to win over the env var", opts["baseURL"])
	}
}

// TestBuildProviderBlock_BaseURLOnPlainProvider checks that the override applies
// even to a provider that carries no baseURL in the catalogue, injecting a fresh
// options.baseURL.
func TestBuildProviderBlock_BaseURLOnPlainProvider(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["openrouter"]

	block, _, err := BuildProviderBlock("openrouter", p, "deepseek/deepseek-v4-flash", "https://gateway.example/v1", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-abc",
	}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://gateway.example/v1" {
		t.Errorf("baseURL = %v, want it injected on a provider without a default", opts["baseURL"])
	}
}

// TestBuildProviderBlock_NoBaseURLOverride confirms the catalogue's static
// baseURL is left untouched when neither flag nor env var is set.
func TestBuildProviderBlock_NoBaseURLOverride(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["ollama"]

	block, _, err := BuildProviderBlock("ollama", p, "llama3.2", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "http://localhost:11434/v1" {
		t.Errorf("baseURL = %v, want the catalogue default", opts["baseURL"])
	}
}

// TestEmbeddedYAML confirms the embedded catalogue is returned verbatim, parses
// as a catalogue, and is a defensive copy callers cannot mutate in place.
func TestEmbeddedYAML(t *testing.T) {
	data := EmbeddedYAML()
	if len(data) == 0 {
		t.Fatal("EmbeddedYAML returned no data")
	}
	if _, err := LoadFrom(""); err != nil {
		t.Fatalf("embedded catalogue should parse: %v", err)
	}

	// Mutating the returned slice must not affect a subsequent call.
	data[0] ^= 0xff
	again := EmbeddedYAML()
	if again[0] == data[0] {
		t.Error("EmbeddedYAML should return a fresh copy, not the underlying bytes")
	}
}

// llamacpp names both a keyless local server and the authenticated remote
// deployment, so its key is optional: written through when set, and replaced by
// Pi's placeholder when not, rather than a $VAR that resolves to nothing.
func TestBuildPiProvider_OptionalKey(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["llamacpp"]

	withKey := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-remote"
		}
		return ""
	}
	prov, _, err := BuildPiProvider("llamacpp", p, "local-model", "", withKey)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.APIKey != "$OPENAI_API_KEY" {
		t.Errorf("apiKey = %q, want the $VAR reference when the key is set", prov.APIKey)
	}

	prov, _, err = BuildPiProvider("llamacpp", p, "local-model", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.APIKey != piPlaceholderAPIKey {
		t.Errorf("apiKey = %q, want the placeholder when the key is unset", prov.APIKey)
	}
}

// A provider whose key is NOT optional keeps the reference even when unset, so
// the key can be exported after the Outfit is applied.
func TestBuildPiProvider_RequiredKeyKeepsReference(t *testing.T) {
	cat, _ := Load()
	prov, _, err := BuildPiProvider("openai-compatible", cat.Providers["openai-compatible"], "gpt-4o", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.APIKey != "$OPENAI_API_KEY" {
		t.Errorf("apiKey = %q, want the $VAR reference to survive an unset key", prov.APIKey)
	}
}

// The opencode block injects the resolved key, so a remote llama.cpp endpoint
// is authenticated rather than 401ing.
func TestBuildProviderBlock_LlamacppReferencesKeyForRemote(t *testing.T) {
	cat, _ := Load()
	resolve := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-remote"
		}
		return ""
	}
	block, _, err := BuildProviderBlock("llamacpp", cat.Providers["llamacpp"], "local-model", "http://198.51.100.1:8000/v1", resolve)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	options := block["options"].(map[string]any)
	if options["apiKey"] != "{env:OPENAI_API_KEY}" {
		t.Errorf("apiKey = %v, want the env reference", options["apiKey"])
	}

	block, _, err = BuildProviderBlock("llamacpp", cat.Providers["llamacpp"], "local-model", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	options = block["options"].(map[string]any)
	if _, ok := options["apiKey"]; ok {
		t.Error("a local keyless server should get no apiKey at all")
	}

	// A remote endpoint keeps the reference even with the key unset, so setting
	// it before the agent runs is enough.
	block, _, err = BuildProviderBlock("llamacpp", cat.Providers["llamacpp"], "local-model", "http://198.51.100.1:8000/v1", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if got := block["options"].(map[string]any)["apiKey"]; got != "{env:OPENAI_API_KEY}" {
		t.Errorf("apiKey = %v, want the env reference for a remote endpoint", got)
	}
}

func TestIsLocalEndpoint(t *testing.T) {
	for _, u := range []string{
		"", "http://localhost:8080/v1", "http://127.0.0.1:8080/v1",
		"http://0.0.0.0:8000/v1", "http://[::1]:8080/v1", "::not a url::",
	} {
		if !IsLocalEndpoint(u) {
			t.Errorf("IsLocalEndpoint(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"http://198.51.100.1:8000/v1", "https://api.example.com/v1"} {
		if IsLocalEndpoint(u) {
			t.Errorf("IsLocalEndpoint(%q) = true, want false", u)
		}
	}
}

// Pi resolves its $VAR reference when it runs, so a placeholder written for a
// remote endpoint can never be repaired by exporting the key afterwards — Pi
// would keep sending the placeholder. The placeholder is therefore only right
// for a local server.
func TestBuildPiProvider_RemoteOptionalKeyKeepsReference(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["llamacpp"] // apiKeyOptional

	prov, _, err := BuildPiProvider("llamacpp", p, "local-model", "http://198.51.100.1:8000/v1", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.APIKey != "$OPENAI_API_KEY" {
		t.Errorf("apiKey = %q, want the reference so exporting the key later works", prov.APIKey)
	}

	// The local server keeps the placeholder: it needs no key, and a reference
	// to a variable set nowhere would hide its models in /model.
	prov, _, err = BuildPiProvider("llamacpp", p, "local-model", "http://127.0.0.1:8080/v1", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if prov.APIKey != piPlaceholderAPIKey {
		t.Errorf("apiKey = %q, want the placeholder for a local server", prov.APIKey)
	}
}

// TestBuildProviderBlock_OMLXKeylessLocal checks that a local oMLX server gets
// no apiKey option at all: the var points nowhere useful for a server that needs
// no auth, and writing it would make opencode fail to resolve it.
func TestBuildProviderBlock_OMLXKeylessLocal(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["omlx"]
	if p == nil {
		t.Fatal("omlx missing from the catalogue")
	}
	block, defaultModel, err := BuildProviderBlock("omlx", p, "my-model", "", noEnv)
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	if defaultModel != "omlx/my-model" {
		t.Errorf("defaultModel = %q, want omlx/my-model", defaultModel)
	}
	options := block["options"].(map[string]any)
	if _, ok := options["apiKey"]; ok {
		t.Errorf("a keyless local server should get no apiKey: %v", options)
	}
	if options["baseURL"] != "http://localhost:8000/v1" {
		t.Errorf("baseURL = %v", options["baseURL"])
	}
}

// TestBuildProviderBlock_OMLXReferencesKeyWhenSet checks the other half of
// apiKeyOptional: a key that is set is written as an env reference, never as the
// resolved secret.
func TestBuildProviderBlock_OMLXReferencesKeyWhenSet(t *testing.T) {
	cat, _ := Load()
	block, _, err := BuildProviderBlock("omlx", cat.Providers["omlx"], "my-model", "",
		envMap(map[string]string{"OPENAI_API_KEY": "sk-secret"}))
	if err != nil {
		t.Fatalf("BuildProviderBlock: %v", err)
	}
	options := block["options"].(map[string]any)
	if options["apiKey"] != EnvRef("OPENAI_API_KEY") {
		t.Errorf("apiKey = %v, want an env reference", options["apiKey"])
	}
	if options["apiKey"] == "sk-secret" {
		t.Error("the resolved secret must never reach the config")
	}
}

// TestBuildPiProvider_OMLXPlaceholderThenReference pins both sides of the Pi
// rule for oMLX: a keyless local server gets the literal placeholder (Pi hides a
// provider's models until some auth is configured), while a remote endpoint gets
// the $VAR reference Pi resolves at run time.
func TestBuildPiProvider_OMLXPlaceholderThenReference(t *testing.T) {
	cat, _ := Load()
	p := cat.Providers["omlx"]

	local, _, err := BuildPiProvider("omlx", p, "my-model", "", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if local.APIKey != piPlaceholderAPIKey {
		t.Errorf("local apiKey = %q, want %q", local.APIKey, piPlaceholderAPIKey)
	}
	if local.API != "openai-completions" {
		t.Errorf("api = %q, want openai-completions", local.API)
	}

	remote, _, err := BuildPiProvider("omlx", p, "my-model", "http://mac-studio.local:8000/v1", noEnv)
	if err != nil {
		t.Fatalf("BuildPiProvider: %v", err)
	}
	if remote.APIKey != "$OPENAI_API_KEY" {
		t.Errorf("remote apiKey = %q, want $OPENAI_API_KEY", remote.APIKey)
	}
}
