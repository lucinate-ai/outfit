// Package fleet observes a set of machines each running `spinloop daemon`: it
// parses the fleet.yaml naming them, resolves each node's bearer token, and
// calls the nodes' control APIs.
//
// The file names nodes and how to reach them; it never holds a secret. A node
// that needs a token names the environment variable holding it, resolved the
// way spinloop resolves every other secret — the process environment first, then
// a .env beside the file.
package fleet

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/opencode"
)

// SplitTag divides a tag named the way tags are named in a limit or an item —
// a key and a value joined by = — into its parts. A name with no =, or with an
// empty side, is not a tag.
func SplitTag(tag string) (key, value string, ok bool) {
	i := strings.Index(tag, "=")
	if i <= 0 || i == len(tag)-1 {
		return "", "", false
	}
	return tag[:i], tag[i+1:], true
}

// DefaultFile is the fleet file consulted when no --fleet is given, resolved
// from the working directory the way ./Spinloop is.
const DefaultFile = "fleet.yaml"

// The kinds a node entry can name. The default is daemon; a node with no
// `kind:` is one. Each names how the fleet reaches and drives that node.
const (
	// KindDaemon is a machine running `spinloop daemon`, reached over its
	// control API. Addressed by its `host`.
	KindDaemon = "daemon"
	// KindCloud is an `spinloop cloud` environment, driven through its cloud
	// control plane. Addressed by the registered environment it names.
	KindCloud = "cloud"
)

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

// WakePolicy is whether routing may start an engine on a node that is not
// running one when no running node serves what is wanted. It sits in the fleet
// file beside prefer for the reason prefer does: it describes how this
// cluster is to be used — may work be started on its machines on demand, or
// only used where it is already running.
type WakePolicy string

const (
	// WakeOn starts an engine on an idle node when nothing is serving.
	WakeOn WakePolicy = "on"
	// WakeOff never starts one: a request nothing is serving fails, naming
	// the node that would have been woken and the command that would start it.
	WakeOff WakePolicy = "off"
)

// ParseWakePolicy validates a wake policy from a file.
func ParseWakePolicy(s string) (WakePolicy, error) {
	switch WakePolicy(s) {
	case WakeOn, WakeOff:
		return WakePolicy(s), nil
	}
	return "", fmt.Errorf("unknown wake policy %q: use %q or %q", s, WakeOn, WakeOff)
}

// Wakes reports whether routing may start an engine on a node that is not
// running one. A file that declares nothing wakes, as routing has always done.
func (c *Config) Wakes() bool {
	return c.WakePolicy != WakeOff
}

// NodeWakes reports whether routing may start an engine on entry specifically:
// entry's own wake setting when it names one, taking precedence over the
// fleet-wide policy; the fleet-wide policy otherwise. This is the check a
// candidate search makes per node — Wakes alone answers for a fleet that
// names no per-node override anywhere.
func (c *Config) NodeWakes(entry NodeConfig) bool {
	if entry.WakePolicy != "" {
		return entry.WakePolicy != WakeOff
	}
	return c.Wakes()
}

// AnyNodeWakes reports whether waking is allowed for at least one node in
// the fleet. A caller that pre-empts Wake with a friendlier refusal when
// nothing at all may be woken (naming the node whose config already matches,
// if one does) uses this to decide whether that pre-emption still applies —
// a fleet-wide `wake: off` no longer means nothing wakes, once one node's
// own setting overrides it.
func (c *Config) AnyNodeWakes() bool {
	for _, entry := range c.Nodes {
		if c.NodeWakes(entry) {
			return true
		}
	}
	return false
}

// Concurrency is the fleet's declared capacity: how much work it may take at
// once. It sits in the file beside wake and prefer for the same reason — how
// much work the fleet's machines will take is a property of the fleet, owned
// by the operator who names them. The limits are a ceiling the operator sets,
// not a measurement of the engines' load.
type Concurrency struct {
	// Total is the most work items the fleet may have in flight at once.
	// A pointer, so a declared zero — which is refused — is told apart from
	// a limit the file never named.
	Total *int `yaml:"total"`
	// Tags bounds the items carrying each tag, keyed the way tags are named
	// elsewhere — a key and a value joined by =. A bound on a tag no node
	// carries is accepted: it simply bounds items that can match nothing.
	Tags map[string]int `yaml:"tags"`
}

// TotalLimit returns the fleet-wide limit and whether the file declared one.
func (c *Config) TotalLimit() (int, bool) {
	if c.Concurrency == nil || c.Concurrency.Total == nil {
		return 0, false
	}
	return *c.Concurrency.Total, true
}

// TagLimit returns the limit on the items carrying this tag, named key=value,
// and whether the file declared one.
func (c *Config) TagLimit(tag string) (int, bool) {
	if c.Concurrency == nil {
		return 0, false
	}
	limit, ok := c.Concurrency.Tags[tag]
	return limit, ok
}

// validate checks what the concurrency section alone can decide: a limit that
// is named is a positive integer, and a per-tag limit is keyed the way tags
// are named. A section that names nothing is accepted and bounds nothing.
func (c *Concurrency) validate() error {
	if c.Total != nil {
		if *c.Total <= 0 {
			return fmt.Errorf(
				"the concurrency total is %d: a limit is how many items may be in flight at once, and must be a positive number",
				*c.Total)
		}
	}
	for tag, limit := range c.Tags {
		if limit <= 0 {
			return fmt.Errorf(
				"the concurrency limit on %q is %d: a limit is how many items may be in flight at once, and must be a positive number",
				tag, limit)
		}
		if _, _, ok := SplitTag(tag); !ok {
			return fmt.Errorf(
				"the concurrency limit is keyed by %q: a limit is keyed by a tag, a key and a value joined by =",
				tag)
		}
	}
	return nil
}

// Config is a parsed fleet.yaml: the nodes, plus where the file was read from
// (the directory whose .env supplies token values).
type Config struct {
	Nodes []NodeConfig `yaml:"nodes"`
	// Gateway names the address the fleet is served under by its gateway. A
	// launch routed through this file points the agent at the gateway rather
	// than at a node: the gateway has done the choosing, so routing contacts
	// no node and wakes none. It is a section rather than a node kind because
	// it is not a machine the fleet drives — it has no control API, and no
	// fleet operation but routing ever looks at it.
	Gateway *GatewayConfig `yaml:"gateway"`
	// Prefer ranks nodes that could all serve a request. It belongs to the
	// file rather than to a node because it describes how this cluster
	// should be used — spread the work, or consolidate it. Empty means
	// PreferIdle.
	Prefer Prefer `yaml:"prefer"`
	// WakePolicy is the fleet-wide wake policy: whether routing may start an
	// engine on a node that is not running one. Empty means WakeOn, as
	// routing has always done when the setting is absent.
	WakePolicy WakePolicy `yaml:"wake"`
	// Concurrency is the fleet's declared capacity: how much work it may
	// take at once. Nil means the file declares no limits, and nothing about
	// the file's behaviour changes.
	Concurrency *Concurrency `yaml:"concurrency"`
	// APIKeyEnv names the environment variable holding the key this fleet's
	// cloud nodes require, shared by every one of them: a remote's engine is
	// always gated by its key, so a fleet of clouds can name the variable
	// once rather than on each node — a node's own EngineTokenEnv overrides
	// it. It is a remote-only default: a daemon gates on its own
	// EngineTokenEnv, and a fleet-wide key must not start gating an engine
	// that was never set up to accept one. As with every other secret in
	// this file, the value is never written here.
	APIKeyEnv string `yaml:"apiKeyEnv"`

	// Path is the file this was read from, and Dir its directory — the .env
	// beside it fills token references.
	Path string `yaml:"-"`
	Dir  string `yaml:"-"`
}

// NodeConfig is one machine as the fleet file describes it. The live node the
// client talks to is a Node (see node.go); this is just the entry.
type NodeConfig struct {
	// Name identifies the node in output and to `fleet start|stop <node>`. For
	// a kind-cloud node it is also the key of the registered environment it
	// drives, <config-dir>/clouds/<name>/cloud.json — the environment is
	// already user-named at `spinloop cloud deploy`, so a cloud node has no
	// separate address to give. The control URLs live in that env's cloud.json
	// anyway, so nothing identifying a deployment is written into the fleet file.
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
	// File names the Spinloop file that describes what this node runs —
	// what `spinloop fleet deploy` reads to create a kind: cloud node's
	// environment, and what `spinloop fleet start` reads to tell a kind:
	// daemon node's engine what to run. Resolved relative to the fleet
	// file's directory. Optional: a node's own Name is tried as a
	// registered `spinloop alias`, then as a same-named subdirectory
	// beside the fleet file, before either command gives up on it. Not
	// read by any other fleet command.
	File string `yaml:"file"`
	// InstanceType names the EC2 instance type a kind: cloud node's
	// environment launches as, read by `spinloop fleet deploy` into the
	// deploy config it derives. It is a property of the cloud environment
	// only — a kind: daemon node's hardware is the operator's to choose, so
	// naming one there is a configuration error. Empty means the node's
	// environment launches as the control plane's default type.
	InstanceType string `yaml:"instance-type"`
	// Tags is the operator's description of what work this node can take on
	// — capability, hardware, or anything else a dispatcher matches on. A
	// tag is a claim the file makes about the node: no node reads it, and it
	// never changes what the node's engine runs. Named key=value where tags
	// are named in a limit or an item; HasTag matches on that form.
	Tags map[string]string `yaml:"tags"`
	// WakePolicy overrides the fleet-wide wake policy for this node alone,
	// in the same `on`/`off` shape. Empty means the fleet-wide setting
	// decides for this node, as it always has. It exists because waking is
	// not free the same way on every node — a cloud environment's wake
	// boots a cloud instance, unlike a local daemon's engine — so an
	// operator may want to decide one node's waking on its own terms rather
	// than through a single fleet-wide switch.
	WakePolicy WakePolicy `yaml:"wake,omitempty"`
}

// HasTag reports whether the node carries the tag named key=value. A name
// that is not shaped like a tag matches nothing.
func (n NodeConfig) HasTag(tag string) bool {
	key, value, ok := SplitTag(tag)
	if !ok {
		return false
	}
	return n.Tags[key] == value
}

// EngineOverride is a node's declared engine endpoint. Each field is optional
// and falls back independently to what routing would otherwise derive — the
// node's own host, and the port and path the daemon reports.
type EngineOverride struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Path string `yaml:"path"`
}

// DefaultGatewayTokenEnv is the variable a gateway's token is resolved under
// when the section names none — the standard OpenAI-compatible key variable.
const DefaultGatewayTokenEnv = "OPENAI_API_KEY"

// GatewayConfig is the fleet file's gateway section: the address the fleet is
// served under, and where the client's token for it lives. As with every other
// secret in this file, the token itself is never written here.
type GatewayConfig struct {
	// URL is the gateway's address. It carries a scheme, the way an endpoint
	// value does — the gateway is reached over HTTP, and a bare host names
	// nothing spinloop could dial.
	URL string `yaml:"url"`
	// TokenEnv names the environment variable holding the gateway's token.
	// Empty means DefaultGatewayTokenEnv.
	TokenEnv string `yaml:"tokenEnv"`
	// Name labels this gateway for a harness a launch configures with no
	// model of its own to name it by — see Label. Optional: it exists only
	// to read better than the address-derived default in a model picker.
	Name string `yaml:"name"`
}

// Label names this gateway for display and for keying a harness's provider
// entry when nothing else does: the section's own Name when set, otherwise
// the address's host — enough to tell two gateways apart in a picker without
// requiring every fleet file to name one explicitly.
func (g GatewayConfig) Label() string {
	if g.Name != "" {
		return g.Name
	}
	if u, err := url.Parse(g.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return g.URL
}

// GatewaySection returns the file's gateway section, with its token variable
// defaulted, and whether the file names a gateway at all.
func (c *Config) GatewaySection() (GatewayConfig, bool) {
	if c.Gateway == nil {
		return GatewayConfig{}, false
	}
	gw := *c.Gateway
	if gw.TokenEnv == "" {
		gw.TokenEnv = DefaultGatewayTokenEnv
	}
	return gw, true
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
				"no fleet at %s: create one listing your nodes, or name a target — --fleet <path> for a file, --env <name> for a registered environment", path)
		}
		return nil, err
	}
	if err := tagDuplicates(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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

// ForEnvironment builds the fleet a `--env <name>` target names: one cloud
// node, named by the registered environment whose cloud.json holds its
// control config. A registered environment and a one-node fleet file naming it
// describe the same thing — `fleet deploy` registers an environment under its
// node's name, which is why every other fleet command can find one by name —
// so this is the same fleet, assembled from the registry rather than read from
// a file.
//
// The name is checked here rather than at the socket: a value that is not a
// plain identifier, and a name nothing is registered under, each fail naming
// the fix before any node is contacted.
//
// Path and Dir are deliberately empty. Dir feeds the adjacent-.env lookup that
// resolves a node's token reference, and a cloud node names none — it is
// reached through its control plane with the operator's own credentials — so
// nothing here reads either field. A fleet assembled this way carries none of
// the settings a fleet file supplies (preference, wake policy, gateway,
// concurrency, a fleet-wide key), and takes each of their defaults; all of
// them describe how several nodes are used, which a fleet of one has no
// occasion for.
func ForEnvironment(name string) (*Config, error) {
	if !cloud.IsEnvName(name) {
		return nil, fmt.Errorf(
			"%q is not an environment name: an environment name is a plain identifier, with no path", name)
	}
	// Loading it is the check: every environment resolves the one way, by
	// name, and a name that resolves to nothing fails here rather than at the
	// first control call.
	if _, err := cloud.LoadEnvironment(name, os.Getenv); err != nil {
		return nil, err
	}
	cfg := &Config{Nodes: []NodeConfig{{Name: name, Kind: KindCloud}}}
	// The same validation a parsed file gets, so a fleet of one cannot reach a
	// command in a state a fleet file could not.
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// tagDuplicates walks the raw file for a node whose tags name one key twice.
// The typed parse would refuse the duplicate too, but its error names the key
// and the lines, not the node; the node is the thing the operator needs to
// find, so the check runs first, over the raw mapping, where the node's name
// is to hand.
func tagDuplicates(data []byte) error {
	var raw struct {
		Nodes []struct {
			Name string    `yaml:"name"`
			Tags yaml.Node `yaml:"tags"`
		} `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil // the typed parse reports what is wrong
	}
	for _, n := range raw.Nodes {
		if n.Tags.Kind != yaml.MappingNode {
			continue
		}
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Tags.Content); i += 2 {
			key := n.Tags.Content[i].Value
			if seen[key] {
				return fmt.Errorf("node %q names the tag key %q more than once", n.Name, key)
			}
			seen[key] = true
		}
	}
	return nil
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
	if c.WakePolicy != "" {
		if _, err := ParseWakePolicy(string(c.WakePolicy)); err != nil {
			return err
		}
	}
	if c.Gateway != nil {
		if c.Gateway.URL == "" {
			return fmt.Errorf("the gateway section names no url: name the gateway's address under `url:`")
		}
		// The section's url is an address spinloop dials over HTTP, so it
		// carries a scheme: a bare host names nothing it could reach.
		if !strings.Contains(c.Gateway.URL, "://") {
			return fmt.Errorf(
				"the gateway section's url %q has no scheme: give the gateway's full address, including http:// or https://",
				c.Gateway.URL)
		}
	}
	if c.Concurrency != nil {
		if err := c.Concurrency.validate(); err != nil {
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
		if n.Kind == "" {
			n.Kind = KindDaemon
		}
		if n.WakePolicy != "" {
			if _, err := ParseWakePolicy(string(n.WakePolicy)); err != nil {
				return fmt.Errorf("node %q: %w", n.Name, err)
			}
		}
		for key, value := range n.Tags {
			if key == "" || value == "" {
				return fmt.Errorf(
					"node %q has a tag with an empty key or value: a tag is a key and a value, both named",
					n.Name)
			}
		}
		switch n.Kind {
		case KindDaemon:
			if n.Host == "" {
				return fmt.Errorf("node %q has no host", n.Name)
			}
			if n.InstanceType != "" {
				return fmt.Errorf(
					"node %q is kind %q: instance-type names the cloud environment's machine, and a daemon's hardware is the operator's to choose, not the fleet file's",
					n.Name, KindDaemon)
			}
		case KindCloud:
			// The node's name *is* the registered environment's key, so it must
			// be env-shaped; a path-like name would be read as a registry
			// subdirectory rather than named.
			if !cloud.IsEnvName(n.Name) {
				return fmt.Errorf(
					"node %q is kind %q: its name must be a registered environment name (no /, no .json)",
					n.Name, KindCloud)
			}
			if n.InstanceType != "" && !cloud.IsInstanceType(n.InstanceType) {
				return fmt.Errorf(
					"node %q has instance-type %q, which is not shaped like an EC2 instance type (a family and size separated by a dot, e.g. g6e.xlarge)",
					n.Name, n.InstanceType)
			}
		default:
			return fmt.Errorf(
				"node %q has kind %q: supported kinds are %q and %q",
				n.Name, n.Kind, KindDaemon, KindCloud)
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
	return c.OnlyNames([]string{name})
}

// OnlyNames narrows the config to several named nodes, in the order given,
// so a command that fans out by default can be pointed at exactly the nodes
// named rather than the whole fleet. An unknown name fails here, before any
// node is touched, naming what could have been typed.
func (c *Config) OnlyNames(names []string) (*Config, error) {
	nodes := make([]NodeConfig, 0, len(names))
	for _, name := range names {
		entry, ok := c.Node(name)
		if !ok {
			return nil, fmt.Errorf("no node %q in %s (known nodes: %s)",
				name, c.Path, strings.Join(c.Names(), ", "))
		}
		nodes = append(nodes, entry)
	}
	narrowed := *c
	narrowed.Nodes = nodes
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
// the .env beside the fleet file — the precedence spinloop uses everywhere, so
// an exported value wins and the .env only fills a gap. A node naming no
// variable needs no token. A node naming one that is set nowhere is a
// configuration error, reported against that node rather than surfacing later
// as an authentication failure.
func (c *Config) Token(n NodeConfig) (string, error) {
	return c.resolveTokenEnv(fmt.Sprintf("node %q", n.Name), n.TokenEnv)
}

// EngineToken resolves the key a node's engine requires, from the variable the
// node names. It is a different credential from the daemon's bearer token —
// one authorises driving the node, the other authorises using its engine — but
// it is referenced and resolved identically, so neither is ever written in the
// fleet file.
func (c *Config) EngineToken(n NodeConfig) (string, error) {
	return c.resolveTokenEnv(fmt.Sprintf("node %q", n.Name), n.EngineTokenEnv)
}

// CloudEngineToken resolves the key a cloud node's engine requires: the
// variable the node names when it names one, else the fleet-wide APIKeyEnv.
// A remote's engine is always gated by its key, so a node that names no
// resolvable key fails here, before a launch depends on it — the way every
// other missing secret in this file is named, the node and the fix.
func (c *Config) CloudEngineToken(n NodeConfig) (string, error) {
	name := n.EngineTokenEnv
	if name == "" {
		name = c.APIKeyEnv
	}
	if name == "" {
		return "", fmt.Errorf(
			"node %q is a cloud environment, so its engine key must be set: name the variable holding it, in this node's `engineTokenEnv` or the file's fleet-wide `apiKeyEnv` (%s)",
			n.Name, c.Path)
	}
	return c.resolveTokenEnv(fmt.Sprintf("node %q", n.Name), name)
}

// GatewayToken resolves the gateway's bearer token from the named variable:
// the process environment first, then the .env beside the fleet file — the
// precedence spinloop uses everywhere, so an exported value wins and the .env
// only fills a gap.
func (c *Config) GatewayToken(name string) (string, error) {
	return c.resolveTokenEnv("the gateway", name)
}

// resolveTokenEnv reads one of the file's token references: the process
// environment first, then the .env beside the fleet file — the precedence
// spinloop uses everywhere, so an exported value wins and the .env only fills a
// gap. A reference naming no variable needs no token. One naming a variable
// that is set nowhere is a configuration error, reported against its owner
// rather than surfacing later as an authentication failure.
func (c *Config) resolveTokenEnv(who, name string) (string, error) {
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
		"%s is not set (%s): export it, or put it in the .env beside %s",
		name, who, c.Path)
}
