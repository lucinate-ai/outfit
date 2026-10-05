// Package metrics collects an engine's own token/request counters and the
// host's GPU/CPU/RAM figures, in process. It is the Go home of the collection
// that first shipped in the remote stats Lambda (remote/lambda/shared/stats.ts):
// the parsers here are ports of that Lambda's, kept value-for-value compatible
// so the remote path can later delegate to a daemon running this code and the
// TypeScript collectors can be deleted. Every stat is optional — a host
// without a source for one (no nvidia-smi, say) simply omits it, which is how
// a macOS node reports engine stats without GPU figures.
package metrics

// Stats is the collected state of one serving host: what is running, its
// engine counters, and its system figures. It mirrors the stats Lambda's
// response field-for-field (minus the Lambda's transport fields), so the
// existing `spinloop remote metrics` formats render it unchanged.
type Stats struct {
	State   string `json:"state"`
	Runner  string `json:"runner,omitempty"`
	ModelID string `json:"modelId,omitempty"`
	// ServedName is the name the engine answers to beside the model id —
	// mirroring daemon.StatusResponse.ServedName from the same record — so a
	// caller resolving a stopped host's model from its metrics gets the same
	// name a running host reports on its status.
	ServedName    string      `json:"servedName,omitempty"`
	UptimeSeconds int         `json:"uptimeSeconds,omitempty"`
	Tokens        *TokenStats `json:"tokens,omitempty"`
	GPUs          []GpuStat   `json:"gpus,omitempty"`
	CPU           *CpuStat    `json:"cpu,omitempty"`
	Memory        *MemoryStat `json:"memory,omitempty"`
	Errors        []string    `json:"errors,omitempty"`
	// LastActiveAt is when the engine last did any work, RFC 3339, and
	// IdleSeconds how long it has been since. They mirror the same two fields
	// on the daemon's status reply and are filled from the same record, so a
	// caller cannot get one answer from status and another from metrics.
	//
	// Both are empty until an engine has run. IdleSeconds is omitted at zero
	// as well, so an engine active this instant serialises a timestamp and no
	// idle value: read LastActiveAt to decide whether there is anything to
	// report, never IdleSeconds.
	//
	// History is the retained system readings, oldest first: one entry per
	// sampler tick while an engine ran, each carrying 0-100% figures rather
	// than raw ones, because that is the axis the bar format draws and raw
	// figures would double the wire size for nothing the graph uses. Absent
	// where no reading has been taken, and retained across a stop — the
	// readings up to the stop answer what the engine was doing until it
	// stopped — so, unlike the running-engine figures above, it can be
	// present while they are absent.
	History []HistorySample `json:"history,omitempty"`
	// Unlike the figures above, these describe the engine whatever its state —
	// a stopped engine still reports when it last worked.
	LastActiveAt string `json:"lastActiveAt,omitempty"`
	IdleSeconds  int    `json:"idleSeconds,omitempty"`
	// Ready is "ready" or "not-ready": whether the running engine has last
	// answered its own health check, mirroring daemon.StatusResponse.Ready
	// from the same record — a caller cannot get one answer from status and
	// another from metrics. Absent, not "not-ready", when it does not apply:
	// no engine is running, its runner has no known health-check convention,
	// or this daemon predates the check.
	Ready string `json:"ready,omitempty"`
	// RetainUntil is the environment's retention deadline, RFC 3339: the idle
	// sweep will not terminate the instance before it. It is a property of the
	// cloud instance, not the engine, so it is empty for local daemon nodes and
	// for remote environments without an update URL. Empty once the deadline
	// has passed, because the stats reply drops it there — a past tag keeps
	// nothing. Formatters omit the line when it is empty.
	RetainUntil string `json:"retainUntil,omitempty"`
}

// TokenStats holds per-engine token/request counters from /metrics.
type TokenStats struct {
	Running          int `json:"running"`
	Counter          int `json:"counter"`
	PromptTokens     int `json:"promptTokens"`
	GenerationTokens int `json:"generationTokens"`
	// Requests is the engine's cumulative request counter, present only
	// where the engine family's metrics expose one — vLLM's
	// request_success_total, today. A family whose metrics carry no
	// cumulative request count (llama.cpp's, today) leaves it nil, so a
	// missing figure and a genuine zero stay distinguishable: a zero is a
	// counter the engine served, a missing one is a figure no engine
	// produced.
	Requests *int `json:"requests,omitempty"`
}

// GpuStat holds per-GPU metrics from nvidia-smi.
type GpuStat struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Utilization int    `json:"utilization"`
	MemoryUsed  int64  `json:"memoryUsed"`
	MemoryTotal int64  `json:"memoryTotal"`
	Temperature int    `json:"temperature"`
}

// CpuStat holds whole-host CPU utilization.
type CpuStat struct {
	Utilization float64 `json:"utilization"`
}

// MemoryStat holds system memory in bytes.
type MemoryStat struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// HistorySample is one point of the retained system-reading history: when the
// reading was taken and the 0-100% figures the bar format plots for it. Every
// figure is optional on the same terms as Stats — a sample taken on a host
// with no source for one simply omits it — so a partial host yields a history
// of partial samples rather than an error.
//
// The JSON field names are one letter each because the history rides the
// remote relay over SSM, whose command output truncates at 4KB: forty
// samples of the window must fit that budget alongside the current reading,
// and at full names they would not.
type HistorySample struct {
	// Time is when the reading was taken, unix seconds.
	Time int64 `json:"t"`
	// CPU is whole-host CPU utilization, percent.
	CPU *float64 `json:"c,omitempty"`
	// Mem is system memory used over total, percent.
	Mem *float64 `json:"m,omitempty"`
	// GPUs is each GPU's figures, percent.
	GPUs []HistoryGPU `json:"g,omitempty"`
}

// HistoryGPU is one GPU's figures in a history sample, one-letter fields as
// its parent.
type HistoryGPU struct {
	Index int `json:"i"`
	// Util is utilization, percent.
	Util int `json:"u"`
	// Mem is memory used over total, percent. Absent where the GPU reports
	// no total.
	Mem *float64 `json:"m,omitempty"`
}
