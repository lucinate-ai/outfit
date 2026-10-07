// The Spinloop a read verb may be given. It selects nothing — the target comes
// from --env or --fleet — and is read only for the environment it carries: its
// ENV instructions and the .env beside it, applied before any control-plane or
// daemon work. That is what lets AWS credentials, a profile, or the
// SPINLOOP_CLOUD_* overrides live in a project's Spinloop rather than in the
// shell that happens to be running the command.
//
// The `cloud` subcommands this replaces also consulted ./Spinloop when none
// was named. The verbs do not: a file sitting in the working directory should
// not silently set environment variables for a command that reads a fleet, and
// naming it is one flag. Nothing else about the rule changes.

package main

import "github.com/spf13/pflag"

// spinloopEnvUsage is the --spinloop flag's help on every read verb.
const spinloopEnvUsage = "read this Spinloop's ENV instructions and adjacent .env before reading the target (it selects nothing)"

// registerSpinloopEnvFlag adds the flag to a read verb's flag set.
func registerSpinloopEnvFlag(fs *pflag.FlagSet, path *string) {
	fs.StringVarP(path, "spinloop", "O", "", spinloopEnvUsage)
}

// applyReadSpinloopEnv reads the named Spinloop and applies the environment it
// carries. An empty path applies nothing: the verbs take the file only when
// told to.
func applyReadSpinloopEnv(path string) error {
	if path == "" {
		return nil
	}
	sel, resolved, err := readSpinloop("spinloop --spinloop=<file>", path)
	if err != nil {
		return err
	}
	return applySpinloopEnv(sel, resolved)
}
