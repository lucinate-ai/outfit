// Waking a node: starting an engine on a machine that is not running one, so a
// launch has somewhere to go. It reuses the daemon's start-with-config call, so
// what a host can serve is decided by the same validation that would reject a
// bad config anywhere else — there is no second description of a node's
// capabilities to drift from the first.

package fleet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// WakeTimeout bounds waiting for a woken node's engine to answer. A cold node
// loads weights first, which on a large model is minutes. A variable so tests
// do not wait.
var WakeTimeout = 5 * time.Minute

// wakePoll is how often a waking node is re-checked.
var wakePoll = 2 * time.Second

// wakeSingleflight coalesces concurrent wakes of the same node in the same
// fleet file into one actual start. Two requests racing to wake the same
// node is the ordinary shape of two agents starting near enough together,
// and a daemon node's own 409 already turns the loser into a joiner — but a
// cloud environment's control plane has no equivalent guard: its instance
// lookup is eventually consistent right after a launch, so two wakes that
// race within that window can each miss the other's not-yet-visible
// instance and each launch one, doubling the bill for what should have been
// a single machine. Keyed by the fleet file's path alongside the node's
// name, so distinct fleets never coalesce across each other.
var wakeSingleflight singleflight.Group

// Waker reports progress while a node is woken. A silent five-minute pause
// reads as a hang, so the caller is given something to print.
type Waker func(format string, args ...any)

// ConfigFor resolves the deploy config a candidate node would be started
// with. The launch path resolves the same config for every candidate — one
// Spinloop describes what any node should start — while the gateway resolves
// each node's own Spinloop source, so different nodes may describe different
// engines.
type ConfigFor func(entry NodeConfig) (inference.DeployConfig, error)

// ConstantConfig adapts one deploy config, already resolved, to a per-candidate
// resolver. The launch path uses it: its Spinloop describes what any node would
// start.
func ConstantConfig(dc inference.DeployConfig, err error) ConfigFor {
	return func(NodeConfig) (inference.DeployConfig, error) { return dc, err }
}

// configResolver resolves each candidate's deploy config at most once per wake,
// by node name. Resolving can cost more than a status read — the gateway
// parses each node's Spinloop source — and a wake asks for the same config
// twice per candidate: once to order them, once to start them.
type configResolver struct {
	fn   ConfigFor
	dcs  map[string]inference.DeployConfig
	errs map[string]error
}

func newConfigResolver(fn ConfigFor) *configResolver {
	return &configResolver{fn: fn, dcs: map[string]inference.DeployConfig{}, errs: map[string]error{}}
}

func (r *configResolver) config(entry NodeConfig) (inference.DeployConfig, error) {
	if err, ok := r.errs[entry.Name]; ok {
		return r.dcs[entry.Name], err
	}
	dc, err := r.fn(entry)
	r.dcs[entry.Name] = dc
	r.errs[entry.Name] = err
	return dc, err
}

// Wake starts an engine for want on a node that is not running one, and waits
// until its engine answers. Candidates are tried in fleet-file order, and a
// node whose stored config already matches is preferred: it has the weights.
//
// A node that refuses the config — a runner or model it cannot serve — is not
// fatal while other candidates remain. When none succeeds, every refusal is
// reported together.
func (c *Config) Wake(ctx context.Context, w Want, cfgFor ConfigFor, results []NodeResult, log Waker) (*Choice, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	resolver := newConfigResolver(cfgFor)
	cands := wakeable(c.candidates(results), resolver)
	if len(cands) == 0 {
		return nil, &ErrNoneServing{Results: results, Want: w, Path: c.Path}
	}

	var refused []string
	for _, cand := range cands {
		if !c.NodeWakes(cand.entry) {
			// Waking is off for this node — its own setting, or the fleet's
			// when it names none — so it is refused here rather than
			// started, the same as a node that refuses the config: another
			// candidate may still serve the request.
			refused = append(refused, fmt.Sprintf("%s: waking is disabled for this node", cand.entry.Name))
			continue
		}
		dc, err := resolver.config(cand.entry)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", cand.entry.Name, err))
			continue
		}
		node, err := c.NewNode(cand.entry)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", cand.entry.Name, err))
			continue
		}
		// The key the engine will be gated with is resolved before it starts,
		// so a variable that is set nowhere fails before anything runs rather
		// than after. A node naming none wakes an ungated engine, which is
		// right for one reached over loopback.
		engineKey, err := c.EngineToken(cand.entry)
		if err != nil {
			return nil, err
		}
		// Concurrent wakes of this same node — another request racing this
		// one, on the same fleet file — coalesce into one actual start via
		// wakeSingleflight: only the first caller through runs startAndWait,
		// and every caller for this node gets its result.
		// The shared call runs on its own background context rather than
		// this caller's: whichever caller happens to be first must not have
		// its start-and-wait cut short by ITS OWN request being cancelled
		// (a client disconnecting) while another caller is still waiting on
		// the same node — waitReady bounds the wait by WakeTimeout on its
		// own regardless.
		key := c.Path + "\x00" + cand.entry.Name
		v, err, _ := wakeSingleflight.Do(key, func() (any, error) {
			return c.startAndWait(context.Background(), node, cand, dc, engineKey, w, log)
		})
		if err != nil {
			var fatal *fatalWakeError
			if errors.As(err, &fatal) {
				// The engine was started, or was already running, but never
				// answered — it is left running, so no other candidate is
				// tried: that would leave two engines up for one request.
				return nil, fatal.err
			}
			refused = append(refused, fmt.Sprintf("%s: %v", cand.entry.Name, err))
			continue
		}
		ready := v.(daemon.StatusResponse)
		cand.result = NodeResult{Name: cand.entry.Name, Outcome: OutcomeOK, Status: ready}
		return c.choiceFor(cand, w, true, engineKey)
	}
	return nil, fmt.Errorf(
		"no node in %s could serve %s:\n  %s",
		c.Path, w.wanted(), strings.Join(refused, "\n  "))
}

// fatalWakeError marks a wake failure that must not be answered by trying
// the next candidate: the engine was started, or was found already running,
// and is left running either way, so falling through would leave two
// engines up for one request rather than one that simply took longer.
type fatalWakeError struct{ err error }

func (e *fatalWakeError) Error() string { return e.err.Error() }
func (e *fatalWakeError) Unwrap() error { return e.err }

// startAndWait starts cand's node — or, when another caller's start won a
// race, joins the engine that start produced — and waits for it to answer.
// It is the unit wakeSingleflight coalesces: everything from the log line a
// caller sees through the readiness wait happens at most once per node per
// overlapping set of wakes, however many requests are waiting on it.
func (c *Config) startAndWait(ctx context.Context, node Node, cand candidate, dc inference.DeployConfig, engineKey string, w Want, log Waker) (daemon.StatusResponse, error) {
	log("Waking %s to serve %s...\n", cand.entry.Name, w.wanted())
	_, err := node.StartWith(ctx, &dc, engineKey)
	if err != nil {
		// Another client may have woken this node first. That is another
		// route to the same place, not a failure — re-read its state and
		// take it if it is now serving what we want. The state alone is not
		// the answer, though: the other start may still be loading, so the
		// same readiness wait applies to a node we did not start ourselves.
		if isAlreadyRunning(err) {
			if status, serr := node.Status(ctx); serr == nil && w.matches(servingNames(status)...) {
				log("%s was already started by someone else; waiting for its engine to answer...\n", cand.entry.Name)
				ready, werr := c.waitReady(ctx, node, cand.entry, w, log)
				if werr != nil {
					return daemon.StatusResponse{}, &fatalWakeError{werr}
				}
				return ready, nil
			}
		}
		return daemon.StatusResponse{}, err
	}
	ready, werr := c.waitReady(ctx, node, cand.entry, w, log)
	if werr != nil {
		return daemon.StatusResponse{}, &fatalWakeError{werr}
	}
	return ready, nil
}

// wakeable keeps the nodes that could be started, in the order to try them: a
// node whose stored config already names the wanted model first, since it has
// the weights and starts sooner. Whether waking is actually allowed for a
// given node is Wake's own concern, not this ordering's — WouldWake reports
// the node that would be tried first on config alone, regardless of policy,
// which is what lets a caller explain a wake-off refusal by naming the node
// it would otherwise have started.
func wakeable(cands []candidate, resolver *configResolver) []candidate {
	var warm, cold []candidate
	for _, c := range cands {
		if !c.result.OK() || c.result.Status.State == string(daemon.StateRunning) {
			// A running engine is never displaced to make room.
			continue
		}
		dc, err := resolver.config(c.entry)
		if err == nil && c.result.Status.Model != "" && c.result.Status.Model == dc.ModelID {
			warm = append(warm, c)
			continue
		}
		cold = append(cold, c)
	}
	return append(warm, cold...)
}

// WaitLoading holds a request for a node that is already running the wanted
// model but has not answered yet, and returns it once its engine does. It is
// the counterpart to Wake for a node nothing needs to start: the weights are
// already being fetched or loaded, so waiting reaches a served request sooner
// than starting a second engine somewhere else — and on a fleet whose other
// nodes cost money to run, it avoids starting one at all.
//
// Candidates are taken in fleet-file order. Only the first is waited for:
// they are all loading the same model, so a second wait would just be the
// first one's timeout twice over.
func (c *Config) WaitLoading(ctx context.Context, w Want, results []NodeResult, log Waker) (*Choice, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	cands := loading(c.candidates(results), w)
	if len(cands) == 0 {
		return nil, &ErrNoneServing{Results: results, Want: w, Path: c.Path}
	}
	cand := cands[0]
	node, err := c.NewNode(cand.entry)
	if err != nil {
		return nil, err
	}
	log("%s is starting %s; waiting for its engine to answer...\n", cand.entry.Name, w.wanted())
	ready, err := c.waitReady(ctx, node, cand.entry, w, log)
	if err != nil {
		return nil, err
	}
	cand.result = NodeResult{Name: cand.entry.Name, Outcome: OutcomeOK, Status: ready}
	// Not woken: this engine was started by whoever started it, so its key is
	// looked up rather than being the one a wake just gated it with.
	return c.choiceFor(cand, w, false, "")
}

// waitReady blocks until the node reports running *and* its engine answers.
// The supervisor reports running when the process exists; llama.cpp then loads
// weights, so a launch that trusted the state alone would hand the agent an
// endpoint that refuses connections.
//
// A daemon that reports its own readiness reading — the engine has answered
// its health check — is taken on that word; it checked from the same machine
// the engine runs on, and a cloud node's reading is the control plane's own
// equivalent check. A ReadyNo reading is taken on its word too: the engine's
// port can accept a connection well before the engine can answer a request
// — llama.cpp and vLLM both open it early and answer their own health check
// 503 while still loading — so an explicit "not ready" must not be
// second-guessed by the weaker TCP probe. That probe is the fallback only
// for a node that reports no reading at all (older builds, or a runner with
// no known health-check convention) — the one case a reading cannot settle.
//
// On timeout the started engine is deliberately left running: it is probably
// still loading, and stopping it throws away the only expensive part.
func (c *Config) waitReady(ctx context.Context, node Node, entry NodeConfig, w Want, log Waker) (daemon.StatusResponse, error) {
	deadline := time.Now().Add(WakeTimeout)
	var last daemon.StatusResponse
	announced := false
	for {
		status, err := node.Status(ctx)
		if err == nil {
			last = status
			if status.State == string(daemon.StateRunning) {
				switch status.Ready {
				case daemon.ReadyYes:
					return status, nil
				case daemon.ReadyNo:
					// Taken on its word: falling back to the TCP probe here
					// would hand out an address the engine itself just said
					// is not ready to answer.
				default:
					baseURL, urlErr := c.EngineBaseURL(entry, status)
					if urlErr != nil {
						return status, urlErr
					}
					if engineAnswers(ctx, baseURL) {
						return status, nil
					}
				}
				if !announced {
					log("%s is up; waiting for its engine to load...\n", entry.Name)
					announced = true
				}
			}
			if status.State == string(daemon.StateCrashed) {
				return status, fmt.Errorf(
					"node %q crashed while starting %s: check its engine log (%s)",
					entry.Name, w.wanted(), status.LogPath)
			}
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf(
				"node %q did not answer within %s: its engine may still be loading, "+
					"so it has been left running — check `spinloop fleet status`",
				entry.Name, WakeTimeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(wakePoll):
		}
	}
}

// engineDialTimeout bounds one readiness probe.
var engineDialTimeout = 2 * time.Second

// engineAnswers reports whether an engine is accepting connections at baseURL.
// A TCP connect is deliberate: it asks the one question that matters — will a
// request reach it — without assuming which paths this engine serves.
func engineAnswers(ctx context.Context, baseURL string) bool {
	addr := hostPortOf(baseURL)
	if addr == "" {
		return false
	}
	dialer := net.Dialer{Timeout: engineDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// isAlreadyRunning reports whether a start was refused because an engine is
// already running — the daemon's conflict, which is a race rather than a fault.
func isAlreadyRunning(err error) bool {
	var he *httpError
	if errors.As(err, &he) && he.status == http.StatusConflict {
		return true
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already running")
}

// WouldWake names the node Wake would try first, without starting anything. It
// is what lets a routing decision be explained before an agent depends on it —
// and the reason the ordering lives in one place rather than being described
// twice.
func (c *Config) WouldWake(results []NodeResult, cfgFor ConfigFor) (NodeConfig, bool) {
	cands := wakeable(c.candidates(results), newConfigResolver(cfgFor))
	if len(cands) == 0 {
		return NodeConfig{}, false
	}
	return cands[0].entry, true
}
