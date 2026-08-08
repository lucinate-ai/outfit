// Package preset reads INI preset files and turns one section into a concrete
// inference-server command.
//
// The format is llama.cpp's (https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md):
// INI files where each `[section]` is a model and every key is a server
// argument with its leading dashes stripped — `ctx-size = 4096` is
// `--ctx-size 4096`. A leading `[*]` (or `[global]`) section holds defaults
// shared by every model; a per-model section overrides them.
//
// llama.cpp presets are designed for the server's router (multi-model) mode,
// loaded with `--models-preset`. There is no equivalent for plain single-model
// serving, so this package flattens a chosen section back into the explicit
// flags you would have typed by hand.
//
// Parsing is engine-neutral; only the *spelling* of flags differs between
// engines, which is what a Dialect carries. The package-level Flags,
// CanonicalKey, Preset.Args and Preset.Command render in the LlamaCpp dialect,
// so callers that predate dialects keep their behaviour.
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
		return Section{}, fmt.Errorf("preset defines %d models (%s); set ALIAS in the Outfit to choose one", len(p.Sections), strings.Join(p.SectionNames(), ", "))
	}
}

// Dialect describes how one inference engine spells its flags: the short
// aliases it accepts, and the flags it takes bare with no value. The zero
// Dialect passes every key through unchanged, which is what an engine with only
// long-form flags wants.
//
// A dialect is never guessed from the preset's contents — it comes from the
// engine the Outfit's PROVIDER names — because the tables are not
// interchangeable: llama.cpp's would rewrite another engine's `m` to `--model`
// and `c` to `--ctx-size`.
type Dialect struct {
	// Aliases maps a short flag alias, as it appears in a preset key (without
	// the leading dash), to its canonical long-form name.
	Aliases map[string]string
	// Boolean lists canonical flag names the engine accepts with no value.
	Boolean map[string]bool
}

// LlamaCpp is the dialect `llama-server` speaks, and the one the package-level
// rendering helpers use.
var LlamaCpp = Dialect{Aliases: canonical, Boolean: boolean}

// VLLM is the dialect `vllm serve` speaks: long-form flags with no short
// aliases and no boolean specials, so keys pass through unchanged.
var VLLM = Dialect{}

// OMLX is the dialect `omlx-cli serve` speaks. It spells every flag in long
// form (`--model-dir`, `--memory-guard`, `--paged-ssd-cache-dir`), so no
// aliasing is applied and keys pass through as written.
var OMLX = Dialect{}

// Flags merges ordered layers of params into flag tokens. Later layers override
// earlier ones by canonical flag name, in place, so the first layer fixes the
// order: pass globals, then the section, then any overrides. It does not
// include the binary name; see Command.
func (d Dialect) Flags(layers ...[]Param) []string {
	var ordered []Param
	at := map[string]int{} // canonical flag name -> index in ordered
	for _, layer := range layers {
		for _, kv := range layer {
			ck := d.CanonicalKey(kv.Key)
			if i, ok := at[ck]; ok {
				ordered[i] = kv
				continue
			}
			at[ck] = len(ordered)
			ordered = append(ordered, kv)
		}
	}

	var args []string
	for _, kv := range ordered {
		args = append(args, d.flagFor(kv.Key, kv.Value)...)
	}
	return args
}

// Flags merges ordered layers of params into `llama-server` flag tokens, in the
// LlamaCpp dialect.
func Flags(layers ...[]Param) []string {
	return LlamaCpp.Flags(layers...)
}

// Args flattens the global defaults and a section into `llama-server` flags,
// with the section's values overriding the globals'.
func (p Preset) Args(sec Section) []string {
	return Flags(p.Global, sec.Params)
}

// Command builds the full argv (binary first) for serving a section in the
// LlamaCpp dialect. Any override layers (e.g. values an Outfit specifies) win
// over the preset.
func (p Preset) Command(binary string, sec Section, overrides ...[]Param) []string {
	return p.CommandIn(LlamaCpp, binary, nil, sec, overrides...)
}

// CommandIn builds the full argv for serving a section with a given engine: the
// binary, then any subcommand the engine needs (`omlx-cli serve` takes one,
// `llama-server` does not), then the flags rendered in that engine's dialect.
func (p Preset) CommandIn(d Dialect, binary string, subcommand []string, sec Section, overrides ...[]Param) []string {
	layers := append([][]Param{p.Global, sec.Params}, overrides...)
	argv := append([]string{binary}, subcommand...)
	return append(argv, d.Flags(layers...)...)
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

// CanonicalKey resolves a preset key to the canonical long-form flag name that
// Flags would emit, so the same setting written different ways (`ngl` and
// `n-gpu-layers`, `c` and `ctx-size`) compares equal. Callers that need to
// match specific flags rather than render them should compare canonical names.
// A dialect with no aliases only lower-cases the key.
func (d Dialect) CanonicalKey(key string) string {
	name := strings.ToLower(strings.TrimSpace(key))
	if c, ok := d.Aliases[name]; ok {
		name = c
	}
	return name
}

// CanonicalKey resolves a preset key in the LlamaCpp dialect.
func CanonicalKey(key string) string {
	return LlamaCpp.CanonicalKey(key)
}

// flagFor turns one preset key/value into its flag tokens.
func (d Dialect) flagFor(key, value string) []string {
	name := d.CanonicalKey(key)
	flag := "--" + name
	if len(name) == 1 { // an unknown single-character key is a short flag
		flag = "-" + name
	}

	value = strings.TrimSpace(value)
	if d.Boolean[name] {
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
