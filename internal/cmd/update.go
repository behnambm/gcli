package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultUpdateBaseURL = "https://github.com/behnambm/gcli/releases/download/latest"

// selfExecutable is a var so tests can point the command at a fake binary.
var selfExecutable = os.Executable

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update gcli to the latest release",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		self, err := selfExecutable()
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: updateTimeout(flagTimeout, cmd.Flags().Changed("timeout"))}
		progress := cmd.ErrOrStderr()
		if !isTerminal(progress) {
			progress = io.Discard
		}
		_, err = doUpdate(cmd.Context(), client, updateBaseURL(), self, toolVersion, cmd.OutOrStdout(), progress)
		return err
	},
}

func updateBaseURL() string {
	if base := os.Getenv("GCLI_UPDATE_URL"); base != "" {
		return base
	}
	return defaultUpdateBaseURL
}

func updateTimeout(flag time.Duration, explicitlySet bool) time.Duration {
	if explicitlySet {
		return flag
	}
	return 10 * time.Minute
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func binaryName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf("unsupported OS %s (published binaries: darwin, linux)", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture %s (published binaries: amd64, arm64)", goarch)
	}
	return fmt.Sprintf("gcli-%s-%s", goos, goarch), nil
}

func doUpdate(ctx context.Context, client *http.Client, baseURL, selfPath, oldVersion string, out, progress io.Writer) (bool, error) {
	name, err := binaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return false, err
	}

	checksums, err := download(ctx, client, baseURL+"/checksums.txt", nil)
	if err != nil {
		return false, fmt.Errorf("download checksums: %w", err)
	}
	expected, err := hashFor(checksums, name)
	if err != nil {
		return false, err
	}

	self, err := os.ReadFile(selfPath)
	if err != nil {
		return false, fmt.Errorf("read current binary: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(self)) == expected {
		fmt.Fprintf(out, "already up to date (%s)\n", oldVersion)
		return false, nil
	}

	binary, err := download(ctx, client, baseURL+"/"+name, func(done, total int64) {
		renderBar(progress, done, total)
	})
	if err != nil {
		return false, fmt.Errorf("download binary: %w", err)
	}
	if progress != io.Discard {
		fmt.Fprintln(progress)
	}
	if fmt.Sprintf("%x", sha256.Sum256(binary)) != expected {
		return false, fmt.Errorf("checksum mismatch for %s", name)
	}

	tmp, err := os.CreateTemp(filepath.Dir(selfPath), ".gcli-update-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), selfPath); err != nil {
		return false, err
	}

	version, err := exec.CommandContext(ctx, selfPath, "--version").Output()
	if err != nil {
		fmt.Fprintln(out, "updated to latest")
		return true, nil
	}
	fields := strings.Fields(string(version))
	newVersion := oldVersion
	if len(fields) > 0 {
		newVersion = fields[len(fields)-1]
	}
	fmt.Fprintf(out, "updated: %s -> %s\n", oldVersion, newVersion)
	return true, nil
}

func renderBar(w io.Writer, done, total int64) {
	const barWidth = 28
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	filled := int(pct / 100 * barWidth)
	bar := strings.Repeat("=", filled)
	if filled < barWidth {
		bar += ">"
	}
	bar += strings.Repeat(" ", barWidth-len(bar))
	if total > 0 {
		fmt.Fprintf(w, "\r[%s] %5.1f%% %5.1f/%5.1f MB",
			bar, pct, float64(done)/(1<<20), float64(total)/(1<<20))
	} else {
		fmt.Fprintf(w, "\r[%s] %5.1f MB", bar, float64(done)/(1<<20))
	}
}

func download(ctx context.Context, client *http.Client, url string, progress func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	if progress == nil {
		return io.ReadAll(resp.Body)
	}
	var body []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			progress(int64(len(body)), resp.ContentLength)
		}
		if err == io.EOF {
			return body, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func hashFor(checksums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s not found in checksums.txt", name)
}
