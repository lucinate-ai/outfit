// Package preset reads llama.cpp INI preset files and turns one model section
// into a concrete `llama-server` command.
//
// llama.cpp presets (https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md)
// are INI files where each `[section]` is a model and every key is a
// `llama-server` argument with its leading dashes stripped — `ctx-size = 4096`
// is `--ctx-size 4096`. A leading `[*]` (or `[global]`) section holds defaults
// shared by every model; a per-model section overrides them.
//
// Presets are designed for the server's router (multi-model) mode, loaded with
// `--models-preset`. There is no equivalent for plain single-model serving, so
// this package flattens a chosen section back into the explicit flags you would
// have typed by hand.
package preset

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// globalSections are the section names treated as shared defaults rather than a
// model. llama.cpp's docs use `[*]`; `[global]` is accepted as a friendly alias.
var globalSections = map[string]bool{"*": true, "global": true}

// Param is a single `key = value` entry from a preset, with the key as written
// (dashes stripped, as INI keys are) and the value verbatim.
type Param struct {
	Key   string
	Value string
}

// Section is a named model preset: the section header and its parameters, in
// file order.
type Section struct {
	Name   string
	Params []Param
}

// Preset is a parsed INI preset: the shared defaults and every model section,
// each in the order they appear in the file.
type Preset struct {
	Global   []Param
	Sections []Section
}

// Parse reads an INI preset file. It is deliberately small: `[section]` headers,
// `key = value` entries (`:` is accepted in place of `=`), and `#`/`;` comments.
func Parse(data []byte) (Preset, error) {
	var p Preset
	cur := -1 // index into p.Sections; -1 means the global section

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(stripComment(scanner.Text()))
		if text == "" {
			continue
		}

		if strings.HasPrefix(text, "[") {
			if !strings.HasSuffix(text, "]") {
				return Preset{}, fmt.Errorf("line %d: malformed section header %q", line, text)
			}
			name := strings.TrimSpace(text[1 : len(text)-1])
			if name == "" {
				return Preset{}, fmt.Errorf("line %d: empty section name", line)
			}
			if globalSections[strings.ToLower(name)] {
				cur = -1
				continue
			}
			p.Sections = append(p.Sections, Section{Name: name})
			cur = len(p.Sections) - 1
			continue
		}

		key, value, ok := splitKeyValue(text)
		if !ok {
			return Preset{}, fmt.Errorf("line %d: expected `key = value`, got %q", line, text)
		}
		param := Param{Key: key, Value: value}
		if cur < 0 {
			p.Global = append(p.Global, param)
		} else {
			p.Sections[cur].Params = append(p.Sections[cur].Params, param)
		}
	}
	if err := scanner.Err(); err != nil {
		return Preset{}, err
	}
	return p, nil
}

// stripComment removes an INI comment. A `#` or `;` is a comment only when it
// starts the line or follows whitespace, so values that embed `#`/`;` without a
// preceding space — JSON, URLs with fragments — are left intact.
func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		if (s[i] == '#' || s[i] == ';') && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

// splitKeyValue splits an INI entry on the first `=` or `:`, trimming both
// sides. It returns ok=false when the key is empty or no separator is present.
func splitKeyValue(s string) (key, value string, ok bool) {
	i := strings.IndexAny(s, "=:")
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	value = strings.TrimSpace(s[i+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// SectionNames lists the model section names in file order.
func (p Preset) SectionNames() []string {
	names := make([]string, len(p.Sections))
	for i, s := range p.Sections {
		names[i] = s.Name
	}
	return names
}

// Select chooses the model section to serve. With a model name it picks the
// matching section (case-insensitively); without one it requires the preset to
// hold exactly one model. A single-section preset is always served, even if the
// requested name does not match — in llama.cpp's `llamacpp` provider the model
// name is only a label, so there is no other section it could mean.
func (p Preset) Select(model string) (Section, error) {
	switch {
	case len(p.Sections) == 0:
		return Section{}, fmt.Errorf("preset defines no model sections")
	case model != "":
		for _, s := range p.Sections {
			if strings.EqualFold(s.Name, model) {
				return s, nil
			}
		}
		if len(p.Sections) == 1 {
			return p.Sections[0], nil
		}
		return Section{}, fmt.Errorf("preset has no [%s] section (available: %s)", model, strings.Join(p.SectionNames(), ", "))
	case len(p.Sections) == 1:
		return p.Sections[0], nil
	default:
		return Section{}, fmt.Errorf("preset defines %d models (%s); set MODEL in the Outfit to choose one", len(p.Sections), strings.Join(p.SectionNames(), ", "))
	}
}

// Args flattens the global defaults and a section into `llama-server` flags, in
// file order, with the section's values overriding the globals'. It does not
// include the binary name; see Command.
func (p Preset) Args(sec Section) []string {
	// Merge global-then-section, keeping first-seen order and letting a later
	// (section) entry overwrite an earlier (global) value in place.
	var ordered []Param
	at := map[string]int{}
	for _, src := range [][]Param{p.Global, sec.Params} {
		for _, kv := range src {
			lk := strings.ToLower(kv.Key)
			if i, ok := at[lk]; ok {
				ordered[i].Value = kv.Value
				continue
			}
			at[lk] = len(ordered)
			ordered = append(ordered, kv)
		}
	}

	var args []string
	for _, kv := range ordered {
		args = append(args, flagFor(kv.Key, kv.Value)...)
	}
	return args
}

// Command builds the full argv (binary first) for serving a section.
func (p Preset) Command(binary string, sec Section) []string {
	return append([]string{binary}, p.Args(sec)...)
}

// canonical maps llama.cpp short flag aliases — as they appear in preset keys,
// i.e. without the leading dash — to their canonical long-form name. Long-form
// keys are passed through unchanged, since `llama-server` registers them
// directly; only the short aliases need rewriting (e.g. `hf` is `-hf`, never
// `--hf`).
var canonical = map[string]string{
	"hf": "hf-repo", "hfr": "hf-repo", "hff": "hf-file", "hft": "hf-token",
	"ngl": "n-gpu-layers", "fa": "flash-attn",
	"ctk": "cache-type-k", "ctv": "cache-type-v",
	"ub": "ubatch-size", "np": "parallel", "ns": "sequences",
	"cb": "cont-batching", "dt": "defrag-thold", "dev": "device",
	"ot": "override-tensor", "sm": "split-mode", "ts": "tensor-split",
	"mg": "main-gpu", "mm": "mmproj", "mmu": "mmproj-url",
	"mu": "model-url", "tb": "threads-batch", "to": "timeout",
	"kvu": "kv-unified",
	// single-character short flags
	"t": "threads", "c": "ctx-size", "n": "n-predict", "b": "batch-size",
	"s": "seed", "m": "model", "a": "alias", "v": "verbose", "p": "prompt",
}

// boolean lists canonical flag names that llama-server accepts with no value. In
// a preset these read `key = 1` / `key = 0`, so a falsy value drops the flag and
// a truthy one emits it bare (`--mmap`, not `--mmap 1`).
var boolean = map[string]bool{
	"mmap": true, "no-mmap": true, "jinja": true, "kv-unified": true,
	"spec-default": true, "cont-batching": true, "no-cont-batching": true,
	"mlock": true, "embedding": true, "embeddings": true, "verbose": true,
	"cpu-moe": true, "color": true,
}

// flagFor turns one preset key/value into its `llama-server` flag tokens.
func flagFor(key, value string) []string {
	name := strings.ToLower(strings.TrimSpace(key))
	if c, ok := canonical[name]; ok {
		name = c
	}
	flag := "--" + name
	if len(name) == 1 { // an unknown single-character key is a short flag
		flag = "-" + name
	}

	value = strings.TrimSpace(value)
	if boolean[name] {
		if isFalsy(value) {
			return nil
		}
		return []string{flag}
	}
	if value == "" {
		return []string{flag}
	}
	return []string{flag, value}
}

// isFalsy reports whether a boolean preset value disables its flag.
func isFalsy(v string) bool {
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return true
	default:
		return false
	}
}

// safeArg matches argv tokens that need no shell quoting when printed.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// FormatCommand renders an argv as a single, copy-pasteable shell command,
// quoting only the tokens that need it (e.g. a JSON chat-template value).
func FormatCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s != "" && safeArg.MatchString(s) {
		return s
	}
	// Single-quote and escape embedded single quotes the POSIX way.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
