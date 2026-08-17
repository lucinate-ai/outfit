package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/preset"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// subcommandFor builds the binary/subcommand/positional piece. A positional model
// rides after the subcommand, appending must not alias the engine's own slice.
func TestSubcommandFor(t *testing.T) {
	eng := serveEngine{
		binary:     func() string { return "the-engine" },
		subcommand: []string{"serve"},
	}

	// No positional hook: the subcommand stands alone.
	if got := subcommandFor(eng, outfit.Selection{Model: "org/model"}); !reflect.DeepEqual(got, []string{"serve"}) {
		t.Errorf("subcommandFor without positional = %v, want [serve]", got)
	}

	// A positional hook appends the model and leaves the engine's slice intact.
	pos := func(sel outfit.Selection) []string {
		if sel.Model == "" {
			return nil
		}
		return []string{sel.Model}
	}
	eng = serveEngine{binary: eng.binary, subcommand: eng.subcommand, positional: pos}
	if got := subcommandFor(eng, outfit.Selection{Model: "org/model"}); !reflect.DeepEqual(got, []string{"serve", "org/model"}) {
		t.Errorf("subcommandFor with positional = %v, want [serve org/model]", got)
	}
	// The engine's own subcommand slice must not have grown to take the positional.
	if !reflect.DeepEqual(eng.subcommand, []string{"serve"}) {
		t.Errorf("subcommandFor mutated engine.subcommand to %v", eng.subcommand)
	}
}

// assembleEngineArgv is the single argv shape both the preset-less serve path and
// the daemon's deploy-config path draw from: binary, subcommand, dialect flags,
// then trailing args.
func TestAssembleEngineArgv(t *testing.T) {
	cases := []struct {
		name     string
		engine   serveEngine
		sel      outfit.Selection
		params   []preset.Param
		trailing []string
		want     []string
	}{
		{
			name:   "plain engine, no subcommand",
			engine: serveEngine{binary: func() string { return "the-engine" }, dialect: preset.LlamaCpp},
			sel:    outfit.Selection{Model: "org/model"},
			params: []preset.Param{{Key: "ctx-size", Value: "8192"}},
			want:   []string{"the-engine", "--ctx-size", "8192"},
		},
		{
			name:   "subcommand with positional model",
			engine: serveEngine{binary: func() string { return "the-engine" }, subcommand: []string{"serve"}, dialect: preset.LlamaCpp, positional: func(sel outfit.Selection) []string { return []string{sel.Model} }},
			sel:    outfit.Selection{Model: "org/model"},
			params: []preset.Param{{Key: "max-model-len", Value: "4096"}},
			want:   []string{"the-engine", "serve", "org/model", "--max-model-len", "4096"},
		},
		{
			name:     "trailing args ride after the dialect flags",
			engine:   serveEngine{binary: func() string { return "the-engine" }, subcommand: []string{"serve"}, dialect: preset.LlamaCpp},
			sel:      outfit.Selection{},
			params:   []preset.Param{{Key: "alias", Value: "friend"}},
			trailing: []string{"--gpu-memory-utilization", "0.92"},
			want:     []string{"the-engine", "serve", "--alias", "friend", "--gpu-memory-utilization", "0.92"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := assembleEngineArgv(tc.engine, subcommandFor(tc.engine, tc.sel), tc.params, tc.trailing)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("assembleEngineArgv = %q, want %q", got, tc.want)
			}
		})
	}
}

// argvFromDeployConfig routes every servable engine through the shared assembler.
// A golden argv per engine pins exactly what a start request assembles, so the
// convergence (and any future change) is provably bounded to the output.
func TestArgvFromDeployConfigPerEngine(t *testing.T) {
	cases := []struct {
		provider string
		dc       remote.DeployConfig
		wantTail []string // asserted after the engine's own binary
	}{
		{
			provider: "llamacpp",
			dc: remote.DeployConfig{
				Runner:          "llamacpp",
				ModelID:         "org/model",
				ContextSize:     16000,
				ServedModelName: "friend",
			},
			wantTail: []string{"--hf-repo", "org/model", "--alias", "friend", "--ctx-size", "16000"},
		},
		{
			provider: "omlx",
			dc: remote.DeployConfig{
				Runner:          "omlx",
				ModelID:         "org/model",
				ServedModelName: "friendly",
				// oMLX serves a whole directory; only a bind, if any, maps to a flag.
				ServeArgs: []string{"--host", "0.0.0.0", "--port", "1234"},
			},
			wantTail: []string{"serve", "--host", "0.0.0.0", "--port", "1234"},
		},
		{
			provider: "vllm",
			dc: remote.DeployConfig{
				Runner:          "vllm",
				ModelID:         "org/model",
				Quant:           "Q4_K_M",
				ContextSize:     32768,
				ServedModelName: "friendly",
				ServeArgs:       []string{"--gpu-memory-utilization", "0.92"},
			},
			wantTail: []string{"serve", "org/model:Q4_K_M", "--served-model-name", "friendly",
				"--max-model-len", "32768", "--gpu-memory-utilization", "0.92"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			eng, err := engineFor(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			argv, err := argvFromDeployConfig(eng, tc.dc)
			if err != nil {
				t.Fatal(err)
			}
			if len(argv) == 0 || argv[0] != eng.binary() {
				t.Fatalf("argv does not start with the engine binary %q: %q", eng.binary(), argv)
			}
			if !reflect.DeepEqual(argv[1:], tc.wantTail) {
				t.Errorf("argv after binary = %q, want %q", argv[1:], tc.wantTail)
			}
		})
	}
}

// buildServeArgv's preset-less path is the plain `serve` caller of the shared
// assembler (the daemon's deploy-config path is golden-pinned above). The design
// promises byte-identity on *both* paths, so the plain serve path must be pinned
// too: a golden argv per engine asserts exactly what a preset-less serve
// assembles, not merely that a flag appears somewhere.
func TestBuildServeArgvPresetlessPerEngine(t *testing.T) {
	cases := []struct {
		provider string
		sel      outfit.Selection
		wantTail []string // asserted after the engine's own binary
	}{
		{
			provider: "llamacpp",
			sel:      outfit.Selection{Provider: "llamacpp", Model: "org/model", Alias: "friend", Context: "16000"},
			wantTail: []string{"--hf-repo", "org/model", "--alias", "friend", "--ctx-size", "16000"},
		},
		{
			provider: "omlx",
			sel:      outfit.Selection{Provider: "omlx", BaseURL: "http://0.0.0.0:1234"},
			wantTail: []string{"serve", "--host", "0.0.0.0", "--port", "1234"},
		},
		{
			provider: "vllm",
			sel:      outfit.Selection{Provider: "vllm", Model: "org/model:Q4_K_M", Alias: "friendly", Context: "32768"},
			wantTail: []string{"serve", "org/model:Q4_K_M", "--served-model-name", "friendly", "--max-model-len", "32768"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			eng, err := engineFor(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			outfitPath := filepath.Join(t.TempDir(), outfit.DefaultFile)
			captureStdout(t, func() {
				argv, err := buildServeArgv(eng, tc.sel, outfitPath)
				if err != nil {
					t.Fatal(err)
				}
				if len(argv) == 0 || argv[0] != eng.binary() {
					t.Fatalf("argv does not start with the engine binary %q: %q", eng.binary(), argv)
				}
				if !reflect.DeepEqual(argv[1:], tc.wantTail) {
					t.Errorf("argv after binary = %q, want %q", argv[1:], tc.wantTail)
				}
			})
		})
	}
}

// The preset branch is the only caller that feeds subcommandFor into CommandIn
// rather than the assembler. On a positional engine its model must still ride
// right after the subcommand, before any flag -- the position subcommandFor
// guarantees on the preset-less and daemon paths. A bad ALIAS naming no section
// is a loud error, not a silently-empty command.
func TestBuildServeArgvPresetBranch(t *testing.T) {
	t.Run("positional model rides after the subcommand", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "preset.ini"), "[friendly]\n")
		eng, err := engineFor("vllm")
		if err != nil {
			t.Fatal(err)
		}
		sel := outfit.Selection{Provider: "vllm", Model: "org/model:Q4_K_M", Alias: "friendly", Preset: "preset.ini"}
		var argv []string
		captureStdout(t, func() {
			var err error
			argv, err = buildServeArgv(eng, sel, filepath.Join(dir, outfit.DefaultFile))
			if err != nil {
				t.Fatal(err)
			}
		})
		if len(argv) < 3 || argv[0] != eng.binary() || argv[1] != "serve" || argv[2] != "org/model:Q4_K_M" {
			t.Fatalf("positional model must follow the subcommand before any flag, got %q", argv)
		}
		// It must not be duplicated past its single subcommand position either.
		if n := countSubstring(argv, "org/model:Q4_K_M"); n != 1 {
			t.Errorf("positional model appears %d times, want 1: %q", n, argv)
		}
	})

	t.Run("a misspelt alias among several models is a loud error", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "preset.ini"), "[alpha]\nmodel = /a.gguf\n\n[beta]\nmodel = /b.gguf\n")
		eng, err := engineFor("llamacpp")
		if err != nil {
			t.Fatal(err)
		}
		sel := outfit.Selection{Provider: "llamacpp", Model: "/a.gguf", Alias: "wrong", Preset: "preset.ini"}
		_, err = buildServeArgv(eng, sel, filepath.Join(dir, outfit.DefaultFile))
		if err == nil {
			t.Fatal("want an error for an alias that matches none of several preset sections, got nil")
		}
		// It must be loud *and* descriptive: the misspelt alias and what is actually
		// available, so the user corrects it rather than serving the wrong model.
		if !strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "available") {
			t.Errorf("the error should name the alias and the available sections: %v", err)
		}
	})
}

// countSubstring counts how many times s occurs in ss.
func countSubstring(ss []string, s string) int {
	n := 0
	for _, v := range ss {
		if v == s {
			n++
		}
	}
	return n
}
