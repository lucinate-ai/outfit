package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

// envFilePath returns the path to the .env file alongside this program's
// source, mirroring the original "the .env next to the tool" behaviour.
func envFilePath() string {
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Join(filepath.Dir(file), ".env")
	}
	return ".env"
}

// resolveEnv looks up an environment variable, preferring the .env file next
// to the tool and falling back to the process environment.
func resolveEnv(name string) string {
	if v := readEnvFileVar(envFilePath(), name); v != "" {
		return v
	}
	return os.Getenv(name)
}

// readEnvFileVar returns the value of name from a dotenv-style file, or "".
func readEnvFileVar(path, name string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := name + "="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"`)
		}
	}
	return ""
}

// configDir returns the user's global opencode config directory.
func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode")
}

// resolveConfigFile picks the config file to update, preferring one that
// already exists so we don't leave a competing file alongside it.
func resolveConfigFile() (string, error) {
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return filepath.Join(dir, "opencode.json"), nil
}

// loadRoot reads and parses the config file as JSONC, returning an empty
// object when the file does not yet exist.
func loadRoot(path string) (hujson.Value, error) {
	src := []byte("{}")
	if data, err := os.ReadFile(path); err == nil {
		src = data
	} else if !os.IsNotExist(err) {
		return hujson.Value{}, err
	}
	root, err := hujson.Parse(src)
	if err != nil {
		return hujson.Value{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return root, nil
}

// writeRoot formats and writes the config with 0600 permissions, since it may
// contain secrets.
func writeRoot(path string, root *hujson.Value) error {
	root.Format()
	out := root.Pack()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	// WriteFile does not change perms on an existing file, so enforce them.
	return os.Chmod(path, 0o600)
}

// writeConfig deep-merges a provider block into the existing config, injecting
// nothing the caller did not put in the block. Existing settings and comments
// outside the managed provider block are preserved.
func writeConfig(path, providerID string, block map[string]any, defaultModel string) error {
	root, err := loadRoot(path)
	if err != nil {
		return err
	}

	ptr := "/provider/" + jsonPointerEscape(providerID)
	merged := block
	if existing := root.Find(ptr); existing != nil {
		var cur map[string]any
		if json.Unmarshal(existing.Pack(), &cur) == nil {
			merged = deepMerge(cur, block)
		}
	}

	var ops []map[string]any
	if root.Find("/$schema") == nil {
		ops = append(ops, op("add", "/$schema", "https://opencode.ai/config.json"))
	}
	if root.Find("/provider") == nil {
		ops = append(ops, op("add", "/provider", map[string]any{}))
	}
	ops = append(ops, op("add", ptr, merged))
	if defaultModel != "" {
		ops = append(ops, op("add", "/model", defaultModel))
	}

	if err := applyPatch(&root, ops); err != nil {
		return err
	}
	return writeRoot(path, &root)
}

// removeConfig removes a provider, or specific model keys within it, from the
// config. When modelKeys is empty the whole provider block is removed.
// Returns the number of removals made. The top-level default model is cleared
// when it points at something that was removed.
func removeConfig(path, providerID string, modelKeys []string) (int, error) {
	root, err := loadRoot(path)
	if err != nil {
		return 0, err
	}

	base := "/provider/" + jsonPointerEscape(providerID)
	var ops []map[string]any
	removed := 0

	// Determine the current default model so we know whether to clear it.
	defaultModel := ""
	if m := root.Find("/model"); m != nil {
		_ = json.Unmarshal(m.Pack(), &defaultModel)
	}
	clearDefault := false

	if len(modelKeys) == 0 {
		if root.Find(base) == nil {
			return 0, nil
		}
		ops = append(ops, op2("remove", base))
		removed++
		if strings.HasPrefix(defaultModel, providerID+"/") {
			clearDefault = true
		}
	} else {
		for _, key := range modelKeys {
			ptr := base + "/models/" + jsonPointerEscape(key)
			if root.Find(ptr) == nil {
				continue
			}
			ops = append(ops, op2("remove", ptr))
			removed++
			if defaultModel == providerID+"/"+key {
				clearDefault = true
			}
		}
	}

	if removed == 0 {
		return 0, nil
	}
	if clearDefault && root.Find("/model") != nil {
		ops = append(ops, op2("remove", "/model"))
	}

	if err := applyPatch(&root, ops); err != nil {
		return 0, err
	}
	if err := writeRoot(path, &root); err != nil {
		return 0, err
	}
	return removed, nil
}

// providerState is one configured provider, read back from the opencode config:
// its model keys (sorted), any options.baseURL, and the per-model limit.context
// for those models that set one. It is what `outfit export` reconstructs an
// Outfit from.
type providerState struct {
	modelKeys []string
	baseURL   string
	contexts  map[string]int
}

// loadConfigState reads the opencode config and reports each configured
// provider's state plus the top-level default model. It is the inverse of
// writeConfig, used to reconstruct an Outfit on export.
func loadConfigState(path string) (providers map[string]providerState, defaultModel string, err error) {
	root, err := loadRoot(path)
	if err != nil {
		return nil, "", err
	}
	if m := root.Find("/model"); m != nil {
		_ = json.Unmarshal(m.Pack(), &defaultModel)
	}

	providers = map[string]providerState{}
	pv := root.Find("/provider")
	if pv == nil {
		return providers, defaultModel, nil
	}
	var raw map[string]struct {
		Options struct {
			BaseURL string `json:"baseURL"`
		} `json:"options"`
		Models map[string]struct {
			Limit struct {
				Context int `json:"context"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(pv.Pack(), &raw); err != nil {
		return nil, "", fmt.Errorf("reading providers from %s: %w", path, err)
	}
	for name, p := range raw {
		keys := make([]string, 0, len(p.Models))
		contexts := map[string]int{}
		for k, m := range p.Models {
			keys = append(keys, k)
			if m.Limit.Context > 0 {
				contexts[k] = m.Limit.Context
			}
		}
		sort.Strings(keys)
		providers[name] = providerState{modelKeys: keys, baseURL: p.Options.BaseURL, contexts: contexts}
	}
	return providers, defaultModel, nil
}

// applyPatch marshals and applies an RFC 6902 patch to the config AST.
func applyPatch(root *hujson.Value, ops []map[string]any) error {
	patch, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if err := root.Patch(patch); err != nil {
		return fmt.Errorf("merging config: %w", err)
	}
	return nil
}

func op(kind, path string, value any) map[string]any {
	return map[string]any{"op": kind, "path": path, "value": value}
}

func op2(kind, path string) map[string]any {
	return map[string]any{"op": kind, "path": path}
}

// jsonPointerEscape escapes a path segment for use in an RFC 6901 JSON Pointer.
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// deepMerge returns dst with src merged in recursively; src wins on conflicts.
func deepMerge(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := out[k].(map[string]any); ok {
				out[k] = deepMerge(dv, sv)
				continue
			}
		}
		out[k] = v
	}
	return out
}
