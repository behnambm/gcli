package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
)

var (
	flagQueryJSON string
	flagFrom      string
	flagTo        string
)

// Flag vars for commands landed in later tasks (9–14). Declared here with
// zero-value defaults so runCommand's flag-reset block compiles; each task
// binds its flags to these vars in its own init().
var (
	flagStep           string
	flagLogsMode       string
	flagLogsLimit      int
	flagFiring         bool
	flagDashGet        string
	flagDashExport     string
	flagAnnTags        string
	flagAnnDashboard   string
	flagAnnFrom        string
	flagAnnTo          string
	flagFrame          int
	flagMetricsLimit   int
	flagMetricsPattern string
	flagPanelsQueries  bool
	flagUninstallYes   bool
)

func init() {
	queryCmd.Flags().StringVar(&flagQueryJSON, "json", "", "raw query-object JSON, or @file.json")
	queryCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time (now-1h, RFC3339)")
	queryCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	_ = queryCmd.MarkFlagRequired("json")
	rootCmd.AddCommand(queryCmd)
}

var queryCmd = &cobra.Command{
	Use:   "query <datasource-uid-or-name>",
	Short: "Run a raw datasource query (works with ANY datasource type)",
	Example: `  gcli query Metrics-Alpha --json '{"expr":"count(up)","instant":true}'
  gcli query PostgreSQL-Metrics --json @q.json --from now-24h`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readBody(flagQueryJSON)
		if err != nil {
			return err
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dsName, err := dsArg(args)
			if err != nil {
				return result{}, err
			}
			ds, err := c.ResolveDatasource(ctx, dsName)
			if err != nil {
				return result{}, err
			}
			reqs, err := buildQueryReqs([]byte(raw), ds)
			if err != nil {
				return result{}, err
			}
			res, err := c.DSQuery(ctx, ds.Type, reqs, flagFrom, flagTo)
			return result{res: res}, err
		})
	},
}

func readBody(s string) (string, error) {
	if strings.HasPrefix(s, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return "", fmt.Errorf("read --json file: %w", err)
		}
		return string(b), nil
	}
	return s, nil
}

// buildQueryReqs parses --json as one query object or an array of query
// objects, preserving user-provided refId/datasource and injecting the
// resolved datasource where absent.
func buildQueryReqs(rawBytes []byte, ds api.Datasource) ([]api.DSQueryReq, error) {
	var raws []map[string]any
	if err := json.Unmarshal(rawBytes, &raws); err != nil {
		var single map[string]any
		if err2 := json.Unmarshal(rawBytes, &single); err2 != nil {
			return nil, fmt.Errorf("invalid --json: must be one query object or an array of query objects: %w", err2)
		}
		raws = []map[string]any{single}
	}
	reqs := make([]api.DSQueryReq, 0, len(raws))
	for i, q := range raws {
		refID, _ := q["refId"].(string)
		if refID == "" {
			if len(raws) == 1 {
				refID = "A"
			} else {
				refID = fmt.Sprintf("Q%d", i+1)
			}
		}
		dsRef := api.DatasourceRef{Type: ds.Type, UID: ds.UID}
		if qds, ok := q["datasource"].(map[string]any); ok {
			if t, _ := qds["type"].(string); t != "" {
				dsRef.Type = t
			}
			if u, _ := qds["uid"].(string); u != "" {
				dsRef.UID = u
			}
		}
		delete(q, "refId")
		delete(q, "datasource")
		reqs = append(reqs, api.DSQueryReq{RefID: refID, Datasource: dsRef, Body: q})
	}
	return reqs, nil
}
