package cloud

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ControlPlaneVersionHeader is the response header every control plane Lambda
// sets to the spinloop version it was deployed with.
const ControlPlaneVersionHeader = "X-Spinloop-Control-Plane-Version"

var (
	// cliVersion is the running binary's version, set once at startup by
	// SetCLIVersion. While empty, no comparison is made.
	cliVersion string
	// versionWarnWriter receives the mismatch warning. A variable so tests can
	// capture it.
	versionWarnWriter io.Writer = os.Stderr
	versionWarnOnce   sync.Once
)

// SetCLIVersion records the running binary's version for the comparison
// against the control plane's.
func SetCLIVersion(v string) {
	cliVersion = v
}

// versionsDiffer reports whether the CLI and control plane versions are both
// real release versions and are not the same. An empty or "dev" value on
// either side is not comparable, and a leading "v" is ignored.
func versionsDiffer(cli, controlPlane string) bool {
	cli = strings.TrimPrefix(strings.TrimSpace(cli), "v")
	controlPlane = strings.TrimPrefix(strings.TrimSpace(controlPlane), "v")
	if cli == "" || cli == "dev" || controlPlane == "" || controlPlane == "dev" {
		return false
	}
	return cli != controlPlane
}

// checkControlPlaneVersion writes a warning to stderr, at most once per
// process, when the response headers carry a control plane version that
// differs from the CLI's. A response without the header (a control plane that
// predates it) produces no warning.
func checkControlPlaneVersion(h http.Header) {
	got := h.Get(ControlPlaneVersionHeader)
	if !versionsDiffer(cliVersion, got) {
		return
	}
	versionWarnOnce.Do(func() {
		fmt.Fprintf(versionWarnWriter,
			"Warning: the control plane is at version %s but this spinloop is %s. Run `spinloop cloud bootstrap` to bring it up to date.\n",
			strings.TrimPrefix(got, "v"), strings.TrimPrefix(cliVersion, "v"))
	})
}
