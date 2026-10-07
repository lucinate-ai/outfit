// The CLI layer's single Viper instance: the one place that reads the SPINLOOP_*
// environment values the CLI owns (SPINLOOP_ALIAS and the SPINLOOP_CLOUD_*
// control-plane settings). Every SPINLOOP_* variable whose precedence is an
// internal contract is deliberately NOT read here — SPINLOOP_PROVIDERS
// (catalog), SPINLOOP_BASE_URL (the injected resolve closures), SPINLOOP_LOG_LEVEL
// (daemon.ParseLevel), SPINLOOP_HARNESS (harness.Resolve, which also reports the
// source of the choice), and the domain-owned SPINLOOP_API_TOKEN /
// SPINLOOP_CONFIG_DIR / SPINLOOP_CLOUD_* of other packages. Reading one of those
// through Viper would create a second reader of the same variable — the exact
// silent drift this migration removes. See the ownership table in
// openspec/changes/migrate-cli-to-cobra-viper/design.md (D3).
package main

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cliViper is built once per process. AutomaticEnv plus the SPINLOOP prefix means
// cliViper.GetString("alias") resolves SPINLOOP_ALIAS and
// cliViper.GetString("cloud_start_url") resolves SPINLOOP_CLOUD_START_URL, and
// a dash in a key is read from the underscored variable.
var cliViper = newCLIViper()

func newCLIViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("SPINLOOP")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	return v
}

// viperGetenv returns a Viper-backed os.Getenv for the SPINLOOP_* variables the
// CLI owns. internal/cloud keeps its func(string) string injection point for
// the SPINLOOP_CLOUD_* config keys; the CLI hands it this closure so the lookup
// runs through cliViper instead of a raw os.Getenv. A name without the SPINLOOP_
// prefix is not a CLI-owned variable, so it falls through to the process
// environment unchanged, which keeps the closure safe as a general getenv.
func viperGetenv() func(string) string {
	return func(name string) string {
		key := strings.TrimPrefix(name, "SPINLOOP_")
		if key == name {
			return os.Getenv(name)
		}
		return cliViper.GetString(key)
	}
}

// resolve binds a command's pflags to cliViper, so a flag whose name is also a
// CLI-owned SPINLOOP_ variable (SPINLOOP_<name>) resolves pflag-changed > env >
// flag default through the one mechanism, instead of a re-implementation at the
// call site. It runs at the top of each command's RunE, and rebuilds cliViper
// first so that only the running command's flags are ever bound: BindPFlags
// accumulates, and a binding left by a command that already ran would answer
// for a key the running command never names — its stale pflag value winning
// over the environment variable of the same name. One command per process
// hides this; a process that runs several (the test suite) does not.
//
// Rebuilding is safe because resolve runs on the one goroutine, at the top of
// RunE, before a command does any work; viperGetenv reads the package variable
// at call time rather than capturing it, so its closures follow the swap.
//
// In the current surface no CLI-owned variable has a flag spelling —
// SPINLOOP_ALIAS and the SPINLOOP_CLOUD_* settings have none — so every binding
// today is a no-op that exists so the precedence stays one mechanism if a flag
// ever names one of them. The flags that have env counterparts today
// (--providers, --base-url, --log-level, --harness) are resolved by the
// internal packages on purpose and stay that way; binding them here would
// create the second reader this migration removes.
func resolve(cmd *cobra.Command) {
	cliViper = newCLIViper()
	cliViper.BindPFlags(cmd.Flags())
}
