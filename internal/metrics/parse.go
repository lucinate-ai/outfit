package metrics

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseGPUStats parses nvidia-smi CSV output for per-GPU stats. The command is
//
//	nvidia-smi --query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu --format=csv,noheader,nounits
//
// which produces lines like "0, NVIDIA L40S, 12, 8192, 46080, 42". Memory
// values are in MiB (nounits strips the label but not the unit); they are
// converted to bytes so byte formatting renders correctly.
func ParseGPUStats(out string) []GpuStat {
	const mib = 1024 * 1024
	var gpus []GpuStat
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 6 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		gpus = append(gpus, GpuStat{
			Index:       index,
			Name:        parts[1],
			Utilization: atoiOrZero(parts[2]),
			MemoryUsed:  int64(atoiOrZero(parts[3])) * mib,
			MemoryTotal: int64(atoiOrZero(parts[4])) * mib,
			Temperature: atoiOrZero(parts[5]),
		})
	}
	return gpus
}

// ParseVmstatCPU parses `vmstat 1 2` output, whose last line is the sampled
// interval. The idle column (id) is field 15 of the standard 17-column layout;
// utilization is 100 - idle. Returns nil when the output is not vmstat's.
func ParseVmstatCPU(out string) *CpuStat {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return nil
	}
	fields := strings.Fields(last)
	if len(fields) < 15 {
		return nil
	}
	idle, err := strconv.Atoi(fields[14])
	if err != nil {
		return nil
	}
	util := float64(100 - idle)
	if util < 0 {
		util = 0
	}
	return &CpuStat{Utilization: util}
}

// ParseFreeMemory parses `free -b` output, reading the "Mem:" line's total and
// used columns. Returns nil when the output is not free's.
func ParseFreeMemory(out string) *MemoryStat {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "Mem:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil
		}
		total, err1 := strconv.ParseInt(fields[1], 10, 64)
		used, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil {
			return nil
		}
		return &MemoryStat{Total: total, Used: used}
	}
	return nil
}

// topCPULine matches macOS `top -l 1`'s summary, e.g.
// "CPU usage: 7.5% user, 10.0% sys, 82.5% idle".
var topCPULine = regexp.MustCompile(`CPU usage:.*?([0-9.]+)% idle`)

// ParseTopCPU parses macOS `top -l 1` output for whole-host CPU utilization,
// reported as 100 - idle. Returns nil when no CPU usage line is present.
func ParseTopCPU(out string) *CpuStat {
	m := topCPULine.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	idle, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	util := 100 - idle
	if util < 0 {
		util = 0
	}
	return &CpuStat{Utilization: util}
}

// vmStatPage matches a macOS `vm_stat` line, e.g. "Pages active:  123456.".
var vmStatPage = regexp.MustCompile(`^Pages ([a-z ]+?):\s+(\d+)\.?$`)

// vmStatPageSize matches vm_stat's header, "... (page size of 16384 bytes)".
var vmStatPageSize = regexp.MustCompile(`page size of (\d+) bytes`)

// ParseVMStatMemory parses macOS memory figures: the total from
// `sysctl -n hw.memsize`, and used from `vm_stat` as the active, wired and
// compressor-occupied pages times the page size. Returns nil when either
// output is unusable.
func ParseVMStatMemory(memsize, vmStat string) *MemoryStat {
	total, err := strconv.ParseInt(strings.TrimSpace(memsize), 10, 64)
	if err != nil || total <= 0 {
		return nil
	}
	pageSize := int64(0)
	if m := vmStatPageSize.FindStringSubmatch(vmStat); m != nil {
		pageSize, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if pageSize <= 0 {
		return nil
	}
	counted := map[string]bool{"active": true, "wired down": true, "occupied by compressor": true}
	var pages int64
	matched := false
	for _, line := range strings.Split(vmStat, "\n") {
		m := vmStatPage.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || !counted[m[1]] {
			continue
		}
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}
		pages += n
		matched = true
	}
	if !matched {
		return nil
	}
	return &MemoryStat{Total: total, Used: pages * pageSize}
}

// engineSpec names the Prometheus metrics one engine family exposes: the
// prefix on every metric, the gauges that count in-flight work, and the
// cumulative counters. The names mirror the stats Lambda's runner-aware
// scrape (remote/lambda/shared/idle.ts) exactly.
type engineSpec struct {
	prefix     string
	running    map[string]bool
	counters   map[string]bool
	generation string
}

var engineSpecs = map[string]engineSpec{
	"vllm": {
		prefix:     "vllm",
		running:    map[string]bool{"num_requests_running": true, "num_requests_waiting": true},
		counters:   map[string]bool{"prompt_tokens_total": true, "generation_tokens_total": true, "request_success_total": true},
		generation: "generation_tokens_total",
	},
	"llamacpp": {
		prefix:     "llamacpp",
		running:    map[string]bool{"requests_processing": true, "requests_deferred": true},
		counters:   map[string]bool{"prompt_tokens_total": true, "tokens_predicted_total": true, "n_decode_total": true},
		generation: "tokens_predicted_total",
	},
}

// metricLine matches one Prometheus sample: "prefix:name{labels} value".
func metricLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^` + prefix + `:([a-z_]+)(?:\{[^}]*\})?\s+([0-9.eE+-]+)$`)
}

// ParseTokenStats parses an engine's Prometheus /metrics text into token and
// request counters. engine selects the metric dialect ("llamacpp" or "vllm");
// an unknown engine, or output with none of that engine's activity metrics,
// yields nil — the engine-stats-absent case, exactly as the Lambda's
// parseMetrics treats a failed scrape.
func ParseTokenStats(out, engine string) *TokenStats {
	spec, ok := engineSpecs[engine]
	if !ok {
		return nil
	}
	line := metricLine(spec.prefix)
	var running, counter float64
	matched := false
	for _, raw := range strings.Split(out, "\n") {
		m := line.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		value, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		switch {
		case spec.running[m[1]]:
			running += value
			matched = true
		case spec.counters[m[1]]:
			counter += value
			matched = true
		}
	}
	if !matched {
		return nil
	}
	return &TokenStats{
		Running:          int(running),
		Counter:          int(counter),
		PromptTokens:     extractCounter(out, spec, "prompt_tokens_total"),
		GenerationTokens: extractCounter(out, spec, spec.generation),
		Requests:         extractCounter(out, spec, "request_success_total"),
	}
}

// extractCounter pulls a single named counter out of the raw scrape, matching
// the Lambda's extractCounter: absent or unparsable reads as zero.
func extractCounter(out string, spec engineSpec, name string) int {
	re := regexp.MustCompile(`(?m)^` + spec.prefix + `:` + name + `(?:\{[^}]*\})?\s+([0-9.eE+-]+)$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return int(v)
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
