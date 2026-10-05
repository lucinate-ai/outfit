// `spinloop status`: what every engine in the target is doing, one row each.
// The target is whatever names one — a registered environment, a fleet file,
// or the fleet file in the working directory — so an environment and a fleet
// holding that same environment read identically. The rendering lives beside
// the fleet's other views in fleet.go; this is the command.

package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/fleet"
)

func statusCmd() *cobra.Command {
	var path, envName, spinloopPath string
	c := &cobra.Command{
		Use:   "status",
		Short: "report every engine's state",
		Long: `reports what each engine in the target is doing: its state, what it
serves, how long since it last did work, and the spinloop version of the
daemon running it. One row per node, queried concurrently, so the command
takes as long as the slowest node rather than all of them added up.

The target is a registered environment (--env), a fleet file (--fleet), or
the fleet.yaml in the working directory. A node that cannot be reached is a
row saying so, not a failure: one unreachable machine never blanks the rest.

An environment's endpoint address is spinloop cloud env, and its retention
deadline spinloop fleet metrics — neither is a column here, because neither
applies to every node.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			if err := applyReadSpinloopEnv(spinloopPath); err != nil {
				return err
			}
			cfg, err := resolveFleetTarget(fleetTarget{envName: envName, fleetPath: path})
			if err != nil {
				return err
			}
			results := cfg.FanOut(context.Background(), fleet.StatusCall)
			renderFleetStatus(os.Stdout, results)
			return nil
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

// cmdStatus runs the command through the tree — the seam the suite calls.
func cmdStatus(args []string) error { return execCmd(statusCmd(), args) }
