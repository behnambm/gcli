package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(labelsCmd)
	rootCmd.AddCommand(valuesCmd)
}

func requirePromType(ds api.Datasource) error {
	if ds.Type != "prometheus" && ds.Type != "victoriametrics-datasource" {
		return fmt.Errorf("datasource %q has type %q, not a Prometheus-type datasource", ds.Name, ds.Type)
	}
	return nil
}

var labelsCmd = &cobra.Command{
	Use:   "labels <datasource> [metric]",
	Short: "List label names (optionally for one metric)",
	Example: `  gcli labels Universal
  gcli labels Universal 'http_requests_total'`,
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
			if len(args) == 2 {
				params.Add("match[]", args[1])
			}
			raw, err := c.ProxyGet(ctx, ds.UID, "/api/v1/labels", params)
			if err != nil {
				return result{}, err
			}
			names, err := api.ParseLabelNames(raw)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta:   frames.Meta{Datasource: ds.Name},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{{Name: "Label", Values: toAny(names)}}}},
			}}, nil
		})
	},
}

var valuesCmd = &cobra.Command{
	Use:   "values <datasource> <label> [metric]",
	Short: "List values for a label",
	Example: `  gcli values Universal job
  gcli values Universal job 'http_requests_total'`,
	Args: cobra.RangeArgs(1, 3),
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
			var label, metric string
			if len(args) >= 2 {
				label = args[1]
			} else {
				label = args[0]
			}
			if len(args) == 3 {
				metric = args[2]
			}
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			if err := requirePromType(ds); err != nil {
				return result{}, err
			}
			params := url.Values{}
			if len(args) == 3 {
				params.Add("match[]", metric)
			}
			raw, err := c.ProxyGet(ctx, ds.UID, "/api/v1/label/"+url.PathEscape(label)+"/values", params)
			if err != nil {
				return result{}, err
			}
			vals, err := api.ParseLabelNames(raw)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta:   frames.Meta{Datasource: ds.Name},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{{Name: label, Values: toAny(vals)}}}},
			}}, nil
		})
	},
}
