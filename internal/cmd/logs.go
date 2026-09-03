package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
)

func init() {
	logsCmd.Flags().StringVar(&flagLogsMode, "mode", "range", "query mode: instant|range|stats")
	logsCmd.Flags().IntVar(&flagLogsLimit, "limit", 50, "max log lines (sent as JSON number — string is silently ignored upstream)")
	logsCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	logsCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs <datasource> <logsql>",
	Short: "Query a VictoriaLogs datasource (LogsQL)",
	Example: `  gcli logs "Logs" '{app="acme-pay"} |= "error"' --limit 100
  gcli logs "Logs" '* | stats by (app) count() rows' --mode stats`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch flagLogsMode {
		case "instant", "range", "stats":
		default:
			return fmt.Errorf("invalid --mode %q: must be instant, range or stats", flagLogsMode)
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			var dsName string
			if len(args) >= 2 {
				dsName = args[0]
			} else {
				d, err := dsArg(nil)
				if err != nil {
					return result{}, err
				}
				dsName = d
			}
			if len(args) == 0 {
				return result{}, fmt.Errorf("missing <logsql> query — pass the query argument")
			}
			query := args[len(args)-1]
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			body := map[string]any{
				"expr":      query,
				"queryType": flagLogsMode,
				"limit":     flagLogsLimit,
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = query
			return result{res: res}, err
		})
	},
}
