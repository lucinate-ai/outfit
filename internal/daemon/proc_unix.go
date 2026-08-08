//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcAttr puts the engine in its own process group, so stop signals reach
// the engine and anything it spawned rather than the daemon's own group.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate sends the polite stop to the engine's process group.
func terminate(p *os.Process) {
	if err := syscall.Kill(-p.Pid, syscall.SIGTERM); err != nil {
		p.Signal(syscall.SIGTERM)
	}
}

// kill hard-stops the engine's process group after the grace window.
func kill(p *os.Process) {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		p.Kill()
	}
}
