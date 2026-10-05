package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// writeFleet puts a fleet.yaml (and optionally a .env) in a temp dir and
// returns the file's path.
func writeFleet(t *testing.T, body string, dotEnv string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFile)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if dotEnv != "" {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotEnv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLoadMultiNode(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
  - name: gpu-box
    host: 198.51.100.7
    port: 5252
    tokenEnv: GPU_BOX_TOKEN
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(cfg.Nodes))
	}

	// A node with no port takes the daemon's default — the two sides share
	// the constant, so this cannot drift.
	studio := cfg.Nodes[0]
	if studio.Name != "studio" || studio.Host != "studio.local" {
		t.Errorf("node 0 = %+v", studio)
	}
	if want := "http://studio.local:" + strconv.Itoa(daemon.DefaultAPIPort); studio.BaseURL() != want {
		t.Errorf("BaseURL = %q, want %q", studio.BaseURL(), want)
	}
	// Kind defaults to daemon.
	if studio.Kind != KindDaemon {
		t.Errorf("kind = %q, want %q", studio.Kind, KindDaemon)
	}

	if got := cfg.Nodes[1].BaseURL(); got != "http://198.51.100.7:5252" {
		t.Errorf("explicit port BaseURL = %q", got)
	}
	if got := cfg.Names(); len(got) != 2 || got[0] != "studio" || got[1] != "gpu-box" {
		t.Errorf("Names() = %v, want file order", got)
	}
	if _, ok := cfg.Node("gpu-box"); !ok {
		t.Error("Node(gpu-box) not found")
	}
	if _, ok := cfg.Node("nope"); ok {
		t.Error("Node(nope) unexpectedly found")
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: a.local
  - name: studio
    host: b.local
`, "")
	_, err := Load(path)
	if err == nil {
		t.Fatal("duplicate node names accepted")
	}
	if !strings.Contains(err.Error(), "studio") {
		t.Errorf("error %q does not name the duplicate", err)
	}
}

func TestLoadRejectsIncompleteNodes(t *testing.T) {
	for name, body := range map[string]string{
		"no nodes":                 "nodes: []\n",
		"no name":                  "nodes:\n  - host: a.local\n",
		"no host":                  "nodes:\n  - name: studio\n",
		"cloud name is a path":     "nodes:\n  - name: a/b\n    kind: cloud\n",
		"cloud name has .json":     "nodes:\n  - name: prod.json\n    kind: cloud\n",
		"daemon instance-type":     "nodes:\n  - name: studio\n    host: a.local\n    instance-type: g6e.xlarge\n",
		"remote bad instance-type": "nodes:\n  - name: prod\n    kind: cloud\n    instance-type: g6exlarge\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFleet(t, body, "")); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

// An unimplemented kind must say so rather than be silently skipped.
func TestLoadUnknownKindNamesIt(t *testing.T) {
	_, err := Load(writeFleet(t, "nodes:\n  - name: prod\n    host: a.local\n    kind: satellite\n", ""))
	if err == nil || !strings.Contains(err.Error(), "satellite") {
		t.Fatalf("error = %v, want one naming the unsupported kind", err)
	}
}

// A kind-cloud node's name is the registered environment it drives; it needs
// no host, and nothing else to name it with.
func TestLoadCloudKindNamedByEnvironment(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: prod
    kind: cloud
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n := cfg.Nodes[0]
	if n.Kind != KindCloud || n.Name != "prod" {
		t.Errorf("cloud node = %+v, want kind remote named prod", n)
	}
	if n.Host != "" {
		t.Errorf("cloud node needs no host, got %q", n.Host)
	}
}

// A kind-cloud node may name the instance type its environment launches as;
// the field parses onto the node so `fleet deploy` can read it.
func TestLoadCloudKindInstanceType(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: prod
    kind: cloud
    instance-type: g6e.2xlarge
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nodes[0].InstanceType != "g6e.2xlarge" {
		t.Errorf("instance-type = %q, want g6e.2xlarge", cfg.Nodes[0].InstanceType)
	}
}

// A cloud node naming a malformed instance type is a configuration error that
// names both the node and the value, not a silent deploy of junk.
func TestLoadCloudKindBadInstanceTypeNamesIt(t *testing.T) {
	_, err := Load(writeFleet(t, `
nodes:
  - name: prod
    kind: cloud
    instance-type: g6exlarge
`, ""))
	if err == nil {
		t.Fatal("accepted a cloud node with a malformed instance-type")
	}
	msg := err.Error()
	if !strings.Contains(msg, "prod") || !strings.Contains(msg, "g6exlarge") {
		t.Errorf("error %q does not name the node and the value", msg)
	}
}

// A daemon node's hardware is the operator's to choose, so naming an instance
// type on one is a configuration error, not silently ignored.
func TestLoadDaemonKindInstanceTypeNamesIt(t *testing.T) {
	_, err := Load(writeFleet(t, `
nodes:
  - name: studio
    host: a.local
    instance-type: g6e.xlarge
`, ""))
	if err == nil {
		t.Fatal("accepted a daemon node with an instance-type")
	}
	if !strings.Contains(err.Error(), "studio") {
		t.Errorf("error %q does not name the node", err)
	}
}

func TestResolveDefaultAndExplicit(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: studio\n    host: a.local\n", "")

	// Explicit path.
	if _, err := Resolve(path); err != nil {
		t.Fatal(err)
	}

	// Default: ./fleet.yaml in the working directory.
	t.Chdir(filepath.Dir(path))
	cfg, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != DefaultFile {
		t.Errorf("Path = %q, want %q", cfg.Path, DefaultFile)
	}
}

func TestResolveMissingFileNamesThePath(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := Resolve("")
	if err == nil {
		t.Fatal("missing fleet file did not error")
	}
	if !strings.Contains(err.Error(), DefaultFile) {
		t.Errorf("error %q does not name the expected path", err)
	}
}

func TestTokenResolution(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: plain
    host: a.local
  - name: gpu-box
    host: b.local
    tokenEnv: GPU_BOX_TOKEN
`, "GPU_BOX_TOKEN=from-dotenv\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := cfg.Node("plain")
	gpu, _ := cfg.Node("gpu-box")

	// No reference: no token, no error — a loopback daemon needs none.
	if tok, err := cfg.Token(plain); err != nil || tok != "" {
		t.Errorf("Token(plain) = %q, %v; want empty, nil", tok, err)
	}

	// Falls back to the .env beside the file.
	t.Setenv("GPU_BOX_TOKEN", "")
	if tok, err := cfg.Token(gpu); err != nil || tok != "from-dotenv" {
		t.Errorf("Token(gpu) = %q, %v; want from-dotenv", tok, err)
	}

	// An exported value wins over the .env.
	t.Setenv("GPU_BOX_TOKEN", "from-env")
	if tok, err := cfg.Token(gpu); err != nil || tok != "from-env" {
		t.Errorf("Token(gpu) = %q, %v; want from-env (environment beats .env)", tok, err)
	}
}

func TestTokenUnsetIsAConfigError(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: gpu-box\n    host: b.local\n    tokenEnv: MISSING_TOKEN\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gpu-box")
	t.Setenv("MISSING_TOKEN", "")
	_, err = cfg.Token(node)
	if err == nil {
		t.Fatal("an unset token variable was accepted as an empty token")
	}
	// The message must name the variable and the node, so a typo is obvious.
	for _, want := range []string{"MISSING_TOKEN", "gpu-box"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The file format carries a token *reference*, never a value: a stray `token:`
// key is not a field, so it cannot smuggle a secret into the file.
func TestLiteralTokenIsNotAField(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: gpu-box\n    host: b.local\n    token: sk-literal-secret\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gpu-box")
	if node.TokenEnv != "" {
		t.Errorf("a literal token populated TokenEnv: %q", node.TokenEnv)
	}
	tok, err := cfg.Token(node)
	if err != nil || tok != "" {
		t.Errorf("Token = %q, %v; a literal `token:` must not be used", tok, err)
	}
}

// A node may declare where its engine serves, for the setups a daemon cannot
// describe. Each field falls back independently.
func TestEngineOverride(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: proxied
    host: node.local
    engine:
      host: proxy.local
      port: 8443
      path: /openai
  - name: published
    host: node2.local
    engine:
      port: 18080
  - name: plain
    host: node3.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	full, _ := cfg.Node("proxied")
	if full.Engine == nil {
		t.Fatal("engine block not parsed")
	}
	if full.Engine.Host != "proxy.local" || full.Engine.Port != 8443 || full.Engine.Path != "/openai" {
		t.Errorf("engine = %+v", *full.Engine)
	}

	partial, _ := cfg.Node("published")
	if partial.Engine == nil || partial.Engine.Port != 18080 {
		t.Fatalf("partial override lost its port: %+v", partial.Engine)
	}
	if partial.Engine.Host != "" {
		t.Errorf("an undeclared host should stay empty so it falls back, got %q", partial.Engine.Host)
	}

	if plain, _ := cfg.Node("plain"); plain.Engine != nil {
		t.Errorf("a node with no engine block should carry none, got %+v", plain.Engine)
	}
}

// The engine key is a reference like the daemon token, resolved the same way,
// and the two are independent credentials.
func TestEngineTokenResolution(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gated
    host: gated.local
    tokenEnv: NODE_TOKEN
    engineTokenEnv: NODE_ENGINE_KEY
  - name: open
    host: open.local
`, "NODE_ENGINE_KEY=from-dotenv\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gated, _ := cfg.Node("gated")

	// The .env beside the file fills a gap.
	got, err := cfg.EngineToken(gated)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-dotenv" {
		t.Errorf("engine token = %q, want the .env value", got)
	}

	// An exported value wins, as everywhere else in spinloop.
	t.Setenv("NODE_ENGINE_KEY", "exported")
	if got, err := cfg.EngineToken(gated); err != nil || got != "exported" {
		t.Errorf("engine token = %q, %v; want the exported value", got, err)
	}

	// A node naming no engine variable needs no engine key.
	open, _ := cfg.Node("open")
	if got, err := cfg.EngineToken(open); err != nil || got != "" {
		t.Errorf("token = %q, %v; want empty and no error", got, err)
	}
}

func TestEngineTokenUnsetNamesTheVariable(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gated
    host: gated.local
    engineTokenEnv: NOWHERE_ENGINE_KEY
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gated")
	_, err = cfg.EngineToken(node)
	if err == nil {
		t.Fatal("an unset engine token variable should be a config error")
	}
	for _, want := range []string{"NOWHERE_ENGINE_KEY", "gated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s, got %q", want, err)
		}
	}
}

// The fleet-wide key is a remote-only default: resolved like every other
// secret in the file, a node's own reference overrides it, and a remote that
// names no resolvable key is named for it.
func TestCloudEngineTokenResolution(t *testing.T) {
	path := writeFleet(t, `
apiKeyEnv: FLEET_KEY
nodes:
  - name: shared
    kind: cloud
  - name: own
    kind: cloud
    engineTokenEnv: OWN_KEY
  - name: box
    host: box.local
`, "FLEET_KEY=from-dotenv\nOWN_KEY=own-dotenv\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	shared, _ := cfg.Node("shared")
	// The fleet's variable, from the .env beside the file.
	if got, err := cfg.CloudEngineToken(shared); err != nil || got != "from-dotenv" {
		t.Errorf("key = %q, %v; want the fleet .env value", got, err)
	}
	// An exported value wins, as everywhere else in spinloop.
	t.Setenv("FLEET_KEY", "exported")
	if got, err := cfg.CloudEngineToken(shared); err != nil || got != "exported" {
		t.Errorf("key = %q, %v; want the exported value", got, err)
	}

	// A node's own reference overrides the fleet-wide one.
	own, _ := cfg.Node("own")
	if got, err := cfg.CloudEngineToken(own); err != nil || got != "own-dotenv" {
		t.Errorf("key = %q, %v; want the node's own value", got, err)
	}

	// A daemon is gated only by its own reference: the fleet-wide key does
	// not reach it.
	box, _ := cfg.Node("box")
	if got, err := cfg.EngineToken(box); err != nil || got != "" {
		t.Errorf("daemon engine token = %q, %v; want empty and no error", got, err)
	}
}

func TestCloudEngineTokenUnsetNamesTheVariable(t *testing.T) {
	path := writeFleet(t, `
apiKeyEnv: NOWHERE_FLEET_KEY
nodes:
  - name: shared
    kind: cloud
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("shared")
	_, err = cfg.CloudEngineToken(node)
	if err == nil {
		t.Fatal("an unset fleet key variable should be a config error")
	}
	for _, want := range []string{"NOWHERE_FLEET_KEY", "shared", "fleet.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s, got %q", want, err)
		}
	}
}

func TestCloudEngineTokenMissingNamesBothPlaces(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: shared
    kind: cloud
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("shared")
	_, err = cfg.CloudEngineToken(node)
	if err == nil {
		t.Fatal("a remote naming no key anywhere should be a config error")
	}
	for _, want := range []string{"shared", "engineTokenEnv", "apiKeyEnv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s, got %q", want, err)
		}
	}
}

// A fleet file written for a newer spinloop — carrying fields this one does not
// know — must still parse: yaml.Unmarshal ignores unknown fields, so an older
// binary reading a newer file is a no-op, not an error.
func TestLoadIgnoresUnknownFleetFields(t *testing.T) {
	path := writeFleet(t, `
apiKeyEnv: SHARED_KEY
nodes:
  - name: shared
    kind: cloud
`, "")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The same file into a struct without the field, the way an older binary
	// would read it.
	var legacy struct {
		Nodes []NodeConfig `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("a fleet file with an unknown field must parse: %v", err)
	}
	// And the current one keeps the field.
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKeyEnv != "SHARED_KEY" {
		t.Errorf("apiKeyEnv = %q, want SHARED_KEY", cfg.APIKeyEnv)
	}
}

func TestPreferSetting(t *testing.T) {
	cases := map[string]Prefer{
		"prefer: active\n": PreferActive,
		"prefer: idle\n":   PreferIdle,
		"":                 "", // absent: the selector applies the idle default
	}
	for decl, want := range cases {
		t.Run(strings.TrimSpace(decl), func(t *testing.T) {
			cfg, err := Load(writeFleet(t, decl+"nodes:\n  - name: a\n    host: a.local\n", ""))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Prefer != want {
				t.Errorf("Prefer = %q, want %q", cfg.Prefer, want)
			}
		})
	}
}

// The file field names a node's Spinloop source; it is stored as declared
// (resolution relative to the fleet directory is resolveNodeSpinloop's job,
// not parsing's), needs no particular kind, and is optional.
func TestFileField(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gpu-env
    kind: cloud
    file: ./envs/gpu.Spinloop
  - name: dev-1
    host: dev1.local
    file: ../shared/dev.Spinloop
  - name: plain
    host: plain.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cloud, _ := cfg.Node("gpu-env")
	if cloud.File != "./envs/gpu.Spinloop" {
		t.Errorf("cloud node File = %q", cloud.File)
	}
	daemonNode, _ := cfg.Node("dev-1")
	if daemonNode.File != "../shared/dev.Spinloop" {
		t.Errorf("daemon node File = %q, want it to parse the same as any other kind", daemonNode.File)
	}
	plain, _ := cfg.Node("plain")
	if plain.File != "" {
		t.Errorf("plain node File = %q, want empty", plain.File)
	}
}

func TestOnlyNames(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: a
    host: a.local
  - name: b
    host: b.local
  - name: c
    host: c.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	narrowed, err := cfg.OnlyNames([]string{"c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := narrowed.Names(); len(got) != 2 || got[0] != "c" || got[1] != "a" {
		t.Errorf("OnlyNames order = %v, want [c a] (the order given, not file order)", got)
	}

	if _, err := cfg.OnlyNames([]string{"a", "nope"}); err == nil {
		t.Fatal("an unknown name among several should fail")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q does not name the unknown node", err)
	}

	// Only(name) is OnlyNames([]string{name}), unchanged for its own callers.
	one, err := cfg.Only("b")
	if err != nil {
		t.Fatal(err)
	}
	if got := one.Names(); len(got) != 1 || got[0] != "b" {
		t.Errorf("Only(b).Names() = %v, want [b]", got)
	}
}

func TestPreferRejectsUnknownValue(t *testing.T) {
	_, err := Load(writeFleet(t, "prefer: whatever\nnodes:\n  - name: a\n    host: a.local\n", ""))
	if err == nil {
		t.Fatal("an unknown prefer value should fail to parse")
	}
	for _, want := range []string{"idle", "active"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got %q", want, err)
		}
	}
}

func TestWakeSetting(t *testing.T) {
	cases := []struct {
		decl string
		wake bool
	}{
		{"wake: on\n", true},
		{"wake: off\n", false},
		{"", true}, // absent: routing wakes, as it always has
	}
	for _, c := range cases {
		name := strings.TrimSpace(c.decl)
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeFleet(t, c.decl+"nodes:\n  - name: a\n    host: a.local\n", ""))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Wakes(); got != c.wake {
				t.Errorf("Wakes() = %v, want %v", got, c.wake)
			}
		})
	}
}

func TestWakeRejectsUnknownValue(t *testing.T) {
	_, err := Load(writeFleet(t, "wake: sometimes\nnodes:\n  - name: a\n    host: a.local\n", ""))
	if err == nil {
		t.Fatal("an unknown wake value should fail to parse")
	}
	for _, want := range []string{"on", "off"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got %q", want, err)
		}
	}
}

// A node's own wake setting overrides the fleet-wide one for that node
// alone; a node naming none is governed by the fleet-wide setting, exactly
// as before per-node overrides existed.
func TestNodeWakeOverride(t *testing.T) {
	cases := []struct {
		name      string
		fleet     string
		wantFleet bool
		wantNode  bool
	}{
		{"node off overrides fleet on",
			"wake: on\nnodes:\n  - name: a\n    host: a.local\n    wake: off\n", true, false},
		{"node on overrides fleet off",
			"wake: off\nnodes:\n  - name: a\n    host: a.local\n    wake: on\n", false, true},
		{"node names none, fleet on",
			"wake: on\nnodes:\n  - name: a\n    host: a.local\n", true, true},
		{"node names none, fleet off",
			"wake: off\nnodes:\n  - name: a\n    host: a.local\n", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(writeFleet(t, c.fleet, ""))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Wakes(); got != c.wantFleet {
				t.Errorf("Wakes() = %v, want %v", got, c.wantFleet)
			}
			if got := cfg.NodeWakes(cfg.Nodes[0]); got != c.wantNode {
				t.Errorf("NodeWakes(a) = %v, want %v", got, c.wantNode)
			}
		})
	}
}

// AnyNodeWakes is true whenever at least one node may be woken, whichever
// level decides it for that node — not just when the fleet-wide setting is
// on.
func TestAnyNodeWakes(t *testing.T) {
	cases := []struct {
		name string
		file string
		want bool
	}{
		{"fleet wakes, no override", "wake: on\nnodes:\n  - name: a\n    host: a.local\n", true},
		{"fleet off, no override anywhere",
			"wake: off\nnodes:\n  - name: a\n    host: a.local\n  - name: b\n    host: b.local\n", false},
		{"fleet off, one node opts in",
			"wake: off\nnodes:\n  - name: a\n    host: a.local\n  - name: b\n    host: b.local\n    wake: on\n", true},
		{"fleet on, every node opts out",
			"wake: on\nnodes:\n  - name: a\n    host: a.local\n    wake: off\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(writeFleet(t, c.file, ""))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.AnyNodeWakes(); got != c.want {
				t.Errorf("AnyNodeWakes() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNodeWakeRejectsUnknownValue(t *testing.T) {
	_, err := Load(writeFleet(t, "nodes:\n  - name: a\n    host: a.local\n    wake: sometimes\n", ""))
	if err == nil {
		t.Fatal("an unknown per-node wake value should fail to parse")
	}
	for _, want := range []string{"on", "off", "a"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got %q", want, err)
		}
	}
}

func TestGatewaySection(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
gateway:
  url: https://gw.example.com
  tokenEnv: GW_TOKEN
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gw, ok := cfg.GatewaySection()
	if !ok {
		t.Fatal("a file with a gateway section should report one")
	}
	if gw.URL != "https://gw.example.com" || gw.TokenEnv != "GW_TOKEN" {
		t.Errorf("gateway = %+v", gw)
	}
}

// A section that names no tokenEnv resolves under the default variable.
func TestGatewaySectionTokenDefaults(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
gateway:
  url: https://gw.example.com
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gw, ok := cfg.GatewaySection()
	if !ok {
		t.Fatal("a file with a gateway section should report one")
	}
	if gw.TokenEnv != DefaultGatewayTokenEnv {
		t.Errorf("tokenEnv = %q, want the default %q", gw.TokenEnv, DefaultGatewayTokenEnv)
	}
}

// The gateway's token resolves the way the file's tokens do: the
// environment first, then the .env beside the file.
func TestGatewayToken(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
gateway:
  url: https://gw.example.com
  tokenEnv: GW_TOKEN
`, "GW_TOKEN=from-dotenv")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// Falls back to the .env beside the file.
	t.Setenv("GW_TOKEN", "")
	if tok, err := cfg.GatewayToken("GW_TOKEN"); err != nil || tok != "from-dotenv" {
		t.Errorf("GatewayToken = %q, %v; want from-dotenv", tok, err)
	}

	// An exported value wins over the .env.
	t.Setenv("GW_TOKEN", "from-env")
	if tok, err := cfg.GatewayToken("GW_TOKEN"); err != nil || tok != "from-env" {
		t.Errorf("GatewayToken = %q, %v; want from-env (environment beats .env)", tok, err)
	}

	// Set nowhere: the error names the variable and the file's .env.
	t.Setenv("GW_TOKEN", "")
	os.Remove(filepath.Join(filepath.Dir(path), ".env"))
	if _, err := cfg.GatewayToken("GW_TOKEN"); err == nil ||
		!strings.Contains(err.Error(), "GW_TOKEN") || !strings.Contains(err.Error(), "fleet.yaml") {
		t.Errorf("an unset gateway token should fail naming the variable and the file, got %v", err)
	}
}

// No section: the accessor says so, and the file behaves as it always has.
func TestNoGatewaySection(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.GatewaySection(); ok {
		t.Error("a file without a gateway section should report none")
	}
}

func TestLoadRefusesAGatewayWithoutAURL(t *testing.T) {
	_, err := Load(writeFleet(t,
		"nodes:\n  - name: a\n    host: a.local\ngateway:\n  tokenEnv: GW_TOKEN\n", ""))
	if err == nil {
		t.Fatal("a gateway section naming no url should be refused")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("the refusal should name the missing field, got %q", err)
	}
}

func TestLoadRefusesAGatewayURLWithoutAScheme(t *testing.T) {
	_, err := Load(writeFleet(t,
		"nodes:\n  - name: a\n    host: a.local\ngateway:\n  url: gw.example.com\n", ""))
	if err == nil {
		t.Fatal("a gateway url without a scheme should be refused")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("the refusal should say the url lacks a scheme, got %q", err)
	}
}

// TestGatewayConfigLabel covers Label()'s precedence: an explicit name wins,
// otherwise the address's host, otherwise the raw address when it does not
// even parse as a URL with a host.
func TestGatewayConfigLabel(t *testing.T) {
	cases := []struct {
		name string
		gw   GatewayConfig
		want string
	}{
		{"explicit name wins", GatewayConfig{URL: "http://localhost:4000", Name: "remote-llms"}, "remote-llms"},
		{"falls back to the URL's host", GatewayConfig{URL: "http://localhost:4000"}, "localhost:4000"},
		{"falls back to the raw value with no host to parse", GatewayConfig{URL: "not-a-url"}, "not-a-url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.gw.Label(); got != c.want {
				t.Errorf("Label() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestLoadParsesAGatewayName confirms the optional `name` field round-trips
// from the fleet file into GatewaySection.
func TestLoadParsesAGatewayName(t *testing.T) {
	cfg, err := Load(writeFleet(t,
		"nodes:\n  - name: a\n    host: a.local\ngateway:\n  url: http://gw.internal:4000\n  name: remote-llms\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	gw, ok := cfg.GatewaySection()
	if !ok {
		t.Fatal("the file names a gateway")
	}
	if gw.Name != "remote-llms" {
		t.Errorf("Name = %q, want %q", gw.Name, "remote-llms")
	}
	if gw.Label() != "remote-llms" {
		t.Errorf("Label() = %q, want %q", gw.Label(), "remote-llms")
	}
}

func TestSplitTag(t *testing.T) {
	cases := []struct {
		tag        string
		key, value string
		ok         bool
	}{
		{"gpu=a100", "gpu", "a100", true},
		{"model=qwen3.8-27b", "model", "qwen3.8-27b", true},
		{"=a100", "", "", false}, // no key
		{"gpu=", "", "", false},  // no value
		{"gpu", "", "", false},   // no =
		{"", "", "", false},
		{"a=b=c", "a", "b=c", true}, // the first = divides
	}
	for _, c := range cases {
		key, value, ok := SplitTag(c.tag)
		if ok != c.ok || key != c.key || value != c.value {
			t.Errorf("SplitTag(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.tag, key, value, ok, c.key, c.value, c.ok)
		}
	}
}

func TestNodeTags(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gpu-box
    host: gpu.local
    tags:
      gpu: a100
      os: linux
  - name: studio
    host: studio.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	box := cfg.Nodes[0]
	if len(box.Tags) != 2 || box.Tags["gpu"] != "a100" || box.Tags["os"] != "linux" {
		t.Errorf("tags = %+v", box.Tags)
	}
	if !box.HasTag("gpu=a100") || !box.HasTag("os=linux") {
		t.Error("a node carries the tags it declares")
	}
	if box.HasTag("gpu=h100") || box.HasTag("cpu=arm") || box.HasTag("gpu") {
		t.Error("a node does not carry tags it does not declare")
	}
	// An untagged node carries nothing and matches only untagged work.
	if has := cfg.Nodes[1].HasTag("gpu=a100"); has {
		t.Error("an untagged node carries no tags")
	}
}

func TestNodeTagsRefuseADuplicateKey(t *testing.T) {
	_, err := Load(writeFleet(t, `
nodes:
  - name: gpu-box
    host: gpu.local
    tags:
      gpu: a100
      gpu: h100
`, ""))
	if err == nil {
		t.Fatal("a tag key named twice should be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "gpu-box") || !strings.Contains(msg, "gpu") {
		t.Errorf("the refusal should name the node and the key, got %q", msg)
	}
}

func TestNodeTagsRefuseAnEmptySide(t *testing.T) {
	_, err := Load(writeFleet(t, `
nodes:
  - name: gpu-box
    host: gpu.local
    tags:
      gpu: ""
`, ""))
	if err == nil {
		t.Fatal("a tag with an empty value should be refused")
	}
	if !strings.Contains(err.Error(), "gpu-box") {
		t.Errorf("the refusal should name the node, got %q", err)
	}
}

func TestConcurrencySection(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: a
    host: a.local
concurrency:
  total: 10
  tags:
    "gpu=a100": 4
    "os=linux": 6
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	total, ok := cfg.TotalLimit()
	if !ok || total != 10 {
		t.Errorf("TotalLimit = (%d, %v), want (10, true)", total, ok)
	}
	if limit, ok := cfg.TagLimit("gpu=a100"); !ok || limit != 4 {
		t.Errorf("TagLimit(gpu=a100) = (%d, %v), want (4, true)", limit, ok)
	}
	if limit, ok := cfg.TagLimit("os=linux"); !ok || limit != 6 {
		t.Errorf("TagLimit(os=linux) = (%d, %v), want (6, true)", limit, ok)
	}
	if _, ok := cfg.TagLimit("gpu=h100"); ok {
		t.Error("no limit is declared on gpu=h100")
	}
}

func TestConcurrencyRefusesANonPositiveTotal(t *testing.T) {
	for _, total := range []string{"0", "-1"} {
		_, err := Load(writeFleet(t,
			"nodes:\n  - name: a\n    host: a.local\nconcurrency:\n  total: "+total+"\n", ""))
		if err == nil {
			t.Fatalf("a total of %s should be refused", total)
		}
		if !strings.Contains(err.Error(), "total") {
			t.Errorf("the refusal should name the total, got %q", err)
		}
	}
}

func TestConcurrencyRefusesANonPositiveTagLimit(t *testing.T) {
	for _, limit := range []string{"0", "-2"} {
		_, err := Load(writeFleet(t,
			"nodes:\n  - name: a\n    host: a.local\nconcurrency:\n  tags:\n    \"gpu=a100\": "+limit+"\n", ""))
		if err == nil {
			t.Fatalf("a tag limit of %s should be refused", limit)
		}
		if !strings.Contains(err.Error(), "gpu=a100") {
			t.Errorf("the refusal should name the tag, got %q", err)
		}
	}
}

func TestConcurrencyRefusesATagKeyShapedWrong(t *testing.T) {
	_, err := Load(writeFleet(t,
		"nodes:\n  - name: a\n    host: a.local\nconcurrency:\n  tags:\n    gpu: 4\n", ""))
	if err == nil {
		t.Fatal("a limit keyed without a = should be refused")
	}
	if !strings.Contains(err.Error(), "keyed by") {
		t.Errorf("the refusal should say how a limit is keyed, got %q", err)
	}
}

func TestConcurrencyAllowsALimitOnATagNoNodeCarries(t *testing.T) {
	cfg, err := Load(writeFleet(t, `
nodes:
  - name: a
    host: a.local
concurrency:
  tags:
    "gpu=a100": 4
`, ""))
	if err != nil {
		t.Fatal("a limit on a tag no node carries is accepted")
	}
	if limit, ok := cfg.TagLimit("gpu=a100"); !ok || limit != 4 {
		t.Errorf("TagLimit = (%d, %v), want (4, true)", limit, ok)
	}
}

func TestConcurrencyAbsentDeclaresNothing(t *testing.T) {
	cfg, err := Load(writeFleet(t, "nodes:\n  - name: a\n    host: a.local\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := cfg.TotalLimit(); ok || total != 0 {
		t.Errorf("TotalLimit = (%d, %v), want (0, false)", total, ok)
	}
	if limit, ok := cfg.TagLimit("gpu=a100"); ok || limit != 0 {
		t.Errorf("TagLimit = (%d, %v), want (0, false)", limit, ok)
	}
}
