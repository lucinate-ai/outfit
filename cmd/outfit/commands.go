// The Cobra command tree: one source of truth for the CLI's command surface.
// Every invocation builds a fresh tree, so values parsed into one run's flags
// can never leak into the next — the property the old code got from building
// a new FlagSet per command.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
	"github.com/spf13/cobra"
)

// newRootCmd builds the command tree for one invocation of outfit.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "outfit",
		Short: "configure coding-agent model providers",
		Long: `configures a coding agent — a harness — to use a model provider, by
deep-merging provider settings into that harness's config. The supported
harnesses are opencode, Pi and lucinate; the harness is chosen at runtime
(--harness/-H, OUTFIT_HARNESS, or a stored default set with outfit harness
--set), never baked into an Outfit, so the same Outfit applies to any of
them.

Each command's --help carries its full description; outfit list shows what a
harness could be configured with, outfit show what it has been.`,
		Version: version,
		// The version prints as the bare version string, as the version
		// subcommand does.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	// --version comes from the Version field; -v joins it, as both spellings
	// worked before the tree existed.
	root.InitDefaultVersionFlag()
	if f := root.Flags().Lookup("version"); f != nil {
		f.Shorthand = "v"
	}
	// The completion protocol must never write to stderr, and the engine's
	// status line flows through the command's error stream: discard it.
	// User-facing errors are unaffected — main prints them to os.Stderr
	// itself, and pflag's flag errors go to its own buffer.
	root.SetErr(io.Discard)

	root.AddCommand(
		addCmd(),
		removeCmd(),
		listCmd(),
		showCmd(),
		applyCmd(),
		unapplyCmd(),
		aliasCmd(),
		unaliasCmd(),
		serveCmd(),
		daemonCmd(),
		exportCmd(),
		initProvidersCmd(),
		harnessCmd(),
		versionCmd(),
		completionCmd(),
	)

	root.AddCommand(fleetCmd())
	root.AddCommand(remoteCmd())
	defaultFlagCompletions(root)
	return root
}

// execCmd parses args through the command's flags and runs it — the seam the
// suite uses to call a command the way the tree does, without a full root
// dispatch.
func execCmd(c *cobra.Command, args []string) error {
	c.SetArgs(args)
	return c.Execute()
}

// harnessCmd builds the `harness` command. It disables Cobra's flag parsing
// because the command has to decide, token by token, which arguments are
// outfit's own and which belong to the launched harness: a leading positional
// that names an Outfit is consumed, a `--` opts out, and everything after the
// hand-off point forwards byte-for-byte. The flags are registered on the
// command's own flag set — the body parses that set, and the completion
// surface reads the same one, so parsing and completion cannot drift apart.
func harnessCmd() *cobra.Command {
	var set, harnessName, providers string
	var get bool
	var outfitPath outfitPathFlag
	var route routeOptions
	c := &cobra.Command{
		Use:   "harness",
		Short: "launch the active harness, optionally applying an Outfit first",
		Long: `launches the active harness, forwarding any trailing args to it. A
leading argument that names an Outfit — a registered alias or a path — is
applied first and not forwarded; put -- before the harness's own args to
keep them, and a leading -- opts out of this entirely. --outfit/-O applies
an Outfit first, as if you had run apply before it. --get prints the active
harness instead of launching it; --set <name> stores the default harness and
exits. Honours -H/--harness and OUTFIT_HARNESS.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		// The engine cannot see past a DisableFlagParsing command, so every
		// word here — attached flag values included — lands in this slot.
		ValidArgsFunction: harnessSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			// Parsing is outfit's own (not Cobra's): a leading positional
			// that names an Outfit is consumed, and everything else forwards
			// byte-for-byte, so the flag set stops at the first positional
			// exactly as the flag package did.
			fs := c.Flags()
			if err := fs.Parse(args); err != nil {
				return err
			}

			if set != "" {
				if err := harness.SavePreference(set); err != nil {
					return err
				}
				prefPath, _ := harness.PreferencePath()
				fmt.Printf("Default harness set to %q (stored in %s).\n", set, prefPath)
				return nil
			}

			h, source, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}

			// --get reports the harness rather than running anything, so it
			// applies nothing either.
			if get {
				pref, _ := harness.LoadPreference()
				fmt.Printf("Active harness: %s (from %s)\n", h.Name(), source)
				if pref == "" {
					fmt.Printf("Stored preference: none (defaults to %s)\n", harness.Default)
				} else {
					fmt.Printf("Stored preference: %s\n", pref)
				}
				fmt.Printf("Available: %s\n", strings.Join(harness.Names(), ", "))
				return nil
			}

			// Take the first positional argument as the Outfit to wear when it
			// names one — a registered alias, a path, or a directory holding
			// one. Everything else is forwarded to the harness untouched, so
			// this can only claim an argument the harness could not have used
			// anyway. An explicit `--` opts out, for an alias that collides
			// with one of the harness's own subcommands.
			rest := fs.Args()
			if !outfitPath.set && !flagsTerminated(args, rest) && len(rest) > 0 && namesAnOutfitOrAlias(rest[0]) {
				outfitPath.set, outfitPath.path = true, rest[0]
				// Reslice rather than rebuild: rest shares its backing array
				// with args, so appending to it would write over the caller's
				// arguments.
				rest = rest[1:]
				// The `--` that separated outfit's Outfit from the harness's
				// own args is ours to drop; any other `--` belongs to the
				// harness and is forwarded.
				if len(rest) > 0 && rest[0] == "--" {
					rest = rest[1:]
				}
			}

			// A .env beside the applied Outfit is where its keys live, so the
			// launched agent is given the same ones. Without an Outfit there is
			// no such file and only the environment (plus any provider key
			// outfit resolves) is passed on. remoteResp carries the live key of
			// a remote endpoint, fetched while applying so the config is
			// written knowing it will be there.
			var envDir string
			var sel outfit.Selection
			var remoteResp *remote.Response
			var choice *fleet.Choice
			if outfitPath.set {
				var err error
				sel, envDir, remoteResp, choice, err = applyBeforeLaunch(outfitPath, providers, h, rest, route)
				if err != nil {
					return err
				}
			} else if route.fleetPath != "" {
				return fmt.Errorf("--fleet needs an Outfit: it is the Outfit's model that decides which node can serve you")
			}
			// The resolver the launch uses knows the remote key too, so every
			// key the agent is given comes from the same place the apply step
			// reported.
			resolveKey := remoteLaunchResolver(opencode.EnvResolver(envDir), remoteResp)

			// Launch the harness, forwarding stdio and any trailing args.
			bin := h.Command()
			cmd := exec.Command(bin, rest...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = harnessEnv(providers, resolveKey, remoteResp)
			// A routed launch points the agent at the node that was chosen. As
			// on the remote path, an explicit setting in the environment already
			// won: routing fills what is unset rather than overriding a
			// deliberate choice.
			if choice != nil {
				cmd.Env = setEnvIfBlank(cmd.Env, "OPENAI_BASE_URL", choice.BaseURL)
				if choice.APIKey != "" {
					cmd.Env = setEnvIfBlank(cmd.Env, "OPENAI_API_KEY", choice.APIKey)
				}
			}
			// A worn Outfit brings its whole local environment to the launched
			// agent: its adjacent .env fills any gaps left above, and its ENV
			// instructions override everything. These shape only the child's
			// environment — outfit never mutates its own — and follow the same
			// precedence the remote commands use: ENV > process environment >
			// .env.
			if outfitPath.set {
				cmd.Env = overlayLocalEnv(cmd.Env, sel, envDir)
			}
			// lucinate reads an OpenAI-compatible key from
			// LUCINATE_OPENAI_API_KEY when its stored secret is empty — which is
			// exactly how outfit configures it, with no secret on disk. Supply
			// the active provider's key here so the launched agent can
			// authenticate the model it boots into, without ever writing it to
			// lucinate's config. An explicit setting already in the child's env
			// wins.
			if h.Name() == "lucinate" {
				if choice != nil && choice.APIKey != "" {
					cmd.Env = setEnvIfBlank(cmd.Env, "LUCINATE_OPENAI_API_KEY", choice.APIKey)
				}
				if key, ok := lucinateLaunchKey(providers, resolveKey, sel, outfitPath.set); ok {
					cmd.Env = setEnvIfAbsent(cmd.Env, "LUCINATE_OPENAI_API_KEY", key)
				}
			}
			if err := cmd.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("%s not found — install the %s harness or add it to your PATH", bin, h.Name())
				}
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					// The harness ran and chose its own exit code; surface it
					// verbatim.
					os.Exit(exitErr.ExitCode())
				}
				return err
			}
			return nil
		},
	}
	fs := c.Flags()
	fs.SetInterspersed(false)
	fs.StringVar(&set, "set", "", "store this harness as the default and exit")
	fs.BoolVar(&get, "get", false, "print the active harness instead of launching it")
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to launch")
	fs.VarP(&outfitPath, "outfit", "O", "apply this Outfit before launching (bare: ./"+outfit.DefaultFile+")")
	// Bare -O arrives as NoOptDefVal; outfitPathFlag maps it to the empty
	// path readOutfit resolves as OUTFIT_ALIAS > ./Outfit.
	fs.Lookup("outfit").NoOptDefVal = "true"
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVar(&route.fleetPath, "fleet", "", "route through this fleet file (overrides the Outfit's FLEET)")
	fs.StringVar(&route.node, "node", "", "pin the launch to this fleet node")
	fs.StringVar(&route.prefer, "prefer", "", "rank fleet nodes by `idle` or `active` (overrides the fleet file)")
	fs.BoolVar(&route.noWake, "no-wake", false, "fail rather than starting an engine on an idle fleet node")
	fs.DurationVar(&route.wakeTimeout, "wake-timeout", 0, "how long to wait for a woken node's engine")
	return c
}

// versionCmd prints the version, the same spelling the old dispatch gave
// `outfit version`.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "version",
		Short:             "print the version",
		Args:              cobra.NoArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: noPositionals,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			fmt.Println(version)
			return nil
		},
	}
}

// fleetCmd builds the fleet parent and its subcommands. The parent runs when
// no subcommand is named and reports the usage, as the old dispatch did.
func fleetCmd() *cobra.Command {
	fleet := &cobra.Command{
		Use:   "fleet",
		Short: "observe and drive the engines in a fleet file",
		Long: `observes and drives the engines named in a fleet file (fleet.yaml by
default; --fleet names another). Observation is fleet-wide (status, metrics,
logs); start, stop and route act on a single node, and with no node they
list the fleet and touch nothing. A node that fails is a rendered row, never
an error — only a problem with the fleet file itself fails a command.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return fleetParentFallback(args)
		},
	}
	fleet.AddCommand(
		fleetStatusCmd(),
		fleetMetricsCmd(),
		fleetLogsCmd(),
		fleetRouteCmd(),
		fleetStartCmd(),
		fleetStopCmd(),
	)
	return fleet
}

// remoteCmd builds the remote parent and its subcommands. The parent runs
// when no subcommand is named and reports the usage, as the old dispatch did.
func remoteCmd() *cobra.Command {
	remote := &cobra.Command{
		Use:   "remote",
		Short: "control the remote GPU inference instance",
		Long: `runs the model on a cloud GPU that exists only while you use it, from
the same Outfit. The endpoint's URLs come from the Outfit's REMOTE — a bare
name selects an environment under ~/.config/outfit/remotes/<name>/, a path
names a file — falling back to the default environment. Each subcommand's
--help says what that step does.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return remoteParentFallback(args)
		},
	}
	remote.AddCommand(
		remoteBootstrapCmd(),
		remoteStartCmd(),
		remotePauseCmd(),
		remoteRestartCmd(),
		remoteStopCmd(),
		remoteStatusCmd(),
		remoteMetricsCmd(),
		remoteLogsCmd(),
		remoteDeployCmd(),
		remoteEnvCmd(),
		remoteListCmd(),
		remoteKeepCmd(),
	)
	return remote
}
