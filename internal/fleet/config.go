// Package fleet observes a set of machines each running `outfit daemon`: it
// parses the fleet.yaml naming them, resolves each node's bearer token, and
// calls the nodes' control APIs.
//
// The file names nodes and how to reach them; it never holds a secret. A node
// that needs a token names the environment variable holding it, resolved the
// way outfit resolves every other secret — the process environment first, then
// a .env beside the file.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/opencode"
)

// DefaultFile is the fleet file consulted when no --fleet is given, resolved
// from the working directory the way ./Outfit is.
const DefaultFile = "fleet.yaml"

// KindDaemon is a node that is a machine running `outfit daemon`, reached over
// its control API. It is the only kind implemented; the field exists so a
// future remote-environment kind (an `outfit remote` environment, reached
// through its stats Lambda) slots in without a file format change.
const KindDaemon = "daemon"

// Prefer is how routing ranks several nodes that could all serve a request.
// Which answer is right depends on the fleet, not on the code, so it is a
// setting rather than a decision.
type Prefer string

const (
	// PreferIdle takes the node inactive longest: work spreads, and a node
	// mid-request is the last one chosen because it is the least idle of
	// all. The default — piling onto a busy engine degrades a session
	// someone is already in, while over-spreading only costs a wake.
	PreferIdle Prefer = "idle"
	// PreferActive takes the most recently active node: sessions
	// consolidate onto one engine, leaving the rest free to be woken for
	// another model or left asleep.
	PreferActive Prefer = "active"
)

// ParsePrefer validates an activity preference from a file or a flag.
func ParsePrefer(s string) (Prefer, error) {
	switch Prefer(s) {
	case PreferIdle, PreferActive:
		return Prefer(s), nil
	}
	return "", fmt.Errorf("unknown preference %q: use %q or %q", s, PreferIdle, PreferActive)
}

// Config is a parsed fleet.yaml: the nodes, plus where the file was read from
// (the directory whose .env supplies token values).
type Config struct {
	Nodes []NodeConfig `yaml:"nodes"`
	// Prefer ranks nodes that could all serve a request. It belongs to the
	// file rather than to a node because it describes how this cluster
	// should be used — spread the work, or consolidate it. Empty means
	// PreferIdle.
	Prefer Prefer `yaml:"prefer"`

	// Path is the file this was read from, and Dir its directory — the .env
	// beside it fills token references.
	Path string `yaml:"-"`
	Dir  string `yaml:"-"`
}

// NodeConfig is one machine as the fleet file describes it. The live node the
// client talks to is a Node (see node.go); this is just the entry.
type NodeConfig struct {
	// Name identifies the node in output and to `fleet start|stop <node>`.
	Name string `yaml:"name"`
	// Host is where the daemon answers — a LAN name, a tailscale name, or an
	// address. Reachability is the client's problem, not the file's.
	Host string `yaml:"host"`
	// Port is the daemon's control API port; zero means the daemon default.
	Port int `yaml:"port"`
	// Kind is the node kind, defaulting to daemon.
	Kind string `yaml:"kind"`
	// TokenEnv names the environment variable holding this node's bearer
	// token. The token itself is never written here. Empty means the daemon
	// needs no token (a loopback-only daemon).
	TokenEnv string `yaml:"tokenEnv"`
	// EngineTokenEnv names the environment variable holding the key this
	// node's *engine* requires — a different credential from the daemon's
	// bearer token, and a node may need either, both, or neither. As with
	// TokenEnv, the value is never written here.
	EngineTokenEnv string `yaml:"engineTokenEnv"`
	// Engine overrides where this node's engine serves, for the setups a
	// daemon cannot describe: an engine behind a reverse proxy, a container
	// publishing it on a different port than it binds inside, a node
	// reached through a tunnel.
	Engine *EngineOverride `yaml:"engine"`
}

// EngineOverride is a node's declared engine endpoint. Each field is optional
// and falls back independently to what routing would otherwise derive — the
// node's own host, and the port and path the daemon reports.
type EngineOverride struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Path string `yaml:"path"`
}

// BaseURL is the root of this node's control API.
func (n NodeConfig) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", n.Host, n.port())
}

// port is the configured port, or the daemon's default.
func (n NodeConfig) port() int {
	if n.Port != 0 {
		return n.Port
	}
	return daemon.DefaultAPIPort
}

// Load reads and validates a fleet file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"no fleet file at %s: create one listing your nodes, or pass --fleet <path>", path)
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.Path = path
	cfg.Dir = filepath.Dir(path)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// Resolve finds the fleet file: an explicit path when given, else
// ./fleet.yaml. The file must exist either way — every fleet command needs to
// know what the fleet is.
func Resolve(flagPath string) (*Config, error) {
	path := flagPath
	if path == "" {
		path = DefaultFile
	}
	return Load(path)
}

// validate checks what the file alone can decide: every node named and
// reachable in principle, names unique, kinds understood.
func (c *Config) validate() error {
	if len(c.Nodes) == 0 {
		return fmt.Errorf("no nodes: list at least one under `nodes:`")
	}
	if c.Prefer != "" {
		if _, err := ParsePrefer(string(c.Prefer)); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if n.Name == "" {
			return fmt.Errorf("node %d has no name", i+1)
		}
		if seen[n.Name] {
			return fmt.Errorf("duplicate node name %q", n.Name)
		}
		seen[n.Name] = true
		if n.Host == "" {
			return fmt.Errorf("node %q has no host", n.Name)
		}
		if n.Kind == "" {
			n.Kind = KindDaemon
		}
		if n.Kind != KindDaemon {
			return fmt.Errorf(
				"node %q has kind %q: only %q is supported yet", n.Name, n.Kind, KindDaemon)
		}
	}
	return nil
}

// Node returns the file entry with this name.
func (c *Config) Node(name string) (NodeConfig, bool) {
	for _, n := range c.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return NodeConfig{}, false
}

// Only narrows the config to one named node, so a command that fans out by
// default can be pointed at a single machine without a second code path: the
// fan-out still runs, over a fleet of one. An unknown name fails here, naming
// what could have been typed, rather than at the socket.
func (c *Config) Only(name string) (*Config, error) {
	entry, ok := c.Node(name)
	if !ok {
		return nil, fmt.Errorf("no node %q in %s (known nodes: %s)",
			name, c.Path, strings.Join(c.Names(), ", "))
	}
	narrowed := *c
	narrowed.Nodes = []NodeConfig{entry}
	return &narrowed, nil
}

// Names lists the node names in file order, for error messages that tell the
// user what they could have typed.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		names = append(names, n.Name)
	}
	return names
}

// Token resolves a node's bearer token: the process environment first, then
// the .env beside the fleet file — the precedence outfit uses everywhere, so
// an exported value wins and the .env only fills a gap. A node naming no
// variable needs no token. A node naming one that is set nowhere is a
// configuration error, reported against that node rather than surfacing later
// as an authentication failure.
func (c *Config) Token(n NodeConfig) (string, error) {
	return c.resolveTokenEnv(n, n.TokenEnv)
}

// EngineToken resolves the key a node's engine requires, from the variable the
// node names. It is a different credential from the daemon's bearer token —
// one authorises driving the node, the other authorises using its engine — but
// it is referenced and resolved identically, so neither is ever written in the
// fleet file.
func (c *Config) EngineToken(n NodeConfig) (string, error) {
	return c.resolveTokenEnv(n, n.EngineTokenEnv)
}

// resolveTokenEnv reads one of a node's token references: the process
// environment first, then the .env beside the fleet file — the precedence
// outfit uses everywhere, so an exported value wins and the .env only fills a
// gap. A node naming no variable needs no token. A node naming one that is set
// nowhere is a configuration error, reported against that node rather than
// surfacing later as an authentication failure.
func (c *Config) resolveTokenEnv(n NodeConfig, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if v := os.Getenv(name); v != "" {
		return v, nil
	}
	vars, err := opencode.ParseEnvFile(filepath.Join(c.Dir, ".env"))
	if err != nil {
		return "", err
	}
	if v := vars[name]; v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"%s is not set (node %q): export it, or put it in the .env beside %s",
		name, n.Name, c.Path)
}
