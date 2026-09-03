package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
	"github.com/behnambm/gcli/internal/render"
)

func init() {
	rootCmd.AddCommand(capabilitiesCmd)
}

var capabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Probe what this token can access (per command group)",
	Long:  "Runs one cheap probe per command group and reports OK / DENIED / ERROR. DENIED means the token role lacks the RBAC permission (or the endpoint is missing on this Grafana). Exit code is always 0.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			caps, err := c.Capabilities(ctx)
			if err != nil {
				return result{}, err
			}
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Group", Values: capGroups(caps)},
					{Name: "Status", Values: capStatuses(caps, color)},
					{Name: "Detail", Values: capDetails(caps)},
				}}},
			}}, nil
		})
	},
}

func capGroups(caps []api.Capability) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		out[i] = cp.Group
	}
	return out
}
func capStatuses(caps []api.Capability, color bool) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		c := "green"
		if cp.Status == "DENIED" {
			c = "yellow"
		} else if cp.Status == "ERROR" {
			c = "red"
		}
		out[i] = render.Colorize(cp.Status, c, color)
	}
	return out
}
func capDetails(caps []api.Capability) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		out[i] = cp.Detail
	}
	return out
}
