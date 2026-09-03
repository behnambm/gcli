// === Behavioral Contract: `gcli help` ===
//   - Bare help prints the embedded guide: install + shell completion, setup
//     (profiles + legacy env vars), token creation, command reference,
//     output formats, exit codes, permission notes
//   - `gcli help <command>` prints that command's cobra help
//   - `gcli help <unknown>` errors and points back to `gcli help`
package cmd

import (
	"strings"
	"testing"
)

func TestHelpCommand_printsFullGuide(t *testing.T) {
	srv := fakeGrafana(t, nil, nil) // never contacted
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "help")

	for _, want := range []string{
		"GRAFANA_URL", "GRAFANA_TOKEN", "gcli prom", "gcli logs", "gcli sql",
		"gcli query", "gcli alerts", "gcli capabilities", "EXIT CODES",
		"service-account", "SETUP", "INSTALL",
		"profiles add", "GCLI_PROFILE", "profiles.yaml",
		"gcli metrics", "gcli labels", "gcli values", "gcli tables",
		"gcli columns", "gcli alert", "gcli panels", "completion",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestHelpCommand_forSubcommand_printsThatCommandsHelp(t *testing.T) {
	srv := fakeGrafana(t, nil, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "help", "prom")

	if !strings.Contains(out, "promql") && !strings.Contains(out, "PromQL") {
		t.Errorf("per-command help = %q, want prom usage", out)
	}
}

func TestHelpCommand_unknownCommand_errorsWithPointer(t *testing.T) {
	srv := fakeGrafana(t, nil, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "help", "nope")

	if err == nil || !strings.Contains(err.Error(), "gcli help") {
		t.Fatalf("err = %v, want pointer back to gcli help", err)
	}
}
