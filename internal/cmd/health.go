package cmd

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
	"github.com/behnambm/gcli/internal/render"
)

func init() {
	rootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Grafana health + per-datasource health probe",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			version, stats, err := c.Health(ctx)
			if err != nil {
				return result{}, err
			}
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", Query: "grafana " + version},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Datasource", Values: hsNames(stats)},
					{Name: "Type", Values: hsTypes(stats)},
					{Name: "Status", Values: hsStatuses(stats, color)},
					{Name: "Message", Values: hsMessages(stats)},
				}}},
			}}, nil
		})
	},
}

func hsNames(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}
func hsTypes(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Type
	}
	return out
}
func hsStatuses(s []api.HealthStatus, color bool) []any {
	out := make([]any, len(s))
	for i, x := range s {
		c := "green"
		if strings.HasPrefix(x.Status, "denied") {
			c = "yellow"
		} else if x.Status != "OK" {
			c = "red"
		}
		out[i] = render.Colorize(x.Status, c, color)
	}
	return out
}
func hsMessages(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Message
	}
	return out
}
