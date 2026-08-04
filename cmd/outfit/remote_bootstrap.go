package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// sharedStackName is the CloudFormation stack holding the shared, account-level
// layer that bootstrap deploys and `outfit remote deploy` discovers.
const sharedStackName = "cloud-vm-llm"

// Seams: package variables so tests drive the flow without AWS, a network, or
// spawning a package manager/cdk.
type bootstrapStep func(ctx context.Context, name string, argv []string, workDir string) error

var (
	bootstrapRunStep         bootstrapStep = runBootstrapStep
	bootstrapDownloadFn                    = remote.DownloadRemote
	bootstrapAccountFn                     = remote.CallerIdentity
	bootstrapStackDeployedFn               = remote.SharedStackDeployed
	bootstrapBakedFn                       = remote.BakedRunners
	bootstrapPreflightFn                   = checkNodeAndPackageManager
)

// packageManagerEnv pins the Node package manager bootstrap drives the CDK
// project with, when the --package-manager flag is not given.
const packageManagerEnv = "OUTFIT_REMOTE_PACKAGE_MANAGER"

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
// OUTFIT_REMOTE_PACKAGE_MANAGER env var — returning the pinned name (or "" to
// auto-detect) and whether the choice was pinned. An unrecognised value errors.
func resolvePackageManagerName(flagVal string) (name string, pinned bool, err error) {
	if flagVal != "" {
		if err := validatePackageManagerName(flagVal); err != nil {
			return "", false, fmt.Errorf("--package-manager: %w", err)
		}
		return flagVal, true, nil
	}
	if env := os.Getenv(packageManagerEnv); env != "" {
		if err := validatePackageManagerName(env); err != nil {
			return "", false, fmt.Errorf("%s: %w", packageManagerEnv, err)
		}
		return env, true, nil
	}
	return "", false, nil
}

// cmdRemoteBootstrap deploys the shared, account-level infrastructure once —
// analogous to `cdk bootstrap` — by downloading the remote/ CDK project and
// driving its shared-stack deploy. It creates no EIP, instance, or environment;
// those come from `outfit remote deploy`.
func cmdRemoteBootstrap(args []string) error {
	fs := flag.NewFlagSet("remote bootstrap", flag.ContinueOnError)
	var (
		runnersFlag = fs.String("runners", "llamacpp,vllm", "comma-separated runner AMIs to bake")
		hfToken     = fs.String("hf-token", "", "Hugging Face token for the shared secret (optional)")
		ref         = fs.String("ref", "", "git ref of remote/ to download (default: matches this binary)")
		dir         = fs.String("dir", "", "where to place the downloaded remote/ sources")
		region      = fs.String("region", "", "AWS region (default: AWS_REGION or us-east-1)")
		dryRun      = fs.Bool("dry-run", false, "print the plan and exit without doing anything")
		assumeYes   = fs.Bool("yes", false, "skip the confirmation prompt")
		wait        = fs.Bool("wait", false, "block until the AMI bake(s) finish")
		forceBake   = fs.Bool("force-bake", false, "re-bake the AMIs even if already bootstrapped")
		pkgMgr      = fs.String("package-manager", "", "package manager to use: pnpm or npm (default: auto-detect, preferring pnpm)")
	)
	fs.BoolVar(dryRun, "n", false, "shorthand for --dry-run")
	fs.BoolVar(assumeYes, "y", false, "shorthand for --yes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runners, err := parseRunners(*runnersFlag)
	if err != nil {
		return err
	}

	pmName, pmPinned, err := resolvePackageManagerName(*pkgMgr)
	if err != nil {
		return err
	}

	resolvedRef := remote.ResolveRef(version, *ref)
	cdkDir := remote.SourceDir(resolvedRef)
	pruneAfter := true
	if *dir != "" {
		cdkDir = *dir
		pruneAfter = false // an explicit --dir is the user's own; leave it alone
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	resolvedRegion := resolveRegion(*region)

	// AWS is best-effort for the plan; it is hard-required for a real run.
	account := "unknown"
	alreadyBootstrapped := false
	var awsCfg aws.Config
	cfg, credsErr := loadCreds(ctx, resolvedRegion)
	if credsErr == nil {
		awsCfg = cfg
		if acct, err := bootstrapAccountFn(ctx, cfg); err == nil {
			account = acct
		}
		if dep, err := bootstrapStackDeployedFn(ctx, cfg, sharedStackName); err == nil {
			alreadyBootstrapped = dep
		}
	}

	var pm packageManager
	if !*dryRun {
		selected, err := bootstrapPreflightFn(pmName, pmPinned)
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

	renderBootstrapPlan(account, resolvedRegion, runners, resolvedRef, cdkDir, alreadyBootstrapped, pm)

	if *dryRun {
		return nil
	}
	if !*assumeYes && !confirmProceed() {
		fmt.Fprintln(os.Stderr, "Aborted; nothing was created.")
		return nil
	}

	if err := bootstrapDownloadFn(ctx, resolvedRef, cdkDir); err != nil {
		return err
	}
	if *hfToken != "" {
		if err := upsertEnvVar(filepath.Join(cdkDir, ".env"), "HF_TOKEN", *hfToken); err != nil {
			return err
		}
	}
	if err := setCdkContext(cdkDir, "runners", strings.Join(runners, ",")); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUsing %s to run the CDK project.\n", pm.name)
	if err := runBootstrapSequence(ctx, cdkDir, runners, alreadyBootstrapped, *forceBake, pm); err != nil {
		return err
	}

	fmt.Println("\nThe account is bootstrapped. Create an endpoint with:")
	fmt.Println("  outfit remote deploy <env>   # names an environment; discovers this shared layer")

	if *wait {
		if credsErr != nil {
			return fmt.Errorf("--wait needs AWS credentials to poll the bake")
		}
		fmt.Fprintln(os.Stderr, "\nWaiting for the AMI bake(s) to finish (this can take 20-40 minutes)...")
		if err := waitForBake(ctx, awsCfg, runners); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "AMI(s) available.")
	} else {
		fmt.Fprintln(os.Stderr, "\nThe AMI bake(s) run in the background (~20-40 min). Re-run with --wait to block, or check the Image Builder console.")
	}

	if pruneAfter {
		if err := remote.PruneSources(remote.SourceRoot(), resolvedRef); err != nil {
			return err
		}
	}
	return nil
}

// runBootstrapSequence runs the package-manager/cdk steps in the sources
// directory with the resolved manager.
func runBootstrapSequence(ctx context.Context, cdkDir string, runners []string, alreadyBootstrapped, forceBake bool, pm packageManager) error {
	run := func(name string, argv ...string) error {
		return bootstrapRunStep(ctx, name, argv, cdkDir)
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
	if !alreadyBootstrapped || forceBake {
		for _, r := range runners {
			if err := run("bake "+r, pm.script("bake", r)...); err != nil {
				return err
			}
		}
	}
	return run("deploy", pm.script("deploy")...)
}

// runBootstrapStep runs one external command in workDir, streaming its stdio,
// mirroring serve.go's exec pattern. Ctrl-C propagates via the context.
func runBootstrapStep(ctx context.Context, name string, argv []string, workDir string) error {
	fmt.Fprintf(os.Stderr, "\n$ %s\n", strings.Join(argv, " "))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
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

// parseRunners splits and validates the --runners list.
func parseRunners(csv string) ([]string, error) {
	var runners []string
	for _, part := range strings.Split(csv, ",") {
		r := strings.TrimSpace(part)
		if r == "" {
			continue
		}
		if _, err := runnerFor(r); err != nil {
			return nil, fmt.Errorf("--runners: %w", err)
		}
		runners = append(runners, r)
	}
	if len(runners) == 0 {
		return nil, fmt.Errorf("--runners is empty")
	}
	return runners, nil
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

// loadCreds resolves an AWS config and confirms credentials are retrievable.
func loadCreds(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := remote.LoadAWSConfig(ctx, region)
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

// setCdkContext sets a context key in cdk.json (an additive JSON edit), so the
// value reaches every cdk invocation without a -c flag.
func setCdkContext(cdkDir, key, value string) error {
	path := filepath.Join(cdkDir, "cdk.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	ctxObj, _ := doc["context"].(map[string]any)
	if ctxObj == nil {
		ctxObj = map[string]any{}
		doc["context"] = ctxObj
	}
	ctxObj[key] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func renderBootstrapPlan(account, region string, runners []string, ref, cdkDir string, alreadyBootstrapped bool, pm packageManager) {
	w := os.Stderr
	fmt.Fprintln(w, "outfit remote bootstrap — shared, account-level setup (once per account)")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "AWS account:  %s\n", account)
	fmt.Fprintf(w, "Region:       %s\n", region)
	fmt.Fprintf(w, "Runners:      %s\n", strings.Join(runners, ", "))
	fmt.Fprintf(w, "Sources:      github.com/lucinate-ai/outfit @ %s  ->  %s\n", ref, cdkDir)
	if alreadyBootstrapped {
		fmt.Fprintln(w, "Note:         the account is already bootstrapped; this will update the shared infrastructure.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This deploys the shared infrastructure every environment reuses:")
	fmt.Fprintln(w, "  • EC2 Image Builder pipelines and the baked AMIs")
	fmt.Fprintln(w, "  • the lifecycle Lambdas (start/stop/monitor/deploy) and their IAM")
	fmt.Fprintln(w, "  • the shared S3 weights bucket, IAM roles, and VPC")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost: an ongoing at-rest cost (bucket, AMIs) plus a per-hour GPU cost only while")
	fmt.Fprintln(w, "an environment is running. See remote/docs/costs.md for the breakdown.")
	fmt.Fprintln(w, "Note: the GPU vCPU quota must be > 0 in this region or a later launch fails.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Commands (in %s, using %s):\n", cdkDir, pm.name)
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.install, " "))
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("cdk", "bootstrap"), " "))
	fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("deploy:image"), " "))
	for _, r := range runners {
		fmt.Fprintf(w, "  %s\n", strings.Join(pm.script("bake", r), " "))
	}
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

// waitForBake polls until every requested runner has an available AMI, or the
// context is cancelled. It is bounded by a generous timeout so it cannot hang
// forever if a bake fails.
func waitForBake(ctx context.Context, cfg aws.Config, runners []string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	for {
		baked, err := bootstrapBakedFn(ctx, cfg)
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
		case <-time.After(60 * time.Second):
		}
	}
}
