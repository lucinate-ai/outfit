// `spinloop dashboard`: the interactive board over whatever the target names —
// a fleet, or a single registered environment, which opens as a board of one.
// The model and the renderers live in dashboard_model.go and
// dashboard_render.go, and the program in fleet_dashboard.go; this is the
// command.

package main

import (
	"github.com/spf13/cobra"
)

func dashboardCmd() *cobra.Command {
	var path, envName, spinloopPath string
	c := &cobra.Command{
		Use:   "dashboard",
		Short: "watch the engines in an interactive tiled view",
		Long: `An interactive live view of the target: a tile per node, each drawing
what the gauge format of fleet metrics prints — state, what it serves, the
resource gauges, the token counters — repainted on an interval. A single
registered environment (--env) opens as a board of one.

The view is read-only apart from four keys: s starts the selected node, k
keeps a cloud environment for a duration you type — it asks how long,
pre-filled with 4h, and reports the deadline the control plane set when the
keep is done — a abandons a start still in flight on it (the wait ends, the
node is free again — a wake the cloud is carrying goes on), x stops it after
a confirmation. The arrow keys move the selection, r forces a refresh, q or
Ctrl+C leaves. The keep key shows only for a node that can be kept — a cloud
environment — and a kept environment's tile and detail view carry its
deadline beside the last-active line, whatever the engine's state.

A node that cannot be reached is still a tile, showing why, and a node whose
token reference is unresolvable holds its reason for the life of the view.
The board needs a terminal; to stream the metrics into a pipe, use fleet
metrics --watch instead.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if err := applyReadSpinloopEnv(spinloopPath); err != nil {
				return err
			}
			return runFleetDashboard(fleetTarget{envName: envName, fleetPath: path})
		},
	}
	fs := c.Flags()
	fs.StringVarP(&path, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	registerSpinloopEnvFlag(fs, &spinloopPath)
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// cmdDashboard runs the command through the tree — the seam the suite calls.
func cmdDashboard(args []string) error { return execCmd(dashboardCmd(), args) }
