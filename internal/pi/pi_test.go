package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucinate-ai/outfit/internal/catalog"
)

// readModels reads and unmarshals models.json for assertions.
func readModels(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("models.json is not valid JSON: %v", err)
	}
	return m
}

func sampleProvider() catalog.PiProvider {
	return catalog.PiProvider{
		BaseURL: "https://openrouter.ai/api/v1",
		API:     "openai-completions",
		APIKey:  "$DEEPSEEK_API_KEY",
		Models: []catalog.PiModel{
			{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash"},
			{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
		},
	}
}

func TestConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".pi", "agent", "models.json"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestWrite_FreshFileWithContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := Write("openrouter", sampleProvider(), 128000, 32000); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := filepath.Join(dir, ".pi", "agent", "models.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}

	prov := readModels(t, path)["providers"].(map[string]any)["openrouter"].(map[string]any)
	if prov["api"] != "openai-completions" {
		t.Errorf("api = %v", prov["api"])
	}
	if prov["apiKey"] != "$DEEPSEEK_API_KEY" {
		t.Errorf("apiKey = %v, want the env interpolation", prov["apiKey"])
	}
	models := prov["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	for _, m := range models {
		mm := m.(map[string]any)
		if mm["contextWindow"] != float64(128000) {
			t.Errorf("model %v contextWindow = %v, want 128000", mm["id"], mm["contextWindow"])
		}
		if mm["maxTokens"] != float64(32000) {
			t.Errorf("model %v maxTokens = %v, want 32000", mm["id"], mm["maxTokens"])
		}
	}
}

func TestWrite_PreservesOtherProvidersAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".pi", "agent", "models.json")

	// Seed a file with another provider and an unknown top-level key.
	os.MkdirAll(filepath.Dir(path), 0o700)
	seed := `{
  "theme": "dark",
  "providers": {
    "anthropic": { "api": "anthropic-messages", "models": [ {"id": "claude"} ] },
    "openrouter": {
      "api": "openai-completions",
      "headers": { "X-Custom": "keep" },
      "models": [ {"id": "deepseek/deepseek-v4-flash", "name": "old"}, {"id": "extra-model"} ]
    }
  }
}`
	os.WriteFile(path, []byte(seed), 0o600)

	if err := Write("openrouter", sampleProvider(), 0, 0); err != nil {
		t.Fatalf("Write: %v", err)
	}

	root := readModels(t, path)
	if root["theme"] != "dark" {
		t.Error("unknown top-level key not preserved")
	}
	providers := root["providers"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Error("sibling provider was dropped")
	}
	or := providers["openrouter"].(map[string]any)
	if hdr, ok := or["headers"].(map[string]any); !ok || hdr["X-Custom"] != "keep" {
		t.Error("unknown provider field (headers) not preserved")
	}
	// Models are unioned by id: extra-model survives, the seeded flash entry is
	// replaced by ours (name updated), pro is added → 3 distinct ids.
	ids := map[string]string{}
	for _, m := range or["models"].([]any) {
		mm := m.(map[string]any)
		name, _ := mm["name"].(string)
		ids[mm["id"].(string)] = name
	}
	if len(ids) != 3 {
		t.Errorf("got model ids %v, want 3 distinct", ids)
	}
	if _, ok := ids["extra-model"]; !ok {
		t.Error("user's extra-model was dropped")
	}
	if ids["deepseek/deepseek-v4-flash"] != "DeepSeek V4 Flash" {
		t.Errorf("flash name = %q, want our entry to win", ids["deepseek/deepseek-v4-flash"])
	}
}

func TestRemove_ModelsAndWholeProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := Write("openrouter", sampleProvider(), 0, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".pi", "agent", "models.json")

	// Remove one model.
	n, err := Remove("openrouter", []string{"deepseek/deepseek-v4-pro"})
	if err != nil || n != 1 {
		t.Fatalf("Remove model = %d, %v; want 1, nil", n, err)
	}
	models := readModels(t, path)["providers"].(map[string]any)["openrouter"].(map[string]any)["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != "deepseek/deepseek-v4-flash" {
		t.Errorf("after model remove, models = %v", models)
	}

	// Removing a non-existent model is a no-op.
	if n, _ := Remove("openrouter", []string{"nope"}); n != 0 {
		t.Errorf("removing missing model returned %d, want 0", n)
	}

	// Remove the whole provider.
	n, err = Remove("openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("Remove provider = %d, %v; want 1, nil", n, err)
	}
	if _, ok := readModels(t, path)["providers"].(map[string]any)["openrouter"]; ok {
		t.Error("provider should have been removed")
	}

	// Removing from a missing provider is a no-op.
	if n, _ := Remove("openrouter", nil); n != 0 {
		t.Errorf("removing missing provider returned %d, want 0", n)
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := Write("openrouter", sampleProvider(), 200000, 50000); err != nil {
		t.Fatal(err)
	}
	states, err := State()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states["openrouter"]
	if !ok {
		t.Fatal("openrouter missing from state")
	}
	if st.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("baseURL = %q", st.BaseURL)
	}
	if len(st.ModelKeys) != 2 {
		t.Errorf("model keys = %v, want 2", st.ModelKeys)
	}
	for _, k := range st.ModelKeys {
		if st.Contexts[k] != 200000 {
			t.Errorf("context for %q = %d, want 200000", k, st.Contexts[k])
		}
		if st.Outputs[k] != 50000 {
			t.Errorf("output for %q = %d, want 50000", k, st.Outputs[k])
		}
	}
}

func TestState_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	states, err := State()
	if err != nil {
		t.Fatalf("State on missing file: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty state, got %v", states)
	}
}
