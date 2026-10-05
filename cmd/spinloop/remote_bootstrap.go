package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/remote"
)

// controlPlaneStackName is the CloudFormation stack holding the account-level
// control plane that bootstrap deploys and `spinloop remote deploy` discovers. It
// is also the namespace the control plane's log groups sit under, so the name
// has one home in internal/remote rather than one per caller.
const controlPlaneStackName = remote.ControlPlaneStackName

// Seams: package variables so tests drive the flow without AWS, a network, or
// spawning a package manager/cdk. Shared by bootstrap and bake.
type stepRunner func(ctx context.Context, name string, argv []string, workDir string) error

var (
	runStep         stepRunner = execStep
	downloadFn                 = remote.DownloadRemote
	accountFn                  = remote.CallerIdentity
	stackDeployedFn            = remote.ControlPlaneStackDeployed
	bakedFn                    = remote.BakedRunners
	preflightFn                = checkNodeAndPackageManager
)

// controlPlaneVersionEnv is read by the CDK app (remote/lib/config.ts) to stamp
// the control plane with the version of the CLI that deploys it.
const controlPlaneVersionEnv = "SPINLOOP_CONTROL_PLANE_VERSION"

// packageManagerEnv pins the Node package manager bootstrap drives the CDK
// project with, when the --package-manager flag is not given.
const packageManagerEnv = "SPINLOOP_REMOTE_PACKAGE_MANAGER"

// packageManager shapes the argv for the two things bootstrap asks of a Node
// package manager: installing dependencies and running a package.json script.
// Both invoke scripts via `run` so a script name never collides with a builtin
// subcommand (pnpm deploy is pnpm's own workspace command, not the deploy
// script). pnpm forwards script arguments directly (pnpm run bake llamacpp);
// npm needs a `--` separator before them (npm run bake -- llamacpp).
type packageManager struct {
	name    string
	install []string
}

var (
	pnpmManager = packageManager{name: "pnpm", install: []string{"pnpm", "install"}}
	npmManager  = packageManager{name: "npm", install: []string{"npm", "install"}}
)

// script returns the argv that runs the named package.json script with args.
func (pm packageManager) script(name string, args ...string) []string {
	if pm.name == "npm" {
		argv := []string{"npm", "run", name}
		if len(args) > 0 {
			argv = append(argv, "--")
			argv = append(argv, args...)
		}
		return argv
	}
	return append([]string{"pnpm", "run", name}, args...)
}

// managerByName maps a validated name to its packageManager, defaulting to the
// preferred pnpm.
func managerByName(name string) packageManager {
	if name == "npm" {
		return npmManager
	}
	return pnpmManager
}

// detectPackageManager picks a manager by PATH, preferring pnpm. When neither is
// present it returns pnpm with ok=false so callers have a name to display.
func detectPackageManager() (packageManager, bool) {
	if _, err := exec.LookPath("pnpm"); err == nil {
		return pnpmManager, true
	}
	if _, err := exec.LookPath("npm"); err == nil {
		return npmManager, true
	}
	return pnpmManager, false
}

// selectPackageManager resolves the manager to use and whether it is on PATH. A
// pinned name selects that manager; an empty name auto-detects.
func selectPackageManager(name string) (packageManager, bool) {
	if name == "" {
		return detectPackageManager()
	}
	pm := managerByName(name)
	_, err := exec.LookPath(pm.name)
	return pm, err == nil
}

// validatePackageManagerName accepts only the two managers bootstrap supports.
func validatePackageManagerName(name string) error {
	switch name {
	case "pnpm", "npm":
		return nil
	default:
		return fmt.Errorf("unknown package manager %q — use pnpm or npm", name)
	}
}

// resolvePackageManagerName applies the override precedence — the flag, then the
// SPINLOOP_REMOTE_PACKAGE_MANAGER env var — returning the pinned name (or "" to
// auto-detect) and whether the choice was pinned. An unrecognised value errors.
func resolvePackageManagerName(flagVal string) (name string, pinned bool, err error) {
	if flagVal != "" {
		if err := validatePackageManagerName(flagVal); err != nil {
			return "", false, fmt.Errorf("--package-manager: %w", err)
		}
		return flagVal, true, nil
	}
	// The env leg of the precedence runs through the CLI's one Viper, like
	// every other SPINLOOP_ read the CLI owns; the flag keeps its say first.
	if env := cliViper.GetString("remote_package_manager"); env != "" {
		if err := validatePackageManagerName(env); err != nil {
			return "", false, fmt.Errorf("%s: %w", packageManagerEnv, err)
		}
		return env, true, nil
	}
	return "", false, nil
}

// remoteBootstrapCmd deploys the account-level control plane once —
// analogous to `cdk bootstrap` — by downloading the remote/ CDK project and
// driving its control-plane deploy. It creates no EIP, instance, or environment;
// those come from `spinloop remote deploy`. It starts no AMI bake either —
// `spinloop remote bake` is the separate step after it.
func remoteBootstrapCmd() *cobra.Command {
	var (
		hfToken   string
		ref       string
		dir       string
		region    string
		dryRun    bool
		assumeYes bool
		pkgMgr    string
	)
	c := &cobra.Command{
		Use:   "bootstrap",
		Short: "set up the once-per-account control plane",
		Long: `does the once-per-account control-plane setup (Image Builder, the
lifecycle Lambdas, shared bucket/roles/VPC) with a consent gate. It bakes no
AMIs — spinloop remote bake is the next step after it.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: noPositionals,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runRemoteBootstrap(hfToken, ref, dir, region, dryRun, assumeYes, pkgMgr)
		},
	}
	fs := c.Flags()
	fs.StringVar(&hfToken, "hf-token", "", "Hugging Face token for the shared secret (optional)")
	fs.StringVar(&ref, "ref", "", "git ref of remote/ to download (default: matches this binary)")
	fs.StringVar(&dir, "dir", "", "where to place the downloaded remote/ sources")
	fs.StringVar(&region, "region", "", "AWS region (default: AWS_REGION or us-east-1)")
	fs.BoolVarP(&dryRun, "dry-run", "n", false, "print the plan and exit without doing anything")
	fs.BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	fs.StringVar(&pkgMgr, "package-manager", "", "package manager to use: pnpm or npm (default: auto-detect, preferring pnpm)")
	fs.SetInterspersed(false)
	compRegister(c, "dir", compFiles)
	return c
}

// runRemoteBootstrap is the body of `spinloop remote bootstrap`.
func runRemoteBootstrap(hfToken, ref, dir, region string, dryRun, assumeYes bool, pkgMgr string) error {
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

	resolvedRegion := resolveRegion(region)

	// AWS is best-effort for the plan; it is hard-required for a real run.
	account := "unknown"
	alreadyBootstrapped := false
	cfg, credsErr := loadCreds(ctx, resolvedRegion)
	if credsErr == nil {
		if acct, err := accountFn(ctx, cfg); err == nil {
			account = acct
		}
		if dep, err := stackDeployedFn(ctx, cfg, controlPlaneStackName); err == nil {
			alreadyBootstrapped = dep
		}
	}

	var pm packageManager
	if !dryRun {
		selected, err := preflightFn(pmName, pmPinned)
		if err != nil {
			return err
		}
		pm = selected
		if credsErr != nil {
			return fmt.Errorf(
				"resolving AWS credentials: %w (configure env credentials, a profile or an SSO session)", credsErr)
		}
	} else {
		pm, _ = selectPackageManager(pmName)
	}

	renderBootstrapPlan(account, resolvedRegion, loc.ref, loc.dir, alreadyBootstrapped, pm)

	if dryRun {
		return nil
	}
	if !assumeYes && !confirmProceed() {
		fmt.Fprintln(os.Stderr, "Aborted; nothing was created.")
		return nil
	}

	if err := downloadFn(ctx, loc.ref, loc.dir); err != nil {
		return err
	}
	if hfToken != "" {
		if err := upsertEnvVar(filepath.Join(loc.dir, ".env"), "HF_TOKEN", hfToken); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "\nUsing %s to run the CDK project.\n", pm.name)
	if err := runBootstrapSequence(ctx, loc.dir, pm); err != nil {
		return err
	}

	fmt.Println("\nThe account is bootstrapped. Before an environment can start, its AMI needs baking:")
	fmt.Println("  spinloop remote bake <runner>   # bakes the AMI(s) an environment runs from; waits until available")
	fmt.Println("  spinloop remote deploy <env>    # names an environment; discovers this control plane")

	return pruneSourceCaches(loc)
}

// runBootstrapSequence runs the package-manager/cdk steps in the sources
// directory with the resolved manager.
func runBootstrapSequence(ctx context.Context, cdkDir string, pm packageManager) error {
	run := func(name string, argv ...string) error {
		return runStep(ctx, name, argv, cdkDir)
	}
	if !dirExists(filepath.Join(cdkDir, "node_modules")) {
		if err := run("install", pm.install...); err != nil {
			return err
		}
	}
	if err := run("cdk bootstrap", pm.script("cdk", "bootstrap")...); err != nil {
		return err
	}
	if err := run("deploy:image", pm.script("deploy:image")...); err != nil {
		return err
	}
	return run("deploy", pm.script("deploy")...)
}

// execStep runs one external command in workDir, streaming its stdio,
// mirroring serve.go's exec pattern. Ctrl-C propagates via the context.
func execStep(ctx context.Context, name string, argv []string, workDir string) error {
	fmt.Fprintf(os.Stderr, "\n$ %s\n", strings.Join(argv, " "))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
	// The control plane reports this version in every response, so a CLI at a
	// different version can warn about the mismatch.
	cmd.Env = append(os.Environ(), controlPlaneVersionEnv+"="+version)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found — is it installed and on PATH?", argv[0])
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s failed (exit %d)", name, exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func resolveRegion(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	return "us-east-1"
}

// loadCreds resolves an AWS config from the ambient credential chain only and
// confirms credentials are retrievable. Bootstrap and bake provision the
// control plane itself, so they need the administrator's own credentials; the
// stored control-plane key is deliberately not consulted here — a day-to-day
// key must not stand in for the admin while the account is being (re)built.
func loadCreds(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := remote.LoadAmbientAWSConfig(ctx, region)
	if err != nil {
		return aws.Config{}, err
	}
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		return aws.Config{}, err
	}
	return cfg, nil
}

// checkNodeAndPackageManager resolves the package manager to use, requiring the
// pinned one (or at least one of pnpm/npm when auto-detecting) on PATH, then
// verifies Node 22+. It returns the resolved manager for the run.
func checkNodeAndPackageManager(name string, pinned bool) (packageManager, error) {
	pm, onPath := selectPackageManager(name)
	if !onPath {
		if pinned {
			return pm, fmt.Errorf(
				"%s was requested via --package-manager/%s but is not on PATH — install it, or omit it to auto-detect",
				pm.name, packageManagerEnv)
		}
		return pm, fmt.Errorf("no Node package manager found on PATH — install pnpm (preferred) or npm, and Node 22+")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return pm, fmt.Errorf("node not found on PATH — install Node 22 or newer")
	}
	out, err := exec.Command(nodePath, "--version").Output()
	if err != nil {
		return pm, fmt.Errorf("checking node version: %w", err)
	}
	if major := parseNodeMajor(string(out)); major > 0 && major < 22 {
		return pm, fmt.Errorf("Node %s found; bootstrap needs Node 22 or newer", strings.TrimSpace(string(out)))
	}
	return pm, nil
}

func parseNodeMajor(version string) int {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// upsertEnvVar sets key=value in a .env file, replacing an existing line or
// appending, and writes it owner-only since it may hold a token.
func upsertEnvVar(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}
	replaced := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), key+"=") {
			lines[i] = key + "=" + value
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	out := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(strings.TrimLeft(out, "\n")), 0o600)
}

func renderBootstrapPlan(account, region string, ref, cdkDir string, alreadyBootstrapped bool, pm packageManager) {
	w := os.Stderr
	fmt.Fprintln(w, "spinloop remote bootstrap — account-level control-plane setup (once per account)")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "AWS account:  %s\n", account)
	fmt.Fprintf(w, "Region:       %s\n", region)
	fmt.Fprintf(w, "Sources:      github.com/spinloop-ai/spinloop @ %s  ->  %s\n", ref, cdkDir)
	if alreadyBootstrapped {
		fmt.Fprintln(w, "Note:         the account is already bootstrapped; this will update the control plane.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This deploys the control plane every environment reuses:")
	fmt.Fprintln(w, "  • EC2 Image Builder pipelines (the AMI bakes are a separate step: spinloop remote bake)")
	fmt.Fprintln(w, "  • the lifecycle Lambdas (start/stop/monitor/deploy) and their IAM")
	fmt.Fprintln(w, "  • the shared S3 weights bucket, IAM roles, and VPC")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost: an ongoing at-rest cost (bucket, and AMIs once baked) plus a per-hour GPU cost only while")
	fmt.Fprintln(w, "an environment is running. See remote/docs/costs.md for the breakdown.")
	fmt.Fprintln(w, "Note: the GPU vCPU quota must be > 0 in this region or a later launch fails.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Commands (in %s, using %s):\n", cdkDir, pm.name)
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.install, " "))
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("cdk", "bootstrap"), " "))
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("deploy:image"), " "))
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("deploy"), " "))
}

func confirmProceed() bool {
	fmt.Fprint(os.Stderr, "\nProceed? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// bakePollInterval is how often waitForBake checks; a variable so tests can
// poll fast, like the dashboard's interval hooks.
var bakePollInterval = 60 * time.Second

// waitForBake polls until every requested runner has an available AMI, or the
// context is cancelled. It is bounded by a generous timeout so it cannot hang
// forever if a bake fails.
func waitForBake(ctx context.Context, cfg aws.Config, runners []string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	for {
		baked, err := bakedFn(ctx, cfg)
		if err != nil {
			return err
		}
		done := true
		for _, r := range runners {
			if !baked[r] {
				done = false
				break
			}
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for the AMI bake(s): %w", ctx.Err())
		case <-time.After(bakePollInterval):
		}
	}
}
