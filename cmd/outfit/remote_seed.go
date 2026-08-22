package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lucinate-ai/outfit/internal/remote"
	"github.com/spf13/cobra"
)

// remoteSeedCmd is the seed subcommand parent. Seeds are account-wide — one
// model seeded once serves every environment that names it — so unlike the
// other remote subcommands these do not act on an environment. What to seed
// still comes from an Outfit, resolved exactly as `outfit remote deploy`
// resolves it, so seeding and deploying in the same directory always speak
// about the same model.
func remoteSeedCmd() *cobra.Command {
	seed := &cobra.Command{
		Use:                "seed",
		Short:              "start, watch and stop model weight seeds",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			if len(args) == 0 {
				return fmt.Errorf("usage: outfit remote seed <start|status|ls|stop> [args]")
			}
			return fmt.Errorf("unknown seed subcommand %q (expected start, status, ls or stop)", args[0])
		},
	}
	seed.AddCommand(
		remoteSeedStartCmd(),
		remoteSeedStatusCmd(),
		remoteSeedListCmd(),
		remoteSeedStopCmd(),
	)
	return seed
}

// seedControlConfig finds the control plane. The seed endpoints are shared
// across environments and the stack outputs carry them, so this is the same
// discovery `deploy` performs.
func seedControlConfig(ctx context.Context, region string) (remote.Config, error) {
	awsCfg, err := remote.LoadAWSConfig(ctx, resolveRegion(region))
	if err != nil {
		return remote.Config{}, err
	}
	layer, err := deployDiscoverFn(ctx, awsCfg, controlPlaneStackName)
	if err != nil {
		return remote.Config{}, err
	}
	return layer.Config, nil
}

func remoteSeedStartCmd() *cobra.Command {
	var (
		force    bool
		revision string
		region   string
	)
	c := &cobra.Command{
		Use:               "start",
		Short:             "seed a model's weights into S3",
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: aliasSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runRemoteSeedStart(args, force, revision, region)
		},
	}
	fs := c.Flags()
	fs.BoolVar(&force, "force", false, "seed weights that are already stored, replacing them")
	fs.StringVar(&revision, "revision", "", "commit or branch to fetch (default: the repository's default branch)")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	return c
}

// runRemoteSeedStart is the body of `outfit remote seed start`.
func runRemoteSeedStart(args []string, force bool, revision, region string) error {
	// Like deploy, this reads the Outfit for what to seed, so it always needs
	// one — there is nothing else that says which model.
	sel, outfitPath, err := readOutfit("outfit remote seed start <file>", outfitArg(args))
	if err != nil {
		return err
	}
	if err := applyOutfitEnv(sel, filepath.Dir(outfitPath)); err != nil {
		return err
	}
	dc, err := deployConfigFor(sel, outfitPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cfg, err := seedControlConfig(ctx, region)
	if err != nil {
		return err
	}
	started, err := remote.SeedStart(ctx, cfg, remote.SeedRequest{
		Runner:   dc.Runner,
		ModelID:  dc.ModelID,
		Quant:    dc.Quant,
		Revision: revision,
		Force:    force,
	})
	if err != nil {
		return err
	}

	switch {
	case started.AlreadySeeded:
		fmt.Printf("%s is already seeded — pass --force to seed it again.\n", dc.ModelID)
	case started.Joined:
		// Not a fresh start; say so rather than letting a repeat look like one.
		fmt.Printf("joined the seed already running for %s (%s).\n", dc.ModelID, started.SeedID)
	default:
		fmt.Printf("seeding %s\n", dc.ModelID)
		fmt.Printf("  seed:     %s\n", started.SeedID)
		fmt.Printf("  instance: %s\n", started.InstanceID)
		fmt.Printf("  weights:  %s\n", started.WeightsPrefix)
	}
	if started.SeedID != "" && !started.AlreadySeeded {
		fmt.Printf("\nFollow it:\n  outfit remote seed status %s\n", started.SeedID)
	}
	return nil
}

func remoteSeedStatusCmd() *cobra.Command {
	var region string
	c := &cobra.Command{
		Use:           "status <seed-id>",
		Short:         "report a seed's progress",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runRemoteSeedStatus(args, region)
		},
	}
	c.Flags().StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	return c
}

// runRemoteSeedStatus is the body of `outfit remote seed status`.
func runRemoteSeedStatus(args []string, region string) error {
	seedID := outfitArg(args)
	if seedID == "" {
		return fmt.Errorf("usage: outfit remote seed status <seed-id> (list them with `outfit remote seed ls`)")
	}

	ctx := context.Background()
	cfg, err := seedControlConfig(ctx, region)
	if err != nil {
		return err
	}
	status, err := remote.SeedGet(ctx, cfg, seedID)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", status.SeedID)
	fmt.Printf("  state:    %s\n", status.State)
	if status.ModelID != "" {
		fmt.Printf("  model:    %s\n", status.ModelID)
	}
	if status.Revision != "" {
		fmt.Printf("  revision: %s\n", status.Revision)
	}
	if status.FilesTotal > 0 {
		fmt.Printf("  progress: %.1f%% (%d/%d files", status.Progress, status.FilesDone, status.FilesTotal)
		if status.BytesTotal > 0 {
			fmt.Printf(", %s of %s", humanBytes(status.BytesDone), humanBytes(status.BytesTotal))
		}
		fmt.Println(")")
	}
	if status.CurrentFile != "" && !isTerminalSeedState(status.State) {
		fmt.Printf("  file:     %s\n", status.CurrentFile)
	}
	if status.DurationSeconds > 0 {
		fmt.Printf("  took:     %ds\n", status.DurationSeconds)
	}
	if status.LastReportAt != "" {
		fmt.Printf("  reported: %s\n", status.LastReportAt)
	}
	if status.Message != "" {
		fmt.Printf("  message:  %s\n", status.Message)
	}
	if status.Err != "" {
		fmt.Printf("  error:    %s\n", status.Err)
	}
	return nil
}

func isTerminalSeedState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "stopped"
}

func remoteSeedListCmd() *cobra.Command {
	var region string
	c := &cobra.Command{
		Use:           "ls",
		Aliases:       []string{"list"},
		Short:         "list seeds",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runRemoteSeedList(region)
		},
	}
	c.Flags().StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	return c
}

// runRemoteSeedList is the body of `outfit remote seed ls`.
func runRemoteSeedList(region string) error {
	ctx := context.Background()
	cfg, err := seedControlConfig(ctx, region)
	if err != nil {
		return err
	}
	seeds, err := remote.SeedList(ctx, cfg)
	if err != nil {
		return err
	}
	// Stated plainly: "none running" must be distinguishable from a command
	// that failed quietly.
	if len(seeds) == 0 {
		fmt.Println("No seeds are running.")
		return nil
	}
	fmt.Printf("%-44s  %-12s  %-8s  %s\n", "SEED", "STATE", "AGE", "MODEL")
	for _, s := range seeds {
		fmt.Printf("%-44s  %-12s  %-8s  %s\n", s.SeedID, s.State, humanAge(s.AgeSeconds), s.ModelID)
	}
	return nil
}

func remoteSeedStopCmd() *cobra.Command {
	var region string
	c := &cobra.Command{
		Use:           "stop <seed-id>",
		Short:         "stop a running seed",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runRemoteSeedStop(args, region)
		},
	}
	c.Flags().StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	return c
}

// runRemoteSeedStop is the body of `outfit remote seed stop`.
func runRemoteSeedStop(args []string, region string) error {
	seedID := outfitArg(args)
	if seedID == "" {
		return fmt.Errorf("usage: outfit remote seed stop <seed-id>")
	}

	ctx := context.Background()
	cfg, err := seedControlConfig(ctx, region)
	if err != nil {
		return err
	}
	stopped, err := remote.SeedStop(ctx, cfg, seedID)
	if err != nil {
		return err
	}
	if !stopped.Stopped {
		// Not an error: stopping twice is safe.
		fmt.Printf("%s is not running.\n", seedID)
		return nil
	}
	fmt.Printf("stopped %s (%s)\n", seedID, strings.Join(stopped.InstanceIDs, ", "))
	return nil
}

// humanBytes renders a byte count for a progress line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// humanAge renders how long a seed has been running.
func humanAge(seconds int) string {
	switch {
	case seconds <= 0:
		return "-"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	}
}

func cmdRemoteSeed(args []string) error { return execCmd(remoteSeedCmd(), args) }
