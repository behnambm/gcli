package cmd

import (
	"context"
	"sort"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
	"github.com/behnambm/gcli/internal/render"
)

func init() {
	alertsCmd.Flags().BoolVar(&flagFiring, "firing", false, "only show Alerting/Pending/NoData/Error alerts")
	rootCmd.AddCommand(alertsCmd)
}

var alertsCmd = &cobra.Command{
	Use:     "alerts",
	Short:   "Current alert states (unified alerting)",
	Example: "  gcli alerts --firing",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			rows, err := c.Alerts(ctx)
			if err != nil {
				return result{}, err
			}
			if flagFiring {
				rows = filterFiring(rows)
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Name", Values: alertNames(rows)},
					{Name: "State", Values: alertStates(rows, color)},
					{Name: "Severity", Values: alertSeverities(rows)},
					{Name: "Folder", Values: alertFolders(rows)},
					{Name: "ActiveAt", Values: alertActiveAts(rows)},
				}}},
			}}, nil
		})
	},
}

func filterFiring(rows []api.AlertRow) []api.AlertRow {
	out := rows[:0]
	for _, r := range rows {
		switch r.Core {
		case "Alerting", "Pending", "NoData", "Error":
			out = append(out, r)
		}
	}
	return out
}

func alertStates(rows []api.AlertRow, color bool) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		c := "green"
		switch r.Core {
		case "Alerting", "NoData", "Error":
			c = "red"
		case "Pending":
			c = "yellow"
		}
		out[i] = render.Colorize(r.State, c, color)
	}
	return out
}

func alertNames(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
func alertSeverities(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Severity
	}
	return out
}
func alertFolders(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Folder
	}
	return out
}
func alertActiveAts(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.ActiveAt
	}
	return out
}
