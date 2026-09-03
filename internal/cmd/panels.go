package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	panelsCmd.Flags().BoolVar(&flagPanelsQueries, "queries", false, "print one row per panel query (expr)")
	rootCmd.AddCommand(panelsCmd)
}

var panelsCmd = &cobra.Command{
	Use:     "panels <dashboard-uid>",
	Short:   "List panels of a dashboard with their queries",
	Example: `  gcli panels 8GbEch5Mz
  gcli panels 8GbEch5Mz --queries`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			pis, err := c.Panels(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if outputOptions(lastCfg).Output == "json" {
				b, _ := json.MarshalIndent(pis, "", "  ")
				return result{raw: b}, nil
			}
			if flagPanelsQueries {
				return result{res: frames.Result{
					Meta: frames.Meta{Datasource: "grafana", Query: args[0]},
					Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
						{Name: "Panel", Values: panelQueryPanels(pis)},
						{Name: "Query", Values: panelQueryExprs(pis)},
					}}},
				}}, nil
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", Query: args[0]},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Panel", Values: panelTitles(pis)},
					{Name: "Type", Values: panelTypes(pis)},
					{Name: "Datasource", Values: panelDatasources(pis)},
					{Name: "Queries", Values: panelQueryCounts(pis)},
				}}},
			}}, nil
		})
	},
}

func panelTitles(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Title
	}
	return out
}
func panelTypes(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Type
	}
	return out
}
func panelDatasources(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Datasource
	}
	return out
}
func panelQueryCounts(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = fmt.Sprintf("%d", len(p.Queries))
	}
	return out
}
func panelQueryPanels(pis []api.PanelInfo) []any {
	var out []any
	for _, p := range pis {
		for range p.Queries {
			out = append(out, p.Title)
		}
	}
	return out
}
func panelQueryExprs(pis []api.PanelInfo) []any {
	var out []any
	for _, p := range pis {
		for _, q := range p.Queries {
			out = append(out, q)
		}
	}
	return out
}
