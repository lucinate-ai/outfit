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
// existing `outfit remote metrics` formats render it unchanged.
type Stats struct {
	State         string      `json:"state"`
	Runner        string      `json:"runner,omitempty"`
	ModelID       string      `json:"modelId,omitempty"`
	UptimeSeconds int         `json:"uptimeSeconds,omitempty"`
	Tokens        *TokenStats `json:"tokens,omitempty"`
	GPUs          []GpuStat   `json:"gpus,omitempty"`
	CPU           *CpuStat    `json:"cpu,omitempty"`
	Memory        *MemoryStat `json:"memory,omitempty"`
	Errors        []string    `json:"errors,omitempty"`
}

// TokenStats holds per-engine token/request counters from /metrics.
type TokenStats struct {
	Running          int `json:"running"`
	Counter          int `json:"counter"`
	PromptTokens     int `json:"promptTokens"`
	GenerationTokens int `json:"generationTokens"`
	Requests         int `json:"requests"`
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
