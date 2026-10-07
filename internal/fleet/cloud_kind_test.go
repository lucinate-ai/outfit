package fleet

import (
	"strings"
	"testing"
)

// The old kind spelling is gone: `kind: remote` fails like any other unknown kind.
func TestLoadRemoteKindIsRejected(t *testing.T) {
	_, err := Load(writeFleet(t, "nodes:\n  - name: prod\n    kind: remote\n", ""))
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("error = %v, want one naming the unsupported kind", err)
	}
}
