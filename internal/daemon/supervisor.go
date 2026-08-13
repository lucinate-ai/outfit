// Package daemon runs an engine under supervision and exposes the control API
// that observes and drives it — the long-lived half of `outfit serve
// --daemon`. The supervisor deliberately does not restart a crashed engine
// (it reports the crash and waits for an explicit start) and holds at most
// one engine at a time.
package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State is the supervised engine's lifecycle state.
type State string

const (
	// StateIdle means nothing has been started yet.
	StateIdle State = "idle"
	// StateRunning means the engine process is alive.
	StateRunning State = "running"
	// StateStopped means the engine was stopped on request or exited cleanly.
	StateStopped State = "stopped"
	// StateCrashed means the engine exited unprompted with a failure.
	StateCrashed State = "crashed"
)

// DefaultGrace is how long Stop waits after the polite signal before killing.
const DefaultGrace = 10 * time.Second

// Supervisor runs at most one engine process: started detached into its own
// process group, its output captured, its exit recorded rather than acted on.
type Supervisor struct {
	// Grace is the SIGTERM-to-SIGKILL window; zero means DefaultGrace.
	Grace time.Duration
	// LogPath receives the engine's stdout+stderr. Empty forwards both to
	// this process's own stdio — the foreground `serve --api` case.
	LogPath string
	// Logger receives the lifecycle records: started, stopped, exited. Nil
	// discards. Note what this is not: the engine's own output, which goes to
	// LogPath and is served over /v1/logs.
	Logger *slog.Logger

	mu       sync.Mutex
	state    State
	cmd      *exec.Cmd
	argv     []string
	started  time.Time
	stopping bool
	done     chan struct{}
	waitErr  error
}

// NewSupervisor returns an idle supervisor logging to logPath.
func NewSupervisor(logPath string) *Supervisor {
	return &Supervisor{LogPath: logPath, state: StateIdle}
}

// log reads the supervisor's logger, defaulting to discarding.
func (s *Supervisor) log() *slog.Logger {
	return loggerOr(s.Logger)
}

// Start launches argv as the supervised engine. It fails when an engine is
// already running — one engine per daemon — naming the one that is.
func (s *Supervisor) Start(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("start: empty engine command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateRunning {
		return fmt.Errorf("an engine is already running (%s); stop it first", s.argv[0])
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	var logFile *os.File
	if s.LogPath == "" {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return err
	}

	s.cmd = cmd
	s.argv = argv
	s.state = StateRunning
	s.started = time.Now()
	s.stopping = false
	done := make(chan struct{})
	s.done = done

	// The binary and the argument count at info; the whole command only at
	// debug. A command assembled from a pushed deploy config can carry a
	// literal --api-key, and info is where a service manager's journal picks
	// records up — the full argv is available to whoever asks for it.
	s.log().Info("engine started",
		slog.String("engine", argv[0]),
		slog.Int("args", len(argv)-1),
		slog.Int("pid", cmd.Process.Pid))
	s.log().Debug("engine command", slog.String("command", strings.Join(argv, " ")))

	go func() {
		err := cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		s.mu.Lock()
		s.waitErr = err
		switch {
		case s.stopping, err == nil:
			s.state = StateStopped
		default:
			// An unprompted failure exit: report it, never restart it.
			s.state = StateCrashed
		}
		state := s.state
		s.mu.Unlock()
		// Recorded here, in the goroutine that already classifies the exit, so
		// a crash is logged the moment it happens rather than whenever someone
		// next polls /v1/status. A node that dies at 03:00 leaves a timestamp.
		if state == StateCrashed {
			s.log().Error("engine crashed",
				slog.String("engine", argv[0]), slog.String("error", err.Error()))
		} else {
			s.log().Info("engine exited", slog.String("engine", argv[0]))
		}
		close(done)
	}()
	return nil
}

// Stop terminates a running engine: the polite group signal first, the hard
// kill after the grace window. Stopping when nothing runs is a no-op — stop
// is idempotent.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	proc := s.cmd.Process
	done := s.done
	grace := s.Grace
	engine := s.argv[0]
	s.mu.Unlock()
	if grace == 0 {
		grace = DefaultGrace
	}

	s.log().Info("stopping engine", slog.String("engine", engine))
	terminate(proc)
	select {
	case <-done:
	case <-time.After(grace):
		// Worth a record of its own: an engine that ignores SIGTERM is a
		// property of that engine, and the grace window is where a slow
		// shutdown turns into a kill.
		s.log().Warn("engine did not exit within the grace period; killing",
			slog.String("engine", engine), slog.Duration("grace", grace))
		kill(proc)
		<-done
	}
	return nil
}

// Wait blocks until the current engine exits, returning its exit error. It
// returns immediately when no engine is running.
func (s *Supervisor) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

// Status reports the engine's state, the command it runs (empty when never
// started), and its uptime in whole seconds (zero unless running).
func (s *Supervisor) Status() (state State, engine string, uptimeSeconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.argv) > 0 {
		engine = s.argv[0]
	}
	if s.state == StateRunning {
		uptimeSeconds = int(time.Since(s.started) / time.Second)
	}
	return s.state, engine, uptimeSeconds
}
