package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	w.Close()

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestRunDispatch(t *testing.T) {
	if err := run(nil); err != nil {
		t.Errorf("no args should print usage, got %v", err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Errorf("help should not error, got %v", err)
	}
	if err := run([]string{"bogus"}); err == nil {
		t.Error("unknown command should error")
	}
}

func TestParseSelection(t *testing.T) {
	// Long flags.
	s, err := parseSelection("add", []string{"--provider", "openrouter", "--model-family", "deepseek-v4", "--model", "m"})
	if err != nil {
		t.Fatal(err)
	}
	if s.provider != "openrouter" || s.family != "deepseek-v4" || s.model != "m" {
		t.Errorf("long flags parsed wrong: %+v", s)
	}

	// Short flags.
	s, err = parseSelection("add", []string{"-p", "ollama", "-f", "llama", "-m", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.provider != "ollama" || s.family != "llama" || s.model != "x" {
		t.Errorf("short flags parsed wrong: %+v", s)
	}

	// Missing provider.
	if _, err := parseSelection("add", []string{"-f", "llama"}); err == nil {
		t.Error("expected error when --provider is missing")
	}

	// Base URL flag, long and short forms.
	s, err = parseSelection("add", []string{"-p", "ollama", "--base-url", "https://long.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.baseURL != "https://long.example/v1" {
		t.Errorf("--base-url parsed wrong: %q", s.baseURL)
	}
	s, err = parseSelection("add", []string{"-p", "ollama", "-b", "https://short.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.baseURL != "https://short.example/v1" {
		t.Errorf("-b parsed wrong: %q", s.baseURL)
	}
}

func TestCmdAdd_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "Default model:") {
		t.Errorf("missing summary in output:\n%s", out)
	}

	path := filepath.Join(dir, "opencode", "opencode.json")
	m := readConfigMap(t, path)
	if _, ok := m["provider"].(map[string]any)["openrouter"]; !ok {
		t.Error("openrouter provider not written")
	}
	if m["model"] != "openrouter/deepseek/deepseek-v4-flash" {
		t.Errorf("model = %v", m["model"])
	}
}

func TestCmdAdd_BaseURLOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openai-compatible", "-f", "gpt", "-b", "https://proxy.example/v1"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "Base URL: https://proxy.example/v1") {
		t.Errorf("missing base URL in summary:\n%s", out)
	}

	path := filepath.Join(dir, "opencode", "opencode.json")
	m := readConfigMap(t, path)
	prov := m["provider"].(map[string]any)["openai-compatible"].(map[string]any)
	opts := prov["options"].(map[string]any)
	if opts["baseURL"] != "https://proxy.example/v1" {
		t.Errorf("baseURL = %v, want the flag override written to config", opts["baseURL"])
	}
}

func TestCmdAdd_Errors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := cmdAdd([]string{"-p", "openrouter"}); err == nil {
		t.Error("expected error when neither family nor model given")
	}
	if err := cmdAdd([]string{"-p", "bogus", "-f", "x"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestCmdRemove_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := resolveConfigFile()
	if err != nil {
		t.Fatal(err)
	}

	// Seed a family, then remove the whole provider via the CLI.
	cat, _ := loadCatalog()
	block, dm, _ := buildProviderBlock("openrouter", cat.Providers["openrouter"], "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-x",
	}))
	if err := writeConfig(path, "openrouter", block, dm); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "openrouter"}); err != nil {
			t.Fatalf("cmdRemove: %v", err)
		}
	})
	if !strings.Contains(out, "Removed provider") {
		t.Errorf("unexpected output:\n%s", out)
	}
	m := readConfigMap(t, path)
	if _, ok := m["provider"].(map[string]any)["openrouter"]; ok {
		t.Error("provider should have been removed")
	}
}

func TestCmdRemove_FamilyAndNoOp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, _ := resolveConfigFile()

	cat, _ := loadCatalog()
	block, dm, _ := buildProviderBlock("openrouter", cat.Providers["openrouter"], "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-x",
	}))
	writeConfig(path, "openrouter", block, dm)

	// Removing the family should clear all its models (and the default model,
	// which pointed at one of them).
	captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "openrouter", "-f", "deepseek-v4"}); err != nil {
			t.Fatalf("cmdRemove family: %v", err)
		}
	})
	m := readConfigMap(t, path)
	or := m["provider"].(map[string]any)["openrouter"].(map[string]any)
	if models, ok := or["models"].(map[string]any); ok && len(models) != 0 {
		t.Errorf("family models not removed: %v", models)
	}

	// A second removal is a no-op.
	out := captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "openrouter", "-f", "deepseek-v4"}); err != nil {
			t.Fatalf("cmdRemove no-op: %v", err)
		}
	})
	if !strings.Contains(out, "Nothing to remove") {
		t.Errorf("expected no-op message, got:\n%s", out)
	}

	// Unknown family is an error.
	if err := cmdRemove([]string{"-p", "openrouter", "-f", "nope"}); err == nil {
		t.Error("expected error for unknown family")
	}
}

func TestCmdList_ProvidersOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	os.WriteFile(path, []byte(`providers:
  mine:
    description: My custom provider
    families:
      base:
        defaultModel: m1
        models:
          m1: {name: Model One}
`), 0o600)

	// Via the --providers flag.
	out := captureStdout(t, func() {
		if err := cmdList([]string{"--providers", path}); err != nil {
			t.Fatalf("cmdList: %v", err)
		}
	})
	if !strings.Contains(out, "mine") || strings.Contains(out, "openrouter") {
		t.Errorf("flag override not honoured:\n%s", out)
	}

	// Via the env var.
	t.Setenv(providersEnv, path)
	out = captureStdout(t, func() {
		if err := cmdList(nil); err != nil {
			t.Fatalf("cmdList: %v", err)
		}
	})
	if !strings.Contains(out, "mine") {
		t.Errorf("env override not honoured:\n%s", out)
	}
}

func TestCmdList(t *testing.T) {
	out := captureStdout(t, func() {
		if err := cmdList(nil); err != nil {
			t.Fatalf("cmdList: %v", err)
		}
	})
	for _, want := range []string{"openrouter", "amazon-bedrock", "family", "default:"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}
