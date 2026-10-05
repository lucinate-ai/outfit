// `spinloop logs`: what every engine in the target has said. The target is
// whatever names one — a registered environment, a fleet file, or the fleet
// file in the working directory. Reading and following live in fleet_logs.go;
// this is the command.

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/fleet"
)

func logsCmd() *cobra.Command {
	var (
		path         string
		envName      string
		follow       bool
		limit        int
		format       string
		source       string
		since        time.Duration
		instance     string
		spinloopPath string
	)
	const followUsage = "keep printing new output as it arrives"
	c := &cobra.Command{
		Use:   "logs",
		Short: "read the engines' output",
		Long: `reads what each engine in the target has said, through whatever the node
answers with — a daemon's log file, or a cloud environment's log store.
Naming a node reads only that one. Nodes are read concurrently.

The target is a registered environment (--env), a fleet file (--fleet), or
the fleet.yaml in the working directory. --fleet has no -f here: that is
--follow's short form, and a flag cannot carry two meanings on one command
line.

--source, --since and --instance narrow a log that can be queried. Only a
cloud environment's can — a daemon's log is one file on one machine, where
none of the three narrows anything — so a node that cannot be narrowed is
read as it would be without them.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			if err := applyReadSpinloopEnv(spinloopPath); err != nil {
				return err
			}
			q := fleet.LogQuery{Source: source, Since: since, Instance: instance}
			return runLogs(fleetTarget{envName: envName, fleetPath: path}, q, follow, limit, format, args)
		},
	}
	fs := c.Flags()
	// --fleet takes no short form here: -f is already --follow, as it is on
	// every other surface that follows something.
	fs.StringVar(&path, "fleet", "", fleetFileUsage)
	fs.StringVar(&envName, "env", "", envFlagTargetUsage)
	registerSpinloopEnvFlag(fs, &spinloopPath)
	fs.BoolVarP(&follow, "follow", "f", false, followUsage)
	fs.IntVar(&limit, "limit", 200, "lines of backlog to print per node")
	fs.StringVar(&format, "format", "text", "output format: text (default) or json")
	fs.StringVar(&source, "source", "", "which log to read on a node that has more than one: engine (default), boot or all")
	fs.DurationVar(&since, "since", time.Hour, "how far back to read on a node whose log can be queried (30m, 2h)")
	fs.StringVar(&instance, "instance", "", "restrict to one instance id, on a node whose log holds more than one")
	c.ValidArgsFunction = noPositionals
	compRegister(c, "fleet", compFiles)
	compRegister(c, "env", compEnvs)
	return c
}

// cmdLogs runs the command through the tree — the seam the suite calls.
func cmdLogs(args []string) error { return execCmd(logsCmd(), args) }

// validateLogQuery rejects the query values that cannot mean anything, before
// any node is contacted.
func validateLogQuery(q fleet.LogQuery, format string, limit int) error {
	switch q.Source {
	case "", cloud.LogSourceEngine, cloud.LogSourceBoot, cloud.LogSourceAll:
	default:
		return fmt.Errorf("--source must be engine, boot or all, got %q", q.Source)
	}
	if q.Since < 0 {
		return fmt.Errorf("--since must be positive, got %s", q.Since)
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("--format must be \"text\" or \"json\", got %q", format)
	}
	if limit <= 0 {
		return fmt.Errorf("--limit must be positive, got %d", limit)
	}
	return nil
}
