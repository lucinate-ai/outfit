// The CLI layer's single Viper instance: the one place that reads the OUTFIT_*
// environment values the CLI owns (OUTFIT_ALIAS and the OUTFIT_REMOTE_*
// control-plane settings). Every OUTFIT_* variable whose precedence is an
// internal contract is deliberately NOT read here — OUTFIT_PROVIDERS
// (catalog), OUTFIT_BASE_URL (the injected resolve closures), OUTFIT_LOG_LEVEL
// (daemon.ParseLevel), OUTFIT_HARNESS (harness.Resolve, which also reports the
// source of the choice), and the domain-owned OUTFIT_API_TOKEN /
// OUTFIT_CONFIG_DIR / OUTFIT_REMOTE_* of other packages. Reading one of those
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

// cliViper is built once per process. AutomaticEnv plus the OUTFIT prefix means
// cliViper.GetString("alias") resolves OUTFIT_ALIAS and
// cliViper.GetString("remote_start_url") resolves OUTFIT_REMOTE_START_URL, and
// a dash in a key is read from the underscored variable.
var cliViper = newCLIViper()

func newCLIViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("OUTFIT")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	return v
}

// viperGetenv returns a Viper-backed os.Getenv for the OUTFIT_* variables the
// CLI owns. internal/remote keeps its func(string) string injection point for
// the OUTFIT_REMOTE_* config keys; the CLI hands it this closure so the lookup
// runs through cliViper instead of a raw os.Getenv. A name without the OUTFIT_
// prefix is not a CLI-owned variable, so it falls through to the process
// environment unchanged, which keeps the closure safe as a general getenv.
func viperGetenv() func(string) string {
	return func(name string) string {
		key := strings.TrimPrefix(name, "OUTFIT_")
		if key == name {
			return os.Getenv(name)
		}
		return cliViper.GetString(key)
	}
}

// resolve binds a command's pflags to cliViper, so a flag whose name is also a
// CLI-owned OUTFIT_ variable (OUTFIT_<name>) resolves pflag-changed > env >
// flag default through the one mechanism, instead of a re-implementation at the
// call site. It runs at the top of each command's RunE, so within a process
// only the running command's flags are ever bound.
//
// In the current surface no CLI-owned variable has a flag spelling —
// OUTFIT_ALIAS and the OUTFIT_REMOTE_* settings have none — so every binding
// today is a no-op that exists so the precedence stays one mechanism if a flag
// ever names one of them. The flags that have env counterparts today
// (--providers, --base-url, --log-level, --harness) are resolved by the
// internal packages on purpose and stay that way; binding them here would
// create the second reader this migration removes.
func resolve(cmd *cobra.Command) {
	cliViper.BindPFlags(cmd.Flags())
}
