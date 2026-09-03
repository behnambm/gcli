package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(alertCmd)
}

var alertCmd = &cobra.Command{
	Use:     "alert <name>",
	Short:   "Full detail of one alert rule (definition + current state)",
	Example: "  gcli alert 'High DB Connections [Metrics Zeta]'",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			d, err := c.AlertDetail(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if outputOptions(lastCfg).Output == "json" {
				b, _ := json.MarshalIndent(d, "", "  ")
				return result{raw: b}, nil
			}
			annKeys := make([]string, 0, len(d.Annotations))
			for k := range d.Annotations {
				annKeys = append(annKeys, k)
			}
			sort.Strings(annKeys)
			annParts := make([]string, 0, len(annKeys))
			for _, k := range annKeys {
				annParts = append(annParts, fmt.Sprintf("%s=%s", k, d.Annotations[k]))
			}
			keys := []any{"Name", "Folder", "Expr", "For", "Severity", "State", "ActiveAt", "Annotations"}
			vals := []any{d.Name, d.Folder, d.Expr, d.For, d.Severity, d.State, d.ActiveAt, strings.Join(annParts, ", ")}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", Query: d.Name},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Key", Values: keys},
					{Name: "Value", Values: vals},
				}}},
			}}, nil
		})
	},
}
