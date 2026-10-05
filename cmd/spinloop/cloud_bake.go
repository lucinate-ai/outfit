package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
)

// defaultBakeRunners is what `spinloop cloud bake` bakes when no runners are
// named, and the full accepted set — the two engines an environment can run.
var defaultBakeRunners = []string{"llamacpp", "vllm"}

// isBakeRunner reports whether r is a runner bake accepts.
func isBakeRunner(r string) bool {
	for _, v := range defaultBakeRunners {
		if r == v {
			return true
		}
	}
	return false
}

// bakeRunnerSlot is the `bake [runner...]` completion: the accepted runners
// not already on the line.
func bakeRunnerSlot(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	taken := map[string]bool{}
	for _, a := range args {
		taken[a] = true
	}
	var out []string
	for _, r := range defaultBakeRunners {
		if !taken[r] {
			out = append(out, r)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// cloudBakeCmd starts an AMI bake for each named runner. It drives the same
// CDK project bootstrap deploys but deploys nothing — the control plane (and
// its Image Builder pipelines) must already exist, so a missing one fails fast
// naming bootstrap rather than deploying implicitly. The wait is the default:
// the step after a bake is `spinloop cloud deploy`, which needs the AMI.
func cloudBakeCmd() *cobra.Command {
	var (
		noWait bool
		ref    string
		dir    string
		region string
		pkgMgr string
	)
	c := &cobra.Command{
		Use:   "bake [runner...]",
		Short: "bake the runner AMIs an environment runs from",
		Long: `starts an AMI bake for each named runner (llamacpp and vllm when
none are named) and waits until the AMI(s) are available (~20-40 min). It
drives the same CDK project bootstrap deploys, deploys nothing — the control
plane must already exist — and --no-wait returns as soon as the bakes are
queued, reporting how to check on them.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: bakeRunnerSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runCloudBake(args, noWait, ref, dir, region, pkgMgr)
		},
	}
	fs := c.Flags()
	fs.BoolVar(&noWait, "no-wait", false, "return as soon as the bakes are queued, rather than waiting for the AMI(s)")
	fs.StringVar(&ref, "ref", "", "git ref of remote/ to download (default: matches this binary)")
	fs.StringVar(&dir, "dir", "", "where to find the downloaded remote/ sources")
	fs.StringVar(&region, "region", "", "AWS region of the control plane (default: AWS_REGION or us-east-1)")
	fs.StringVar(&pkgMgr, "package-manager", "", "package manager to use: pnpm or npm (default: auto-detect, preferring pnpm)")
	compRegister(c, "dir", compFiles)
	return c
}

// runCloudBake is the body of `spinloop cloud bake`.
func runCloudBake(args []string, noWait bool, ref, dir, region string, pkgMgr string) error {
	runners := defaultBakeRunners
	if len(args) > 0 {
		runners = args
		for _, r := range args {
			if !isBakeRunner(r) {
				return fmt.Errorf("unknown runner %q — bake accepts llamacpp and vllm", r)
			}
		}
	}

	pmName, pmPinned, err := resolvePackageManagerName(pkgMgr)
	if err != nil {
		return err
	}

	loc, err := resolveSourceLocation(ref, dir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	pm, err := preflightFn(pmName, pmPinned)
	if err != nil {
		return err
	}

	resolvedRegion := resolveRegion(region)
	cfg, err := loadCreds(ctx, resolvedRegion)
	if err != nil {
		return fmt.Errorf(
			"resolving AWS credentials: %w (configure env credentials, a profile or an SSO session)", err)
	}

	deployed, err := stackDeployedFn(ctx, cfg, controlPlaneStackName)
	if err != nil {
		return err
	}
	if !deployed {
		return fmt.Errorf(
			"the control plane is not deployed in %s — run `spinloop cloud bootstrap` first", resolvedRegion)
	}

	if err := downloadFn(ctx, loc.ref, loc.dir); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUsing %s to run the CDK project.\n", pm.name)
	run := func(name string, argv ...string) error {
		return runStep(ctx, name, argv, loc.dir)
	}
	if !dirExists(filepath.Join(loc.dir, "node_modules")) {
		if err := run("install", pm.install...); err != nil {
			return err
		}
	}
	for _, r := range runners {
		if err := run("bake "+r, pm.script("bake", r)...); err != nil {
			return err
		}
	}

	if noWait {
		fmt.Fprintln(os.Stderr, "\nThe AMI bake(s) run in the background (~20-40 min) — the commands above say how to check on them.")
	} else {
		fmt.Fprintln(os.Stderr, "\nWaiting for the AMI bake(s) to finish (this can take 20-40 minutes)...")
		if err := waitForBake(ctx, cfg, runners); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "AMI(s) available.")
	}

	return pruneSourceCaches(loc)
}

func cmdCloudBake(args []string) error { return execCmd(cloudBakeCmd(), args) }
