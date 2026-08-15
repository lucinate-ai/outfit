package main

import (
	"reflect"
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
