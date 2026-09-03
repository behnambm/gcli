package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

func validateIdent(s string) error {
	if !identRe.MatchString(s) {
		return fmt.Errorf("invalid identifier %q: must match %s", s, identRe.String())
	}
	return nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func introspect(ctx context.Context, c *api.Client, dsName, rawSQL, query string) (frames.Result, error) {
	ds, err := c.ResolveDatasource(ctx, dsName)
	if err != nil {
		return frames.Result{}, err
	}
	body := map[string]any{"rawSql": rawSQL, "format": "table"}
	res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, "now-1h", "now")
	res.Meta.Query = query
	res.Meta.Datasource = ds.Name
	return res, err
}

func init() {
	rootCmd.AddCommand(tablesCmd)
	rootCmd.AddCommand(columnsCmd)
}

var tablesCmd = &cobra.Command{
	Use:     "tables <sql-datasource>",
	Short:   "List tables in a SQL datasource",
	Example: "  gcli tables Billing",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dsName, err := dsArg(args[:1])
			if err != nil {
				return result{}, err
			}
			res, err := introspect(ctx, c, dsName,
				"SELECT table_name AS table FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_name",
				"information_schema.tables")
			return result{res: res}, err
		})
	},
}

var columnsCmd = &cobra.Command{
	Use:     "columns <sql-datasource> <table>",
	Short:   "List columns of a table in a SQL datasource",
	Example: "  gcli columns Billing invoices",
	Args:    cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		table := args[len(args)-1]
		if err := validateIdent(table); err != nil {
			return err
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			var dsName string
			if len(args) == 2 {
				dsName = args[0]
			} else {
				d, err := dsArg(nil)
				if err != nil {
					return result{}, err
				}
				dsName = d
			}
			res, err := introspect(ctx, c, dsName,
				"SELECT column_name AS column, data_type AS type, is_nullable AS nullable FROM information_schema.columns WHERE table_name = "+quoteIdent(table)+" ORDER BY ordinal_position",
				"information_schema.columns: "+table)
			return result{res: res}, err
		})
	},
}
