package fleet

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// remoteNode is one member of the fleet as seen through a remote, scale-to-zero
// environment: a `remote.Config` reached over its cloud control plane rather than a
// machine's daemon. It answers the same operations a local node answers, so the same
// fan-out and rendering pass over both kinds — which is the whole point of routing a
// remote environment through the node contract.
//
// The control-plane state (EC2) and a daemon's engine state are different
// vocabularies. We do not translate one into the other: the status carries the
// control-plane state as the control plane reports it. A remote environment's
// "running" is the instance state, not a claim about the engine.
type remoteNode struct {
	name string
	cfg  remote.Config
}

// NewRemoteNode builds the live node for a named remote environment. The config
// must be complete enough to send a signed control call — start, stop and region —
// so a config that cannot be resolved is a configuration error against this node
// rather than a call that fails part-way through.
func NewRemoteNode(name string, cfg remote.Config) (Node, error) {
	if cfg.StartURL == "" || cfg.StopURL == "" || cfg.Region == "" {
		return nil, fmt.Errorf(
			"remote environment %q is not fully configured: start_url, stop_url and region are all required",
			name)
	}
	return &remoteNode{name: name, cfg: cfg}, nil
}

func (n *remoteNode) Name() string { return n.name }

func (n *remoteNode) Status(ctx context.Context) (daemon.StatusResponse, error) {
	resp, err := remote.Status(ctx, n.cfg)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	return statusFromRemote(*resp), nil
}

func (n *remoteNode) Metrics(ctx context.Context) (metrics.Stats, error) {
	resp, err := remote.Stats(ctx, n.cfg)
	if err != nil {
		return metrics.Stats{}, err
	}
	return statsFromRemote(*resp), nil
}

func (n *remoteNode) Start(ctx context.Context) (daemon.StatusResponse, error) {
	return n.StartWithProgress(ctx, func(string) {})
}

// StartWithProgress is Start carrying the control plane's own status lines up
// as the boot proceeds — the environment's state and the wait to the next
// poll, one line per retry. The caller shows or drops them.
func (n *remoteNode) StartWithProgress(ctx context.Context, progress func(string)) (daemon.StatusResponse, error) {
	resp, err := remote.Start(ctx, n.cfg, progress, nil, nil)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	return statusFromRemote(*resp), nil
}

// StartWith is how a router wakes a node to serve something. A remote environment
// is not woken: what it serves is set by `outfit remote deploy`, a heavier flow
// (provisioning, weight seeding, ingress) that a node start must not conflate.
func (n *remoteNode) StartWith(ctx context.Context, dc *remote.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	_ = dc
	_ = engineKey
	return daemon.StatusResponse{}, fmt.Errorf(
		"%s is a remote environment, not a node to be woken: tell it what to serve with `outfit remote deploy`",
		n.name)
}

func (n *remoteNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	resp, err := remote.Stop(ctx, n.cfg)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	return statusFromRemote(*resp), nil
}

// remoteEngineTail caps how many engine log events a node read pulls. Remote logs
// are a bounded tail pulled from the log store, not a byte cursor, so a follow of
// a chatty engine must not page through an unbounded window.
const remoteEngineTail = 1000

func (n *remoteNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	// The offset is accepted to satisfy the node contract, but remote logs are a
	// tail of the log store, not a position to resume from, so it is not a cursor.
	_ = offset
	res, err := remote.FetchLogs(ctx, n.cfg, remote.LogQuery{
		Environment: n.cfg.Environment,
		Source:      remote.LogSourceEngine,
		Limit:       remoteEngineTail,
	})
	if err != nil {
		return daemon.LogsResponse{}, err
	}
	return logsFromRemote(res), nil
}

// statusFromRemote maps the control plane's status reply onto the node's status.
// It carries only what a control-plane reply can honestly be mapped across: the
// state and its last-active record. The version is not in this reply — the stats
// reply carries it — so it is empty here, and runner/model are likewise absent.
func statusFromRemote(resp remote.Response) daemon.StatusResponse {
	return daemon.StatusResponse{
		State:        resp.State,
		LastActiveAt: resp.LastActiveAt,
		IdleSeconds:  resp.IdleSeconds,
	}
}

// statsFromRemote maps the stats Lambda's reply onto the shared stats shape. The
// per-stat fields already alias the metrics types the collector produces, so the
// mapping is a field-for-field copy; the reply's version and instance facts have
// no home on the node's stats and are left to the remote view.
func statsFromRemote(resp remote.StatsResponse) metrics.Stats {
	return metrics.Stats{
		State:         resp.State,
		Runner:        resp.Runner,
		ModelID:       resp.ModelID,
		UptimeSeconds: resp.UptimeSeconds,
		Tokens:        resp.Tokens,
		GPUs:          resp.GPUs,
		CPU:           resp.CPU,
		Memory:        resp.Memory,
		Errors:        resp.Errors,
		LastActiveAt:  resp.LastActiveAt,
		IdleSeconds:   resp.IdleSeconds,
	}
}

// logsFromRemote maps a fetched log tail onto the node's log reply. Events arrive
// oldest first, so the content reads top to bottom. An empty tail is reported as
// a missing log rather than an empty one: that is the state a reader can act on
// (the engine has not run here, or has sent nothing).
func logsFromRemote(res remote.LogResult) daemon.LogsResponse {
	if len(res.Events) == 0 {
		return daemon.LogsResponse{Missing: true}
	}
	msgs := make([]string, len(res.Events))
	for i, e := range res.Events {
		msgs[i] = e.Message
	}
	content := strings.Join(msgs, "\n")
	return daemon.LogsResponse{
		Content:    content,
		NextOffset: int64(len(content)),
		Size:       int64(len(content)),
	}
}
