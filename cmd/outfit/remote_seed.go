package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemoteSeed dispatches the seed subcommands. Seeds are account-wide — one
// model seeded once serves every environment that names it — so unlike the
// other remote subcommands these do not act on an environment. What to seed
// still comes from an Outfit, resolved exactly as `outfit remote deploy`
// resolves it, so seeding and deploying in the same directory always speak
// about the same model.
func cmdRemoteSeed(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote seed <start|status|ls|stop> [args]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return cmdRemoteSeedStart(rest)
	case "status":
		return cmdRemoteSeedStatus(rest)
	case "ls", "list":
		return cmdRemoteSeedList(rest)
	case "stop":
		return cmdRemoteSeedStop(rest)
	default:
		return fmt.Errorf("unknown seed subcommand %q (expected start, status, ls or stop)", sub)
	}
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

func cmdRemoteSeedStart(args []string) error {
	fs := flag.NewFlagSet("remote seed start", flag.ContinueOnError)
	var (
		force    bool
		revision string
		region   string
	)
	fs.BoolVar(&force, "force", false, "seed weights that are already stored, replacing them")
	fs.StringVar(&revision, "revision", "", "commit or branch to fetch (default: the repository's default branch)")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}

	// Like deploy, this reads the Outfit for what to seed, so it always needs
	// one — there is nothing else that says which model.
	sel, outfitPath, err := readOutfit("outfit remote seed start <file>", outfitArg(fs))
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

func cmdRemoteSeedStatus(args []string) error {
	fs := flag.NewFlagSet("remote seed status", flag.ContinueOnError)
	var region string
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	seedID := fs.Arg(0)
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

func cmdRemoteSeedList(args []string) error {
	fs := flag.NewFlagSet("remote seed ls", flag.ContinueOnError)
	var region string
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}

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

func cmdRemoteSeedStop(args []string) error {
	fs := flag.NewFlagSet("remote seed stop", flag.ContinueOnError)
	var region string
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	seedID := fs.Arg(0)
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
