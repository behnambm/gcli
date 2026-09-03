package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	uninstallCmd.Flags().BoolVar(&flagUninstallYes, "yes", false, "skip the confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the gcli binary",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		self, err := selfExecutable()
		if err != nil {
			return err
		}
		return doUninstall(self, cmd.InOrStdin(), cmd.OutOrStdout(), flagUninstallYes)
	},
}

func doUninstall(selfPath string, in io.Reader, out io.Writer, force bool) error {
	if _, err := os.Stat(selfPath); err != nil {
		return fmt.Errorf("gcli binary not found at %s", selfPath)
	}
	if !force {
		fmt.Fprintf(out, "remove %s? [y/N] ", selfPath)
		line, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
		default:
			fmt.Fprintln(out, "not removed")
			return nil
		}
	}
	if err := os.Remove(selfPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s\n", selfPath)
	fmt.Fprintln(out, "note: shell completion scripts installed manually are left in place")
	return nil
}
