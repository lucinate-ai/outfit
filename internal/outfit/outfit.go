// Package outfit defines a provider Selection and the declarative, Dockerfile-
// style Outfit file that describes one.
//
// An Outfit is a declarative description of a single opencode provider plus an
// optional model family and/or model — the file equivalent of one `outfit
// add` invocation. It uses a flat, Dockerfile-style syntax:
//
//	# point opencode at one provider
//	PROVIDER openrouter
//	FAMILY   deepseek-v4
//	MODEL    deepseek/deepseek-v4-pro   # optional; the provider-native model ref
//	ALIAS    deepseek                   # optional; friendly name for the model
//	CONTEXT  128k                       # optional; context window
//	OUTPUT   32k                        # optional; max output tokens
//	BASEURL  https://gateway/v1         # optional; API base URL override
//	PRESET   ./preset.ini               # optional; llama.cpp preset for `serve`
//
// MODEL is the reference the provider itself understands: an OpenRouter/Bedrock
// model id, an Ollama name, or — for llamacpp — a Hugging Face repo
// (org/model:quant) or a path to a .gguf. ALIAS overrides the friendly name the
// harness shows for it (and the name llama-server reports under `serve`).
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

// Selection holds the provider, model family and/or model, and optional
// overrides that describe one opencode provider configuration. It is the shared
// currency between the CLI flags, the Outfit file, and the apply/export paths.
type Selection struct {
	Provider  string
	Family    string
	Model     string
	Alias     string
	Context   string
	Output    string
	Providers string
	BaseURL   string
	Preset    string
}

// Outfit keywords, in their canonical (lower-cased) form for matching.
const (
	kwProvider = "provider"
	kwFamily   = "family"
	kwModel    = "model"
	kwAlias    = "alias"
	kwContext  = "context"
	kwOutput   = "output"
	kwBaseURL  = "baseurl"
	kwPreset   = "preset"
)

// canonicalKeyword resolves an Outfit keyword (already lower-cased) to its
// canonical form, accepting a few friendly aliases for the base URL. It returns
// "" for an unrecognised keyword.
func canonicalKeyword(kw string) string {
	switch kw {
	case kwProvider, kwFamily, kwModel, kwAlias, kwContext, kwOutput, kwPreset:
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
			return Selection{}, fmt.Errorf("line %d: unknown keyword %q (expected PROVIDER, FAMILY, MODEL, ALIAS, CONTEXT, OUTPUT, BASEURL, or PRESET)", line, fields[0])
		}
		switch {
		case len(fields) < 2:
			return Selection{}, fmt.Errorf("line %d: %s needs a value", line, strings.ToUpper(canon))
		case len(fields) > 2:
			return Selection{}, fmt.Errorf("line %d: %s takes a single value, got %d", line, strings.ToUpper(canon), len(fields)-1)
		}
		value := fields[1]

		if prev, ok := seen[canon]; ok {
			return Selection{}, fmt.Errorf("line %d: duplicate %s (already set on line %d)", line, strings.ToUpper(canon), prev)
		}
		seen[canon] = line

		switch canon {
		case kwProvider:
			sel.Provider = value
		case kwFamily:
			sel.Family = value
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
		}
	}
	if err := scanner.Err(); err != nil {
		return Selection{}, err
	}

	if sel.Provider == "" {
		return Selection{}, fmt.Errorf("Outfit is missing a PROVIDER instruction")
	}
	return sel, nil
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
	line("FAMILY", sel.Family)
	line("MODEL", sel.Model)
	line("ALIAS", sel.Alias)
	line("CONTEXT", sel.Context)
	line("OUTPUT", sel.Output)
	line("BASEURL", sel.BaseURL)
	line("PRESET", sel.Preset)
	return b.String()
}
