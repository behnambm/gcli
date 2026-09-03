package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
)

func init() {
	promCmd.Flags().StringVar(&flagStep, "step", "", "range query step (e.g. 1m, 5m, 1h); empty = instant query")
	promCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	promCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(promCmd)
}

var promCmd = &cobra.Command{
	Use:   "prom <datasource> <promql>",
	Short: "Run a PromQL query against a Prometheus-type datasource",
	Example: `  gcli prom Universal 'sum(rate(http_requests_total[5m])) by (job)' --step 1m
  gcli prom 'Metrics Zeta' 'count(up)'`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
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
				return result{}, fmt.Errorf("missing <promql> query — pass the query argument, e.g. gcli prom 'count(up)'")
			}
			query := args[len(args)-1]
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			if ds.Type != "prometheus" && ds.Type != "victoriametrics-datasource" {
				return result{}, fmt.Errorf("datasource %q has type %q, not a Prometheus-type datasource", ds.Name, ds.Type)
			}
			body := map[string]any{
				"expr":    query,
				"instant": flagStep == "",
			}
			if flagStep != "" {
				body["range"] = true
				body["interval"] = flagStep
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = query
			return result{res: res}, err
		})
	},
}
