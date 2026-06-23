package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

// readConfigMap reads a config file, standardises the JSONC, and unmarshals it
// into a map for assertions. It also fails the test if the file is not valid
// JSONC or not standardisable to JSON, which guards the output shape.
func readConfigMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := hujson.Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	v.Standardize()
	var m map[string]any
	if err := json.Unmarshal(v.Pack(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return m
}

func sampleBlock(key string) map[string]any {
	return map[string]any{
		"options": map[string]any{"apiKey": "secret"},
		"models":  map[string]any{key: map[string]any{"name": key}},
	}
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]any{
		"a":      1,
		"nested": map[string]any{"keep": true, "override": "old"},
	}
	src := map[string]any{
		"b":      2,
		"nested": map[string]any{"override": "new", "added": 3},
	}
	got := deepMerge(dst, src)
	want := map[string]any{
		"a":      1,
		"b":      2,
		"nested": map[string]any{"keep": true, "override": "new", "added": 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deepMerge = %v, want %v", got, want)
	}
	// dst must not be mutated.
	if dst["nested"].(map[string]any)["override"] != "old" {
		t.Error("deepMerge mutated dst")
	}
}

func TestJSONPointerEscape(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"a/b":            "a~1b",
		"a~b":            "a~0b",
		"deepseek/v4~x":  "deepseek~1v4~0x",
		"amazon-bedrock": "amazon-bedrock",
	}
	for in, want := range cases {
		if got := jsonPointerEscape(in); got != want {
			t.Errorf("escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadEnvFileVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("# comment\nFOO=bar\nQUOTED=\"baz qux\"\n  SPACED = ignored-no-prefix\n"), 0o600)

	if got := readEnvFileVar(path, "FOO"); got != "bar" {
		t.Errorf("FOO = %q, want bar", got)
	}
	if got := readEnvFileVar(path, "QUOTED"); got != "baz qux" {
		t.Errorf("QUOTED = %q, want 'baz qux'", got)
	}
	if got := readEnvFileVar(path, "MISSING"); got != "" {
		t.Errorf("MISSING = %q, want empty", got)
	}
	if got := readEnvFileVar(filepath.Join(dir, "nope"), "FOO"); got != "" {
		t.Errorf("missing file should yield empty, got %q", got)
	}
}

func TestWriteConfig_FreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	m := readConfigMap(t, path)
	if m["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("missing/incorrect $schema: %v", m["$schema"])
	}
	if m["model"] != "openrouter/m1" {
		t.Errorf("model = %v", m["model"])
	}
	or := m["provider"].(map[string]any)["openrouter"].(map[string]any)
	if or["options"].(map[string]any)["apiKey"] != "secret" {
		t.Error("apiKey not written")
	}

	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}
}

func TestWriteConfig_PreservesExistingAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	seed := `{
  // keep this comment
  "theme": "tokyonight",
  "provider": {
    "anthropic": { "models": { "claude": { "name": "Claude" } } }
  }
}`
	os.WriteFile(path, []byte(seed), 0o600)

	if err := WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "// keep this comment") {
		t.Error("comment was not preserved")
	}

	m := readConfigMap(t, path)
	if m["theme"] != "tokyonight" {
		t.Error("theme not preserved")
	}
	providers := m["provider"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Error("existing anthropic provider was dropped")
	}
	if _, ok := providers["openrouter"]; !ok {
		t.Error("openrouter provider not added")
	}
}

func TestWriteConfig_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	for i := 0; i < 2; i++ {
		if err := WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1"); err != nil {
			t.Fatalf("WriteConfig run %d: %v", i, err)
		}
	}
	first, _ := os.ReadFile(path)
	if err := WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestWriteConfig_DeepMergesProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	// First add with model a.
	WriteConfig(path, "openrouter", map[string]any{
		"options": map[string]any{"apiKey": "k", "custom": "keepme"},
		"models":  map[string]any{"a": map[string]any{"name": "A"}},
	}, "openrouter/a")
	// Second add with model b; existing custom option and model a must survive.
	WriteConfig(path, "openrouter", map[string]any{
		"models": map[string]any{"b": map[string]any{"name": "B"}},
	}, "openrouter/b")

	or := readConfigMap(t, path)["provider"].(map[string]any)["openrouter"].(map[string]any)
	models := or["models"].(map[string]any)
	if _, ok := models["a"]; !ok {
		t.Error("model a was dropped on second add")
	}
	if _, ok := models["b"]; !ok {
		t.Error("model b not added")
	}
	if or["options"].(map[string]any)["custom"] != "keepme" {
		t.Error("custom option not preserved across merge")
	}
}

func TestRemoveConfig_WholeProviderClearsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1")

	n, err := RemoveConfig(path, "openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("RemoveConfig = %d, %v; want 1, nil", n, err)
	}
	m := readConfigMap(t, path)
	if _, ok := m["provider"].(map[string]any)["openrouter"]; ok {
		t.Error("provider not removed")
	}
	if _, ok := m["model"]; ok {
		t.Error("default model should have been cleared")
	}
}

func TestRemoveConfig_ModelKeysWithSlash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	WriteConfig(path, "openrouter", map[string]any{
		"models": map[string]any{
			"deepseek/flash": map[string]any{"name": "Flash"},
			"deepseek/pro":   map[string]any{"name": "Pro"},
		},
	}, "openrouter/deepseek/flash")

	// Remove the non-default model; default must remain.
	n, err := RemoveConfig(path, "openrouter", []string{"deepseek/pro"})
	if err != nil || n != 1 {
		t.Fatalf("RemoveConfig = %d, %v; want 1, nil", n, err)
	}
	m := readConfigMap(t, path)
	models := m["provider"].(map[string]any)["openrouter"].(map[string]any)["models"].(map[string]any)
	if _, ok := models["deepseek/pro"]; ok {
		t.Error("deepseek/pro not removed")
	}
	if _, ok := models["deepseek/flash"]; !ok {
		t.Error("deepseek/flash should remain")
	}
	if m["model"] != "openrouter/deepseek/flash" {
		t.Errorf("default model changed unexpectedly: %v", m["model"])
	}

	// Remove the default model; default must be cleared.
	if _, err := RemoveConfig(path, "openrouter", []string{"deepseek/flash"}); err != nil {
		t.Fatal(err)
	}
	m = readConfigMap(t, path)
	if _, ok := m["model"]; ok {
		t.Error("default model should be cleared after removing it")
	}
}

func TestRemoveConfig_NoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	WriteConfig(path, "openrouter", sampleBlock("m1"), "openrouter/m1")

	n, err := RemoveConfig(path, "does-not-exist", nil)
	if err != nil || n != 0 {
		t.Fatalf("RemoveConfig = %d, %v; want 0, nil", n, err)
	}
}

func TestResolveConfigFile(t *testing.T) {
	// Prefers an existing .jsonc.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "opencode"), 0o755)
	jsonc := filepath.Join(dir, "opencode", "opencode.jsonc")
	os.WriteFile(jsonc, []byte("{}"), 0o600)
	got, err := ResolveConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if got != jsonc {
		t.Errorf("got %q, want existing %q", got, jsonc)
	}

	// Defaults to opencode.json when none exist.
	dir2 := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir2)
	got, err = ResolveConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir2, "opencode", "opencode.json"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
