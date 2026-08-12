// Package outfit defines a provider Selection and the declarative, Dockerfile-
// style Outfit file that describes one.
//
// An Outfit is a declarative description of a single opencode provider plus a
// model — the file equivalent of one `outfit add` invocation. It uses a flat,
// Dockerfile-style syntax:
//
//	# point opencode at one provider
//	PROVIDER openrouter
//	MODEL    deepseek/deepseek-v4-pro   # the provider-native model ref
//	ALIAS    deepseek                   # optional; friendly name for the model
//	CONTEXT  128k                       # optional; context window
//	OUTPUT   32k                        # optional; max output tokens
//	BASEURL  https://gateway/v1         # optional; API base URL override
//	PRESET   ./preset.ini               # optional; llama.cpp preset for `serve`
//	REMOTE   ./remote.json              # optional; remote-instance config for `remote`
//	FLEET    ./fleet.yaml               # optional; the fleet a harness launch routes through
//	ENV      AWS_PROFILE=dev            # optional, repeatable; local env var
//
// MODEL is the reference the provider itself understands: an OpenRouter/Bedrock
// model id, an Ollama name, or — for llamacpp — a Hugging Face repo
// (org/model:quant) or a path to a .gguf. ALIAS overrides the friendly name the
// harness shows for it (and the name llama-server reports under `serve`).
//
// ENV sets an environment variable for the local `outfit` process — the one
// keyword that may appear more than once. It carries a single KEY=VALUE token
// and is used by the remote commands (which read it before signing AWS calls);
// it is local-only and never reaches a deployed instance.
//
// Keywords are matched case-insensitively, but UPPERCASE is canonical (it is
// what `outfit export` emits). Blank lines, full-line `#` comments, and
// trailing ` #` comments are ignored.
package outfit

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Selection holds the provider, model and/or alias, and optional overrides that
// describe one opencode provider configuration. It is the shared currency
// between the CLI flags, the Outfit file, and the apply/export paths.
type Selection struct {
	Provider  string
	Model     string
	Alias     string
	Context   string
	Output    string
	Providers string
	BaseURL   string
	Preset    string
	Remote    string
	// Fleet is the FLEET instruction's value: a path to a fleet file whose
	// nodes a launch chooses between, or a URL naming an endpoint that has
	// already chosen (see FleetIsEndpoint).
	Fleet string
	// DisplayName is the harness provider's display name, derived at apply time
	// rather than parsed from an Outfit — like BaseURL, it may be filled from the
	// remote environment. It is set only when a REMOTE renames the provider, to
	// label it distinctly from a local engine of the same kind; empty otherwise,
	// leaving the catalogue engine name.
	DisplayName string
	// Env holds the Outfit's ENV instructions in file order. Unlike the other
	// fields it may carry several entries, since ENV may repeat.
	Env []EnvVar
}

// EnvVar is one ENV instruction: an environment variable to set for the local
// outfit process.
type EnvVar struct {
	Key   string
	Value string
}

// Outfit keywords, in their canonical (lower-cased) form for matching.
const (
	kwProvider = "provider"
	kwModel    = "model"
	kwAlias    = "alias"
	kwContext  = "context"
	kwOutput   = "output"
	kwBaseURL  = "baseurl"
	kwPreset   = "preset"
	kwRemote   = "remote"
	kwFleet    = "fleet"
	kwEnv      = "env"
)

// canonicalKeyword resolves an Outfit keyword (already lower-cased) to its
// canonical form, accepting a few friendly aliases for the base URL. It returns
// "" for an unrecognised keyword.
func canonicalKeyword(kw string) string {
	switch kw {
	case kwProvider, kwModel, kwAlias, kwContext, kwOutput, kwPreset, kwRemote, kwFleet, kwEnv:
		return kw
	case kwBaseURL, "base-url", "base_url", "url":
		return kwBaseURL
	default:
		return ""
	}
}

// DefaultFile is the filename `outfit apply` looks for when no path is given.
const DefaultFile = "Outfit"

// Parse parses an Outfit file into a Selection. It enforces that the file names
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
		canon := canonicalKeyword(strings.ToLower(fields[0]))
		if canon == "" {
			return Selection{}, fmt.Errorf("line %d: unknown keyword %q (expected PROVIDER, MODEL, ALIAS, CONTEXT, OUTPUT, BASEURL, PRESET, REMOTE, FLEET, or ENV)", line, fields[0])
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
		case kwBaseURL:
			sel.BaseURL = value
		case kwPreset:
			sel.Preset = value
		case kwRemote:
			sel.Remote = value
		case kwFleet:
			sel.Fleet = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Selection{}, err
	}

	if sel.Provider == "" {
		return Selection{}, fmt.Errorf("Outfit is missing a PROVIDER instruction")
	}
	// REMOTE and FLEET are two different answers to where the model is served
	// from — a deployed endpoint, or a machine on your network. An Outfit
	// stating both is a mistake rather than a precedence to resolve. BASEURL is
	// not in conflict: it is the pinned address that already wins over REMOTE,
	// and it wins over FLEET the same way.
	if sel.Remote != "" && sel.Fleet != "" {
		return Selection{}, fmt.Errorf(
			"Outfit sets both REMOTE (line %d) and FLEET (line %d): each names where the model is served from, so state one",
			seen[kwRemote], seen[kwFleet])
	}
	return sel, nil
}

// FleetIsEndpoint reports whether a FLEET value names an endpoint that has
// already chosen a node — a gateway — rather than a fleet file to choose from.
// A value carrying a scheme is an endpoint; anything else is a path. Keeping
// both behind one instruction is what lets a gateway slot in later without a
// second keyword.
func (s Selection) FleetIsEndpoint() bool {
	return strings.Contains(s.Fleet, "://")
}

// stripComment removes a comment from an Outfit line. A line whose first
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

// Format renders a Selection as a canonical, UPPERCASE Outfit file. The "%-8s"
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
	line("BASEURL", sel.BaseURL)
	line("PRESET", sel.Preset)
	line("REMOTE", sel.Remote)
	line("FLEET", sel.Fleet)
	for _, e := range sel.Env {
		line("ENV", e.Key+"="+e.Value)
	}
	return b.String()
}
