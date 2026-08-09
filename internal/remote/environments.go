package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucinate-ai/outfit/internal/config"
)

// ConfigHome returns outfit's own config directory, where both the legacy
// remote.json and the environments registry live. It delegates to
// internal/config.Dir, so the OUTFIT_CONFIG_DIR override and the fallback
// rules are resolved in one place; it fails when the directory cannot be
// determined (see config.Dir).
func ConfigHome() (string, error) {
	return config.Dir()
}

// remotesRoot is the environments registry directory: one subdirectory per
// named environment, each holding a remote.json.
func remotesRoot() (string, error) {
	home, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "remotes"), nil
}

// EnvDir returns an environment's directory, <config-dir>/remotes/<name>. A
// remote deployment's state (currently just remote.json) lives here, keyed by
// name so several instances never share a file.
func EnvDir(name string) (string, error) {
	root, err := remotesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// EnvConfigPath returns the remote.json inside an environment's directory.
func EnvConfigPath(name string) (string, error) {
	dir, err := EnvDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "remote.json"), nil
}

// IsEnvName reports whether a REMOTE value is a bare environment name rather
// than a file path. A name has no path separator and no .json suffix; anything
// path-like is left to resolve as a file, so existing `REMOTE ./remote.json`
// usage is unaffected.
func IsEnvName(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, `/\`) {
		return false
	}
	if strings.HasSuffix(value, ".json") {
		return false
	}
	return true
}

// EnvInfo describes one registered environment for listing. OK is false when
// the environment's remote.json is missing or unreadable.
type EnvInfo struct {
	Name    string
	BaseURL string
	Region  string
	OK      bool
}

// ListEnvironments returns the registered environments, sorted by name. An
// absent registry is not an error — it yields no environments. Each entry's
// remote.json is read best-effort: a directory without a readable one is still
// listed, with OK false, rather than failing the whole listing.
func ListEnvironments() ([]EnvInfo, error) {
	root, err := remotesRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envs []EnvInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := EnvInfo{Name: e.Name()}
		envPath, err := EnvConfigPath(e.Name())
		if err != nil {
			return nil, err
		}
		if data, err := os.ReadFile(envPath); err == nil {
			var cfg Config
			if json.Unmarshal(data, &cfg) == nil {
				info.BaseURL, info.Region, info.OK = cfg.BaseURL, cfg.Region, true
			}
		}
		envs = append(envs, info)
	}
	return envs, nil
}

// SaveEnvironment registers a deployed environment: its remote.json (the
// shared control URLs, region, base URL, and the environment identifier) is
// written under the registry, owner-only, since it names a deployment's URLs
// and address. Registering a second environment never touches the first.
func SaveEnvironment(name string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir, err := EnvDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "remote.json"), append(data, '\n'), 0o600)
}

// LoadDefault loads the remote config used when no Outfit names an environment:
// the `default` environment, falling back to the legacy single per-user file
// (~/.config/outfit/remote.json) for setups that predate the registry. As with
// LoadConfig a missing file is not fatal — environment variables alone may carry
// the config — and finishConfig reports where to put it otherwise.
func LoadDefault(getenv func(string) string) (Config, error) {
	defaultPath, err := EnvConfigPath("default")
	if err != nil {
		return Config{}, err
	}
	legacyPath, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	for _, path := range []string{defaultPath, legacyPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Config{}, err
		}
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, err)
		}
		return finishConfig(cfg, getenv, path)
	}
	return finishConfig(Config{}, getenv, defaultPath)
}
