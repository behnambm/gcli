package cmd

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed help.txt
var helpText string

func init() {
	rootCmd.SetHelpCommand(helpCmd)
}

var helpCmd = &cobra.Command{
	Use:   "help [command]",
	Short: "Full setup and usage guide",
	Long:  "Prints the complete gcli guide: setup, env vars, token creation, every command with examples, output formats, exit codes, permission notes. With a command name, shows that command's help.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			for _, c := range rootCmd.Commands() {
				if c.Name() == args[0] {
					return c.Help()
				}
			}
			return fmt.Errorf("unknown command %q — run `gcli help` for the full guide", args[0])
		}
		fmt.Fprint(cmd.OutOrStdout(), helpText)
		return nil
	},
}
