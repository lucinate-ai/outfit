package main

import (
	"github.com/spf13/cobra"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/harness"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
)

// openLaunchCmd builds the launch command — the one body shared by `harness
// open` and its one-word shortcut `code`. It disables Cobra's flag parsing
// because the command has to decide, token by token, which arguments are
// spinloop's own and which belong to the launched harness: a leading positional
// that names a Spinloop is consumed, a `--` opts out, and everything after the
// hand-off point forwards byte-for-byte. The flags are registered on the
// command's own flag set — the body parses that set, and the completion surface
// reads the same one, so parsing and completion cannot drift apart. Because both
// spellings come from this single builder they share flags, Spinloop
// application, and launch, and cannot drift from each other.
func openLaunchCmd(use, short, long string) *cobra.Command {
	var harnessName, providers string
	var spinloopPath spinloopPathFlag
	var route routeOptions
	c := &cobra.Command{
		Use:                use,
		Short:              short,
		Long:               long,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		// The engine cannot see past a DisableFlagParsing command, so every
		// word here — attached flag values included — lands in this slot.
		ValidArgsFunction: launchSlot,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			// Parsing is spinloop's own (not Cobra's): spinloop's own flags are
			// recognised wherever they appear, one leading positional that
			// names a Spinloop is consumed alongside them, and everything from
			// the first argument that is neither forwards byte-for-byte.
			fs := c.Flags()
			rest, err := splitHarnessArgs(fs, &spinloopPath, args)
			if err != nil {
				return err
			}
			// -h/--help is consumed by the flag set, not intercepted: Cobra
			// registers the help flag before RunE runs, but with flag parsing
			// off it never checks it, so without this the flag would be set and
			// the agent launched. A `--` before it leaves it a forwarded
			// positional, so the harness still gets its own --help.
			if fs.Changed("help") {
				return c.Help()
			}

			h, _, err := harness.Resolve(harnessName)
			if err != nil {
				return err
			}
			// A named Spinloop — the flag's value, a leading positional, or the
			// alias SPINLOOP_ALIAS names — travels to its fleet only by flag, so
			// a fleet.yaml in the working directory is not picked up for it. A
			// valueless --spinloop wears the default Spinloop and is not named.
			route.spinloopNamed = spinloopPath.path != "" || spinloopAliasInForce()

			// A .env beside the applied Spinloop is where its keys live, so the
			// launched agent is given the same ones. Without a Spinloop there is
			// no such file and only the environment (plus any provider key
			// spinloop resolves) is passed on. cloudResp carries the live key of
			// a cloud endpoint, fetched while applying so the config is
			// written knowing it will be there.
			var envDir string
			var sel spinloop.Selection
			var cloudResp *cloud.Response
			var choice *fleet.Choice
			if spinloopPath.set {
				var err error
				sel, envDir, cloudResp, choice, err = applyBeforeLaunch(spinloopPath, providers, h, rest, route)
				if err != nil {
					return err
				}
			} else if route.envName != "" {
				// No Spinloop was applied, but --env still names where the
				// model is served from: configure the harness from what is
				// actually deployed there instead of doing nothing with the
				// flag.
				var err error
				sel, envDir, cloudResp, choice, err = applyFromEnvironment(providers, h, route)
				if err != nil {
					return err
				}
			} else if route.fleetPath != "" {
				// No Spinloop, but a fleet was named. A gateway resolves the
				// model per request, so a fleet that names one needs no
				// Spinloop; a fleet that does not still does, and this says
				// so.
				var err error
				sel, envDir, cloudResp, choice, err = applyFromGateway(providers, h, route)
				if err != nil {
					return err
				}
			}
			return launchAgent(h, rest, providers, envDir, cloudResp, sel, spinloopPath.set, choice)
		},
	}
	fs := c.Flags()
	fs.SetInterspersed(false)
	fs.StringVarP(&harnessName, "harness", "H", "", "which harness to launch")
	fs.VarP(&spinloopPath, "spinloop", "O", "apply this Spinloop before launching (bare: ./"+spinloop.DefaultFile+")")
	// Bare -O arrives as NoOptDefVal; spinloopPathFlag maps it to the empty
	// path readSpinloop resolves as SPINLOOP_ALIAS > ./Spinloop.
	fs.Lookup("spinloop").NoOptDefVal = "true"
	fs.StringVar(&providers, "providers", "", "path to a providers.yaml override")
	fs.StringVarP(&route.envName, "env", "e", "", "launch against this registered environment (its name keys the provider, its API key is fetched and injected)")
	compRegister(c, "env", compEnvs)
	fs.StringVarP(&route.fleetPath, "fleet", "f", "", "route through this fleet file (default: ./fleet.yaml, when the Spinloop is not named)")
	fs.StringVar(&route.node, "node", "", "pin the launch to this fleet node")
	fs.StringVar(&route.prefer, "prefer", "", "rank fleet nodes by `idle` or `active` (overrides the fleet file)")
	fs.BoolVar(&route.noWake, "no-wake", false, "fail rather than starting an engine on an idle fleet node")
	fs.DurationVar(&route.wakeTimeout, "wake-timeout", 0, "how long to wait for a woken node's engine")
	return c
}

// openCmd builds the `open` subcommand of the harness group — the launch a bare
// `harness` used to be.
func openCmd() *cobra.Command {
	return openLaunchCmd("open",
		"launch the active harness, forwarding trailing args",
		`launches the active harness, forwarding any trailing args to it. A
leading argument that names a Spinloop — a registered alias or a path — is
applied first and not forwarded; put -- before the harness's own args to keep
them, and a leading -- opts out of this entirely. --spinloop/-O applies a
Spinloop first, as if you had run harness apply before it. Honours -H/
--harness and SPINLOOP_HARNESS.`)
}

// codeCmd is `spinloop code`: the one-word shortcut for `spinloop harness
// open`. It is built by the same openLaunchCmd as `harness open`, so it takes
// the same flags, applies a Spinloop the same way, and launches the same
// harness — a new spelling of it, not a second implementation.
func codeCmd() *cobra.Command {
	return openLaunchCmd("code",
		"launch the active harness — a shortcut for harness open",
		`launches the active harness: a one-word shortcut for spinloop harness open,
taking the same flags and applying a Spinloop the same way. A leading argument
that names a Spinloop — a registered alias or a path — is applied first and not
forwarded; put -- before the harness's own args to keep them, and a leading --
opts out of this entirely. Every flag it accepts is harness open's — run
spinloop harness open --help for the same list.`)
}

// cmdCode runs the code command through the tree — the seam the suite calls
// directly, mirroring cmdOpen.
func cmdCode(args []string) error { return execCmd(codeCmd(), args) }
