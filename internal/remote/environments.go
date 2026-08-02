package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configHome returns outfit's own config directory
// (${XDG_CONFIG_HOME:-~/.config}/outfit), where both the legacy remote.json and
// the environments registry live.
func configHome() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "outfit")
}

// remotesRoot is the environments registry directory: one subdirectory per
// named environment, each holding a remote.json.
func remotesRoot() string {
	return filepath.Join(configHome(), "remotes")
}

// EnvDir returns an environment's directory,
// ${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>. A remote deployment's
// state (currently just remote.json) lives here, keyed by name so several
// instances never share a file.
func EnvDir(name string) string {
	return filepath.Join(remotesRoot(), name)
}

// EnvConfigPath returns the remote.json inside an environment's directory.
func EnvConfigPath(name string) string {
	return filepath.Join(EnvDir(name), "remote.json")
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
	entries, err := os.ReadDir(remotesRoot())
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
		if data, err := os.ReadFile(EnvConfigPath(e.Name())); err == nil {
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
	if err := os.MkdirAll(EnvDir(name), 0o700); err != nil {
		return err
	}
	return os.WriteFile(EnvConfigPath(name), append(data, '\n'), 0o600)
}

// LoadDefault loads the remote config used when no Outfit names an environment:
// the `default` environment, falling back to the legacy single per-user file
// (~/.config/outfit/remote.json) for setups that predate the registry. As with
// LoadConfig a missing file is not fatal — environment variables alone may carry
// the config — and finishConfig reports where to put it otherwise.
func LoadDefault(getenv func(string) string) (Config, error) {
	for _, path := range []string{EnvConfigPath("default"), ConfigPath()} {
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
	return finishConfig(Config{}, getenv, EnvConfigPath("default"))
}
