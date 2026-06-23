package main

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
// drift from the opencode config schema (https://opencode.ai/docs/config/):
// the top-level model is "provider/model", so every family's defaultModel must
// be a real model key, custom providers must declare an npm package, and an
// apiKeyPrefix is meaningless without an apiKeyEnv.
func TestCatalogIntegrity(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
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
		if len(p.Families) == 0 {
			t.Errorf("provider %q: no model families", name)
		}
		for fname, fam := range p.Families {
			if len(fam.Models) == 0 {
				t.Errorf("provider %q family %q: no models", name, fname)
			}
			if fam.DefaultModel == "" {
				t.Errorf("provider %q family %q: no defaultModel", name, fname)
				continue
			}
			if _, ok := fam.Models[fam.DefaultModel]; !ok {
				t.Errorf("provider %q family %q: defaultModel %q is not one of its models", name, fname, fam.DefaultModel)
			}
		}
	}
}

// TestMatchFamily checks the reverse lookup used by `outfit export`: a set of
// configured model keys maps back to a family only when it matches exactly.
func TestMatchFamily(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]
	famKeys := p.Families["deepseek-v4"].modelKeys()

	if got := matchFamily(p, famKeys); got != "deepseek-v4" {
		t.Errorf("exact match = %q, want deepseek-v4", got)
	}

	// A superset (all the family's models plus a stray) is not a match.
	if got := matchFamily(p, append(append([]string{}, famKeys...), "stray-model")); got != "" {
		t.Errorf("superset matched %q, want no match", got)
	}

	// A subset (one model short) is not a match either.
	if len(famKeys) > 1 {
		if got := matchFamily(p, famKeys[:len(famKeys)-1]); got != "" {
			t.Errorf("subset matched %q, want no match", got)
		}
	}

	// Unrelated keys match nothing.
	if got := matchFamily(p, []string{"something-else"}); got != "" {
		t.Errorf("unrelated keys matched %q, want no match", got)
	}
}

func TestResolveCatalogPath(t *testing.T) {
	t.Setenv(providersEnv, "/from/env.yaml")
	if got := resolveCatalogPath("/from/flag.yaml"); got != "/from/flag.yaml" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := resolveCatalogPath(""); got != "/from/env.yaml" {
		t.Errorf("env should be used when flag empty, got %q", got)
	}
	t.Setenv(providersEnv, "")
	if got := resolveCatalogPath(""); got != "" {
		t.Errorf("expected empty (embedded), got %q", got)
	}
}

func TestLoadCatalogFrom(t *testing.T) {
	// Embedded fallback.
	if _, err := loadCatalogFrom(""); err != nil {
		t.Fatalf("embedded catalogue: %v", err)
	}

	// Override file.
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	os.WriteFile(path, []byte(`providers:
  mine:
    description: My custom provider
    families:
      base:
        defaultModel: m1
        models:
          m1:
            name: Model One
`), 0o600)

	cat, err := loadCatalogFrom(path)
	if err != nil {
		t.Fatalf("loadCatalogFrom: %v", err)
	}
	if _, ok := cat.Providers["mine"]; !ok {
		t.Error("custom provider not loaded from override file")
	}
	if _, ok := cat.Providers["openrouter"]; ok {
		t.Error("override should replace, not merge with, the embedded catalogue")
	}

	// Missing file is an error.
	if _, err := loadCatalogFrom(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Error("expected error for missing override file")
	}
}

func TestBuildProviderBlock_OpenRouterKeyInjected(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]

	block, model, err := buildProviderBlock("openrouter", p, "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-abc",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
	}
	if want := "openrouter/deepseek/deepseek-v4-flash"; model != want {
		t.Errorf("default model = %q, want %q", model, want)
	}
	opts := block["options"].(map[string]any)
	if opts["apiKey"] != "sk-or-v1-abc" {
		t.Errorf("apiKey = %v, want injected key", opts["apiKey"])
	}
	if models := block["models"].(map[string]any); len(models) != 2 {
		t.Errorf("got %d models, want 2", len(models))
	}
}

func TestBuildProviderBlock_RequiredKeyMissing(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]
	if _, _, err := buildProviderBlock("openrouter", p, "deepseek-v4", "", "", noEnv); err == nil {
		t.Fatal("expected error when required key is missing")
	}
}

func TestBuildProviderBlock_KeyPrefixMismatch(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]
	_, _, err := buildProviderBlock("openrouter", p, "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "wrong-prefix-key",
	}))
	if err == nil || !strings.Contains(err.Error(), "start with") {
		t.Fatalf("expected prefix mismatch error, got %v", err)
	}
}

func TestBuildProviderBlock_BedrockNoKeyRegionFromEnv(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["amazon-bedrock"]

	block, model, err := buildProviderBlock("amazon-bedrock", p, "claude", "", "", envMap(map[string]string{
		"AWS_REGION": "eu-west-2",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
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

func TestBuildProviderBlock_CustomProviderDefaults(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, model, err := buildProviderBlock("ollama", p, "llama", "", "", noEnv)
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
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
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, model, err := buildProviderBlock("ollama", p, "", "my-model", "", noEnv)
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
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

func TestBuildProviderBlock_UnknownFamily(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]
	if _, _, err := buildProviderBlock("openrouter", p, "nope", "", "", noEnv); err == nil {
		t.Fatal("expected error for unknown family")
	}
}

// TestBuildProviderBlock_BaseURLFlagOverrides checks that the --base-url value
// wins over both the catalogue's static baseURL and the per-provider
// optionsFromEnv mapping (here OLLAMA_BASE_URL).
func TestBuildProviderBlock_BaseURLFlagOverrides(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, _, err := buildProviderBlock("ollama", p, "llama", "", "https://flag.example/v1", envMap(map[string]string{
		"OLLAMA_BASE_URL": "https://per-provider.example/v1",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://flag.example/v1" {
		t.Errorf("baseURL = %v, want the --base-url flag value", opts["baseURL"])
	}
}

// TestBuildProviderBlock_BaseURLFromEnv checks that, with no flag, the general
// OUTFIT_BASE_URL env var overrides the catalogue's static baseURL.
func TestBuildProviderBlock_BaseURLFromEnv(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, _, err := buildProviderBlock("ollama", p, "llama", "", "", envMap(map[string]string{
		baseURLEnv: "https://from-env.example/v1",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://from-env.example/v1" {
		t.Errorf("baseURL = %v, want the OUTFIT_BASE_URL value", opts["baseURL"])
	}
}

// TestBuildProviderBlock_BaseURLFlagBeatsEnv checks the precedence: an explicit
// --base-url flag wins over OUTFIT_BASE_URL.
func TestBuildProviderBlock_BaseURLFlagBeatsEnv(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, _, err := buildProviderBlock("ollama", p, "llama", "", "https://flag.example/v1", envMap(map[string]string{
		baseURLEnv: "https://from-env.example/v1",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
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
	cat, _ := loadCatalog()
	p := cat.Providers["openrouter"]

	block, _, err := buildProviderBlock("openrouter", p, "deepseek-v4", "", "https://gateway.example/v1", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-abc",
	}))
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "https://gateway.example/v1" {
		t.Errorf("baseURL = %v, want it injected on a provider without a default", opts["baseURL"])
	}
}

// TestBuildProviderBlock_NoBaseURLOverride confirms the catalogue's static
// baseURL is left untouched when neither flag nor env var is set.
func TestBuildProviderBlock_NoBaseURLOverride(t *testing.T) {
	cat, _ := loadCatalog()
	p := cat.Providers["ollama"]

	block, _, err := buildProviderBlock("ollama", p, "llama", "", "", noEnv)
	if err != nil {
		t.Fatalf("buildProviderBlock: %v", err)
	}
	opts := block["options"].(map[string]any)
	if opts["baseURL"] != "http://localhost:11434/v1" {
		t.Errorf("baseURL = %v, want the catalogue default", opts["baseURL"])
	}
}
