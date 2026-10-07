// Package spinloop defines a provider Selection and the declarative, Dockerfile-
// style Spinloop file that describes one.
//
// A Spinloop is a declarative description of a single opencode provider plus a
// model — the file equivalent of one `spinloop add` invocation. It uses a flat,
// Dockerfile-style syntax:
//
//	# point opencode at one provider
//	PROVIDER openrouter
//	MODEL    deepseek/deepseek-v4-pro   # the provider-native model ref
//	ALIAS    deepseek                   # optional; friendly name for the model
//	CONTEXT  128k                       # optional; context window (per request)
//	OUTPUT   32k                        # optional; max output tokens
//	PARALLEL 2                          # optional; concurrent request slots for `serve`
//	BASEURL  https://gateway/v1         # optional; API base URL override
//	PRESET   ./preset.ini               # optional; llama.cpp preset for `serve`
//	ENV      AWS_PROFILE=dev            # optional, repeatable; local env var
//
// MODEL is the reference the provider itself understands: an OpenRouter/Bedrock
// model id, an Ollama name, or — for llamacpp — a Hugging Face repo
// (org/model:quant) or a path to a .gguf. ALIAS overrides the friendly name the
// harness shows for it (and the name llama-server reports under `serve`).
//
// ENV sets an environment variable for the local `spinloop` process — the one
// keyword that may appear more than once. It carries a single KEY=VALUE token
// and is used by the cloud commands (which read it before signing AWS calls);
// it is local-only and never reaches a deployed instance.
//
// Keywords are matched case-insensitively, but UPPERCASE is canonical (it is
// what `spinloop export` emits). Blank lines, full-line `#` comments, and
// trailing ` #` comments are ignored.
package spinloop

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Selection holds the provider, model and/or alias, and optional overrides that
// describe one opencode provider configuration. It is the shared currency
// between the CLI flags, the Spinloop file, and the apply/export paths.
type Selection struct {
	Provider  string
	Model     string
	Alias     string
	Context   string
	Output    string
	Providers string
	BaseURL   string
	Preset    string
	// Parallel is the PARALLEL instruction's value: a count of concurrent
	// request slots the served engine should run with. It has no meaning for
	// a hosted-harness selection, only for a served one — see the
	// local-serving capability for how it and Context translate into each
	// engine's own flags.
	Parallel string
	// DisplayName is the harness provider's display name, derived at apply time
	// rather than parsed from a Spinloop — like BaseURL, it may be filled from the
	// named environment. It is set only when an environment's --env flag renames
	// the provider, to label it distinctly from a local engine of the same kind;
	// empty otherwise, leaving the catalogue engine name.
	DisplayName string
	// Env holds the Spinloop's ENV instructions in file order. Unlike the other
	// fields it may carry several entries, since ENV may repeat.
	Env []EnvVar
	// DiscoveredModels is set at apply time, like DisplayName, never parsed
	// from a Spinloop file: the model IDs a gateway reported when the
	// selection named neither a model nor an alias to route by. A harness
	// whose config format holds more than one model per provider uses it to
	// populate its model list instead of leaving one empty.
	DiscoveredModels []string
}

// EnvVar is one ENV instruction: an environment variable to set for the local
// spinloop process.
type EnvVar struct {
	Key   string
	Value string
}

// Spinloop keywords, in their canonical (lower-cased) form for matching.
const (
	kwProvider = "provider"
	kwModel    = "model"
	kwAlias    = "alias"
	kwContext  = "context"
	kwOutput   = "output"
	kwParallel = "parallel"
	kwBaseURL  = "baseurl"
	kwPreset   = "preset"
	kwEnv      = "env"
)

// canonicalKeyword resolves a Spinloop keyword (already lower-cased) to its
// canonical form, accepting a few friendly aliases for the base URL. It returns
// "" for an unrecognised keyword.
func canonicalKeyword(kw string) string {
	switch kw {
	case kwProvider, kwModel, kwAlias, kwContext, kwOutput, kwParallel, kwPreset, kwEnv:
		return kw
	case kwBaseURL, "base-url", "base_url", "url":
		return kwBaseURL
	default:
		return ""
	}
}

// DefaultFile is the filename `spinloop apply` looks for when no path is given.
const DefaultFile = "Spinloop"

// Parse parses a Spinloop file into a Selection. It enforces that the file names
// exactly one provider and sets each instruction at most once.
func Parse(data []byte) (Selection, error) {
	var sel Selection
	seen := map[string]int{} // keyword -> line it first appeared on

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(stripComment(scanner.Text()))
		if text == "" {
			continue
		}

		fields := strings.Fields(text)
		kw := strings.ToLower(fields[0])
		canon := canonicalKeyword(kw)
		if canon == "" {
			if kw == "remote" {
				return Selection{}, fmt.Errorf(
					"line %d: the REMOTE instruction was removed: name the environment with `spinloop cloud deploy --env <name>` at deploy time, and pass --env <name> to the commands that act on it (cloud subcommands, apply, unapply, harness)",
					line)
			}
			return Selection{}, fmt.Errorf("line %d: unknown keyword %q (expected PROVIDER, MODEL, ALIAS, CONTEXT, OUTPUT, PARALLEL, BASEURL, PRESET, or ENV)", line, fields[0])
		}
		switch {
		case len(fields) < 2:
			return Selection{}, fmt.Errorf("line %d: %s needs a value", line, strings.ToUpper(canon))
		case len(fields) > 2:
			return Selection{}, fmt.Errorf("line %d: %s takes a single value, got %d", line, strings.ToUpper(canon), len(fields)-1)
		}
		value := fields[1]

		// ENV is the one repeatable instruction, so it sidesteps the single-set
		// check and appends. Its value is a KEY=VALUE token; the key must be
		// non-empty (an empty value is allowed, to clear a variable).
		if canon == kwEnv {
			key, val, ok := strings.Cut(value, "=")
			if !ok || key == "" {
				return Selection{}, fmt.Errorf("line %d: ENV takes KEY=VALUE with a non-empty key, got %q", line, value)
			}
			sel.Env = append(sel.Env, EnvVar{Key: key, Value: val})
			continue
		}

		if prev, ok := seen[canon]; ok {
			return Selection{}, fmt.Errorf("line %d: duplicate %s (already set on line %d)", line, strings.ToUpper(canon), prev)
		}
		seen[canon] = line

		switch canon {
		case kwProvider:
			sel.Provider = value
		case kwModel:
			sel.Model = value
		case kwAlias:
			sel.Alias = value
		case kwContext:
			sel.Context = value
		case kwOutput:
			sel.Output = value
		case kwParallel:
			sel.Parallel = value
		case kwBaseURL:
			sel.BaseURL = value
		case kwPreset:
			sel.Preset = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Selection{}, err
	}

	if sel.Provider == "" {
		return Selection{}, fmt.Errorf("Spinloop is missing a PROVIDER instruction")
	}
	return sel, nil
}

// stripComment removes a comment from a Spinloop line. A line whose first
// non-blank character is `#` is dropped entirely; otherwise a trailing ` #`
// (or tab-`#`) comment is removed. Provider, family, and model identifiers
// never contain spaces, so this cannot truncate a real value.
func stripComment(s string) string {
	if t := strings.TrimLeft(s, " \t"); strings.HasPrefix(t, "#") {
		return ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		if j := strings.Index(s[i:], "#"); j >= 0 {
			return s[:i+j]
		}
	}
	return s
}

// Format renders a Selection as a canonical, UPPERCASE Spinloop file. The "%-8s"
// padding aligns every value at the same column.
func Format(sel Selection) string {
	var b strings.Builder
	line := func(keyword, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-8s %s\n", keyword, value)
		}
	}
	line("PROVIDER", sel.Provider)
	line("MODEL", sel.Model)
	line("ALIAS", sel.Alias)
	line("CONTEXT", sel.Context)
	line("OUTPUT", sel.Output)
	line("PARALLEL", sel.Parallel)
	line("BASEURL", sel.BaseURL)
	line("PRESET", sel.Preset)
	for _, e := range sel.Env {
		line("ENV", e.Key+"="+e.Value)
	}
	return b.String()
}
