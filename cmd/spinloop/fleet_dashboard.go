// `fleet dashboard`: the command layer — flag wiring, the terminal check,
// building the model from the fleet file, and running the program. The model
// and renderers live in dashboard_model.go and dashboard_render.go, so the
// screen logic is tested without the command and the command without the
// screen.

package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"golang.org/x/term"
)

// runFleetDashboard opens the view. The terminal check comes first — before
// the fleet file is even read — so a piped invocation fails the same way
// wherever the fleet file stands: it never half-enters the view.
func runFleetDashboard(target fleetTarget) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("the dashboard needs an interactive terminal — " +
			"stream the metrics instead with fleet metrics --watch")
	}
	m, err := dashModelFor(target)
	if err != nil {
		return err
	}
	return runDashProgram(m)
}

// dashModelFor turns a fleet file into the board: its nodes, in file order,
// and — for the entries that could not become nodes — the standing outcome
// their tile shows. A broken fleet file is an error here, before any part of
// the view exists.
func dashModelFor(target fleetTarget) (dashModel, error) {
	cfg, err := resolveFleetTarget(target)
	if err != nil {
		return dashModel{}, err
	}
	entries := make([]dashEntry, 0, len(cfg.Nodes))
	results := make([]fleet.NodeResult, 0, len(cfg.Nodes))
	actions := make([]dashAction, 0, len(cfg.Nodes))
	for _, entry := range cfg.Nodes {
		e := dashEntry{name: entry.Name, kind: entry.Kind}
		node, err := cfg.NewNode(entry)
		if err != nil {
			e.standing = fleet.NodeResult{
				Name:    entry.Name,
				Outcome: fleet.OutcomeConfigError,
				Err:     err,
			}
		} else {
			e.node = node
		}
		entries = append(entries, e)
		results = append(results, e.standing)
		actions = append(actions, dashAction{})
	}
	return dashModel{fleetPath: describeTarget(cfg), entries: entries, results: results, actions: actions, gauge: true}, nil
}

// runDashProgram runs the view on the alternate screen. Bubble Tea restores
// the terminal on the way out, whatever key got here. The program holds the
// model by pointer: a value model would drop the mutations Init makes (the
// program does not read Init's receiver back), and the first round's answers
// — expensive cloud calls, for cloud environments — must not be discarded.
func runDashProgram(m dashModel) error {
	prog := tea.NewProgram(&m, tea.WithAltScreen())
	// The calls behind in-flight actions report their status lines from
	// their own goroutines: the program's Send is safe to call from any
	// goroutine and is a no-op once the program has left, so a start that
	// outlives the view reports into nothing rather than panicking.
	m.send = prog.Send
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}
