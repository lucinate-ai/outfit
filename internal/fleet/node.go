package fleet

import (
	"context"
	"errors"
	"net/http"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// Outcome classifies how a node call ended. A fleet view renders these as rows
// rather than failing, so one unreachable box never blanks the rest of the
// fleet.
type Outcome string

const (
	// OutcomeOK means the node answered.
	OutcomeOK Outcome = "ok"
	// OutcomeUnreachable means the daemon could not be contacted at all —
	// refused, timed out, no such host.
	OutcomeUnreachable Outcome = "unreachable"
	// OutcomeUnauthorized means the daemon rejected the client's token.
	// Distinct from unreachable: the box is up, the credential is wrong.
	OutcomeUnauthorized Outcome = "unauthorized"
	// OutcomeConfigError means this node could not even be called — most
	// often a tokenEnv naming a variable that is set nowhere. A typo surfaces
	// on that node's row instead of masquerading as an auth failure.
	OutcomeConfigError Outcome = "config-error"
	// OutcomeFailed means the daemon answered with an error (a refused start,
	// an unservable config). The node is healthy; the request was not.
	OutcomeFailed Outcome = "failed"
	// OutcomeUnsupported means the daemon does not serve the endpoint asked
	// for — it predates it. Distinct from failed: the fix is upgrading that
	// node, not retrying or reconfiguring it, and a fleet mid-rollout will
	// legitimately hold a mix.
	OutcomeUnsupported Outcome = "unsupported"
)

// Node is one member of the fleet. Only daemonNode implements it today; the
// interface exists so a remote-environment kind (an `outfit remote`
// environment read through its stats Lambda, which already yields
// metrics.Stats) can be added without reworking the fan-out or the renderers.
type Node interface {
	// Name is the node's name from the fleet file.
	Name() string
	Status(ctx context.Context) (daemon.StatusResponse, error)
	Metrics(ctx context.Context) (metrics.Stats, error)
	Start(ctx context.Context) (daemon.StatusResponse, error)
	// StartWith starts the engine on a deploy config the caller supplies,
	// which is how a router wakes a node to serve what it wants.
	StartWith(ctx context.Context, dc *remote.DeployConfig) (daemon.StatusResponse, error)
	Stop(ctx context.Context) (daemon.StatusResponse, error)
	Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error)
}

// daemonNode is a machine running `outfit daemon`, reached over its control
// API.
type daemonNode struct {
	name   string
	client *Client
}

func (n *daemonNode) Name() string { return n.name }

func (n *daemonNode) Status(ctx context.Context) (daemon.StatusResponse, error) {
	return n.client.Status(ctx)
}

func (n *daemonNode) Metrics(ctx context.Context) (metrics.Stats, error) {
	return n.client.Metrics(ctx)
}

func (n *daemonNode) Start(ctx context.Context) (daemon.StatusResponse, error) {
	return n.client.Start(ctx)
}

func (n *daemonNode) StartWith(ctx context.Context, dc *remote.DeployConfig) (daemon.StatusResponse, error) {
	return n.client.StartWith(ctx, dc)
}

func (n *daemonNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	return n.client.Stop(ctx)
}

func (n *daemonNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	return n.client.Logs(ctx, offset, limit)
}

// NewNode builds the live Node for one fleet-file entry, resolving its token.
// A token reference that resolves to nothing fails here — before any call is
// attempted — so the error names the variable rather than surfacing later as
// a 401.
func (c *Config) NewNode(entry NodeConfig) (Node, error) {
	token, err := c.Token(entry)
	if err != nil {
		return nil, err
	}
	return &daemonNode{
		name:   entry.Name,
		client: &Client{BaseURL: entry.BaseURL(), Token: token},
	}, nil
}

// NodeResult is one node's outcome, as a value: either the answer or a typed
// failure. Fan-out returns these rather than errors, so a bad node is a
// rendered row and the command still succeeds.
type NodeResult struct {
	// Name is the node this describes.
	Name string
	// Outcome says how the call ended; Err carries the detail when it is not
	// OutcomeOK.
	Outcome Outcome
	Err     error

	// Status, Metrics and Logs hold the answer, whichever the call asked for.
	Status  daemon.StatusResponse
	Metrics metrics.Stats
	Logs    daemon.LogsResponse
}

// OK reports whether the node answered.
func (r NodeResult) OK() bool { return r.Outcome == OutcomeOK }

// Detail is the failure text for a row, or "" when the node answered.
func (r NodeResult) Detail() string {
	if r.Err == nil {
		return ""
	}
	return r.Err.Error()
}

// classify turns a call error into the outcome a row shows. A 401 is the
// token; any other daemon reply is a refused request; anything that never got
// an answer is unreachable.
func classify(err error) Outcome {
	if err == nil {
		return OutcomeOK
	}
	var he *httpError
	if errors.As(err, &he) {
		if he.status == http.StatusUnauthorized {
			return OutcomeUnauthorized
		}
		if he.status == http.StatusNotFound {
			// The daemon answered, but has no such endpoint: it is older than
			// the client. Naming that beats reporting a bare 404.
			return OutcomeUnsupported
		}
		return OutcomeFailed
	}
	return OutcomeUnreachable
}

// result builds a NodeResult from a call's error.
func result(name string, err error) NodeResult {
	return NodeResult{Name: name, Outcome: classify(err), Err: err}
}

// Result builds a NodeResult carrying a status reply, classifying the error
// the same way fan-out does. It is how a caller outside this package (the
// fleet command's start/stop) turns one node call into a result.
func Result(name string, err error, status daemon.StatusResponse) NodeResult {
	r := result(name, err)
	r.Status = status
	return r
}
