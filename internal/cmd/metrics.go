package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	metricsCmd.Flags().IntVar(&flagMetricsLimit, "limit", 200, "max metric names (passed as ?limit= to the datasource)")
	metricsCmd.Flags().StringVar(&flagMetricsPattern, "pattern", "", "substring filter (case-insensitive)")
	rootCmd.AddCommand(metricsCmd)
}

var metricsCmd = &cobra.Command{
	Use:   "metrics <datasource> [pattern]",
	Short: "List metric names from a Prometheus-type datasource",
	Example: `  gcli metrics Universal 'http_'
  gcli metrics 'Metrics Zeta' --limit 5000`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dsName, err := dsArg(args[:1])
			if err != nil {
				return result{}, err
			}
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			if err := requirePromType(ds); err != nil {
				return result{}, err
			}
			params := url.Values{}
			params.Set("limit", fmt.Sprintf("%d", flagMetricsLimit))
			raw, err := c.ProxyGet(ctx, ds.UID, "/api/v1/label/__name__/values", params)
			if err != nil {
				return result{}, err
			}
			names, err := api.ParseLabelNames(raw)
			if err != nil {
				return result{}, err
			}
			pattern := flagMetricsPattern
			if pattern == "" && len(args) == 2 {
				pattern = args[1]
			}
			if pattern != "" {
				names = filterNames(names, pattern)
			}
			return result{res: frames.Result{
				Meta:   frames.Meta{Datasource: ds.Name, Query: pattern},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{{Name: "Metric", Values: toAny(names)}}}},
			}}, nil
		})
	},
}

func filterNames(names []string, pattern string) []string {
	p := strings.ToLower(pattern)
	out := names[:0]
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), p) {
			out = append(out, n)
		}
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
