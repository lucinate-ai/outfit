package fleet

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// cloudNode is one member of the fleet as seen through a remote, scale-to-zero
// environment: a `cloud.Config` reached over its cloud control plane rather than a
// machine's daemon. It answers the same operations a local node answers, so the same
// fan-out and rendering pass over both kinds — which is the whole point of routing a
// cloud environment through the node contract.
//
// The control-plane state (EC2) and a daemon's engine state are different
// vocabularies. We do not translate one into the other: the status carries the
// control-plane state as the control plane reports it. A cloud environment's
// "running" is the instance state, not a claim about the engine.
type cloudNode struct {
	name string
	cfg  cloud.Config
	// logs holds the position a follow of this node's engine log has reached.
	// It is the same cursor `spinloop cloud logs -f` uses, and for the same
	// reason: CloudWatch has no resumable read position of its own, so a poll
	// re-asks a little behind the newest event already seen and this
	// suppresses what the overlap re-reads, by event id.
	logs *cloud.FollowCursor

	// mu guards the facts Metrics retains for the capabilities to answer from.
	// A board refreshing several nodes reads and writes these from different
	// goroutines, so they are not left bare.
	mu sync.Mutex
	// instance is what the replies so far have said about the instance this
	// environment runs on, and uptime how long it has been running. They are
	// what Cost and Instance answer from, so neither costs a call of its own:
	// the replies that carried them have already been paid for.
	instance Instance
	uptime   int
}

// NewCloudNode builds the live node for a named cloud environment. The config
// must be complete enough to send a signed control call — start, stop and region —
// so a config that cannot be resolved is a configuration error against this node
// rather than a call that fails part-way through.
func NewCloudNode(name string, cfg cloud.Config) (Node, error) {
	if cfg.StartURL == "" || cfg.StopURL == "" || cfg.Region == "" {
		return nil, fmt.Errorf(
			"cloud environment %q is not fully configured: start_url, stop_url and region are all required",
			name)
	}
	return &cloudNode{name: name, cfg: cfg, logs: cloud.NewFollowCursor(cloud.FollowOverlap)}, nil
}

func (n *cloudNode) Name() string { return n.name }

func (n *cloudNode) Status(ctx context.Context) (daemon.StatusResponse, error) {
	resp, err := cloud.Status(ctx, n.cfg)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	n.mu.Lock()
	n.instance.BaseURL = resp.BaseURL
	n.instance.RetainUntil = resp.RetainUntil
	n.mu.Unlock()
	// The release is not in this reply — the stats one carries it, read from
	// the daemon on the box — so a running environment is asked for it, as the
	// environment-only status command asked. A stopped one has no daemon to
	// answer, and a failure to reach it leaves the version empty rather than
	// failing a status that otherwise succeeded.
	if resp.State == "running" || resp.State == "ready" {
		if stats, err := cloud.Stats(ctx, n.cfg); err == nil {
			n.mu.Lock()
			n.instance.Version = stats.Version
			n.instance.ID = stats.InstanceID
			n.instance.Type = stats.InstanceType
			n.mu.Unlock()
		}
	}
	return statusFromCloud(*resp), nil
}

func (n *cloudNode) Metrics(ctx context.Context) (metrics.Stats, error) {
	resp, err := cloud.Stats(ctx, n.cfg)
	if err != nil {
		return metrics.Stats{}, err
	}
	// The reply carries three facts the shared stats shape has no room for.
	// Retaining them here is what lets Cost and Version answer without a
	// second call for a reading already in hand.
	n.mu.Lock()
	n.uptime = resp.UptimeSeconds
	n.instance.ID = resp.InstanceID
	n.instance.Type = resp.InstanceType
	n.instance.Version = resp.Version
	n.mu.Unlock()
	return statsFromCloud(*resp), nil
}

// Cost prices the session this environment has been running, from the instance
// type it launched as and the region it launched in. Both come from the last
// metrics reading, so a caller that has already taken one pays only for the
// price lookup.
//
// A reading not yet taken, an instance that is not running, or a price the
// Price List API does not return all yield the zero Cost and no error: a node
// with no price to show reads the same however it came to have none.
func (n *cloudNode) Cost(ctx context.Context) (Cost, error) {
	n.mu.Lock()
	instanceType, uptime := n.instance.Type, n.uptime
	n.mu.Unlock()
	if instanceType == "" || uptime <= 0 {
		return Cost{}, nil
	}
	price, err := cloud.GetOnDemandPrice(ctx, n.cfg.Region, instanceType)
	if err != nil || price <= 0 {
		return Cost{}, nil
	}
	return Cost{SoFar: float64(uptime) / 3600.0 * price, PerHour: price}, nil
}

// Instance is what the replies so far have said about the instance this
// environment runs on. Empty fields are ones no reply carried — a stopped
// environment has no instance id, and a control plane that could not reach the
// daemon reports no version.
func (n *cloudNode) Instance() Instance {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.instance
}

// LogsMatching reads this environment's log store, narrowed by what the caller
// asked for. Unlike Logs it is a query rather than a cursor: the caller states
// the window it wants, so nothing is retained between calls.
func (n *cloudNode) LogsMatching(ctx context.Context, q LogQuery) (daemon.LogsResponse, error) {
	source := q.Source
	if source == "" {
		source = cloud.LogSourceEngine
	}
	limit := q.Limit
	if limit <= 0 {
		limit = cloudEngineTail
	}
	rq := cloud.LogQuery{
		Environment: n.cfg.Environment,
		Source:      source,
		Limit:       limit,
		Instance:    q.Instance,
	}
	if q.Since > 0 {
		rq.Start = time.Now().Add(-q.Since)
	}
	res, err := FetchLogsFn(ctx, n.cfg, rq)
	if err != nil {
		return daemon.LogsResponse{}, err
	}
	// A query is not a follow, so every event it returns is fresh to it.
	return logsFromCloud(res.Events, len(res.Events) == 0), nil
}

func (n *cloudNode) Start(ctx context.Context) (daemon.StatusResponse, error) {
	return n.StartWithProgress(ctx, func(StartPhase) {})
}

// StartWithProgress is Start with each phase of the boot passed to report as
// the start enters it: the attempt going out, a refusal for want of capacity
// and when the next attempt is due, the instance coming up, a dropped
// connection. Each phase replaces the one before it. Callers may render or
// discard them.
func (n *cloudNode) StartWithProgress(ctx context.Context, report func(StartPhase)) (daemon.StatusResponse, error) {
	if report == nil {
		// cloud.Start invokes its callbacks on every retry path, so nil is
		// substituted with a no-op here rather than left to whichever paths a
		// given start takes.
		report = func(StartPhase) {}
	}
	progress, onState := StartPhases(report)
	resp, err := cloud.Start(ctx, n.cfg, progress, onState, nil)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	return statusFromCloud(*resp), nil
}

// StartWith is how a router wakes a node to serve something. A remote
// environment's engine is not configured by a start: what it serves and the
// key that gates it are fixed by `spinloop cloud deploy`, a heavier flow
// (provisioning, weight seeding, ingress) that a node start must not
// conflate — so dc and engineKey are ignored, and this boots the instance
// exactly as Start does.
//
// An undeployed environment is not checked for here: a status read cannot
// tell a stopped-but-deployed environment from an undeployed one — the
// control plane only relays what an environment serves on its status reply
// while it is running — so that check has to happen where deployment is
// actually confirmed, by reading the environment's deploy config directly
// (its stats reply, in the gateway's own candidate matching) before a
// candidate ever reaches this call. A caller that skips that matching and
// hands an undeployed environment straight to StartWith gets the boot
// call's own answer instead, whatever that turns out to be.
func (n *cloudNode) StartWith(ctx context.Context, dc *inference.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	_ = dc
	_ = engineKey
	return n.StartWithProgress(ctx, func(StartPhase) {})
}

func (n *cloudNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	resp, err := cloud.Stop(ctx, n.cfg)
	if err != nil {
		return daemon.StatusResponse{}, err
	}
	return statusFromCloud(*resp), nil
}

// Keep pins this environment's instance so the idle sweep does not terminate it
// before now plus d. The deadline is computed here and passed as an absolute
// time, the way the CLI computes it; a keep is what keeps the instance, not
// what the engine is doing, so the call needs no engine or deploy context. If
// the control plane's reply names the deadline it recorded, that is returned
// verbatim; when the reply omits it — a control plane that predates keep
// echoing the value back — the requested deadline is returned instead, so a
// caller always holds something to show.
func (n *cloudNode) Keep(ctx context.Context, d time.Duration) (string, error) {
	deadline := time.Now().Add(d)
	resp, err := cloud.Keep(ctx, n.cfg, deadline)
	if err != nil {
		return "", err
	}
	if resp.RetainUntil != "" {
		return resp.RetainUntil, nil
	}
	return deadline.UTC().Format(time.RFC3339), nil
}

// FetchLogsFn is exported so a test in this package or in cmd/spinloop can
// substitute it. The read goes to the cloud's log store through the AWS SDK
// rather than an HTTP Function URL, so there is no HTTP test server that can
// stand in for it.
var FetchLogsFn = cloud.FetchLogs

// cloudEngineTail caps how many engine log events a node read pulls. Cloud logs
// are a bounded tail pulled from the log store, not a byte cursor, so a follow of
// a chatty engine must not page through an unbounded window.
const cloudEngineTail = 1000

func (n *cloudNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	// daemon.TailLog means a fresh open of the view: start the cursor over,
	// so this open shows its own tail rather than having it suppressed as
	// already seen by whatever this node last followed.
	if offset == daemon.TailLog {
		n.logs.Reset()
	}
	start := n.logs.Start()
	res, err := FetchLogsFn(ctx, n.cfg, cloud.LogQuery{
		Environment: n.cfg.Environment,
		Source:      cloud.LogSourceEngine,
		Limit:       cloudEngineTail,
		Start:       start,
	})
	if err != nil {
		return daemon.LogsResponse{}, err
	}
	fresh := n.logs.Advance(res.Events)
	// Missing only on the read that had no lower bound yet finding nothing:
	// that is genuinely no log, ever. A later poll with nothing new is a
	// quiet log, not a missing one.
	missing := start.IsZero() && len(res.Events) == 0
	return logsFromCloud(fresh, missing), nil
}

// statusFromCloud maps the control plane's status reply onto the node's status.
// It carries what a status reply can honestly be mapped across: the state, what
// the engine is serving (runner, model, served name) and its last-active record.
// The version is not in this reply — the stats reply carries it — so it is empty
// here. The serving fields are empty when the daemon reports none: an engine that
// is not running one, or a daemon the control plane could not reach.
//
// A running environment's reply also names where its engine answers — the
// instance's published address, which a daemon on the instance cannot know for
// itself but the control plane can. It is carried as the engine's host, so
// routing resolves a cloud node's address the way it resolves any node's.
// Absent (a stopped or undeployed environment reports none) means no engine
// address, exactly as the parts would be.
//
// Healthy is the control plane's own readiness reading — the same health
// check (hitting the engine's /health, excluding the 503 it answers while
// still loading weights) a running cloud view already carries — mapped
// onto Ready the way a local daemon's own reading is, so a router waiting
// for a cloud engine to answer trusts this instead of falling back to
// whether its port merely accepts a connection, which it can do well before
// the model has loaded. Absent (an older control plane, or the SSM agent
// not yet reachable) leaves Ready empty, the same "no reading yet" a local
// daemon reports before its own first check lands.
func statusFromCloud(resp cloud.Response) daemon.StatusResponse {
	s := daemon.StatusResponse{
		State:        resp.State,
		Runner:       resp.Runner,
		Model:        resp.ModelID,
		ServedName:   resp.ServedName,
		LastActiveAt: resp.LastActiveAt,
		IdleSeconds:  resp.IdleSeconds,
	}
	if resp.Healthy != nil {
		if *resp.Healthy {
			s.Ready = daemon.ReadyYes
		} else {
			s.Ready = daemon.ReadyNo
		}
	}
	if u, err := url.Parse(resp.BaseURL); resp.BaseURL != "" && err == nil && u.Host != "" {
		port, _ := strconv.Atoi(u.Port())
		s.Engine = &daemon.EngineEndpoint{
			Host: u.Hostname(),
			Port: port,
			Path: u.Path,
		}
	}
	return s
}

// statsFromCloud maps the stats Lambda's reply onto the shared stats shape. The
// per-stat fields already alias the metrics types the collector produces, so the
// mapping is a field-for-field copy. The reply's version and instance facts are
// deliberately not among them: they describe a cloud environment and nothing
// else, so putting them on the shape every node answers with would leave a
// field every daemon reports empty. Metrics retains them on the node instead,
// where Cost and Version answer from them.
func statsFromCloud(resp cloud.StatsResponse) metrics.Stats {
	return metrics.Stats{
		State:         resp.State,
		Runner:        resp.Runner,
		ModelID:       resp.ModelID,
		ServedName:    resp.ServedName,
		UptimeSeconds: resp.UptimeSeconds,
		Tokens:        resp.Tokens,
		GPUs:          resp.GPUs,
		CPU:           resp.CPU,
		Memory:        resp.Memory,
		History:       resp.History,
		Errors:        resp.Errors,
		LastActiveAt:  resp.LastActiveAt,
		IdleSeconds:   resp.IdleSeconds,
		RetainUntil:   resp.RetainUntil,
	}
}

// logsFromCloud maps one poll's fresh events — the ones cloudNode.Logs's
// FollowCursor has not already returned — onto the node's log reply. Events
// arrive oldest first, so the content reads top to bottom. missing reports
// that this was a from-the-beginning read that found nothing: the state a
// reader can act on (the engine has not run here, or has sent nothing),
// distinct from a later poll simply having nothing new to add. NextOffset
// only needs to say "not a fresh open" to the next call — the real position
// lives in the node's own cursor — so it carries the newest event shown for
// whoever finds that useful to see, and 0 has the same effect when there is
// none.
func logsFromCloud(fresh []cloud.LogEvent, missing bool) daemon.LogsResponse {
	if len(fresh) == 0 {
		return daemon.LogsResponse{Missing: missing}
	}
	msgs := make([]string, len(fresh))
	for i, e := range fresh {
		msgs[i] = e.Message
	}
	// Each event is already one complete, discrete line — unlike a byte-stream
	// tail, there is never a trailing partial line to leave unterminated — so
	// the join ends in a newline the same way a local engine log's lines do.
	content := strings.Join(msgs, "\n") + "\n"
	return daemon.LogsResponse{
		Content:    content,
		NextOffset: fresh[len(fresh)-1].Timestamp.UnixMilli(),
		Size:       int64(len(content)),
	}
}
