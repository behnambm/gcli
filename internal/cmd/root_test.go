// === Behavioral Contract: `gcli --version` ===
//   - rootCmd.Version is set to the toolVersion stamp ("dev" unless overridden
//     at build time via -ldflags), so cobra auto-registers a --version flag
//   - `gcli --version` prints "gcli version <stamp>" to stdout and exits 0,
//     never contacting the Grafana server
package cmd

import (
	"strings"
	"testing"
)

func TestVersionFlag_printsToolVersion(t *testing.T) {
	// Arrange: --version must not need a server, but set env so the flag is
	// exercised through the normal command plumbing.
	srv := fakeGrafana(t, nil, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	// Act
	out := runCommand(t, "--version")

	// Assert: the default cobra template emits "<name> version <stamp>".
	if !strings.Contains(out, "gcli") || !strings.Contains(out, "version") {
		t.Errorf("output = %q", out)
	}
}
