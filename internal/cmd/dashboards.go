package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	dashboardsCmd.Flags().StringVar(&flagDashGet, "get", "", "fetch full JSON of dashboard by uid")
	dashboardsCmd.Flags().StringVar(&flagDashExport, "export", "", "write dashboard JSON to file (provisioning format)")
	rootCmd.AddCommand(dashboardsCmd)
}

var dashboardsCmd = &cobra.Command{
	Use:   "dashboards [search-query]",
	Short: "Search dashboards, or fetch full JSON with --get",
	Example: `  gcli dashboards account
  gcli dashboards --get 8GbEch5Mz
  gcli dashboards --get 8GbEch5Mz --export account.json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagDashExport != "" && flagDashGet == "" {
			return fmt.Errorf("--export requires --get <uid>")
		}
		if flagDashGet != "" {
			return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
				raw, err := c.DashboardJSON(ctx, flagDashGet)
				if err != nil {
					return result{}, err
				}
				if flagDashExport != "" {
					var envelope map[string]json.RawMessage
					if err := json.Unmarshal(raw, &envelope); err != nil {
						return result{}, err
					}
					dash, ok := envelope["dashboard"]
					if !ok {
						return result{}, fmt.Errorf("dashboard JSON has no dashboard object")
					}
					var doc map[string]any
					if err := json.Unmarshal(dash, &doc); err != nil {
						return result{}, err
					}
					// provisioning format: no id/version (server-assigned)
					delete(doc, "id")
					delete(doc, "version")
					indented, err := json.MarshalIndent(doc, "", "  ")
					if err != nil {
						return result{}, err
					}
					if err := os.WriteFile(flagDashExport, indented, 0o644); err != nil {
						return result{}, err
					}
					return result{raw: []byte("written: " + flagDashExport)}, nil
				}
				var indented bytes.Buffer
				if err := json.Indent(&indented, raw, "", "  "); err != nil {
					return result{}, err
				}
				return result{raw: indented.Bytes()}, nil
			})
		}
		q := ""
		if len(args) == 1 {
			q = args[0]
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dbs, err := c.SearchDashboards(ctx, q)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Title", Values: dbsTitles(dbs)},
					{Name: "UID", Values: dbsUIDs(dbs)},
					{Name: "Type", Values: dbsTypes(dbs)},
					{Name: "Folder", Values: dbsFolders(dbs)},
				}}},
			}}, nil
		})
	},
}

func dbsTitles(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.Title
	}
	return out
}
func dbsUIDs(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.UID
	}
	return out
}
func dbsTypes(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.Type
	}
	return out
}
func dbsFolders(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.FolderTitle
	}
	return out
}
