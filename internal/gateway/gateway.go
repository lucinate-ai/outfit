// The fleet gateway: one OpenAI-compatible endpoint in front of a fleet. It
// answers agent requests with the fleet's own selector, holds each node's
// engine key, and wakes a node when nothing is serving what a request asks
// for — so a machine running an agent needs nothing but a URL and one token.
//
// It is a foreground process, the way `spinloop serve` is: it holds the fleet
// file it serves, and a machine that hosts agents names the address it prints
// in that file's gateway section.

package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// DefaultListen is where the gateway answers when --listen is not given: a
// fixed port on every interface, so a fleet file's gateway section can name
// one address without knowing the machine it lands on.
const DefaultListen = ":4000"

// DefaultMaxRequestBytes is the largest completion request body the gateway
// reads when --max-request-bytes is not given. A request carries a whole
// conversation, so the figure sits well above a long agent turn.
const DefaultMaxRequestBytes int64 = 64 << 20

// LoopbackListen is where `--loopback` binds the gateway: the default port on
// loopback, the safe bind a local-only gateway wants — one that Listen's token
// check accepts without a token.
const LoopbackListen = "127.0.0.1:4000"

// cacheTTL is how long a fan-out reading stays fresh for routing. A burst of
// requests pays one fan-out, not one each; a request older than this sees the
// fleet as it is now. A variable so tests do not wait.
var cacheTTL = 2 * time.Second

// sourcesTTL is how long the reading of what each node's own source describes
// stays fresh for the models list. A source changes on a deploy, not on a
// request, so this is far longer than the status reading's: the cost is
// reading each node's Spinloop and its preset, which a burst of models
// requests must not pay per request. A variable so tests do not wait.
var sourcesTTL = 30 * time.Second

// pathsServed is the surface the gateway answers, for the 404 that names it.
var pathsServed = []string{
	"/v1/models", "/v1/chat/completions", "/v1/completions", "/v1/fleet", "/health",
}

// Options shapes a handler beyond the fleet it serves.
type Options struct {
	// ConfigFor resolves the deploy config a candidate node would be started
	// with — each node's own Spinloop source. nil disables waking: nothing can
	// be started, and a request nothing is serving fails saying so.
	ConfigFor fleet.ConfigFor
	// Log receives the gateway's log lines; nil discards them.
	Log *slog.Logger
	// Now is the clock the reading cache ages against; nil uses time.Now.
	Now func() time.Time
	// MaxRequestBytes is the largest completion request body the gateway
	// accepts; a larger one is answered 413. Zero or less uses
	// DefaultMaxRequestBytes.
	MaxRequestBytes int64
}

// Handler is the gateway: the fleet it serves, the token its callers present,
// and the state a burst of requests shares — the last reading of the fleet,
// and the reading's age.
type Handler struct {
	cfg    *fleet.Config
	token  string
	cfgFor fleet.ConfigFor
	log    *slog.Logger
	now    func() time.Time

	maxRequestBytes int64

	mu         sync.Mutex
	results    []fleet.NodeResult
	at         time.Time
	wakeable   map[string]string
	wakeableAt time.Time
}

// New builds a gateway handler over a resolved fleet file. The token is the
// one callers must present — empty on loopback, where none is needed, as the
// daemon's control API allows.
func New(cfg *fleet.Config, token string, opts Options) *Handler {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	maxBytes := opts.MaxRequestBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRequestBytes
	}
	return &Handler{cfg: cfg, token: token, cfgFor: opts.ConfigFor, log: log, now: now, maxRequestBytes: maxBytes}
}

// Listen opens the gateway's listener, applying the daemon's exposure rule:
// a non-loopback address is refused without a token, because it would put an
// engine's full output on the network for anyone to read.
func Listen(addr, token string) (net.Listener, error) {
	if token == "" && !loopbackAddr(addr) {
		return nil, fmt.Errorf(
			"refusing to listen on non-loopback %q without a token: "+
				"pass --api-token-file <path>, set %s, or pass --api-token — "+
				"or bind loopback, e.g. --listen 127.0.0.1:4000, which needs none",
			addr, daemon.TokenEnvVar)
	}
	return net.Listen("tcp", addr)
}

// loopbackAddr reports whether a listen address binds only loopback. An empty
// or wildcard host binds every interface, so it is not loopback.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Topology is what the gateway reports the fleet to be: its nodes, with the
// tags the file gives them and the serving facts they report, and the
// file's fleet-level settings. It is how a process that holds no fleet file
// learns what the fleet is: the gateway's file is the single source of truth,
// and this is that truth, joined with what the nodes say.
type Topology struct {
	// Wake is whether the fleet starts an engine on a node that is not
	// running one: the file's policy, with its default made visible.
	Wake bool `json:"wake"`
	// Prefer is how the fleet ranks several matching nodes, as the file
	// declares it: absent where the file declares nothing.
	Prefer string `json:"prefer,omitempty"`
	// Concurrency is the fleet's declared capacity, as the file declares it:
	// absent where the file declares no limits.
	Concurrency *Concurrency `json:"concurrency,omitempty"`
	// Nodes is the fleet, in the file's order.
	Nodes []NodeTopology `json:"nodes"`
}

// Concurrency is the fleet's declared capacity as the topology reports it.
type Concurrency struct {
	// Total is the most work items the fleet may have in flight at once;
	// absent where the file declares no fleet-wide limit.
	Total *int `json:"total,omitempty"`
	// Tags bounds the items carrying each tag, keyed the way tags are named
	// — a key and a value joined by =.
	Tags map[string]int `json:"tags,omitempty"`
}

// NodeTopology is one node as the topology reports it: the file's claims
// about it and the facts it reports, or the failure of reaching it.
type NodeTopology struct {
	// Name and Kind are the file's: the node's identity and how the fleet
	// reaches it.
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Tags is what the file gives the node: its description of what work the
	// node takes on. Absent where the file gives it none.
	Tags map[string]string `json:"tags,omitempty"`
	// State is the node's state — the daemon's, where it answers, and the
	// way the call to it ended, where it does not: a node that does not
	// answer is reported, not an error.
	State string `json:"state"`
	// Detail is the failure's text where the node did not answer; empty
	// where it did.
	Detail string `json:"detail,omitempty"`
	// Model and ServedName are what the running engine serves: the model id
	// and the name it answers under where it reports one. Absent where
	// nothing is running.
	Model      string `json:"model,omitempty"`
	ServedName string `json:"servedName,omitempty"`
	// Ready is whether the running engine has answered its own health
	// check: "ready" or "not-ready", absent where it does not apply.
	Ready string `json:"ready,omitempty"`
	// LastActiveAt is when the engine last did work, RFC 3339, absent until
	// it has.
	LastActiveAt string `json:"lastActiveAt,omitempty"`
	// WakeableModel is the model a request would start a node that is not
	// running with, from its own source: named where the source describes
	// one and the fleet wakes, and absent for a running node, a node whose
	// source describes no model, and a fleet that does not wake.
	WakeableModel string `json:"wakeableModel,omitempty"`
}

// ServeHTTP is the gateway's whole surface: the paths it serves, a health
// check that touches no node, and a 404 that names the rest.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handler http.HandlerFunc
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		handler = h.handleHealth
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		handler = h.handleModels
	case r.Method == http.MethodGet && r.URL.Path == "/v1/fleet":
		handler = h.handleTopology
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/completions"):
		handler = h.handleCompletion
	default:
		handler = func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, fmt.Errorf(
				"the gateway serves %s, not %s %s", strings.Join(pathsServed, ", "), r.Method, r.URL.Path))
		}
	}
	h.authenticate(handler)(w, r)
}

// authenticate gates every request behind the bearer token, on the daemon's
// terms: an empty token means no auth, which Listen permits on loopback only.
func (h *Handler) authenticate(next http.HandlerFunc) http.HandlerFunc {
	if h.token == "" {
		return next
	}
	want := []byte("Bearer " + h.token)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid bearer token"))
			return
		}
		next(w, r)
	}
}

// handleHealth answers that the gateway is up. It contacts no node on
// purpose: it is how an operator tells the gateway down from the fleet down.
func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleModels lists what a request can reach, in the OpenAI list shape: what
// the running nodes report, and — when the fleet allows waking — what a
// stopped node's own source describes, so a client sees the models it can ask
// for. Served name first, duplicates once. Nothing reachable is an empty
// list, not an error.
func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	results := h.reading(r.Context())
	wakeable := h.wakeableModels(r.Context())
	seen := map[string]bool{}
	data := []map[string]any{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		data = append(data, map[string]any{"id": name, "object": "model"})
	}
	for _, res := range results {
		if !res.OK() || res.Status.State != string(daemon.StateRunning) {
			continue
		}
		name := res.Status.ServedName
		if name == "" {
			name = res.Status.Model
		}
		add(name)
	}
	for _, res := range results {
		if !res.OK() || res.Status.State == string(daemon.StateRunning) {
			// A running engine is never displaced to make room, so a running
			// node's source's model is not one this gateway would answer; a
			// node that does not answer its status cannot be started either.
			continue
		}
		add(wakeable[res.Name])
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// wakeableModels is what a request could start: for each node for which
// waking is allowed (its own `wake` setting, or the fleet's when it names
// none), the model it would be started with, under the served-name-first
// naming a running node reports. A daemon node's model comes from its own
// Spinloop source; a cloud node's comes from its own stats reply. It is
// resolved at most once per sourcesTTL, shared by every models request —
// which bounds how often a cloud node's resolution pays a live control-
// plane call, the same way it bounds how often a daemon node's pays a
// Spinloop file read.
func (h *Handler) wakeableModels(ctx context.Context) map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wakeable != nil && h.now().Sub(h.wakeableAt) < sourcesTTL {
		return h.wakeable
	}
	cfgFor := h.combinedConfigFor(ctx)
	m := map[string]string{}
	for _, entry := range h.cfg.Nodes {
		if !h.cfg.NodeWakes(entry) {
			continue
		}
		dc, err := cfgFor(entry)
		if err != nil {
			continue
		}
		name := dc.ServedModelName
		if name == "" {
			name = dc.ModelID
		}
		if name != "" {
			m[entry.Name] = name
		}
	}
	h.wakeable = m
	h.wakeableAt = h.now()
	return m
}

// cloudConfigFor resolves what a kind: cloud node would be started with:
// the environment's stored deploy config, read from its stats reply. The
// status reply a fan-out already holds is no good for this — the control
// plane only relays deploy facts on the status reply while the environment
// is running (see remote-node's "A running environment's status..."
// requirement), so a stopped environment's status names nothing served even
// though it is deployed. The stats reply carries the runner and model id
// unconditionally, since the stats Lambda reads the deploy config directly
// rather than relaying it alongside instance state; an undeployed
// environment's stats read fails outright (no config to read), which is
// what "nothing deployed" looks like here. It carries the served name
// alongside the model id too, the same field the deploy config's ALIAS
// sets, so a stopped cloud node's wakeable name matches what it reported
// while running rather than falling back to the bare model id.
func (h *Handler) cloudConfigFor(ctx context.Context) fleet.ConfigFor {
	return func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
		node, err := h.cfg.NewNode(entry)
		if err != nil {
			return inference.DeployConfig{}, err
		}
		stats, err := node.Metrics(ctx)
		if err != nil {
			return inference.DeployConfig{}, fmt.Errorf("%s: %w (run `spinloop cloud deploy` if nothing is deployed)", entry.Name, err)
		}
		return inference.DeployConfig{ModelID: stats.ModelID, ServedModelName: stats.ServedName}, nil
	}
}

// combinedConfigFor resolves what any node — daemon or remote — would be
// started with: a daemon node through the gateway's own cfgFor (its
// Spinloop source), a cloud node through its own stats reply
// (cloudConfigFor). A daemon node fails the way it always has when the
// gateway holds no cfgFor at all; a cloud node's resolution does not
// depend on cfgFor, so it still works when the gateway was built with none.
func (h *Handler) combinedConfigFor(ctx context.Context) fleet.ConfigFor {
	cloudFor := h.cloudConfigFor(ctx)
	return func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
		if entry.Kind == fleet.KindCloud {
			return cloudFor(entry)
		}
		if h.cfgFor == nil {
			return inference.DeployConfig{}, fmt.Errorf(
				"this gateway can wake no node: it has no way to resolve a node's Spinloop source")
		}
		return h.cfgFor(entry)
	}
}

// handleTopology answers with the fleet's topology: the reading the models
// list and the routing take, joined with the file's claims about each node
// and its fleet-level settings. A node that does not answer is reported in
// its place, the way the fleet's own views report it, rather than failing
// the whole reply.
func (h *Handler) handleTopology(w http.ResponseWriter, r *http.Request) {
	results := h.reading(r.Context())
	wakeable := h.wakeableModels(r.Context())

	topo := Topology{Wake: h.cfg.Wakes(), Prefer: string(h.cfg.Prefer)}
	if c := h.cfg.Concurrency; c != nil {
		topo.Concurrency = &Concurrency{Total: c.Total, Tags: c.Tags}
	}
	topo.Nodes = make([]NodeTopology, 0, len(results))
	for _, res := range results {
		entry, ok := h.cfg.Node(res.Name)
		nt := NodeTopology{Name: res.Name}
		if ok {
			nt.Kind = entry.Kind
			nt.Tags = entry.Tags
		}
		if !res.OK() {
			nt.State = string(res.Outcome)
			nt.Detail = res.Detail()
		} else {
			st := res.Status
			nt.State = st.State
			nt.Model = st.Model
			nt.ServedName = st.ServedName
			nt.Ready = st.Ready
			nt.LastActiveAt = st.LastActiveAt
			// A running engine is never displaced to make room, so only a
			// node that is not running reports what a request would start it
			// with — and wakeableModels already holds nothing when the fleet
			// does not wake or no source can be resolved.
			if st.State != string(daemon.StateRunning) {
				nt.WakeableModel = wakeable[res.Name]
			}
		}
		topo.Nodes = append(topo.Nodes, nt)
	}
	writeJSON(w, http.StatusOK, topo)
}

// reading returns the fleet's last fan-out, taking one when the last is stale.
// It is the whole freshness story: a burst of requests shares one reading,
// and a request older than cacheTTL sees the fleet as it is now. The mutex is
// held for the fan-out, so requests that arrive mid-fan-out wait for it and
// take its result rather than fanning out again.
func (h *Handler) reading(ctx context.Context) []fleet.NodeResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.results != nil && h.now().Sub(h.at) < cacheTTL {
		return h.results
	}
	h.results = h.cfg.FanOut(ctx, fleet.StatusCall)
	h.at = h.now()
	return h.results
}

// handleCompletion routes a completion request to the node serving its model,
// waking one when nothing is and the fleet file allows it.
func (h *Handler) handleCompletion(w http.ResponseWriter, r *http.Request) {
	model, body, err := requestModel(w, r, h.maxRequestBytes)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf(
				"the request body is larger than the gateway's limit of %d bytes: raise it with --max-request-bytes",
				tooLarge.Limit))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"the request names no model: completion requests need a `model` field"))
		return
	}
	ctx := r.Context()
	prefer, err := h.cfg.Preference("")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	want := fleet.Want{Model: model, Prefer: prefer}

	results := h.reading(ctx)
	choice, err := h.cfg.Choose(h.reachable(results), want)
	if err != nil {
		var none *fleet.ErrNoneServing
		if !errors.As(err, &none) {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		choice, err = h.routeNothingServing(ctx, want, none)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
	}

	h.log.Info("routed",
		slog.String("model", model),
		slog.String("node", choice.Node.Name),
		slog.Bool("woken", choice.Woken))
	h.proxy(w, r, body, choice)
}

// requestModel pulls the model field out of a completion request and returns
// it with the full body, which the proxy must forward unmodified. A body
// larger than limit returns an error wrapping *http.MaxBytesError, and any
// other failed read returns an error, so a body that was not read in full is
// never returned. A body that is not a JSON object fails saying so, rather
// than being routed at a guess.
func requestModel(w http.ResponseWriter, r *http.Request, limit int64) (string, []byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return "", nil, fmt.Errorf("reading the request: %w", err)
	}
	var req struct {
		Model string `json:"model"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return "", nil, fmt.Errorf("the request is not a JSON body: %v", err)
		}
	}
	return req.Model, body, nil
}

// reachable marks the running engines the gateway cannot reach as not
// candidates: a node whose engine is bound to loopback, without an override
// taking responsibility for reachability, answers only on its own machine, so
// selecting it would hold a request for the wake timeout at best. The mark
// carries the daemon's own explanation, which names the bind and the fix, so
// a fleet where that is the only match fails saying so.
func (h *Handler) reachable(results []fleet.NodeResult) []fleet.NodeResult {
	out := make([]fleet.NodeResult, len(results))
	for i, res := range results {
		out[i] = res
		if !res.OK() || res.Status.State != string(daemon.StateRunning) {
			continue
		}
		entry, ok := h.cfg.Node(res.Name)
		if !ok {
			continue
		}
		if _, err := h.cfg.EngineBaseURL(entry, res.Status); err != nil {
			out[i] = fleet.NodeResult{
				Name:    res.Name,
				Outcome: fleet.OutcomeUnreachable,
				Err:     err,
				Status:  res.Status,
				At:      res.At,
			}
		}
	}
	return out
}

// routeNothingServing answers a request that nothing in the fleet is serving.
// A node already running the wanted model whose engine has not answered yet is
// waited for rather than passed over: it is loading the weights this request
// needs, so it serves sooner than a node started from cold, and waking a
// second node would leave two engines up for one request.
func (h *Handler) routeNothingServing(ctx context.Context, want fleet.Want, none *fleet.ErrNoneServing) (*fleet.Choice, error) {
	if starting := h.cfg.Loading(none.Results, want); len(starting) > 0 {
		h.log.Info("waiting for a starting engine",
			slog.String("model", want.Model),
			slog.String("node", starting[0].Name))
		return h.cfg.WaitLoading(ctx, want, none.Results, h.progress())
	}
	return h.wakeFor(ctx, want, none.Results)
}

// progress adapts the fleet's progress reporting to the gateway's log, which
// is where a held request's waiting has to be visible: the caller is given
// nothing until the request completes.
func (h *Handler) progress() fleet.Waker {
	return func(format string, args ...any) {
		h.log.Info(strings.TrimSuffix(fmt.Sprintf(format, args...), "\n"))
	}
}

// wakeFor starts a node for a request nothing is serving, when waking is
// allowed for at least one node in the fleet, and holds the request until
// the engine answers. A concurrent request waking the same node loses its
// start to a 409 and takes the node the other one started — the same
// engine, the same wait.
//
// A node whose resolved config matches the request but whose own waking is
// disabled is left for Wake to refuse itself, the same way it refuses a node
// whose config does not match: Wake's per-candidate loop already names it
// ("waking is disabled for this node") in the refusal it reports when
// nothing else can serve the request either.
func (h *Handler) wakeFor(ctx context.Context, want fleet.Want, results []fleet.NodeResult) (*fleet.Choice, error) {
	none := &fleet.ErrNoneServing{Results: results, Want: want, Path: h.cfg.Path}
	matching := h.matchingConfigFor(want.Model, h.combinedConfigFor(ctx))
	if !h.cfg.AnyNodeWakes() {
		return nil, h.refuseWake(want, none, matching)
	}
	return h.cfg.Wake(ctx, want, matching, results, h.progress())
}

// refuseWake is the no-node-wakes answer: nothing is started, and the
// failure names the node whose source describes the model and the command
// that would start it — or, when no source describes it, that there is
// nothing to start.
func (h *Handler) refuseWake(want fleet.Want, none error, cfgFor fleet.ConfigFor) error {
	for _, entry := range h.cfg.Nodes {
		if _, err := cfgFor(entry); err == nil {
			return fmt.Errorf("%s\nwake is off in %s: %q's source describes %s; start it with `spinloop fleet start %s`",
				none, h.cfg.Path, entry.Name, want.Model, entry.Name)
		}
	}
	return fmt.Errorf("%s\nwake is off in %s, and no node's source describes %s", none, h.cfg.Path, want.Model)
}

// matchingConfigFor wraps a resolver with the one condition a wake has to
// meet: the resolved config is the model the request asks for. A node whose
// config describes a different model is not a candidate — it would be
// started with the wrong engine — and its refusal says so.
func (h *Handler) matchingConfigFor(model string, base fleet.ConfigFor) fleet.ConfigFor {
	return func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
		dc, err := base(entry)
		if err != nil {
			return dc, err
		}
		if dc.ModelID != model && dc.ServedModelName != model {
			described := dc.ServedModelName
			if described == "" {
				described = dc.ModelID
			}
			return dc, fmt.Errorf("its source describes %s, not %s", described, model)
		}
		return dc, nil
	}
}

// proxy forwards the request to the chosen engine and the engine's reply back
// to the caller, unmodified: the body goes out as it came in, a streamed
// reply passes through as it is produced, and the reply the engine gives is
// the reply the caller gets — the gateway never retries another node.
func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, body []byte, choice *fleet.Choice) {
	target, err := url.Parse(choice.BaseURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("node %q's engine address is not a URL: %v", choice.Node.Name, err))
		return
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// The caller asks the gateway for /v1/<rest>; the engine's base
			// URL already carries its own prefix, so /v1 comes off the
			// request and the rest goes on the base.
			pr.Out.URL.Path = joinPath(target.Path, strings.TrimPrefix(pr.In.URL.Path, "/v1"))
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
		},
		// -1 flushes after every write: a streamed reply reaches the caller
		// as the engine produces it, not when a buffer fills.
		FlushInterval: -1,
		// The default transport, with its connection pooling and no overall
		// request timeout: a completion may take as long as the model takes.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.log.Error("route failed",
				slog.String("node", choice.Node.Name),
				slog.String("error", err.Error()))
			writeError(w, http.StatusBadGateway, fmt.Errorf(
				"the engine on %s failed to answer: %v", choice.Node.Name, err))
		},
	}

	out := r.Clone(r.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	if choice.APIKey != "" {
		// The caller's authoriser never travels past the gateway: the engine
		// is reached with the key its fleet entry names, resolved the way
		// every other fleet client resolves it.
		out.Header.Set("Authorization", "Bearer "+choice.APIKey)
	} else {
		out.Header.Del("Authorization")
	}
	p.ServeHTTP(w, out)
}

// joinPath joins a base path and a request path with at most one slash
// between them, either piece absent.
func joinPath(base, rest string) string {
	base = strings.TrimRight(base, "/")
	rest = "/" + strings.TrimLeft(rest, "/")
	return base + rest
}

// writeJSON sends a JSON reply.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends a JSON error in the shape the OpenAI surface uses.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": "gateway_error"}})
}
