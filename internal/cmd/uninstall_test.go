// === Behavioral Contract: `gcli uninstall` ===
//   - Confirming the prompt removes the gcli binary
//   - Declining the prompt keeps the binary and succeeds
//   - force (--yes) removes without prompting
//   - A missing binary surfaces as an error
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstall_confirmed_removesBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gcli")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doUninstall(path, strings.NewReader("y\n"), &out, false); err != nil {
		t.Fatalf("doUninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("binary still exists after confirmed uninstall")
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("output = %q, missing removed path", out.String())
	}
}

func TestUninstall_declined_keepsBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gcli")
	content := []byte("binary")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doUninstall(path, strings.NewReader("n\n"), &out, false); err != nil {
		t.Fatalf("doUninstall: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("binary removed despite decline: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("binary content changed despite decline")
	}
}

func TestUninstall_force_skipsPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gcli")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doUninstall(path, strings.NewReader(""), &out, true); err != nil {
		t.Fatalf("doUninstall force: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("binary still exists after forced uninstall")
	}
}

func TestUninstall_missingBinary_errors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-gcli")
	err := doUninstall(path, strings.NewReader("y\n"), &bytes.Buffer{}, false)
	if err == nil {
		t.Fatal("doUninstall = nil error, want error for missing binary")
	}
}
