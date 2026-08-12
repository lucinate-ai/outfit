// Node selection: which machine in the fleet serves the agent a launch is
// about to start. The ranking is pure and the I/O is not, kept apart so the
// rules — prefer a node already serving the model, spread or consolidate,
// never displace a running engine — are testable without a network.

package fleet

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// DefaultPath is the OpenAI-compatible prefix appended to a node's engine
// address when neither the node nor its daemon names another.
const DefaultPath = "/v1"

// Want describes what a launch is looking for. Model and Alias are the two
// names a node might report serving — an Outfit's MODEL, and the ALIAS the
// engine may be serving it under — and either matching is a match.
type Want struct {
	Model string
	Alias string
	// Node pins the selection to one node by name, skipping the search.
	Node string
	// Prefer ranks nodes that all match; empty means PreferIdle.
	Prefer Prefer
}

// prefer is the ranking in force, defaulting to idle.
func (w Want) prefer() Prefer {
	if w.Prefer == "" {
		return PreferIdle
	}
	return w.Prefer
}

// matches reports whether a node serving `serving` is serving what is wanted.
// A launch that names no model wants any running engine.
func (w Want) matches(serving string) bool {
	if w.Model == "" && w.Alias == "" {
		return true
	}
	return serving != "" && (serving == w.Model || serving == w.Alias)
}

// wanted names the model for a message, preferring the Outfit's own MODEL.
func (w Want) wanted() string {
	if w.Model != "" {
		return w.Model
	}
	if w.Alias != "" {
		return w.Alias
	}
	return "any model"
}

// Preference resolves the activity preference in force: the flag when given,
// then the fleet file's own, then idle. An unrecognised flag value fails
// naming both accepted values.
func (c *Config) Preference(flag string) (Prefer, error) {
	if flag != "" {
		return ParsePrefer(flag)
	}
	if c.Prefer != "" {
		return c.Prefer, nil
	}
	return PreferIdle, nil
}

// Choice is the node a launch will use: which node, where its engine answers,
// the key to reach it with, and why this one was chosen.
type Choice struct {
	// Node is the fleet-file entry chosen.
	Node NodeConfig
	// BaseURL is the engine's OpenAI-compatible address.
	BaseURL string
	// APIKey is the engine's key, empty when it needs none.
	APIKey string
	// Reason explains the choice in one line, naming the preference that
	// ranked it so a surprising selection is traceable to its setting.
	Reason string
	// Woken records that this node was started to satisfy the launch.
	Woken bool
}

// candidate pairs a node's file entry with what it answered, keeping the
// fleet-file index so ties break the same way every time.
type candidate struct {
	entry  NodeConfig
	index  int
	result NodeResult
}

// candidates zips a fleet's nodes with a fan-out's results. The two are in the
// same order by construction (FanOut fills by index), so this cannot mismatch.
func (c *Config) candidates(results []NodeResult) []candidate {
	out := make([]candidate, 0, len(results))
	for i, r := range results {
		if i >= len(c.Nodes) {
			break
		}
		out = append(out, candidate{entry: c.Nodes[i], index: i, result: r})
	}
	return out
}

// running keeps the nodes that answered and are serving what is wanted. A node
// that did not answer is skipped rather than fatal, exactly as it is a row
// rather than a failure in `outfit fleet status`.
func running(cands []candidate, w Want) []candidate {
	var out []candidate
	for _, c := range cands {
		if !c.result.OK() || c.result.Status.State != string(daemon.StateRunning) {
			continue
		}
		if w.matches(c.result.Status.Model) {
			out = append(out, c)
		}
	}
	return out
}

// rank orders matching nodes best-first under the preference in force. Ties
// break by fleet-file order, so the same fleet in the same state chooses the
// same node every time.
//
// A node that reports no activity has never done any work, so it is the most
// idle there is — and correspondingly the least active.
func rank(cands []candidate, p Prefer) {
	idle := func(c candidate) (int, bool) {
		s := c.result.Status
		if s.LastActiveAt == "" {
			return 0, false // never active
		}
		return s.IdleSeconds, true
	}
	sort.SliceStable(cands, func(i, j int) bool {
		li, oki := idle(cands[i])
		lj, okj := idle(cands[j])
		switch {
		case oki != okj:
			// Never-active sorts first under idle, last under active.
			return (p == PreferIdle) == !oki
		case li != lj:
			if p == PreferActive {
				return li < lj
			}
			return li > lj
		default:
			return cands[i].index < cands[j].index
		}
	})
}

// Select chooses the node a launch should use, querying the fleet and applying
// the ranking. It never starts anything: a fleet with nothing serving what is
// wanted yields ErrNoneServing, which the caller may answer by waking a node.
func (c *Config) Select(ctx context.Context, w Want) (*Choice, error) {
	scope := c
	if w.Node != "" {
		var err error
		if scope, err = c.Only(w.Node); err != nil {
			return nil, err
		}
	}
	results := scope.FanOut(ctx, StatusCall)
	return scope.choose(results, w)
}

// ErrNoneServing reports that no node is serving what was wanted. It carries
// the fleet's state so the caller can wake a node or explain the refusal
// without asking again.
type ErrNoneServing struct {
	// Results is every node's answer, for a message that shows the fleet.
	Results []NodeResult
	// Want is what was being looked for.
	Want Want
	// Path is the fleet file consulted.
	Path string
}

func (e *ErrNoneServing) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "no node in %s is serving %s:", e.Path, e.Want.wanted())
	for _, r := range e.Results {
		fmt.Fprintf(&b, "\n  %-16s %s", r.Name, describe(r))
	}
	return b.String()
}

// describe renders one node's state for a routing message.
func describe(r NodeResult) string {
	if !r.OK() {
		if detail := r.Detail(); detail != "" {
			return string(r.Outcome) + " (" + detail + ")"
		}
		return string(r.Outcome)
	}
	s := r.Status.State
	if r.Status.Model != "" {
		s += "  " + r.Status.Model
	}
	return s
}

// choose applies the ranking to a fan-out's results and resolves the winner's
// endpoint. A pinned node that is running something else fails saying so
// rather than being restarted: another person may be using it.
func (c *Config) choose(results []NodeResult, w Want) (*Choice, error) {
	cands := c.candidates(results)
	if w.Node != "" && len(cands) == 1 {
		only := cands[0]
		if !only.result.OK() {
			return nil, fmt.Errorf("node %q: %s", only.entry.Name, describe(only.result))
		}
		if only.result.Status.State == string(daemon.StateRunning) && !w.matches(only.result.Status.Model) {
			return nil, fmt.Errorf(
				"node %q is serving %s, not %s: it will not be restarted — pick another node, or stop it yourself",
				only.entry.Name, only.result.Status.Model, w.wanted())
		}
	}
	matching := running(cands, w)
	if len(matching) == 0 {
		return nil, &ErrNoneServing{Results: results, Want: w, Path: c.Path}
	}
	rank(matching, w.prefer())
	best := matching[0]
	return c.choiceFor(best, w, false)
}

// choiceFor turns a chosen candidate into the address and key a launch needs.
func (c *Config) choiceFor(best candidate, w Want, woken bool) (*Choice, error) {
	baseURL, err := c.EngineBaseURL(best.entry, best.result.Status)
	if err != nil {
		return nil, err
	}
	key, err := c.engineKeyFor(best.entry, best.result.Status)
	if err != nil {
		return nil, err
	}
	return &Choice{
		Node:    best.entry,
		BaseURL: baseURL,
		APIKey:  key,
		Reason:  reasonFor(best, w, woken),
		Woken:   woken,
	}, nil
}

// reasonFor explains a choice in one line, naming the preference that ranked
// it — a surprising selection should be traceable to the setting that caused
// it rather than look arbitrary.
func reasonFor(c candidate, w Want, woken bool) string {
	if woken {
		return fmt.Sprintf("woken to serve %s", w.wanted())
	}
	serving := c.result.Status.Model
	if serving == "" {
		serving = "an engine"
	}
	switch {
	case c.result.Status.LastActiveAt == "":
		return fmt.Sprintf("serving %s, no work yet (prefer %s)", serving, w.prefer())
	default:
		return fmt.Sprintf("serving %s, last active %ds ago (prefer %s)",
			serving, c.result.Status.IdleSeconds, w.prefer())
	}
}

// EngineBaseURL is where a node's engine answers, built from what the fleet
// file says and what the node reports, in that order: an explicit engine
// override is used as given, otherwise the host is the node's own and the port
// and path come from the daemon.
//
// A daemon's own view of its engine address (127.0.0.1) is useless to a remote
// caller, which is why the daemon reports parts and this composes them.
func (c *Config) EngineBaseURL(n NodeConfig, status daemon.StatusResponse) (string, error) {
	host, port, path := n.Host, 0, ""
	if ep := status.Engine; ep != nil {
		port, path = ep.Port, ep.Path
	}
	if o := n.Engine; o != nil {
		if o.Host != "" {
			host = o.Host
		}
		if o.Port != 0 {
			port = o.Port
		}
		if o.Path != "" {
			path = o.Path
		}
	}
	if port == 0 && !strings.Contains(host, "://") {
		if status.Engine == nil {
			return "", fmt.Errorf(
				"node %q reports no engine endpoint: upgrade its daemon, or set this node's engine port in %s",
				n.Name, c.Path)
		}
		return "", fmt.Errorf("node %q reports no engine port", n.Name)
	}
	// A daemon that says its engine is bound to loopback is describing an
	// engine only that machine can reach. Declaring an engine override is
	// taking responsibility for reachability, so it silences this.
	if n.Engine == nil && status.Engine != nil && status.Engine.LoopbackOnly && !hostIsLoopback(n.Host) {
		return "", fmt.Errorf(
			"node %q has its engine bound to loopback, so it answers only on that machine: "+
				"bind the engine to a reachable address (llama.cpp's --host 0.0.0.0), "+
				"or give this node an engine host/port in %s",
			n.Name, c.Path)
	}
	if path == "" {
		path = DefaultPath
	}
	return joinEngineURL(host, port, path), nil
}

// joinEngineURL composes the engine address. A host carrying a scheme is taken
// as a whole origin — a reverse proxy in front of the engine, where the port
// may already be implied — so an override can name an https endpoint.
func joinEngineURL(host string, port int, path string) string {
	path = "/" + strings.TrimLeft(path, "/")
	if strings.Contains(host, "://") {
		origin := strings.TrimRight(host, "/")
		if u, err := url.Parse(origin); err == nil && u.Port() == "" && port != 0 {
			origin = u.Scheme + "://" + net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
		}
		return origin + path
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
}

// hostIsLoopback reports whether a fleet-file host names this machine.
func hostIsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// engineKeyFor resolves the key a node's engine requires. A gated engine whose
// node names no variable fails here, before anything is launched: an agent that
// cannot authenticate is worse than a message saying so.
func (c *Config) engineKeyFor(n NodeConfig, status daemon.StatusResponse) (string, error) {
	if status.Engine == nil || !status.Engine.RequiresKey {
		return "", nil
	}
	if n.EngineTokenEnv == "" {
		return "", fmt.Errorf(
			"node %q has a gated engine but names no engine key: add `engineTokenEnv: <VAR>` to it in %s",
			n.Name, c.Path)
	}
	return c.EngineToken(n)
}

// hostPortOf reduces a base URL to the address a dialler needs, filling in the
// scheme's default port when the URL states none.
func hostPortOf(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
