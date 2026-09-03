package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Grafana version + build info",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			v, err := c.Version(ctx)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Key", Values: []any{"version", "commit", "database"}},
					{Name: "Value", Values: []any{v["version"], v["commit"], v["database"]}},
				}}},
			}}, nil
		})
	},
}
