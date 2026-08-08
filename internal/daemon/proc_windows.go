//go:build windows

package daemon

import (
	"os"
	"os/exec"
)

// setProcAttr is a no-op on Windows, which has no process groups to join.
func setProcAttr(cmd *exec.Cmd) {}

// terminate has no polite cross-process signal on Windows; the engine is
// killed outright and the grace window simply passes unused.
func terminate(p *os.Process) {
	p.Kill()
}

// kill hard-stops the engine.
func kill(p *os.Process) {
	p.Kill()
}
