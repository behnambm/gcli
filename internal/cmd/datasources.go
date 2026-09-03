package cmd

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(datasourcesCmd)
}

var datasourcesCmd = &cobra.Command{
	Use:   "datasources",
	Short: "List datasources this token can access",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			if outputOptions(lastCfg).Output == "json" {
				raw, err := c.DatasourcesRaw(ctx)
				if err != nil {
					return result{}, err
				}
				var buf bytes.Buffer
				if err := json.Indent(&buf, raw, "", "  "); err != nil {
					return result{}, err
				}
				return result{raw: buf.Bytes()}, nil
			}
			dss, err := c.Datasources(ctx)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Name", Values: namesOf(dss)},
					{Name: "UID", Values: uidsOf(dss)},
					{Name: "Type", Values: typesOf(dss)},
					{Name: "URL", Values: urlsOf(dss)},
					{Name: "Default", Values: defaultsOf(dss)},
				}}},
			}}, nil
		})
	},
}

func namesOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.Name
	}
	return out
}
func uidsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.UID
	}
	return out
}
func typesOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.Type
	}
	return out
}
func urlsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.URL
	}
	return out
}
func defaultsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = ""
		if d.IsDefault {
			out[i] = "yes"
		}
	}
	return out
}
