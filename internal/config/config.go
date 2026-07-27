// Package config owns outfit's own config file — the small JSON document under
// ${XDG_CONFIG_HOME:-~/.config}/outfit/config.json holding the machine-local
// state: the default-harness preference and the Outfit alias registry.
//
// It is a leaf package: it imports nothing else from this module, so both
// internal/harness (for the preference) and cmd (for aliases) can depend on it
// without a cycle, and locating an Outfit stays harness-agnostic. In particular
// it must never import internal/outfit — parsing an Outfit to find its ALIAS is
// the caller's job; this package is a dumb key/value store.
//
// Every write goes through Update, which is a read-modify-write of the whole
// document. That is the invariant: storing an alias must not clobber the
// harness preference, and vice versa.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// fileName is outfit's own config file, inside its config directory.
const fileName = "config.json"

// Path returns the config file's location: $XDG_CONFIG_HOME/outfit/config.json,
// falling back to ~/.config when the variable is unset.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "outfit", fileName)
}

// File is outfit's config document.
//
// Unknown top-level keys are round-tripped through extra, so a read-modify-write
// by this version never drops what another version wrote — the same courtesy
// internal/pi extends to Pi's models.json. (The reverse is not possible: a
// binary predating the alias registry rewrites the whole document and will drop
// the aliases key.)
type File struct {
	Harness string            `json:"harness,omitempty"`
	Aliases map[string]string `json:"aliases,omitempty"`

	// extra holds top-level keys this version does not know about.
	extra map[string]json.RawMessage
}

// The top-level keys this version owns; everything else lands in extra.
const (
	keyHarness = "harness"
	keyAliases = "aliases"
)

// Load reads the config file. A missing file is not an error — it yields an
// empty File, so a first run needs no special case.
func Load() (*File, error) {
	f := &File{}
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", Path(), err)
	}
	for key, raw := range doc {
		switch key {
		case keyHarness:
			if err := json.Unmarshal(raw, &f.Harness); err != nil {
				return nil, fmt.Errorf("parsing %s: %s: %w", Path(), key, err)
			}
		case keyAliases:
			if err := json.Unmarshal(raw, &f.Aliases); err != nil {
				return nil, fmt.Errorf("parsing %s: %s: %w", Path(), key, err)
			}
		default:
			if f.extra == nil {
				f.extra = map[string]json.RawMessage{}
			}
			f.extra[key] = raw
		}
	}
	return f, nil
}

// Save writes f back, creating the directory 0700 and the file 0600 — it can
// sit alongside secrets, and it records where the user's projects live. Callers
// should prefer Update, which reads first.
func (f *File) Save() error {
	doc := map[string]json.RawMessage{}
	for key, raw := range f.extra {
		doc[key] = raw
	}
	set := func(key string, value any, keep bool) error {
		if !keep {
			delete(doc, key)
			return nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		doc[key] = raw
		return nil
	}
	if err := set(keyHarness, f.Harness, f.Harness != ""); err != nil {
		return err
	}
	if err := set(keyAliases, f.Aliases, len(f.Aliases) > 0); err != nil {
		return err
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Update loads the config, hands it to mutate, and saves the result. This is
// the only way callers should write: it guarantees an unrelated setting
// survives.
func Update(mutate func(*File) error) error {
	f, err := Load()
	if err != nil {
		return err
	}
	if err := mutate(f); err != nil {
		return err
	}
	return f.Save()
}

// Alias returns the Outfit path registered under name.
func (f *File) Alias(name string) (string, bool) {
	path, ok := f.Aliases[name]
	return path, ok
}

// SetAlias points name at path, creating the registry if this is the first one.
func (f *File) SetAlias(name, path string) {
	if f.Aliases == nil {
		f.Aliases = map[string]string{}
	}
	f.Aliases[name] = path
}

// RemoveAlias drops name, reporting whether it was registered.
func (f *File) RemoveAlias(name string) bool {
	if _, ok := f.Aliases[name]; !ok {
		return false
	}
	delete(f.Aliases, name)
	return true
}

// AliasNames returns the registered names in stable order.
func (f *File) AliasNames() []string {
	names := make([]string, 0, len(f.Aliases))
	for name := range f.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidAliasName checks that name can be typed where an Outfit path goes. It
// has to be a plain name: anything path-shaped could be confused with a file,
// and anything flag-shaped could not be passed to `outfit unalias` at all.
func ValidAliasName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("an alias name cannot be empty")
	case strings.ContainsAny(name, `/\`):
		// Rejected on every platform, not just Windows: the name has to mean
		// the same thing in a config file that travels between machines.
		return fmt.Errorf("alias name %q cannot contain a path separator", name)
	case name == "." || name == "..":
		return fmt.Errorf("alias name %q looks like a path — an alias is a plain name (e.g. qwen3.6-27b)", name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf(`alias name %q cannot start with "-": it would parse as a flag`, name)
	case strings.IndexFunc(name, unicode.IsSpace) >= 0:
		return fmt.Errorf("alias name %q cannot contain whitespace", name)
	}
	return nil
}

// NameShaped reports whether s could be an alias name — the cheap test callers
// use before consulting the registry at all, so a path-shaped argument never
// causes a config read.
func NameShaped(s string) bool { return ValidAliasName(s) == nil }
