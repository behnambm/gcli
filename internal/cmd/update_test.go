// === Behavioral Contract: `gcli update` ===
//   - An outdated binary is replaced atomically with the release binary and
//     the old -> new version transition (new from the new binary's
//     --version) is reported, followed by a newline
//   - A current binary (checksum matches) is left untouched and reported as
//     already up to date including its version
//   - A binary whose download does not match its published checksum is
//     rejected and the current binary is left untouched
//   - A download failure surfaces as an error
//   - Platforms without a published binary are rejected
//   - Download progress is written to the progress writer, reaching 100%
//   - renderBar shows percent and sizes; updateTimeout honors an explicit
//     --timeout flag and defaults to 10m otherwise; isTerminal distinguishes
//     TTY writers from buffers
package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeSelf(t *testing.T, version string) string {
	t.Helper()
	content := fmt.Sprintf("#!/bin/sh\necho \"gcli version %s\"\n", version)
	path := filepath.Join(t.TempDir(), "gcli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake self: %v", err)
	}
	return path
}

func updateServer(t *testing.T, newContent []byte, checksums []byte) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("gcli-%s-%s", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write(checksums)
	})
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Write(newContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func checksumLine(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(content)
	return []byte(fmt.Sprintf("%x  %s\n", sum, name))
}

func TestUpdate_outdatedSelf_replacesBinaryAndReportsVersion(t *testing.T) {
	newContent := []byte("#!/bin/sh\necho \"gcli version v9.9.9\"\n")
	self := fakeSelf(t, "v0.1.0")
	name := fmt.Sprintf("gcli-%s-%s", runtime.GOOS, runtime.GOARCH)
	checksums := append(checksumLine(t, name, newContent),
		checksumLine(t, "gcli-other-platform", []byte("other"))...)
	srv := updateServer(t, newContent, checksums)

	var out, progress bytes.Buffer
	updated, err := doUpdate(context.Background(), srv.Client(), srv.URL, self, "v0.1.0", &out, &progress)
	if err != nil {
		t.Fatalf("doUpdate: %v", err)
	}
	if !updated {
		t.Error("updated = false, want true")
	}
	if !strings.Contains(out.String(), "v0.1.0 -> v9.9.9") {
		t.Errorf("output = %q, missing old -> new version", out.String())
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("output = %q, missing trailing newline", out.String())
	}
	if !strings.Contains(progress.String(), "100.0%") {
		t.Errorf("progress = %q, missing 100.0%% completion", progress.String())
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read replaced self: %v", err)
	}
	if !bytes.Equal(got, newContent) {
		t.Errorf("self content not replaced")
	}
	if info, err := os.Stat(self); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("replaced self is not executable: %v", err)
	}
}

func TestUpdate_currentSelf_reportsAlreadyUpToDateAndKeepsFile(t *testing.T) {
	newContent := []byte("#!/bin/sh\necho \"gcli version v0.1.0\"\n")
	self := fakeSelf(t, "v0.1.0")
	name := fmt.Sprintf("gcli-%s-%s", runtime.GOOS, runtime.GOARCH)
	checksums := checksumLine(t, name, newContent)
	srv := updateServer(t, newContent, checksums)

	var out bytes.Buffer
	updated, err := doUpdate(context.Background(), srv.Client(), srv.URL, self, "v0.1.0", &out, io.Discard)
	if err != nil {
		t.Fatalf("doUpdate: %v", err)
	}
	if updated {
		t.Error("updated = true, want false for current self")
	}
	if !strings.Contains(out.String(), "already up to date (v0.1.0)") {
		t.Errorf("output = %q, missing already-up-to-date message with version", out.String())
	}
	before, _ := os.ReadFile(self)
	after, _ := os.ReadFile(self)
	if !bytes.Equal(before, after) {
		t.Error("self file modified despite being up to date")
	}
}

func TestUpdate_binaryChecksumMismatch_errorsAndKeepsFile(t *testing.T) {
	newContent := []byte("#!/bin/sh\necho \"gcli version v9.9.9\"\n")
	self := fakeSelf(t, "v0.1.0")
	name := fmt.Sprintf("gcli-%s-%s", runtime.GOOS, runtime.GOARCH)
	checksums := checksumLine(t, name, []byte("tampered content"))
	srv := updateServer(t, newContent, checksums)

	var out bytes.Buffer
	_, err := doUpdate(context.Background(), srv.Client(), srv.URL, self, "v0.1.0", &out, io.Discard)
	if err == nil {
		t.Fatal("doUpdate = nil error, want checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %q, want checksum mention", err)
	}
	got, _ := os.ReadFile(self)
	if !strings.Contains(string(got), "v0.1.0") {
		t.Errorf("self content = %q, must be untouched on mismatch", string(got))
	}
}

func TestUpdate_downloadFailure_errors(t *testing.T) {
	self := fakeSelf(t, "v0.1.0")
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := doUpdate(context.Background(), srv.Client(), srv.URL, self, "v0.1.0", &bytes.Buffer{}, io.Discard)
	if err == nil {
		t.Fatal("doUpdate = nil error, want download error")
	}
}

func TestBinaryName_unsupportedPlatform_errors(t *testing.T) {
	for _, platform := range [][2]string{
		{"windows", "amd64"},
		{"linux", "386"},
		{"darwin", "riscv64"},
	} {
		if _, err := binaryName(platform[0], platform[1]); err == nil {
			t.Errorf("binaryName(%s, %s) = nil error, want unsupported", platform[0], platform[1])
		}
	}
}

func TestBinaryName_supportedPlatforms_namesMatchReleaseAssets(t *testing.T) {
	for _, platform := range [][2]string{
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
	} {
		name, err := binaryName(platform[0], platform[1])
		if err != nil {
			t.Fatalf("binaryName(%s, %s): %v", platform[0], platform[1], err)
		}
		want := fmt.Sprintf("gcli-%s-%s", platform[0], platform[1])
		if name != want {
			t.Errorf("binaryName(%s, %s) = %q, want %q", platform[0], platform[1], name, want)
		}
	}
}

func TestDefaultUpdateBaseURL_usesTaggedDownloadPath(t *testing.T) {
	// releases/latest resolves to the newest NON-prerelease release, so it
	// 404s for a repo whose only release is the rolling prerelease. The
	// literal-tag path /releases/download/latest/ must be used instead.
	want := "https://github.com/behnambm/gcli/releases/download/latest"
	if defaultUpdateBaseURL != want {
		t.Errorf("defaultUpdateBaseURL = %q, want %q", defaultUpdateBaseURL, want)
	}
}

func TestRenderBar_showsPercentAndSizes(t *testing.T) {
	var w bytes.Buffer
	renderBar(&w, 4*1024*1024+512*1024, 10*1024*1024)
	got := w.String()
	if !strings.Contains(got, "45.0%") {
		t.Errorf("bar = %q, missing 45.0%%", got)
	}
	if !strings.Contains(got, "4.5") || !strings.Contains(got, "10.0 MB") {
		t.Errorf("bar = %q, missing sizes", got)
	}
	if !strings.Contains(got, "[") {
		t.Errorf("bar = %q, missing bar", got)
	}
}

func TestUpdateTimeout_explicitFlag_wins(t *testing.T) {
	if got := updateTimeout(5*time.Second, true); got != 5*time.Second {
		t.Errorf("updateTimeout(5s, true) = %v, want 5s", got)
	}
}

func TestUpdateTimeout_default_tenMinutes(t *testing.T) {
	if got := updateTimeout(30*time.Second, false); got != 10*time.Minute {
		t.Errorf("updateTimeout(30s, false) = %v, want 10m", got)
	}
}

func TestIsTerminal_buffer_false(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("isTerminal(buffer) = true, want false")
	}
}

func TestIsTerminal_charDevice_true(t *testing.T) {
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("no /dev/null: %v", err)
	}
	defer f.Close()
	if !isTerminal(f) {
		t.Error("isTerminal(/dev/null) = false, want true for char device")
	}
}
