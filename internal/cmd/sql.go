package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/timeparse"
)

func init() {
	sqlCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	sqlCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(sqlCmd)
}

var sqlCmd = &cobra.Command{
	Use:   "sql <datasource> <query...>",
	Short: "Run a SQL query against a SQL datasource (PostgreSQL etc.)",
	Example: `  gcli sql Billing 'SELECT count(*) FROM invoices'
  gcli sql Billing 'SELECT * FROM events WHERE ts BETWEEN $__timeFrom AND $__timeTo' --from now-24h`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			var dsName, q string
			if len(args) >= 2 {
				dsName, q = args[0], strings.Join(args[1:], " ")
			} else {
				d, err := dsArg(nil)
				if err != nil {
					return result{}, err
				}
				dsName, q = d, strings.Join(args, " ")
			}
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			sql, err := substituteMacros(q)
			if err != nil {
				return result{}, err
			}
			body := map[string]any{
				"rawSql": sql,
				"format": "table",
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = q
			return result{res: res}, err
		})
	},
}

func substituteMacros(q string) (string, error) {
	if !strings.Contains(q, "$__timeFrom") && !strings.Contains(q, "$__timeTo") {
		return q, nil
	}
	now := time.Now()
	fromMS, err := timeparse.ParseToEpochMS(flagFrom, now)
	if err != nil {
		return "", fmt.Errorf("--from: %w", err)
	}
	toMS, err := timeparse.ParseToEpochMS(flagTo, now)
	if err != nil {
		return "", fmt.Errorf("--to: %w", err)
	}
	q = strings.ReplaceAll(q, "$__timeFrom", strconv.FormatInt(fromMS, 10))
	q = strings.ReplaceAll(q, "$__timeTo", strconv.FormatInt(toMS, 10))
	return q, nil
}
