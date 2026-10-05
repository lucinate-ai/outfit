package fleet

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
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

// ProgressStarter is an optional node capability: a start whose call reports
// its situation as it works, one phase at a time. A node whose start is a
// single request and a single reply (a daemon that queues the work and
// returns) has no such situation and does not implement it. A caller that can
// show progress (the dashboard) asserts for it and falls back to Start when
// it is absent.
//
// report is called with each phase the start enters, and each call replaces
// the one before it: a start has one situation at a time, so the value a
// caller holds is the whole of what the start is doing.
type ProgressStarter interface {
	StartWithProgress(ctx context.Context, report func(StartPhase)) (daemon.StatusResponse, error)
}

// Keeper is an optional node capability: a node whose instance can be pinned so
// the idle sweep does not terminate it before a stated deadline. Only remote
// environments have it — the retention tag lives on a cloud instance — so a
// local daemon node does not implement it. A caller that can offer a keep
// (the dashboard) asserts for it and hides the key when it is absent.
//
// d is how long, from now, to keep the instance. The return is the control
// plane's own deadline, RFC 3339: the absolute instant the instance is kept
// until, as the control plane recorded it — not the caller's clock plus d — so
// what a caller shows matches what the control plane holds.
type Keeper interface {
	Keep(ctx context.Context, d time.Duration) (string, error)
}

// Coster is an optional node capability: a node that can price the time it has
// been running. Only a cloud environment can — the price comes from the
// instance type it launched as and the region it launched in, neither of which
// a machine someone already owns has an answer for — so a daemon node does not
// implement it. A caller offering a cost (metrics --cost) asserts for it and
// renders the node as it would without the flag when it is absent.
//
// The lookup crosses the network, so it is made only when a caller asks. A
// price that cannot be fetched is not an error: the node reports no cost, the
// same as a node that cannot be priced at all, because a missing price and an
// unpriceable node read the same in a table.
type Coster interface {
	Cost(ctx context.Context) (Cost, error)
}

// Cost is what a node has spent on the session it is running, and the rate it
// is spending at. Zero means no figure is available — the instance is not
// running, its type is unknown, or the price lookup did not complete.
type Cost struct {
	// SoFar is the estimated spend on the current running session.
	SoFar float64
	// PerHour is the on-demand rate the estimate was computed from.
	PerHour float64
}

// Reported says whether there is a figure to show. A caller renders nothing
// rather than a zero: "$0.00" claims a node cost nothing, which is a different
// statement from having no price for it.
func (c Cost) Reported() bool { return c.PerHour > 0 }

// SourceLogger is an optional node capability: a node whose log is a store that
// can be queried — by which log to read, how far back, and which instance
// produced it. Only a cloud environment has one; a daemon's log is a byte
// offset into one file on one machine, where none of the three narrows
// anything. A caller offering those flags asserts for it and reads the node
// through Logs when it is absent.
type SourceLogger interface {
	LogsMatching(ctx context.Context, q LogQuery) (daemon.LogsResponse, error)
}

// LogQuery is what a caller can narrow a queryable log by. A zero field means
// "do not narrow by this".
type LogQuery struct {
	// Source names which of the node's logs to read — its engine's output, its
	// boot record, or both.
	Source string
	// Since bounds how far back to read.
	Since time.Duration
	// Instance restricts the read to one instance id, for a node whose store
	// holds the output of more than one.
	Instance string
	// Limit caps the events returned, keeping the most recent.
	Limit int
}

// InstanceReporter is an optional node capability: a node running on an
// instance it can describe. A daemon node runs on a machine its operator
// already knows about and does not implement it; a cloud environment reports
// the instance it launched, the release on it, the address it answers at and
// how long it is retained — the facts the environment-only commands used to
// render and the shared engine reply has no room for.
//
// The values come from the replies the node has already received, so asking
// costs nothing. A field the node has not been told is empty.
type InstanceReporter interface {
	Instance() Instance
}

// Instance is what a node reports about the instance it runs on. Every field
// is optional: a reply that did not carry one leaves it empty, and a renderer
// omits what is empty rather than printing a blank.
type Instance struct {
	// ID is the instance identifier the control plane assigned.
	ID string
	// Type is the instance type it launched as.
	Type string
	// Version is the spinloop release running on it.
	Version string
	// BaseURL is where its engine answers, as the control plane published it.
	BaseURL string
	// RetainUntil is its retention deadline, RFC 3339, while it has one.
	RetainUntil string
}

// Node is one member of the fleet. Only daemonNode implements it today; the
// interface exists so a remote-environment kind (an `spinloop cloud`
// environment read through its stats Lambda, which already yields
// metrics.Stats) can be added without reworking the fan-out or the renderers.
type Node interface {
	// Name is the node's name from the fleet file.
	Name() string
	Status(ctx context.Context) (daemon.StatusResponse, error)
	Metrics(ctx context.Context) (metrics.Stats, error)
	Start(ctx context.Context) (daemon.StatusResponse, error)
	// StartWith starts the engine on a deploy config the caller supplies,
	// gated with the key it supplies — which is how a router wakes a node to
	// serve what it wants, and how that engine comes to be gated at all.
	StartWith(ctx context.Context, dc *inference.DeployConfig, engineKey string) (daemon.StatusResponse, error)
	Stop(ctx context.Context) (daemon.StatusResponse, error)
	Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error)
}

// daemonNode is a machine running `spinloop daemon`, reached over its control
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

func (n *daemonNode) StartWith(ctx context.Context, dc *inference.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	return n.client.StartWith(ctx, dc, engineKey)
}

func (n *daemonNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	return n.client.Stop(ctx)
}

func (n *daemonNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	return n.client.Logs(ctx, offset, limit)
}

// NewNode builds the live Node for one fleet-file entry. A daemon node resolves
// its bearer token here — a reference that resolves to nothing fails before any
// call is attempted, naming the variable rather than surfacing later as a 401.
// A cloud node loads its registered environment's control config; a missing
// environment fails the same way, as a per-node error the fan-out renders as a
// row rather than a blanked view.
func (c *Config) NewNode(entry NodeConfig) (Node, error) {
	if entry.Kind == KindCloud {
		// The node's name is the registered environment's key, and every
		// environment resolves the one way: by name, from the registry.
		cfg, err := cloud.LoadEnvironment(entry.Name, os.Getenv)
		if err != nil {
			return nil, err
		}
		return NewCloudNode(entry.Name, cfg)
	}
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

	// Cost is what this node has spent on its running session. Filled only by
	// a call that asked for it, and only for a node that can be priced; see
	// Cost.Reported for the difference between "nothing" and "no figure".
	Cost Cost
	// Instance describes the instance this node runs on, for a node that runs
	// on one it can describe. The zero value means the node reported none,
	// which is every node that is a machine rather than an instance.
	Instance Instance

	// At is when this reading was taken — set by the fan-out as the call
	// returns. Reads are concurrent and of uneven duration, so a reading can
	// land after one taken later than it; a caller that draws the newest
	// reading orders them by this rather than by arrival. It is also the
	// reading's age, which is what tells a display an answer no longer
	// describes the node now. Zero on a result built outside the fan-out.
	At time.Time
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
