package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/tailscale/hujson"
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

// readConfigMap reads a config file, standardises the JSONC, and unmarshals it
// into a map for assertions.
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

// noEnv is a resolver that finds nothing.
func noEnv(string) string { return "" }

// envMap returns a resolver backed by a map.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
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

func TestVersionFlag(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		out := captureStdout(t, func() {
			if err := run([]string{arg}); err != nil {
				t.Fatalf("run(%q): %v", arg, err)
			}
		})
		if strings.TrimSpace(out) != version {
			t.Errorf("run(%q) printed %q, want %q", arg, strings.TrimSpace(out), version)
		}
	}
}

func TestParseSelection(t *testing.T) {
	// Long flags.
	s, _, err := parseSelection("add", []string{"--provider", "openrouter", "--model-family", "deepseek-v4", "--model", "m"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "openrouter" || s.Family != "deepseek-v4" || s.Model != "m" {
		t.Errorf("long flags parsed wrong: %+v", s)
	}

	// Short flags.
	s, _, err = parseSelection("add", []string{"-p", "ollama", "-f", "llama", "-m", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "ollama" || s.Family != "llama" || s.Model != "x" {
		t.Errorf("short flags parsed wrong: %+v", s)
	}

	// Missing provider.
	if _, _, err := parseSelection("add", []string{"-f", "llama"}); err == nil {
		t.Error("expected error when --provider is missing")
	}

	// Base URL flag, long and short forms.
	s, _, err = parseSelection("add", []string{"-p", "ollama", "--base-url", "https://long.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseURL != "https://long.example/v1" {
		t.Errorf("--base-url parsed wrong: %q", s.BaseURL)
	}
	s, _, err = parseSelection("add", []string{"-p", "ollama", "-u", "https://short.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseURL != "https://short.example/v1" {
		t.Errorf("-u parsed wrong: %q", s.BaseURL)
	}

	// Context flag, long and short.
	s, _, err = parseSelection("add", []string{"-p", "ollama", "--context", "128k"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Context != "128k" {
		t.Errorf("--context parsed wrong: %+v", s)
	}
	s, _, err = parseSelection("add", []string{"-p", "ollama", "-c", "200000"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Context != "200000" {
		t.Errorf("-c parsed wrong: %+v", s)
	}

	// Output flag, long and short.
	s, _, err = parseSelection("add", []string{"-p", "ollama", "--output", "32k"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Output != "32k" {
		t.Errorf("--output parsed wrong: %+v", s)
	}
	s, _, err = parseSelection("add", []string{"-p", "ollama", "-o", "16000"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Output != "16000" {
		t.Errorf("-o parsed wrong: %+v", s)
	}

	// Harness flag is returned separately and never leaks into the Selection.
	for _, args := range [][]string{
		{"-p", "ollama", "-f", "llama", "--harness", "pi"},
		{"-p", "ollama", "-f", "llama", "-H", "pi"},
	} {
		_, h, err := parseSelection("add", args)
		if err != nil {
			t.Fatal(err)
		}
		if h != "pi" {
			t.Errorf("harness flag parsed wrong for %v: %q", args, h)
		}
	}
}

func TestCmdAdd_ContextSize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "Context window: 128000 tokens") {
		t.Errorf("missing context summary in output:\n%s", out)
	}
	// With no --output given, output defaults to a quarter of the context.
	if !strings.Contains(out, "Max output: 32000 tokens") {
		t.Errorf("missing default output summary in output:\n%s", out)
	}

	path := filepath.Join(dir, "opencode", "opencode.json")
	models := readConfigMap(t, path)["provider"].(map[string]any)["openrouter"].(map[string]any)["models"].(map[string]any)
	for key, m := range models {
		limit, ok := m.(map[string]any)["limit"].(map[string]any)
		if !ok {
			t.Fatalf("model %q has no limit block: %v", key, m)
		}
		// JSON round-trips numbers as float64.
		if limit["context"] != float64(128000) {
			t.Errorf("model %q context = %v, want 128000", key, limit["context"])
		}
		// opencode requires limit.output whenever limit.context is set.
		if limit["output"] != float64(32000) {
			t.Errorf("model %q output = %v, want 32000", key, limit["output"])
		}
	}
}

// TestCmdAdd_OutputSize covers an explicit --output: it is written verbatim and
// is not derived from the context.
func TestCmdAdd_OutputSize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-c", "128k", "-o", "64k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "Max output: 64000 tokens") {
		t.Errorf("missing explicit output summary:\n%s", out)
	}

	path := filepath.Join(dir, "opencode", "opencode.json")
	models := readConfigMap(t, path)["provider"].(map[string]any)["openrouter"].(map[string]any)["models"].(map[string]any)
	for key, m := range models {
		limit := m.(map[string]any)["limit"].(map[string]any)
		if limit["output"] != float64(64000) {
			t.Errorf("model %q output = %v, want 64000", key, limit["output"])
		}
	}
}

// TestCmdAdd_OutputErrors covers the two ways an output limit is rejected:
// without a context to sit under, and when it exceeds that context.
func TestCmdAdd_OutputErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-o", "32k"}); err == nil {
		t.Error("expected error for --output without --context")
	}
	if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-c", "8k", "-o", "32k"}); err == nil {
		t.Error("expected error for an output limit exceeding the context")
	}
}

func TestCmdAdd_ContextSizeInvalid(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")
	if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-c", "not-a-size"}); err == nil {
		t.Error("expected error for an unparseable context size")
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
		if err := cmdAdd([]string{"-p", "openai-compatible", "-f", "gpt", "-u", "https://proxy.example/v1"}); err != nil {
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
	path, err := opencode.ResolveConfigFile()
	if err != nil {
		t.Fatal(err)
	}

	// Seed a family, then remove the whole provider via the CLI.
	cat, _ := catalog.Load()
	block, dm, _ := catalog.BuildProviderBlock("openrouter", cat.Providers["openrouter"], "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-x",
	}))
	if err := opencode.WriteConfig(path, "openrouter", block, dm); err != nil {
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
	path, _ := opencode.ResolveConfigFile()

	cat, _ := catalog.Load()
	block, dm, _ := catalog.BuildProviderBlock("openrouter", cat.Providers["openrouter"], "deepseek-v4", "", "", envMap(map[string]string{
		"DEEPSEEK_API_KEY": "sk-or-v1-x",
	}))
	opencode.WriteConfig(path, "openrouter", block, dm)

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
	t.Setenv(catalog.ProvidersEnv, path)
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

func TestCmdInitProviders_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	out := captureStdout(t, func() {
		if err := cmdInitProviders([]string{path}); err != nil {
			t.Fatalf("cmdInitProviders: %v", err)
		}
	})
	if !strings.Contains(out, "Wrote "+path) {
		t.Errorf("missing confirmation in output:\n%s", out)
	}

	// The written file must be byte-for-byte the embedded catalogue.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, catalog.EmbeddedYAML()) {
		t.Error("written providers.yaml does not match the embedded catalogue")
	}

	// And it must load as a catalogue via the --providers path.
	if _, err := catalog.LoadFrom(path); err != nil {
		t.Errorf("written catalogue does not parse: %v", err)
	}
}

func TestCmdInitProviders_NoClobber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")
	if err := os.WriteFile(path, []byte("# do not touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Without --force, an existing file is left untouched and the command errors.
	if err := cmdInitProviders([]string{path}); err == nil {
		t.Error("expected an error when the target file already exists")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# do not touch\n" {
		t.Errorf("existing file was clobbered: %q", got)
	}

	// With --force, it is overwritten with the embedded catalogue.
	captureStdout(t, func() {
		if err := cmdInitProviders([]string{"--force", path}); err != nil {
			t.Fatalf("cmdInitProviders --force: %v", err)
		}
	})
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, catalog.EmbeddedYAML()) {
		t.Error("--force did not overwrite with the embedded catalogue")
	}
}

func TestCmdInitProviders_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	captureStdout(t, func() {
		if err := cmdInitProviders(nil); err != nil {
			t.Fatalf("cmdInitProviders: %v", err)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "providers.yaml")); err != nil {
		t.Errorf("default providers.yaml not written: %v", err)
	}
}
