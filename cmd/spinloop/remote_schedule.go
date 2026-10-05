package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/remote"
)

// remoteScheduleCmd is the schedule subcommand parent. A schedule is a cron
// expression that starts or stops one environment; the control plane stores
// the list and runs it, so schedules fire with no machine of the operator's
// switched on.
func remoteScheduleCmd() *cobra.Command {
	schedule := &cobra.Command{
		Use:   "schedule",
		Short: "start and stop an environment on a cron schedule",
		Long: `starts and stops an environment on cron schedules that the control plane runs, so
they fire whether or not any of your machines is on. Each schedule is a
five-field cron expression (minute hour day-of-month month day-of-week) in a
time zone, with the action start or stop. A scheduled start does what
spinloop remote start does; a scheduled stop pauses the instance, and is
skipped while the instance is kept (spinloop remote keep).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          groupFallback,
	}
	schedule.AddCommand(
		remoteScheduleSetCmd(),
		remoteScheduleShowCmd(),
		remoteScheduleClearCmd(),
	)
	return schedule
}

func remoteScheduleSetCmd() *cobra.Command {
	var (
		envName  string
		starts   []string
		stops    []string
		timezone string
	)
	c := &cobra.Command{
		Use:   "set",
		Short: "replace the environment's schedules",
		Long: `replaces the environment's whole list of schedules with the ones given. Repeat
--start and --stop for more than one. The time zone is an IANA name such as
Europe/London and applies to every schedule given; it defaults to UTC.

  spinloop remote schedule set --start "0 8 * * 1-5" --stop "0 18 * * 1-5" \
      --timezone Europe/London`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runRemoteScheduleSet(envName, starts, stops, timezone)
		},
	}
	fs := c.Flags()
	fs.StringVar(&envName, "env", "", envFlagUsage)
	fs.StringArrayVar(&starts, "start", nil, "cron expression that starts the environment (repeatable)")
	fs.StringArrayVar(&stops, "stop", nil, "cron expression that stops the environment (repeatable)")
	fs.StringVar(&timezone, "timezone", "", "IANA time zone for the schedules (default UTC)")
	compRegister(c, "env", compEnvs)
	c.ValidArgsFunction = noPositionals
	return c
}

func remoteScheduleShowCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:           "show",
		Short:         "list the environment's schedules and their next runs",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runRemoteScheduleShow(envName)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	c.ValidArgsFunction = noPositionals
	return c
}

func remoteScheduleClearCmd() *cobra.Command {
	var envName string
	c := &cobra.Command{
		Use:           "clear",
		Short:         "remove every schedule of the environment",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runRemoteScheduleClear(envName)
		},
	}
	c.Flags().StringVar(&envName, "env", "", envFlagUsage)
	compRegister(c, "env", compEnvs)
	c.ValidArgsFunction = noPositionals
	return c
}

// scheduleListFor builds the list a set sends: every --start, then every
// --stop, each in the one time zone.
func scheduleListFor(starts, stops []string, timezone string) []remote.Schedule {
	list := make([]remote.Schedule, 0, len(starts)+len(stops))
	for _, expr := range starts {
		list = append(list, remote.Schedule{Action: "start", Cron: expr, Timezone: timezone})
	}
	for _, expr := range stops {
		list = append(list, remote.Schedule{Action: "stop", Cron: expr, Timezone: timezone})
	}
	return list
}

// runRemoteScheduleSet is the body of `spinloop remote schedule set`.
func runRemoteScheduleSet(envName string, starts, stops []string, timezone string) error {
	if len(starts) == 0 && len(stops) == 0 {
		return fmt.Errorf("nothing to set: pass --start and/or --stop (use `spinloop remote schedule clear` to remove schedules)")
	}
	cfg, err := resolveRemoteConfig(envName, "")
	if err != nil {
		return err
	}
	list, err := remote.SetSchedules(context.Background(), cfg, scheduleListFor(starts, stops, timezone))
	if err != nil {
		return err
	}
	printSchedules(os.Stdout, list)
	return nil
}

// runRemoteScheduleShow is the body of `spinloop remote schedule show`.
func runRemoteScheduleShow(envName string) error {
	cfg, err := resolveRemoteConfig(envName, "")
	if err != nil {
		return err
	}
	list, err := remote.GetSchedules(context.Background(), cfg)
	if err != nil {
		return err
	}
	printSchedules(os.Stdout, list)
	return nil
}

// runRemoteScheduleClear is the body of `spinloop remote schedule clear`.
func runRemoteScheduleClear(envName string) error {
	cfg, err := resolveRemoteConfig(envName, "")
	if err != nil {
		return err
	}
	list, err := remote.ClearSchedules(context.Background(), cfg)
	if err != nil {
		return err
	}
	printSchedules(os.Stdout, list)
	return nil
}

// printSchedules writes one line per schedule — action, expression, zone —
// then the next time a start and a stop fire, when there is one. An empty list
// says so.
func printSchedules(w io.Writer, list *remote.ScheduleList) {
	if len(list.Schedules) == 0 {
		fmt.Fprintln(w, "no schedules")
		return
	}
	for _, s := range list.Schedules {
		zone := s.Timezone
		if zone == "" {
			zone = "UTC"
		}
		fmt.Fprintf(w, "%-5s  %s  (%s)\n", s.Action, s.Cron, zone)
	}
	if next := formatNextRun(list.Next.Start); next != "" {
		fmt.Fprintf(w, "next start: %s\n", next)
	}
	if next := formatNextRun(list.Next.Stop); next != "" {
		fmt.Fprintf(w, "next stop: %s\n", next)
	}
}

// formatNextRun renders a next-run time as an RFC 3339 instant in UTC, or ""
// for none. A value that does not parse is returned as sent, so a control
// plane reply in another form is shown rather than hidden.
func formatNextRun(raw string) string {
	if raw == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.UTC().Format(time.RFC3339)
}

func cmdRemoteSchedule(args []string) error { return execCmd(remoteScheduleCmd(), args) }
