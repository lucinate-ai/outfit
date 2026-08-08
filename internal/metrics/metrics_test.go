package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

const nvidiaSMIFixture = `0, NVIDIA L40S, 12, 8192, 46080, 42
1, NVIDIA L40S, 97, 40960, 46080, 71
`

func TestParseGPUStats(t *testing.T) {
	gpus := ParseGPUStats(nvidiaSMIFixture)
	if len(gpus) != 2 {
		t.Fatalf("got %d GPUs, want 2", len(gpus))
	}
	const mib = 1024 * 1024
	want := GpuStat{Index: 0, Name: "NVIDIA L40S", Utilization: 12,
		MemoryUsed: 8192 * mib, MemoryTotal: 46080 * mib, Temperature: 42}
	if gpus[0] != want {
		t.Errorf("gpu 0 = %+v, want %+v", gpus[0], want)
	}
	if gpus[1].Utilization != 97 || gpus[1].Index != 1 {
		t.Errorf("gpu 1 = %+v", gpus[1])
	}
}

func TestParseGPUStatsGarbage(t *testing.T) {
	if got := ParseGPUStats("no gpus here\n\nshort, line\n"); got != nil {
		t.Errorf("garbage parsed to %+v, want nil", got)
	}
}

const vmstatFixture = `procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----
 r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st
 1  0      0 947184  84224 590452    0    0    31    17  210  350  3  1 95  1  0
 2  0      0 947184  84224 590452    0    0     0     0  180  300 20  5 70  5  0
`

func TestParseVmstatCPU(t *testing.T) {
	cpu := ParseVmstatCPU(vmstatFixture)
	if cpu == nil {
		t.Fatal("got nil CPU stat")
	}
	if cpu.Utilization != 30 {
		t.Errorf("utilization = %v, want 30 (100 - id 70)", cpu.Utilization)
	}
	if ParseVmstatCPU("nothing useful") != nil {
		t.Error("garbage parsed to a CPU stat")
	}
}

const freeFixture = `              total        used        free      shared  buff/cache   available
Mem:    33020416512  4294967296 12884901888   270532608 15840547328 27917287424
Swap:             0           0           0
`

func TestParseFreeMemory(t *testing.T) {
	mem := ParseFreeMemory(freeFixture)
	if mem == nil {
		t.Fatal("got nil memory stat")
	}
	if mem.Total != 33020416512 || mem.Used != 4294967296 {
		t.Errorf("memory = %+v", mem)
	}
	if ParseFreeMemory("no Mem line") != nil {
		t.Error("garbage parsed to a memory stat")
	}
}

const topFixture = `Processes: 500 total, 2 running, 498 sleeping, 2600 threads
Load Avg: 2.11, 2.45, 2.51
CPU usage: 7.55% user, 10.12% sys, 82.31% idle
PhysMem: 24G used (2210M wired), 8032M unused.
`

func TestParseTopCPU(t *testing.T) {
	cpu := ParseTopCPU(topFixture)
	if cpu == nil {
		t.Fatal("got nil CPU stat")
	}
	if got := fmt.Sprintf("%.2f", cpu.Utilization); got != "17.69" {
		t.Errorf("utilization = %s, want 17.69 (100 - idle 82.31)", got)
	}
	if ParseTopCPU("no such line") != nil {
		t.Error("garbage parsed to a CPU stat")
	}
}

const vmStatFixture = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                              104520.
Pages active:                            512000.
Pages inactive:                          400000.
Pages speculative:                        30000.
Pages wired down:                        180000.
Pages occupied by compressor:            120000.
`

func TestParseVMStatMemory(t *testing.T) {
	mem := ParseVMStatMemory("34359738368\n", vmStatFixture)
	if mem == nil {
		t.Fatal("got nil memory stat")
	}
	if mem.Total != 34359738368 {
		t.Errorf("total = %d", mem.Total)
	}
	// (512000 active + 180000 wired + 120000 compressor) * 16384 page size.
	if want := int64(812000) * 16384; mem.Used != want {
		t.Errorf("used = %d, want %d", mem.Used, want)
	}
	if ParseVMStatMemory("not a number", vmStatFixture) != nil {
		t.Error("bad memsize parsed to a stat")
	}
	if ParseVMStatMemory("34359738368", "no pages") != nil {
		t.Error("bad vm_stat parsed to a stat")
	}
}

const llamacppMetricsFixture = `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
llamacpp:prompt_tokens_total 4096
llamacpp:tokens_predicted_total 1024
llamacpp:n_decode_total 900
llamacpp:requests_processing 2
llamacpp:requests_deferred 1
llamacpp:request_success_total 17
other:noise 5
`

func TestParseTokenStatsLlamacpp(t *testing.T) {
	tokens := ParseTokenStats(llamacppMetricsFixture, "llamacpp")
	if tokens == nil {
		t.Fatal("got nil token stats")
	}
	want := TokenStats{Running: 3, Counter: 4096 + 1024 + 900,
		PromptTokens: 4096, GenerationTokens: 1024, Requests: 17}
	if *tokens != want {
		t.Errorf("tokens = %+v, want %+v", *tokens, want)
	}
}

const vllmMetricsFixture = `vllm:num_requests_running{model_name="m"} 1
vllm:num_requests_waiting{model_name="m"} 4
vllm:prompt_tokens_total{model_name="m"} 100
vllm:generation_tokens_total{model_name="m"} 50
vllm:request_success_total{finished_reason="stop",model_name="m"} 9
`

func TestParseTokenStatsVllm(t *testing.T) {
	tokens := ParseTokenStats(vllmMetricsFixture, "vllm")
	if tokens == nil {
		t.Fatal("got nil token stats")
	}
	want := TokenStats{Running: 5, Counter: 159, PromptTokens: 100,
		GenerationTokens: 50, Requests: 9}
	if *tokens != want {
		t.Errorf("tokens = %+v, want %+v", *tokens, want)
	}
}

func TestParseTokenStatsUnrecognised(t *testing.T) {
	if ParseTokenStats("random text", "llamacpp") != nil {
		t.Error("garbage parsed to token stats")
	}
	if ParseTokenStats(llamacppMetricsFixture, "omlx") != nil {
		t.Error("unknown engine parsed to token stats")
	}
}

// fixtureRunner returns a Collector.Run that serves canned output per command
// and records what ran.
func fixtureRunner(out map[string]string, ran *[]string) func(context.Context, string, ...string) (string, error) {
	return func(_ context.Context, name string, _ ...string) (string, error) {
		if ran != nil {
			*ran = append(*ran, name)
		}
		s, ok := out[name]
		if !ok {
			return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
		}
		return s, nil
	}
}

func TestCollectorLinux(t *testing.T) {
	c := &Collector{GOOS: "linux", Run: fixtureRunner(map[string]string{
		"nvidia-smi": nvidiaSMIFixture,
		"vmstat":     vmstatFixture,
		"free":       freeFixture,
	}, nil)}
	var stats Stats
	c.System(context.Background(), &stats)
	if len(stats.GPUs) != 2 || stats.CPU == nil || stats.Memory == nil {
		t.Errorf("stats = %+v", stats)
	}
	if len(stats.Errors) != 0 {
		t.Errorf("errors = %v", stats.Errors)
	}
}

func TestCollectorDarwin(t *testing.T) {
	c := &Collector{GOOS: "darwin", Run: fixtureRunner(map[string]string{
		"top":     topFixture,
		"sysctl":  "34359738368\n",
		"vm_stat": vmStatFixture,
	}, nil)}
	var stats Stats
	c.System(context.Background(), &stats)
	if stats.GPUs != nil {
		t.Errorf("darwin reported GPUs: %+v", stats.GPUs)
	}
	if stats.CPU == nil || stats.Memory == nil {
		t.Errorf("stats = %+v", stats)
	}
	if len(stats.Errors) != 0 {
		t.Errorf("errors = %v", stats.Errors)
	}
}

func TestCollectorMissingCommandIsSilent(t *testing.T) {
	c := &Collector{GOOS: "linux", Run: fixtureRunner(map[string]string{
		"vmstat": vmstatFixture,
		"free":   freeFixture,
	}, nil)}
	var stats Stats
	c.System(context.Background(), &stats)
	if stats.GPUs != nil {
		t.Errorf("missing nvidia-smi reported GPUs: %+v", stats.GPUs)
	}
	if len(stats.Errors) != 0 {
		t.Errorf("missing command reported errors: %v", stats.Errors)
	}
	if stats.CPU == nil || stats.Memory == nil {
		t.Error("other stats missing")
	}
}

func TestCollectorFailureIsReported(t *testing.T) {
	c := &Collector{GOOS: "linux", Run: func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "free" {
			return "", errors.New("boom")
		}
		return "", exec.ErrNotFound
	}}
	var stats Stats
	c.System(context.Background(), &stats)
	if len(stats.Errors) != 1 || !strings.Contains(stats.Errors[0], "boom") {
		t.Errorf("errors = %v, want one containing boom", stats.Errors)
	}
}

func TestScrapeTokenStats(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		fmt.Fprint(w, llamacppMetricsFixture)
	}))
	defer srv.Close()

	tokens, err := ScrapeTokenStats(context.Background(),
		ScrapeTarget{BaseURL: srv.URL + "/v1", Engine: "llamacpp", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.PromptTokens != 4096 {
		t.Errorf("tokens = %+v", tokens)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/metrics" {
		t.Errorf("path = %q, want /metrics (the /v1 suffix stripped)", gotPath)
	}
}

func TestScrapeTokenStatsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := ScrapeTokenStats(context.Background(),
		ScrapeTarget{BaseURL: srv.URL, Engine: "llamacpp"}); err == nil {
		t.Error("401 scrape did not error")
	}
	if _, err := ScrapeTokenStats(context.Background(),
		ScrapeTarget{BaseURL: "http://127.0.0.1:1", Engine: "llamacpp"}); err == nil {
		t.Error("unreachable scrape did not error")
	}
}
