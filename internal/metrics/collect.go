package metrics

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
)

// nvidiaSMIArgs is the nvidia-smi query the GPU parser expects — the same
// query the stats Lambda runs over SSM.
var nvidiaSMIArgs = []string{
	"--query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu",
	"--format=csv,noheader,nounits",
}

// ioregAcceleratorArgs dumps the I/O Kit accelerator services. -w 0 turns line
// wrapping off so each property stays on one line when stdout is a pipe.
var ioregAcceleratorArgs = []string{"-rd1", "-c", "IOAccelerator", "-w", "0"}

// Collector gathers system stats with host commands: nvidia-smi/vmstat/free
// on Linux; sysctl, vm_stat, top and ioreg on macOS, where the GPU is the I/O
// Kit accelerator service (utilisation and name, no memory or temperature). Both
// the command runner and the platform are injectable so tests feed fixture
// output for either platform without running anything.
type Collector struct {
	// Run executes a command and returns its combined output. Nil means run
	// it for real.
	Run func(ctx context.Context, name string, args ...string) (string, error)
	// GOOS overrides the platform; empty means runtime.GOOS.
	GOOS string
}

func (c *Collector) run(ctx context.Context, name string, args ...string) (string, error) {
	if c.Run != nil {
		return c.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func (c *Collector) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

// System collects the host's GPU, CPU and RAM figures into stats, appending
// any collection errors to stats.Errors. A stat whose source is absent on
// this host — the command is not installed, or the platform has no source for
// it at all — is left nil rather than reported as an error, so absence stays
// distinguishable from zero and a partial host still reports the rest.
func (c *Collector) System(ctx context.Context, stats *Stats) {
	if gpus, err := c.gpus(ctx); reportable(err) {
		stats.Errors = append(stats.Errors, "gpu: "+err.Error())
	} else {
		stats.GPUs = gpus
	}
	if cpu, err := c.cpu(ctx); reportable(err) {
		stats.Errors = append(stats.Errors, "cpu: "+err.Error())
	} else {
		stats.CPU = cpu
	}
	if mem, err := c.memory(ctx); reportable(err) {
		stats.Errors = append(stats.Errors, "memory: "+err.Error())
	} else {
		stats.Memory = mem
	}
}

// reportable filters collection failures worth surfacing: a missing command
// is the absent-source case and stays silent, anything else is reported.
func reportable(err error) bool {
	return err != nil && !errors.Is(err, exec.ErrNotFound)
}

func (c *Collector) gpus(ctx context.Context) ([]GpuStat, error) {
	switch c.goos() {
	case "darwin":
		// A host with no accelerator service prints nothing, which parses to
		// no GPUs.
		out, err := c.run(ctx, "ioreg", ioregAcceleratorArgs...)
		if err != nil {
			return nil, err
		}
		return ParseIOAcceleratorGPU(out), nil
	case "linux":
	default:
		return nil, nil
	}
	out, err := c.run(ctx, "nvidia-smi", nvidiaSMIArgs...)
	if err != nil {
		return nil, err
	}
	return ParseGPUStats(out), nil
}

func (c *Collector) cpu(ctx context.Context) (*CpuStat, error) {
	if c.goos() == "darwin" {
		out, err := c.run(ctx, "top", "-l", "1", "-n", "0")
		if err != nil {
			return nil, err
		}
		return ParseTopCPU(out), nil
	}
	out, err := c.run(ctx, "vmstat", "1", "2")
	if err != nil {
		return nil, err
	}
	return ParseVmstatCPU(out), nil
}

func (c *Collector) memory(ctx context.Context) (*MemoryStat, error) {
	if c.goos() == "darwin" {
		memsize, err := c.run(ctx, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return nil, err
		}
		vmStat, err := c.run(ctx, "vm_stat")
		if err != nil {
			return nil, err
		}
		return ParseVMStatMemory(memsize, vmStat), nil
	}
	out, err := c.run(ctx, "free", "-b")
	if err != nil {
		return nil, err
	}
	return ParseFreeMemory(out), nil
}
