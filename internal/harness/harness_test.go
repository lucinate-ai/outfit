package harness

import (
	"github.com/lucinate-ai/outfit/internal/catalog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/config"
	"github.com/lucinate-ai/outfit/internal/outfit"
)

func TestCommand(t *testing.T) {
	// Each harness launches under its own binary name.
	for name, want := range map[string]string{"opencode": "opencode", "pi": "pi", "lucinate": "lucinate"} {
		h, ok := Lookup(name)
		if !ok {
			t.Fatalf("harness %q not registered", name)
		}
		if got := h.Command(); got != want {
			t.Errorf("%s Command() = %q, want %q", name, got, want)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	// Isolate preference and env from the developer's real config.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(HarnessEnv, "")

	// Default when nothing is set.
	h, source, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if h.Name() != "opencode" || source != "default" {
		t.Errorf("default resolve = %s (%s), want opencode (default)", h.Name(), source)
	}

	// Stored preference beats the default.
	if err := SavePreference("pi"); err != nil {
		t.Fatal(err)
	}
	h, source, _ = Resolve("")
	if h.Name() != "pi" || source != "stored preference" {
		t.Errorf("preference resolve = %s (%s), want pi (stored preference)", h.Name(), source)
	}

	// Env beats the stored preference.
	t.Setenv(HarnessEnv, "opencode")
	h, source, _ = Resolve("")
	if h.Name() != "opencode" || source != HarnessEnv {
		t.Errorf("env resolve = %s (%s), want opencode (%s)", h.Name(), source, HarnessEnv)
	}

	// Flag beats everything.
	h, source, _ = Resolve("pi")
	if h.Name() != "pi" || source != "--harness flag" {
		t.Errorf("flag resolve = %s (%s), want pi (--harness flag)", h.Name(), source)
	}

	// Unknown name errors.
	if _, _, err := Resolve("bogus"); err == nil {
		t.Error("expected error for an unknown harness")
	}
}

func TestPreferenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// No file yet → empty.
	if pref, err := LoadPreference(); err != nil || pref != "" {
		t.Fatalf("LoadPreference (none) = %q, %v; want \"\", nil", pref, err)
	}

	if err := SavePreference("pi"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "outfit", "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}
	if pref, _ := LoadPreference(); pref != "pi" {
		t.Errorf("LoadPreference = %q, want pi", pref)
	}

	// Saving an unknown harness is rejected.
	if err := SavePreference("bogus"); err == nil {
		t.Error("expected error saving an unknown harness")
	}
}

func TestLoadPreference_MalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "outfit", "config.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("{invalid}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPreference(); err == nil {
		t.Error("expected error for malformed config")
	}
}

// TestSavePreferenceKeepsAliases guards the reason the config file moved into
// internal/config: storing the harness rewrites the document, so it must not
// take the alias registry with it.
func TestSavePreferenceKeepsAliases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := config.Update(func(f *config.File) error {
		f.SetAlias("qwen3.6-27b", "/models/qwen/Outfit")
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := SavePreference("pi"); err != nil {
		t.Fatal(err)
	}

	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Alias("qwen3.6-27b"); !ok {
		t.Error("SavePreference dropped the alias registry")
	}
	if f.Harness != "pi" {
		t.Errorf("Harness = %q, want pi", f.Harness)
	}
}

func TestNamesAndLookup(t *testing.T) {
	names := Names()
	if len(names) != 3 || names[0] != "lucinate" || names[1] != "opencode" || names[2] != "pi" {
		t.Errorf("Names = %v, want [lucinate opencode pi]", names)
	}
	if _, ok := Lookup("pi"); !ok {
		t.Error("Lookup(pi) should succeed")
	}
	if _, ok := Lookup("lucinate"); !ok {
		t.Error("Lookup(lucinate) should succeed")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) should fail")
	}
}

// A config written with no key for a remote endpoint succeeds and then fails on
// the first request, so it has to be called out at the time.
func TestMissingKeyWarning(t *testing.T) {
	keyed := &catalog.Provider{APIKeyEnv: "OPENAI_API_KEY"}
	unset := func(string) string { return "" }
	set := func(string) string { return "sk-test" }

	if w := missingKeyWarning(keyed, "http://198.51.100.1:8000/v1", unset); w == "" {
		t.Error("a remote endpoint with no key should warn")
	} else if !strings.Contains(w, "OPENAI_API_KEY") {
		t.Errorf("the warning should name the variable to set, got %q", w)
	}

	if w := missingKeyWarning(keyed, "http://198.51.100.1:8000/v1", set); w != "" {
		t.Errorf("a key that is set should not warn, got %q", w)
	}
	if w := missingKeyWarning(keyed, "http://127.0.0.1:8080/v1", unset); w != "" {
		t.Errorf("a local server needs no key, got %q", w)
	}
	if w := missingKeyWarning(&catalog.Provider{}, "https://api.example.com/v1", unset); w != "" {
		t.Errorf("a provider with no key variable should not warn, got %q", w)
	}
}

// TestLucinateResolves confirms the lucinate harness is selectable like the
// others.
func TestLucinateResolves(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(HarnessEnv, "")
	h, source, err := Resolve("lucinate")
	if err != nil {
		t.Fatal(err)
	}
	if h.Name() != "lucinate" || source != "--harness flag" {
		t.Errorf("resolve = %s (%s), want lucinate (--harness flag)", h.Name(), source)
	}
	if err := SavePreference("lucinate"); err != nil {
		t.Fatalf("SavePreference(lucinate): %v", err)
	}
}

// TestLucinateApplyStateRemove round-trips a selection through the lucinate
// adapter.
func TestLucinateApplyStateRemove(t *testing.T) {
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())

	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := Lookup("lucinate")
	resolve := func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-or-v1-test"
		}
		return ""
	}
	sel := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}

	sum, err := h.Apply(cat.Providers["openrouter"], sel, 0, 0, resolve)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if sum.DefaultModel != "deepseek/deepseek-v4-pro" {
		t.Errorf("DefaultModel = %q", sum.DefaultModel)
	}
	// The key note must mention the launch-time variable, never the secret.
	joined := strings.Join(sum.Notes, "\n")
	if !strings.Contains(joined, "LUCINATE_OPENAI_API_KEY") {
		t.Errorf("notes should mention LUCINATE_OPENAI_API_KEY, got %q", joined)
	}
	if strings.Contains(joined, "sk-or-v1-test") {
		t.Error("the resolved secret must never appear in the notes")
	}

	states, def, err := h.State()
	if err != nil {
		t.Fatal(err)
	}
	if def != "" {
		t.Errorf("lucinate has no top-level default model, got %q", def)
	}
	st, ok := states["openrouter"]
	if !ok || st.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("state = %+v, want openrouter with its base URL", states)
	}

	n, err := h.Remove("openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v; want 1, nil", n, err)
	}
}

// TestLucinateRejectsNonOpenAIProvider confirms a provider without a lucinate
// marker cannot be applied under the lucinate harness.
func TestLucinateRejectsNonOpenAIProvider(t *testing.T) {
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())
	cat, _ := catalog.Load()
	h, _ := Lookup("lucinate")
	sel := outfit.Selection{Provider: "amazon-bedrock", Model: "some-model"}
	if _, err := h.Apply(cat.Providers["amazon-bedrock"], sel, 0, 0, func(string) string { return "" }); err == nil {
		t.Error("expected amazon-bedrock to be unsupported by lucinate")
	}
}

// TestOpencodeApplyStateRemove round-trips a selection through the opencode
// adapter, verifying the config is written, readable, and removable.
func TestOpencodeApplyStateRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := Lookup("opencode")
	resolve := func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-or-v1-test"
		}
		return ""
	}
	sel := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}

	sum, err := h.Apply(cat.Providers["openrouter"], sel, 0, 0, resolve)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if sum.DefaultModel != "openrouter/deepseek/deepseek-v4-pro" {
		t.Errorf("DefaultModel = %q", sum.DefaultModel)
	}

	states, def, err := h.State()
	if err != nil {
		t.Fatal(err)
	}
	if def != "openrouter/deepseek/deepseek-v4-pro" {
		t.Errorf("defaultModel = %q", def)
	}
	st, ok := states["openrouter"]
	if !ok {
		t.Fatalf("state = %+v, want openrouter present", states)
	}
	if len(st.ModelKeys) != 1 || st.ModelKeys[0] != "deepseek/deepseek-v4-pro" {
		t.Errorf("model keys = %v, want [deepseek/deepseek-v4-pro]", st.ModelKeys)
	}

	n, err := h.Remove("openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v; want 1, nil", n, err)
	}
}

// TestOpencodeApplyWithContextSize verifies that context and output limits are
// propagated through the adapter into the opencode config and readable back.
func TestOpencodeApplyWithContextSize(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := Lookup("opencode")
	resolve := func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-or-v1-test"
		}
		return ""
	}
	sel := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}

	_, err = h.Apply(cat.Providers["openrouter"], sel, 128000, 32000, resolve)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	states, _, err := h.State()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states["openrouter"]
	if !ok {
		t.Fatal("openrouter not in state")
	}
	key := st.ModelKeys[0]
	if st.Contexts[key] != 128000 {
		t.Errorf("context = %d, want 128000", st.Contexts[key])
	}
	if st.Outputs[key] != 32000 {
		t.Errorf("output = %d, want 32000", st.Outputs[key])
	}
}

// TestOpencodeRemoveModelKey removes a single model from a multi-model provider
// without deleting the provider block or other models.
func TestOpencodeRemoveModelKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cat, _ := catalog.Load()
	h, _ := Lookup("opencode")
	resolve := func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-or-v1-test"
		}
		return ""
	}

	// Apply two models.
	sel1 := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro", Alias: "v4"}
	sel2 := outfit.Selection{Provider: "openrouter", Model: "qwen3.6-27b", Alias: "qwen"}
	h.Apply(cat.Providers["openrouter"], sel1, 0, 0, resolve)
	h.Apply(cat.Providers["openrouter"], sel2, 0, 0, resolve)

	// Remove just "v4".
	n, err := h.Remove("openrouter", []string{"v4"})
	if err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v; want 1, nil", n, err)
	}

	states, _, err := h.State()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states["openrouter"]
	if !ok {
		t.Fatal("openrouter should still exist")
	}
	if len(st.ModelKeys) != 1 || st.ModelKeys[0] != "qwen" {
		t.Errorf("ModelKeys = %v, want [qwen]", st.ModelKeys)
	}
}

// TestPiApplyStateRemove round-trips a selection through the Pi adapter.
func TestPiApplyStateRemove(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := Lookup("pi")
	resolve := func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-or-v1-test"
		}
		return ""
	}
	sel := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}

	sum, err := h.Apply(cat.Providers["openrouter"], sel, 0, 0, resolve)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Pi has no default model — Summary.DefaultModel should be empty.
	if sum.DefaultModel != "" {
		t.Errorf("Pi DefaultModel = %q, want empty", sum.DefaultModel)
	}

	states, def, err := h.State()
	if err != nil {
		t.Fatal(err)
	}
	if def != "" {
		t.Errorf("Pi should have no top-level default model, got %q", def)
	}
	st, ok := states["openrouter"]
	if !ok || st.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("state = %+v, want openrouter with its base URL", states)
	}

	n, err := h.Remove("openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v; want 1, nil", n, err)
	}
}

// TestModelKey verifies the alias takes precedence over the model name.
func TestModelKey(t *testing.T) {
	sel := outfit.Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}
	if got := modelKey(sel); got != "deepseek/deepseek-v4-pro" {
		t.Errorf("no alias: modelKey = %q, want deepseek/deepseek-v4-pro", got)
	}

	sel.Alias = "my-model"
	if got := modelKey(sel); got != "my-model" {
		t.Errorf("with alias: modelKey = %q, want my-model", got)
	}
}
