// Package harness abstracts the coding agent that outfit configures. opencode
// and Pi are the supported harnesses; each knows how to apply, remove and read
// back a provider selection in its own config format.
//
// The active harness is chosen at runtime — never from an Outfit file, so an
// Outfit stays portable across harnesses — with this precedence:
//
//	--harness/-H flag  >  OUTFIT_HARNESS env  >  stored preference  >  opencode
//
// The stored preference lives in ${XDG_CONFIG_HOME:-~/.config}/outfit/config.json
// and is managed with `outfit harness --set <name>`.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/outfit"
)

// HarnessEnv is the environment variable that selects the harness.
const HarnessEnv = "OUTFIT_HARNESS"

// Default is the harness used when nothing else selects one.
const Default = "opencode"

// ProviderState is one configured provider read back from a harness config: its
// model keys (sorted), base URL, and per-model context and output limits. It is
// what `outfit export` reconstructs an Outfit from.
type ProviderState struct {
	ModelKeys []string
	BaseURL   string
	Contexts  map[string]int
	Outputs   map[string]int
}

// Summary is the result of an Apply: the config file written, the chosen
// default model (may be empty — Pi has no default-model setting), and any
// harness-specific notes to show the user.
type Summary struct {
	ConfigPath   string
	DefaultModel string
	Notes        []string
}

// Harness configures one coding agent.
type Harness interface {
	// Name is the harness's identifier (e.g. "opencode").
	Name() string
	// ConfigPath returns the harness config file this harness writes.
	ConfigPath() (string, error)
	// Apply writes a single provider selection into the harness config.
	// contextWindow and outputTokens, when > 0, are the resolved limits to set.
	Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int) (Summary, error)
	// Remove removes a provider, or specific model keys within it. With no
	// modelKeys the whole provider is removed. Returns the number of removals.
	Remove(providerID string, modelKeys []string) (int, error)
	// State reports each configured provider plus the top-level default model
	// ("" when the harness has no such concept).
	State() (providers map[string]ProviderState, defaultModel string, err error)
}

// registry holds the available harnesses by name.
var registry = map[string]Harness{
	"opencode": opencodeHarness{},
	"pi":       piHarness{},
}

// Names returns the available harness names in stable order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Lookup returns the harness with the given name.
func Lookup(name string) (Harness, bool) {
	h, ok := registry[name]
	return h, ok
}

// Resolve selects the active harness from the flag value, the OUTFIT_HARNESS
// env var, the stored preference, then the default, in that order. It returns
// the harness and a short label naming where the choice came from.
func Resolve(flag string) (Harness, string, error) {
	name, source := flag, "--harness flag"
	if name == "" {
		if env := os.Getenv(HarnessEnv); env != "" {
			name, source = env, HarnessEnv
		}
	}
	if name == "" {
		if pref, _ := LoadPreference(); pref != "" {
			name, source = pref, "stored preference"
		}
	}
	if name == "" {
		name, source = Default, "default"
	}
	h, ok := registry[name]
	if !ok {
		return nil, "", fmt.Errorf("unknown harness %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	return h, source, nil
}

// PreferencePath returns the path to outfit's own config file, where the
// default-harness preference is stored.
func PreferencePath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "outfit", "config.json")
}

// preference is the on-disk shape of outfit's config file.
type preference struct {
	Harness string `json:"harness"`
}

// LoadPreference returns the stored default harness, or "" when none is set.
func LoadPreference() (string, error) {
	data, err := os.ReadFile(PreferencePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var p preference
	if err := json.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("parsing %s: %w", PreferencePath(), err)
	}
	return p.Harness, nil
}

// SavePreference stores name as the default harness, validating it first.
func SavePreference(name string) error {
	if _, ok := registry[name]; !ok {
		return fmt.Errorf("unknown harness %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	path := PreferencePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(preference{Harness: name}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
