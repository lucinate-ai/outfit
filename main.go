// Command configure-opencode updates the current user's global opencode
// configuration to use OpenRouter with DeepSeek V4 models.
//
// It deep-merges an OpenRouter provider (DeepSeek V4 Flash and Pro) into the
// existing opencode config under ${XDG_CONFIG_HOME:-$HOME/.config}/opencode,
// preserving any other configuration. Unlike a jq-based approach it parses
// JSONC (comments and trailing commas) and keeps comments outside the managed
// provider block. The OpenRouter API key is read from the .env file next to
// this program at run time and injected directly into the config, so opencode
// works from any directory (it only auto-loads .env from the current project).
//
// Usage: go run .   (from this directory)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tailscale/hujson"
)

const (
	apiKeyVar    = "DEEPSEEK_API_KEY"
	defaultModel = "openrouter/deepseek/deepseek-v4-flash"
	schemaURL    = "https://opencode.ai/config.json"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	envFile := envFilePath()

	key, err := readAPIKey(envFile)
	if err != nil {
		return err
	}

	configFile, err := resolveConfigFile()
	if err != nil {
		return err
	}

	if err := writeConfig(configFile, key); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n\n", configFile)
	fmt.Printf("opencode is now configured for the current user to use OpenRouter\n")
	fmt.Printf("with the key from %s (existing config preserved).\n", envFile)
	fmt.Println("Available models:")
	fmt.Println("  - openrouter/deepseek/deepseek-v4-flash  (default)")
	fmt.Println("  - openrouter/deepseek/deepseek-v4-pro")
	fmt.Println()
	fmt.Println("Run 'opencode' from any directory to use the configuration.")
	return nil
}

// envFilePath returns the path to the .env file alongside this program's
// source, mirroring the original script's "next to me" behaviour.
func envFilePath() string {
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Join(filepath.Dir(file), ".env")
	}
	return ".env"
}

// readAPIKey reads and validates the OpenRouter API key from the .env file.
func readAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s not found. Create it with a line:\n  %s=sk-or-v1-...", path, apiKeyVar)
		}
		return "", err
	}

	prefix := apiKeyVar + "="
	value, found := "", false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			value = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"`)
			found = true
			break
		}
	}

	switch {
	case !found || value == "":
		return "", fmt.Errorf("%s is not set in %s.\nAdd your OpenRouter key:  %s=sk-or-v1-...", apiKeyVar, path, apiKeyVar)
	case !strings.HasPrefix(value, "sk-or-"):
		return "", fmt.Errorf("%s does not look like an OpenRouter key (expected sk-or-...)", apiKeyVar)
	}
	return value, nil
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

// writeConfig deep-merges the OpenRouter settings into the existing config
// (creating it if absent), injecting the API key and default model. Existing
// settings and comments are preserved; only the openrouter block and model
// are managed by this tool.
func writeConfig(path, key string) error {
	src := []byte("{}")
	if data, err := os.ReadFile(path); err == nil {
		src = data
	} else if !os.IsNotExist(err) {
		return err
	}

	root, err := hujson.Parse(src)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	// Build our OpenRouter block, deep-merging over anything the user already
	// has under provider.openrouter so their extra models/options survive.
	openrouter := map[string]any{
		"options": map[string]any{"apiKey": key},
		"models": map[string]any{
			"deepseek/deepseek-v4-flash": map[string]any{"name": "DeepSeek V4 Flash"},
			"deepseek/deepseek-v4-pro":   map[string]any{"name": "DeepSeek V4 Pro"},
		},
	}
	if existing := root.Find("/provider/openrouter"); existing != nil {
		var cur map[string]any
		if err := json.Unmarshal(existing.Pack(), &cur); err == nil {
			openrouter = deepMerge(cur, openrouter)
		}
	}

	// Assemble an RFC 6902 patch. Order matters: create parents before
	// children, and create them only when absent so we never replace an
	// existing object (which would drop sibling providers and their comments).
	var ops []map[string]any
	if root.Find("/$schema") == nil {
		ops = append(ops, op("add", "/$schema", schemaURL))
	}
	if root.Find("/provider") == nil {
		ops = append(ops, op("add", "/provider", map[string]any{}))
	}
	ops = append(ops,
		op("add", "/provider/openrouter", openrouter),
		op("add", "/model", defaultModel),
	)

	patch, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if err := root.Patch(patch); err != nil {
		return fmt.Errorf("merging config: %w", err)
	}
	root.Format()

	out := root.Pack()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	// Enforce 0600 even if the file already existed (perms aren't applied to
	// existing files by WriteFile) since it now contains the secret.
	return os.Chmod(path, 0o600)
}

func op(kind, path string, value any) map[string]any {
	return map[string]any{"op": kind, "path": path, "value": value}
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
