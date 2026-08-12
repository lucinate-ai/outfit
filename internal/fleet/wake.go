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

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// WakeTimeout bounds waiting for a woken node's engine to answer. A cold node
// loads weights first, which on a large model is minutes. A variable so tests
// do not wait.
var WakeTimeout = 5 * time.Minute

// wakePoll is how often a waking node is re-checked.
var wakePoll = 2 * time.Second

// Waker reports progress while a node is woken. A silent five-minute pause
// reads as a hang, so the caller is given something to print.
type Waker func(format string, args ...any)

// Wake starts an engine for want on a node that is not running one, and waits
// until its engine answers. Candidates are tried in fleet-file order, and a
// node whose stored config already matches is preferred: it has the weights.
//
// A node that refuses the config — a runner or model it cannot serve — is not
// fatal while other candidates remain. When none succeeds, every refusal is
// reported together.
func (c *Config) Wake(ctx context.Context, w Want, dc remote.DeployConfig, results []NodeResult, log Waker) (*Choice, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	cands := wakeable(c.candidates(results), dc)
	if len(cands) == 0 {
		return nil, &ErrNoneServing{Results: results, Want: w, Path: c.Path}
	}

	var refused []string
	for _, cand := range cands {
		node, err := c.NewNode(cand.entry)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", cand.entry.Name, err))
			continue
		}
		log("Waking %s to serve %s...\n", cand.entry.Name, w.wanted())
		status, err := node.StartWith(ctx, &dc)
		if err != nil {
			// Another client may have woken this node first. That is
			// another route to the same place, not a failure — re-read
			// its state and take it if it is now serving what we want.
			if isAlreadyRunning(err) {
				if status, err = node.Status(ctx); err == nil && w.matches(status.Model) {
					log("%s was already started by someone else; using it.\n", cand.entry.Name)
					cand.result = NodeResult{Name: cand.entry.Name, Outcome: OutcomeOK, Status: status}
					return c.choiceFor(cand, w, true)
				}
			}
			refused = append(refused, fmt.Sprintf("%s: %v", cand.entry.Name, err))
			continue
		}
		ready, err := c.waitReady(ctx, node, cand.entry, w, log)
		if err != nil {
			return nil, err
		}
		cand.result = NodeResult{Name: cand.entry.Name, Outcome: OutcomeOK, Status: ready}
		return c.choiceFor(cand, w, true)
	}
	return nil, fmt.Errorf(
		"no node in %s could serve %s:\n  %s",
		c.Path, w.wanted(), strings.Join(refused, "\n  "))
}

// wakeable keeps the nodes that could be started, in the order to try them: a
// node whose stored config already names the wanted model first, since it has
// the weights and starts sooner.
func wakeable(cands []candidate, dc remote.DeployConfig) []candidate {
	var warm, cold []candidate
	for _, c := range cands {
		if !c.result.OK() || c.result.Status.State == string(daemon.StateRunning) {
			// A running engine is never displaced to make room.
			continue
		}
		if c.result.Status.Model != "" && c.result.Status.Model == dc.ModelID {
			warm = append(warm, c)
			continue
		}
		cold = append(cold, c)
	}
	return append(warm, cold...)
}

// waitReady blocks until the node reports running *and* its engine answers.
// The supervisor reports running when the process exists; llama.cpp then loads
// weights, so a launch that trusted the state alone would hand the agent an
// endpoint that refuses connections.
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
				baseURL, urlErr := c.EngineBaseURL(entry, status)
				if urlErr != nil {
					return status, urlErr
				}
				if engineAnswers(ctx, baseURL) {
					return status, nil
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
				"node %q did not answer within %s of being started: its engine may still be loading, "+
					"so it has been left running — check `outfit fleet status`",
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
func (c *Config) WouldWake(results []NodeResult, dc remote.DeployConfig) (NodeConfig, bool) {
	cands := wakeable(c.candidates(results), dc)
	if len(cands) == 0 {
		return NodeConfig{}, false
	}
	return cands[0].entry, true
}
