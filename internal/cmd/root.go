package cmd

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagURL     string
	flagToken   string
	flagOutput  string
	flagTimeout time.Duration
	flagNoColor bool
	flagVerbose bool
	flagProfile string
)

// toolVersion is stamped at build time:
//
//	go build -ldflags "-X gcli/internal/cmd.toolVersion=v0.2.0"
var toolVersion = "dev"

var rootCmd = &cobra.Command{
	Use:           "gcli",
	Short:         "Read-only Grafana CLI",
	Long:          "gcli reads metrics, logs, SQL and Grafana state from a Grafana instance. Run `gcli help` for the full guide.",
	Version:       toolVersion,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagURL, "url", "", "Grafana URL (overrides GRAFANA_URL)")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "service-account token (overrides GRAFANA_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&flagProfile, "profile", "", "profile name from profiles.yaml (overrides GCLI_PROFILE and default:)")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "output format: table|json|csv (default table)")
	rootCmd.PersistentFlags().DurationVar(&flagTimeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable ANSI colors")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "dump HTTP requests/responses (token redacted)")
	rootCmd.PersistentFlags().IntVar(&flagFrame, "frame", 0, "CSV output: render frame at index N (default 0 = first)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrln("error:", err)
		if hint := hintOf(err); hint != "" {
			rootCmd.PrintErrln("hint:", hint)
		}
		os.Exit(exitCode(err))
	}
}
