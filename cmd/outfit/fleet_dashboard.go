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
	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// fleetDashboardCmd builds the `fleet dashboard` subcommand.
func fleetDashboardCmd() *cobra.Command {
	var path string
	c := &cobra.Command{
		Use:   "dashboard",
		Short: "watch the fleet in an interactive tiled view",
		Long: `An interactive live view of the fleet: a tile per node, each drawing
what the bar format of fleet metrics prints — state, what it serves, the
resource bars, the token counters — repainted on an interval.

The view is read-only apart from two keys: s starts the selected node, x
stops it after a confirmation. j/k or the arrows move the selection, r forces
a refresh, q or Ctrl+C leaves.

A node that cannot be reached is still a tile, showing why, and a node whose
token reference is unresolvable holds its reason for the life of the view.
The board needs a terminal; to stream the metrics into a pipe, use fleet
metrics --watch instead.`,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runFleetDashboard(path)
		},
	}
	fs := c.Flags()
	fs.StringVar(&path, "fleet", "", fleetFileUsage)
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	return c
}

// runFleetDashboard opens the view. The terminal check comes first — before
// the fleet file is even read — so a piped invocation fails the same way
// wherever the fleet file stands: it never half-enters the view.
func runFleetDashboard(path string) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("the dashboard needs an interactive terminal — " +
			"stream the metrics instead with fleet metrics --watch")
	}
	m, err := dashModelFor(path)
	if err != nil {
		return err
	}
	return runDashProgram(m)
}

// dashModelFor turns a fleet file into the board: its nodes, in file order,
// and — for the entries that could not become nodes — the standing outcome
// their tile shows. A broken fleet file is an error here, before any part of
// the view exists.
func dashModelFor(path string) (dashModel, error) {
	cfg, err := fleet.Resolve(path)
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
	return dashModel{fleetPath: cfg.Path, entries: entries, results: results, actions: actions}, nil
}

// runDashProgram runs the view on the alternate screen. Bubble Tea restores
// the terminal on the way out, whatever key got here. The program holds the
// model by pointer: a value model would drop the mutations Init makes (the
// program does not read Init's receiver back), and the first round's answers
// — expensive cloud calls, for remote environments — must not be discarded.
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
