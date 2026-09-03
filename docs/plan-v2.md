# gcli v2 — Features + Gap Fixes + Housekeeping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 4 features (metrics discovery, SQL introspection, alert detail, panel mining), close the 6 deferred spec gaps, and do housekeeping (tool version, Makefile, dead code, ledger minors, docs) on the working gcli.

**Architecture:** All additions are bounded extensions of the existing gcli patterns: new commands build `frames.Result` and go through `run()`; new API methods wrap existing `Get`/`Post`/`DSQuery`; tests extend the shared `fakeGrafana` fixture. No new dependencies, no architectural changes.

**Tech Stack:** Go 1.27, stdlib + cobra (unchanged from v1).

**Spec:** v1 spec [design-spec.md](design-spec.md) + approved v2 scope (brainstormed 2026-08-30: features list, gap-fix list, housekeeping list). v1 plan: [plan-v1.md](plan-v1.md).

## Global Constraints

- Module `gcli`, Go 1.27, dir `grafana-cli/`; dep: cobra ONLY.
- All commands read-only: GET + ds/query POST only; NO writes through the API ever.
- Datasource resolution: `ResolveDatasource(ctx, nameOrUID)` (exact uid, then case-insensitive name) — reuse, do not re-implement.
- Commands render via `run(cmd, fn func(ctx context.Context, c *api.Client) (result, error)) error`; `result{res frames.Result, raw []byte}`; `lastCfg` holds resolved config for coloring (`outputOptions(lastCfg)`).
- Flag vars are forward-declared in `cmd/query.go` vars block — BIND new flags to new vars there, never redeclare existing ones.
- Tests: hermetic httptest via shared `fakeGrafana` (in `cmd/query_test.go`, extended per task) + `runCommand`/`setEnv`/`runCommandErr` helpers; flag vars reset in the reset block.
- Exit codes unchanged: 1 config, 2 network, 3 401, 4 403/404, 5 query error.
- User files `firecrawl-local/scripts/save-page.py` + `research/research-result.md` are in the tree — never stage them. Stage task files by name.
- Commits conventional (`feat(gcli): ...`, `fix(gcli): ...`, `docs(gcli): ...`, `chore(gcli): ...`), never push.

---

### Task 1: ProxyGet API helper + `metrics` command

**Files:**
- Create: `grafana-cli/internal/api/proxy.go`
- Create: `grafana-cli/internal/cmd/metrics.go`
- Modify: `grafana-cli/internal/cmd/query.go` (vars block: add `flagMetricsLimit int`)
- Modify: `grafana-cli/internal/cmd/query_test.go` (fakeGrafana proxy branch, reset block)
- Test: `grafana-cli/internal/cmd/metrics_test.go`

**Interfaces:**
- Consumes: `Client.Get`, `ResolveDatasource`, `run`, `lastCfg`, `frames`.
- Produces: `(*Client).ProxyGet(ctx, dsUID, path string, params url.Values) (json.RawMessage, error)`; `parseLabelNames(raw json.RawMessage) ([]string, error)` (helper reused by Task 2).

- [ ] **Step 1: Write failing tests**

`grafana-cli/internal/cmd/metrics_test.go`:
```go
package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func TestMetricsCommandListsAndFilters(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "metrics", "Metrics Iota", "up")
	if !strings.Contains(out, "up") {
		t.Errorf("output = %q, want metric name", out)
	}
	if strings.Contains(out, "node_memory") {
		t.Errorf("pattern filter failed:\n%s", out)
	}
}

func TestMetricsCommandRequiresPromType(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out, err := runCommandErr(t, "metrics", "PostgreSQL Metrics")
	if err == nil {
		t.Fatal("want error for non-prometheus datasource")
	}
	_ = out
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestMetrics`
Expected: FAIL — "unknown command \"metrics\"" / `runCommandErr` undefined (implement `runCommandErr` below in this task; before it exists the compile fails).

- [ ] **Step 3: Implement API helper**

`grafana-cli/internal/api/proxy.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ProxyGet performs a read-only GET through the Grafana datasource proxy
// (path is appended to /api/datasources/proxy/uid/<uid>) and returns the
// raw JSON response body.
func (c *Client) ProxyGet(ctx context.Context, dsUID, path string, params url.Values) (json.RawMessage, error) {
	q := ""
	if len(params) > 0 {
		q = "?" + params.Encode()
	}
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/datasources/proxy/uid/"+url.PathEscape(dsUID)+path+q, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// parseLabelNames parses the Prometheus-compatible label API envelope:
// {"status":"success","data":["a","b"]}.
func parseLabelNames(raw json.RawMessage) ([]string, error) {
	var env struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse label API response: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("label API returned status %q", env.Status)
	}
	return env.Data, nil
}
```

- [ ] **Step 4: Implement command + helpers**

`grafana-cli/internal/cmd/metrics.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
)

func init() {
	metricsCmd.Flags().IntVar(&flagMetricsLimit, "limit", 200, "max metric names (passed as ?limit= to the datasource)")
	metricsCmd.Flags().StringVar(&flagMetricsPattern, "pattern", "", "substring filter (case-insensitive)")
	rootCmd.AddCommand(metricsCmd)
}

var metricsCmd = &cobra.Command{
	Use:     "metrics <datasource> [pattern]",
	Short:   "List metric names from a Prometheus-type datasource",
	Example: `  gcli metrics Metrics Iota 'http_'
  gcli metrics Metrics Zeta --limit 5000`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if ds.Type != "prometheus" && ds.Type != "victoriametrics-datasource" {
				return result{}, fmt.Errorf("datasource %q has type %q, not a Prometheus-type datasource", ds.Name, ds.Type)
			}
			params := url.Values{}
			params.Set("limit", fmt.Sprintf("%d", flagMetricsLimit))
			raw, err := c.ProxyGet(ctx, ds.UID, "/api/v1/label/__name__/values", params)
			if err != nil {
				return result{}, err
			}
			names, err := parseLabelNames(raw)
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
```

`query.go` vars block — add:
```go
	flagMetricsLimit   int
	flagMetricsPattern string
```

`query_test.go`:
- fakeGrafana new branch (before the default 404):
```go
	case "/api/datasources/proxy/uid/u1/api/v1/label/__name__/values":
		w.Write([]byte(`{"status":"success","data":["up","node_memory_MemTotal_bytes","http_requests_total"]}`))
```
- reset block additions:
```go
	flagMetricsLimit = 200
	flagMetricsPattern = ""
```
- new helper (real implementation — returns the error instead of t.Fatal):
```go
func runCommandErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"--no-color"}, args...))
	err := rootCmd.Execute()
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	return buf.String(), err
}

func resetAllFlags() {
	flagStep = ""
	flagLogsMode = "range"
	flagLogsLimit = 50
	flagFrom = "now-1h"
	flagTo = "now"
	flagQueryJSON = ""
	flagFiring = false
	flagDashGet = ""
	flagDashExport = ""
	flagAnnTags = ""
	flagAnnDashboard = ""
	flagAnnFrom = "now-24h"
	flagAnnTo = "now"
	flagOutput = ""
	flagVerbose = false
	flagNoColor = false
	flagFrame = 0
	flagMetricsLimit = 200
	flagMetricsPattern = ""
}
```
Refactor existing `runCommand` to call `resetAllFlags()` instead of its inline reset block (behavior identical).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/api/proxy.go grafana-cli/internal/cmd/metrics.go grafana-cli/internal/cmd/query.go grafana-cli/internal/cmd/query_test.go grafana-cli/internal/cmd/metrics_test.go
git commit -m "feat(gcli): metrics command — name discovery via datasource proxy"
```

---

### Task 2: `labels` + `values` commands

**Files:**
- Create: `grafana-cli/internal/cmd/labels.go`
- Test: `grafana-cli/internal/cmd/labels_test.go`

**Interfaces:**
- Consumes: `ProxyGet`, `parseLabelNames` (Task 1), `ResolveDatasource`, `run`, `frames`, `filterNames`, `toAny`.
- Produces: nothing beyond the two commands.

- [ ] **Step 1: Write failing tests**

`grafana-cli/internal/cmd/labels_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestLabelsCommandNoMetric(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "labels", "Metrics Iota")
	for _, want := range []string{"job", "instance"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want label %q", out, want)
		}
	}
}

func TestLabelsCommandWithMetric(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	// match[] must reach the proxy: fake captures RawQuery (extend fakeGrafana below)
	runCommand(t, "labels", "Metrics Iota", "up")
	if !strings.Contains(lastProxyQuery, "match%5B%5D=up") && !strings.Contains(lastProxyQuery, "match%5B%5D=up") {
		t.Errorf("proxy query = %q, want match[]=up", lastProxyQuery)
	}
}

func TestValuesCommand(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "values", "Metrics Iota", "job")
	if !strings.Contains(out, "api") {
		t.Errorf("output = %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run "TestLabels|TestValues"`
Expected: FAIL — unknown command / `lastProxyQuery` undefined

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/labels.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
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
	Use:     "labels <datasource> [metric]",
	Short:   "List label names (optionally for one metric)",
	Example: `  gcli labels Metrics Iota
  gcli labels Metrics Iota 'http_requests_total'`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
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
			names, err := parseLabelNames(raw)
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
	Use:     "values <datasource> <label> [metric]",
	Short:   "List values for a label",
	Example: `  gcli values Metrics Iota job
  gcli values Metrics Iota job 'http_requests_total'`,
	Args: cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if err := requirePromType(ds); err != nil {
				return result{}, err
			}
			params := url.Values{}
			if len(args) == 3 {
				params.Add("match[]", args[2])
			}
			raw, err := c.ProxyGet(ctx, ds.UID, "/api/v1/label/"+url.PathEscape(args[1])+"/values", params)
			if err != nil {
				return result{}, err
			}
			vals, err := parseLabelNames(raw)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta:   frames.Meta{Datasource: ds.Name},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{{Name: args[1], Values: toAny(vals)}}}},
			}}, nil
		})
	},
}
```

Note: `requirePromType` duplicates the check in metrics.go's RunE — refactor metrics.go to use it (delete the inline check).

`query_test.go`:
- package var: `var lastProxyQuery string`
- fakeGrafana: capture query on proxy branch:
```go
	if strings.HasPrefix(r.URL.Path, "/api/datasources/proxy/") {
		lastProxyQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/api/datasources/proxy/uid/u1/api/v1/label/__name__/values":
			w.Write([]byte(`{"status":"success","data":["up","node_memory_MemTotal_bytes","http_requests_total"]}`))
		case "/api/datasources/proxy/uid/u1/api/v1/labels":
			w.Write([]byte(`{"status":"success","data":["job","instance"]}`))
		case "/api/datasources/proxy/uid/u1/api/v1/label/job/values":
			w.Write([]byte(`{"status":"success","data":["api","worker"]}`))
		default:
			http.NotFound(w, r)
		}
		return
	}
```
(Replace the previous single proxy branch from Task 1 with this block.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/labels.go grafana-cli/internal/cmd/labels_test.go grafana-cli/internal/cmd/metrics.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): labels + values commands — label browsing via datasource proxy"
```

---

### Task 3: SQL introspection — `tables` + `columns` commands

**Files:**
- Create: `grafana-cli/internal/cmd/introspect.go`
- Test: `grafana-cli/internal/cmd/introspect_test.go`

**Interfaces:**
- Consumes: `DSQuery`, `ResolveDatasource`, `run`, `frames`.
- Produces: `validateIdent(s string) error` (regex `^[A-Za-z_][A-Za-z0-9_$]*$`), `quoteIdent(s string) string` (double-quote with `""` escaping).

- [ ] **Step 1: Write failing tests**

`grafana-cli/internal/cmd/introspect_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestTablesCommand(t *testing.T) {
	var gotRaw string
	srv := fakeGrafana(t, nil) // captures ds/query body via onQuery? use a local capture instead:
	_ = srv
	_ = gotRaw
	// (extend approach below — see Step 3 capture)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "tables", "PostgreSQL Metrics")
	if !strings.Contains(out, "invoices") {
		t.Errorf("output = %q", out)
	}
}

func TestColumnsCommandRejectsBadIdentifier(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "columns", "PostgreSQL Metrics", "invoices; DROP TABLE x")
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %v, want invalid-identifier error", err)
	}
}

func TestValidateIdent(t *testing.T) {
	for _, ok := range []string{"invoices", "Orders_2026", "a$b"} {
		if err := validateIdent(ok); err != nil {
			t.Errorf("validateIdent(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"x;drop", "1abc", "a b", `a"b`, ""} {
		if err := validateIdent(bad); err == nil {
			t.Errorf("validateIdent(%q): want error", bad)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run "TestTables|TestColumns|TestValidateIdent"`
Expected: FAIL — unknown command "tables"

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/introspect.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
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
	Example: "  gcli tables PostgreSQL Metrics",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			res, err := introspect(ctx, c, args[0],
				"SELECT table_name AS table FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_name",
				"information_schema.tables")
			return result{res: res}, err
		})
	},
}

var columnsCmd = &cobra.Command{
	Use:     "columns <sql-datasource> <table>",
	Short:   "List columns of a table in a SQL datasource",
	Example: "  gcli columns PostgreSQL Metrics invoices",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateIdent(args[1]); err != nil {
			return err
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			res, err := introspect(ctx, c, args[0],
				"SELECT column_name AS column, data_type AS type, is_nullable AS nullable FROM information_schema.columns WHERE table_name = "+quoteIdent(args[1])+" ORDER BY ordinal_position",
				"information_schema.columns: "+args[1])
			return result{res: res}, err
		})
	},
}
```

Capture the rawSql in tests — extend `fakeGrafana`'s `/api/ds/query` branch: it already decodes `gotBody` via `onQuery`; the local capture variant is not needed — instead assert via output. For the tables test, add a ds/query response keyed on rawSql presence: extend fakeGrafana ds/query branch:
```go
	case "/api/ds/query":
		if onQuery != nil {
			onQuery(t, r)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if q0, ok := body["queries"].([]any); ok && len(q0) > 0 {
			if m, ok := q0[0].(map[string]any); ok {
				if raw, _ := m["rawSql"].(string); strings.Contains(raw, "information_schema.tables") {
					w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"table","type":"string"}]},"data":{"values":[["invoices","users"]]}}]}}}`))
					return
				}
			}
		}
		w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/introspect.go grafana-cli/internal/cmd/introspect_test.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): tables + columns commands — SQL schema introspection"
```

---

### Task 4: `alert` detail command (ruler merge — closes gap #1)

**Files:**
- Modify: `grafana-cli/internal/api/alerts.go` (add AlertDetail + ruler types + merge)
- Create: `grafana-cli/internal/cmd/alert.go`
- Modify: `grafana-cli/internal/cmd/query_test.go` (fakeGrafana ruler branch)
- Test: `grafana-cli/internal/api/alerts_test.go` (extend), `grafana-cli/internal/cmd/alert_test.go`

**Interfaces:**
- Consumes: `Client.Get`, `Alerts` (Task 13 v1), `run`, `frames`.
- Produces: `api.AlertDetail{Name, Folder, Expr, For, Severity string; Annotations map[string]string; State, ActiveAt string}`; `(*Client).AlertDetail(ctx, name string) (AlertDetail, error)` — matches by `grafana_alert.title` OR `labels.alertname` (case-insensitive); state merged from `Alerts()` rows matched by name; error `alert %q not found` when no rule matches.

- [ ] **Step 1: Write failing tests**

Extend `grafana-cli/internal/api/alerts_test.go`:
```go
func TestAlertDetailFromRuler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ruler/grafana/api/v1/rules":
			w.Write([]byte(`{"Acme Pay":[{"name":"PostgreSQL Service","interval":"1m","rules":[{"expr":"sum by(pod) (pg_stat_activity_count{metrics-zeta-cluster=\"metrics-service\"}) > 900","for":"1m","labels":{"severity":"warning","target":"acme-credit"},"annotations":{"description":"PostgreSQL service has more than 900 connections"},"grafana_alert":{"id":41,"title":"High DB Connections [Metrics Zeta]"}}]}]}`))
		case "/api/prometheus/grafana/api/v1/rules":
			w.Write([]byte(`{"status":"success","data":{"groups":[{"name":"g","file":"Acme Pay","rules":[{"name":"High DB Connections [Metrics Zeta]","state":"inactive","alerts":[{"labels":{"alertname":"High DB Connections [Metrics Zeta]","grafana_folder":"Acme Pay"},"state":"Alerting","activeAt":"2026-08-30T10:00:00Z"}]}]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	d, err := c.AlertDetail(context.Background(), "high db connections [Metrics Zeta]")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "High DB Connections [Metrics Zeta]" || d.Folder != "Acme Pay" {
		t.Errorf("detail = %+v", d)
	}
	if d.Expr == "" || d.For != "1m" || d.Severity != "warning" {
		t.Errorf("detail = %+v", d)
	}
	if d.State != "Alerting" || d.ActiveAt == "" {
		t.Errorf("state merge failed: %+v", d)
	}
}

func TestAlertDetailNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ruler/grafana/api/v1/rules" {
			w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.AlertDetail(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found", err)
	}
}
```

`grafana-cli/internal/cmd/alert_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestAlertCommandPrintsDetail(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "alert", "High DB Connections")
	for _, want := range []string{"expr", "severity", "warning"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/ -run TestAlertDetail && go test ./internal/cmd/ -run TestAlert`
Expected: FAIL — "undefined: AlertDetail"

- [ ] **Step 3: Implement API**

Append to `grafana-cli/internal/api/alerts.go`:
```go
type AlertDetail struct {
	Name        string            `json:"name"`
	Folder      string            `json:"folder"`
	Expr        string            `json:"expr"`
	For         string            `json:"for"`
	Severity    string            `json:"severity"`
	Annotations map[string]string `json:"annotations,omitempty"`
	State       string            `json:"state,omitempty"`
	ActiveAt    string            `json:"activeAt,omitempty"`
}

type rulerGroup struct {
	Name  string      `json:"name"`
	Rules []rulerRule `json:"rules"`
}
type rulerRule struct {
	Expr         string            `json:"expr"`
	For          string            `json:"for"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	GrafanaAlert struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"grafana_alert"`
}

// AlertDetail returns one alert's rule definition (ruler endpoint) merged
// with its current state (rules endpoint). Name matches the rule title or
// the alertname label, case-insensitively.
func (c *Client) AlertDetail(ctx context.Context, name string) (AlertDetail, error) {
	var env map[string][]rulerGroup
	if err := c.Get(ctx, "/api/ruler/grafana/api/v1/rules", &env); err != nil {
		return AlertDetail{}, err
	}
	var d AlertDetail
	for folder, groups := range env {
		for _, g := range groups {
			for _, r := range g.Rules {
				if !strings.EqualFold(r.GrafanaAlert.Title, name) && !strings.EqualFold(r.Labels["alertname"], name) {
					continue
				}
				d = AlertDetail{
					Name:        r.GrafanaAlert.Title,
					Folder:      folder,
					Expr:        r.Expr,
					For:         r.For,
					Severity:    r.Labels["severity"],
					Annotations: r.Annotations,
				}
				if d.Name == "" {
					d.Name = r.Labels["alertname"]
				}
				// merge current state
				rows, aerr := c.Alerts(ctx)
				if aerr == nil {
					for _, row := range rows {
						if strings.EqualFold(row.Name, d.Name) {
							d.State = row.State
							d.ActiveAt = row.ActiveAt
							break
						}
					}
				}
				return d, nil
			}
		}
	}
	return AlertDetail{}, fmt.Errorf("alert %q not found", name)
}
```

- [ ] **Step 4: Implement command**

`grafana-cli/internal/cmd/alert.go`:
```go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
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
		if outputOptions(lastCfg).Output == "json" {
			return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
				d, err := c.AlertDetail(ctx, args[0])
				if err != nil {
					return result{}, err
				}
				b, _ := json.MarshalIndent(d, "", "  ")
				return result{raw: b}, nil
			})
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			d, err := c.AlertDetail(ctx, args[0])
			if err != nil {
				return result{}, err
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
```

`query_test.go` fakeGrafana additions:
```go
	case "/api/ruler/grafana/api/v1/rules":
		w.Write([]byte(`{"Acme Pay":[{"name":"PostgreSQL Service","rules":[{"expr":"sum(pg_stat_activity_count) > 900","for":"1m","labels":{"severity":"warning"},"annotations":{"description":"too many connections"},"grafana_alert":{"id":41,"title":"High DB Connections"}}]}]}`))
```

Note: `outputOptions(lastCfg).Output` — lastCfg is set by run() only; RunE checking it BEFORE run() means lastCfg holds the PREVIOUS run's config. Correct approach: check `flagOutput` (the flag var) — but default handling happens in config.Load. Simpler: always go through one run() closure and branch inside it. Restructure:
```go
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
			...table build...
		})
	},
```
(lastCfg is set before fn runs — correct inside the closure.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/api/alerts.go grafana-cli/internal/api/alerts_test.go grafana-cli/internal/cmd/alert.go grafana-cli/internal/cmd/alert_test.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): alert detail command with ruler merge"
```

---

### Task 5: `panels` command (dashboard panel mining)

**Files:**
- Modify: `grafana-cli/internal/api/grafana.go` (add PanelInfo + Panels)
- Create: `grafana-cli/internal/cmd/panels.go`
- Modify: `grafana-cli/internal/cmd/query_test.go` (fakeGrafana dashboard fixture with nested row)
- Test: `grafana-cli/internal/api/grafana_test.go` (extend), `grafana-cli/internal/cmd/panels_test.go`

**Interfaces:**
- Consumes: `DashboardJSON`, `run`, `frames`.
- Produces: `api.PanelInfo{Title, Type, Datasource string; Queries []string}`; `(*Client).Panels(ctx, dashUID string) ([]PanelInfo, error)` — flattens rows recursively, extracts datasource from panel.datasource (string form `"prometheus"` or object form `{type,uid}` → `"type (uid)"`) falling back to first target's datasource; extracts each target's `expr` (non-empty only).

- [ ] **Step 1: Write failing tests**

`grafana-cli/internal/cmd/panels_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestPanelsCommandFlattensRows(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "panels", "account")
	for _, want := range []string{"CPU", "Nested CPU", "prometheus (u1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
}

func TestPanelsQueriesFlag(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "panels", "account", "--queries")
	if !strings.Contains(out, `sum(rate(cpu[5m]))`) {
		t.Errorf("output = %q, want query expr", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestPanels`
Expected: FAIL — unknown command "panels"

- [ ] **Step 3: Implement API**

Append to `grafana-cli/internal/api/grafana.go`:
```go
type PanelInfo struct {
	Title      string
	Type       string
	Datasource string
	Queries    []string
}

type dashboardModel struct {
	Dashboard struct {
		Panels []rawPanel `json:"panels"`
	} `json:"dashboard"`
}

type rawPanel struct {
	Title      string          `json:"title"`
	Type       string          `json:"type"`
	Datasource json.RawMessage `json:"datasource"`
	Targets    []json.RawMessage `json:"targets"`
	Panels     []rawPanel      `json:"panels"`
}

// Panels flattens a dashboard's panels (recurse into rows) and extracts
// each panel's datasource and query expressions.
func (c *Client) Panels(ctx context.Context, dashUID string) ([]PanelInfo, error) {
	raw, err := c.DashboardJSON(ctx, dashUID)
	if err != nil {
		return nil, err
	}
	var m dashboardModel
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse dashboard JSON: %w", err)
	}
	var out []PanelInfo
	flattenPanels(m.Dashboard.Panels, &out)
	return out, nil
}

func flattenPanels(panels []rawPanel, out *[]PanelInfo) {
	for _, p := range panels {
		if p.Type == "row" {
			flattenPanels(p.Panels, out)
			continue
		}
		pi := PanelInfo{Title: p.Title, Type: p.Type, Datasource: datasourceString(p.Datasource)}
		for _, t := range p.Targets {
			var tgt struct {
				Expr       string          `json:"expr"`
				Datasource json.RawMessage `json:"datasource"`
			}
			if err := json.Unmarshal(t, &tgt); err != nil {
				continue
			}
			if pi.Datasource == "" {
				pi.Datasource = datasourceString(tgt.Datasource)
			}
			if tgt.Expr != "" {
				pi.Queries = append(pi.Queries, tgt.Expr)
			}
		}
		*out = append(*out, pi)
	}
}

// datasourceString renders a Grafana datasource ref (string form "type",
// or object form {"type":..,"uid":..}) as "type" or "type (uid)".
func datasourceString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var o struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.Type == "" {
		return ""
	}
	if o.UID != "" {
		return o.Type + " (" + o.UID + ")"
	}
	return o.Type
}
```

- [ ] **Step 4: Implement command**

`grafana-cli/internal/cmd/panels.go`:
```go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
)

var flagPanelsQueries bool

func init() {
	panelsCmd.Flags().BoolVar(&flagPanelsQueries, "queries", false, "print one row per panel query (expr)")
	rootCmd.AddCommand(panelsCmd)
}

var panelsCmd = &cobra.Command{
	Use:     "panels <dashboard-uid>",
	Short:   "List panels of a dashboard with their queries",
	Example: `  gcli panels 8GbEch5Mz
  gcli panels 8GbEch5Mz --queries`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			pis, err := c.Panels(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if outputOptions(lastCfg).Output == "json" {
				b, _ := json.MarshalIndent(pis, "", "  ")
				return result{raw: b}, nil
			}
			if flagPanelsQueries {
				return result{res: frames.Result{
					Meta: frames.Meta{Datasource: "grafana", Query: args[0]},
					Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
						{Name: "Panel", Values: panelQueryPanels(pis)},
						{Name: "Query", Values: panelQueryExprs(pis)},
					}}},
				}}, nil
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", Query: args[0]},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Panel", Values: panelTitles(pis)},
					{Name: "Type", Values: panelTypes(pis)},
					{Name: "Datasource", Values: panelDatasources(pis)},
					{Name: "Queries", Values: panelQueryCounts(pis)},
				}}},
			}}, nil
		})
	},
}

func panelTitles(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Title
	}
	return out
}
func panelTypes(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Type
	}
	return out
}
func panelDatasources(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = p.Datasource
	}
	return out
}
func panelQueryCounts(pis []api.PanelInfo) []any {
	out := make([]any, len(pis))
	for i, p := range pis {
		out[i] = fmt.Sprintf("%d", len(p.Queries))
	}
	return out
}
func panelQueryPanels(pis []api.PanelInfo) []any {
	var out []any
	for _, p := range pis {
		for range p.Queries {
			out = append(out, p.Title)
		}
	}
	return out
}
func panelQueryExprs(pis []api.PanelInfo) []any {
	var out []any
	for _, p := range pis {
		for _, q := range p.Queries {
			out = append(out, q)
		}
	}
	return out
}
```

`query.go` vars block add: `flagPanelsQueries bool`; `query_test.go` reset block add `flagPanelsQueries = false`; fakeGrafana add branch:
```go
	case "/api/dashboards/uid/account":
		w.Write([]byte(`{"dashboard":{"uid":"account","title":"Account","panels":[{"id":1,"title":"CPU","type":"timeseries","datasource":{"type":"prometheus","uid":"u1"},"targets":[{"refId":"A","expr":"sum(rate(cpu[5m]))"}]},{"id":2,"title":"Row","type":"row","panels":[{"id":3,"title":"Nested CPU","type":"timeseries","datasource":"prometheus","targets":[{"refId":"A","expr":"sum(rate(cpu[5m]))"}]}]}]},"meta":{}}`))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/api/grafana.go grafana-cli/internal/cmd/panels.go grafana-cli/internal/cmd/panels_test.go grafana-cli/internal/cmd/query.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): panels command — dashboard panel query mining"
```

---

### Task 6: Provisioning-format dashboard export (gap #2)

**Files:**
- Modify: `grafana-cli/internal/cmd/dashboards.go` (--export branch)
- Test: `grafana-cli/internal/cmd/dashboards_test.go` (extend)

**Interfaces:**
- Consumes: `DashboardJSON`, `run`, `result.raw`.

- [ ] **Step 1: Write failing test**

Extend `grafana-cli/internal/cmd/dashboards_test.go`:
```go
func TestDashboardsExportProvisioningFormat(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	dir := t.TempDir()
	path := dir + "/account.json"
	out := runCommand(t, "dashboards", "--get", "account", "--export", path)
	if !strings.Contains(out, "written:") {
		t.Fatalf("output = %q", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("exported file is not JSON: %v", err)
	}
	if _, ok := doc["title"]; !ok {
		t.Errorf("export must contain dashboard fields at top level: %s", b)
	}
	for _, key := range []string{"id", "version", "meta"} {
		if _, ok := doc[key]; ok {
			t.Errorf("provisioning export must not contain %q: %s", key, b)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestDashboardsExportProvisioningFormat`
Expected: FAIL — exported file still has meta/id/version (current envelope behavior)

- [ ] **Step 3: Implement**

In `grafana-cli/internal/cmd/dashboards.go`, replace the --export branch's file-writing part:
```go
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
```
(Keep the non-export `--get` path as-is: raw envelope printed.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/dashboards.go grafana-cli/internal/cmd/dashboards_test.go
git commit -m "fix(gcli): dashboard export writes provisioning format"
```

---

### Task 7: Annotations source column (gap #3)

**Files:**
- Modify: `grafana-cli/internal/api/grafana.go` (Annotation struct + Annotations fill)
- Modify: `grafana-cli/internal/cmd/annotations.go` (add column)
- Test: `grafana-cli/internal/cmd/annotations_test.go` (extend)

**Interfaces:**
- Consumes: existing Annotation type.
- Produces: `Annotation.Source string` — derived client-side (Grafana's annotations API has no source field): `login` when non-empty, else `alertName` when alertId != 0, else `"-"`.

- [ ] **Step 1: Write failing test**

Extend `grafana-cli/internal/cmd/annotations_test.go`:
```go
func TestAnnotationsSourceColumn(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "annotations")
	if !strings.Contains(out, "Source") || !strings.Contains(out, "deployer") {
		t.Errorf("output = %q, want Source column with login", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestAnnotationsSourceColumn`
Expected: FAIL — no Source column

- [ ] **Step 3: Implement**

`grafana-cli/internal/api/grafana.go` — extend the Annotation struct:
```go
type Annotation struct {
	ID        int64    `json:"id"`
	AlertID   int64    `json:"alertId"`
	AlertName string   `json:"alertName"`
	Login     string   `json:"login"`
	TimeMS    int64    `json:"time"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags"`
	NewState  string   `json:"newState"`
	PrevState string   `json:"prevState"`
	Source    string   `json:"-"` // derived: login → alertName → "-"
}
```
In `Annotations()` after fetch:
```go
	for i := range anns {
		switch {
		case anns[i].Login != "":
			anns[i].Source = anns[i].Login
		case anns[i].AlertID != 0 && anns[i].AlertName != "":
			anns[i].Source = "alert: " + anns[i].AlertName
		case anns[i].AlertID != 0:
			anns[i].Source = "alert"
		default:
			anns[i].Source = "-"
		}
	}
```
`grafana-cli/internal/cmd/annotations.go` — add Source column between Time and State:
```go
				{Name: "Time", Values: annTimes(anns)},
				{Name: "Source", Values: annSources(anns)},
				{Name: "State", Values: annStates(anns)},
```
```go
func annSources(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = a.Source
	}
	return out
}
```
`query_test.go` fakeGrafana annotations fixture — add `"login":"deployer","alertName":""` to the entry.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/api/grafana.go grafana-cli/internal/cmd/annotations.go grafana-cli/internal/cmd/annotations_test.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): annotations source column (login/alert derived)"
```

---

### Task 8: `datasources --json` full payload (gap #4)

**Files:**
- Modify: `grafana-cli/internal/api/grafana.go` (add DatasourcesRaw)
- Modify: `grafana-cli/internal/cmd/datasources.go`
- Modify: `grafana-cli/internal/cmd/query_test.go` (fake datasources fixture gains a jsonData field)
- Test: `grafana-cli/internal/cmd/datasources_test.go`

**Interfaces:**
- Produces: `(*Client).DatasourcesRaw(ctx) (json.RawMessage, error)`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/datasources_test.go`:
```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDatasourcesJSONFullPayload(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "datasources", "-o", "json")
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %q (%v)", out, err)
	}
	if len(arr) == 0 {
		t.Fatal("empty array")
	}
	if _, ok := arr[0]["jsonData"]; !ok {
		t.Errorf("full payload must keep jsonData field:\n%s", out)
	}
	if strings.Contains(out, `"frames"`) {
		t.Errorf("normalized frames leaked into full-payload mode:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestDatasourcesJSONFullPayload`
Expected: FAIL — jsonData missing (normalized output)

- [ ] **Step 3: Implement**

`grafana-cli/internal/api/grafana.go` append:
```go
// DatasourcesRaw returns the full /api/datasources payload untouched.
func (c *Client) DatasourcesRaw(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/datasources", &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
```
`grafana-cli/internal/cmd/datasources.go` — branch inside the run closure:
```go
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
			...existing table code unchanged...
		})
	},
```
Add imports `"bytes"`, `"encoding/json"` to datasources.go.
`query_test.go` fake datasources fixture: change the first entry to include a jsonData field:
```go
		w.Write([]byte(`[{"uid":"u1","name":"Metrics Iota","type":"prometheus","url":"x","isDefault":true,"jsonData":{"httpMethod":"POST"}},{"uid":"u2","name":"vlog","type":"victoriametrics-logs-datasource","url":"v"},{"uid":"u3","name":"PostgreSQL Metrics","type":"grafana-postgresql-datasource","url":"b"}]`))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS (existing tests asserting name/uid output still pass — table mode unchanged)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/api/grafana.go grafana-cli/internal/cmd/datasources.go grafana-cli/internal/cmd/datasources_test.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): datasources --json returns full raw payload"
```

---

### Task 9: Raw multi-query `queries[]` input (gap #5)

**Files:**
- Modify: `grafana-cli/internal/cmd/query.go` (parse array input)
- Test: `grafana-cli/internal/cmd/query_test.go` (extend)

**Interfaces:**
- Consumes: `DSQuery`, `DSQueryReq`, `ResolveDatasource`, `readBody`.
- Produces: `buildQueryReqs(rawBytes []byte, ds api.Datasource) ([]api.DSQueryReq, error)` — single object or array; refId: kept if present, `"A"` for single missing, `Q<n>` for array missing; datasource: element's datasource kept if present (type/uid), else resolved datasource injected.

- [ ] **Step 1: Write failing tests**

Extend `grafana-cli/internal/cmd/query_test.go`:
```go
func TestQueryCommandArrayInput(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "query", "Metrics Iota", "--json", `[{"expr":"up","refId":"A"},{"expr":"count(up)","refId":"B","datasource":{"type":"prometheus","uid":"u2"}}]`)
	qs := gotBody["queries"].([]any)
	if len(qs) != 2 {
		t.Fatalf("queries = %d, want 2", len(qs))
	}
	q0 := qs[0].(map[string]any)
	if q0["refId"] != "A" || q0["expr"] != "up" {
		t.Errorf("q0 = %v", q0)
	}
	ds0 := q0["datasource"].(map[string]any)
	if ds0["uid"] != "u1" {
		t.Errorf("q0 datasource uid not injected: %v", ds0)
	}
	q1 := qs[1].(map[string]any)
	ds1 := q1["datasource"].(map[string]any)
	if ds1["uid"] != "u2" {
		t.Errorf("q1 user datasource must be kept: %v", ds1)
	}
}

func TestBuildQueryReqsSingleKeepsRefA(t *testing.T) {
	ds := api.Datasource{Type: "prometheus", UID: "u1", Name: "Metrics Iota"}
	reqs, err := buildQueryReqs([]byte(`{"expr":"up"}`), ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].RefID != "A" || reqs[0].Datasource.UID != "u1" {
		t.Errorf("reqs = %+v", reqs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run "TestQueryCommandArrayInput|TestBuildQueryReqsSingleKeepsRefA"`
Expected: FAIL — array input errors "invalid --json" / buildQueryReqs undefined

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/query.go` — replace the body-parsing part of RunE:
```go
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
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
```
Add (replacing the old inline single-query build):
```go
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
```
Note: the fake's ds/query response only keys result "A" — the array test asserts request payload only (gotBody), output rendering is irrelevant there; `run()` renders res.Frames (empty) fine.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS (existing single-query tests keep refId "A" behavior)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/query.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): raw query command accepts multi-query arrays"
```

---

### Task 10: Tool `--version` flag + dead code removal

**Files:**
- Modify: `grafana-cli/internal/cmd/root.go` (Version)
- Modify: `grafana-cli/internal/render/render.go` (remove defaultWidth)
- Test: `grafana-cli/internal/cmd/root_test.go`

**Interfaces:**
- Produces: `var toolVersion = "dev"` in cmd package — settable via `-ldflags "-X gcli/internal/cmd.toolVersion=v0.2.0"`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/root_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "--version")
	if !strings.Contains(out, "gcli") || !strings.Contains(out, "version") {
		t.Errorf("output = %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestVersionFlag`
Expected: FAIL — "unknown flag: --version"

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/root.go`:
```go
// toolVersion is stamped at build time:
//   go build -ldflags "-X gcli/internal/cmd.toolVersion=v0.2.0"
var toolVersion = "dev"
```
In rootCmd definition add: `Version: toolVersion,` (cobra auto-adds the --version flag and `gcli version` conflict check — note: cobra refuses a `version` subcommand when Version is set unless `rootCmd.SetVersionTemplate` handles it; the existing `versionCmd` shows the GRAFANA version, so rename-safe approach: keep the subcommand, cobra allows both when the subcommand exists? NO — cobra's auto --version flag coexists fine with a `version` subcommand; only `InitDefaultVersionFlag` skips adding when Version empty. The subcommand `version` remains reachable as `gcli version`; `--version` prints "gcli version dev" by default template. Both work; no rename needed.)

`grafana-cli/internal/render/render.go` — delete the line `defaultWidth  = 120` from the const block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v && go vet ./... && go build -ldflags "-X gcli/internal/cmd.toolVersion=v0.2.0" -o gcli . && ./gcli --version`
Expected: PASS; last prints `gcli version v0.2.0`

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/root.go grafana-cli/internal/cmd/root_test.go grafana-cli/internal/render/render.go
git commit -m "feat(gcli): tool version flag + remove dead const"
```

---

### Task 11: Makefile

**Files:**
- Create: `grafana-cli/Makefile`

- [ ] **Step 1: Write Makefile**

`grafana-cli/Makefile`:
```make
.PHONY: build test vet install clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS = -X gcli/internal/cmd.toolVersion=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o gcli .

test:
	go test ./...

vet:
	go vet ./...

install: build
	install -m 0755 gcli /usr/local/bin/gcli

clean:
	rm -f gcli
```

- [ ] **Step 2: Verify**

Run: `cd grafana-cli && make build && ./gcli --version && make test`
Expected: build succeeds, version prints, tests pass

- [ ] **Step 3: Commit**

```bash
git add grafana-cli/Makefile
git commit -m "chore(gcli): Makefile — build/test/vet/install"
```

---

### Task 12: Ledger minors batch

**Files:**
- Modify: `grafana-cli/internal/timeparse/timeparse.go` (Atoi overflow)
- Modify: `grafana-cli/internal/cmd/logs_test.go` (SetArgs restore, dead lines)
- Modify: `grafana-cli/internal/api/alerts.go` (legacy non-hidden error gate)
- Modify: `grafana-cli/testdata/rules.json` (label scrub + totals removal)
- Test: `grafana-cli/internal/api/alerts_test.go` (extend), `grafana-cli/internal/timeparse/timeparse_test.go` (extend)

**Interfaces:** none new.

- [ ] **Step 1: Write failing tests**

Extend `grafana-cli/internal/timeparse/timeparse_test.go`:
```go
func TestParseToEpochMSOverflow(t *testing.T) {
	if _, err := ParseToEpochMS("now-99999999999999999999h", time.Now()); err == nil {
		t.Fatal("want error for overflowing duration")
	}
}
```
Extend `grafana-cli/internal/api/alerts_test.go`:
```go
func TestAlertsLegacyNonHiddenErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/prometheus/grafana/api/v1/rules", "/api/v2/alerts/statuses":
			http.NotFound(w, r)
		case "/api/alerts":
			http.Error(w, "boom", 500)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.Alerts(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 500 {
		t.Fatalf("err = %v, want unwrapped *HTTPError 500", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/timeparse/ -run TestParseToEpochMSOverflow && go test ./internal/api/ -run TestAlertsLegacyNonHiddenErrorPropagates`
Expected: FAIL both

- [ ] **Step 3: Implement**

`timeparse.go` — in the relative branch:
```go
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: duration overflows", s)
	}
```
`logs_test.go` — in TestLogsRejectsBadMode: delete the two lines `old := rootCmd.SilenceErrors` / `_ = old`; add after SetArgs: `defer rootCmd.SetArgs(nil)`.
`alerts.go` — in Alerts(), replace the final fallthrough:
```go
	rows, err3 := c.alertsFromLegacy(ctx)
	if err3 == nil {
		return rows, nil
	}
	if !isHidden(err3) {
		// a real failure (500/network) must not be masked as DENIED downstream
		return nil, err3
	}
	return nil, fmt.Errorf("no alerts endpoint accessible: rules %w; v2 statuses %w; legacy %w", err, err2, err3)
```
`testdata/rules.json` — scrub via jq:
```bash
jq 'del(.data.totals) | .data.groups |= .[:1] | .data.groups[0].rules |= .[:1] | walk(if type=="object" then del(.pod, .target) else . end)' grafana-cli/testdata/rules.json > /tmp/rules-scrubbed.json && mv /tmp/rules-scrubbed.json grafana-cli/testdata/rules.json
```
(If jq unavailable, do the equivalent edit by hand: remove `totals`, keep first group/first rule, drop pod/target keys.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v && grep -c "glsa_\|@example.com" testdata/rules.json || true`
Expected: PASS; no secrets

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/timeparse/timeparse.go grafana-cli/internal/timeparse/timeparse_test.go grafana-cli/internal/cmd/logs_test.go grafana-cli/internal/api/alerts.go grafana-cli/internal/api/alerts_test.go grafana-cli/testdata/rules.json
git commit -m "fix(gcli): ledger minors — overflow check, test cleanup, legacy error gate, fixture scrub"
```

---

### Task 13: Docs refresh — help.txt (install section + new commands) + README

**Files:**
- Modify: `grafana-cli/internal/cmd/help.txt`
- Modify: `grafana-cli/internal/cmd/help_test.go` (assert new sections)
- Modify: `grafana-cli/README.md`

**Interfaces:** none new.

- [ ] **Step 1: Update help.txt**

Add after the intro block (before SETUP) an INSTALL section, and add the new commands to the COMMANDS section:

```text
INSTALL
-------
  cd grafana-cli && make install      (builds + installs /usr/local/bin/gcli)
  or: make build                      (binary at ./gcli)
  Requires Go 1.27+.

  Shell completion (bash/zsh/fish):
  source <(gcli completion bash)      — or `gcli completion zsh` for zsh
```

Add to COMMANDS (after `gcli sql ...` line, before "Grafana state:"):
```text
Discover datasources:
  gcli metrics <ds> [pattern]          metric names (Prometheus-type)
  gcli labels <ds> [metric]            label names
  gcli values <ds> <label> [metric]    label values
  gcli tables <sql-ds>                 SQL tables
  gcli columns <sql-ds> <table>        SQL table columns
```
Add after `gcli alerts [--firing]` line:
```text
  gcli alert <name>                    one alert's full definition + state
  gcli panels <dashboard-uid> [--queries]   dashboard panel queries
```

- [ ] **Step 2: Update help_test.go**

Extend TestHelpCommandPrintsGuide's `want` list:
```go
	for _, want := range []string{"GRAFANA_URL", "GRAFANA_TOKEN", "gcli prom", "gcli logs", "gcli sql", "gcli query", "gcli alerts", "gcli capabilities", "EXIT CODES", "service-account", "INSTALL", "gcli metrics", "gcli labels", "gcli values", "gcli tables", "gcli columns", "gcli alert", "gcli panels", "completion"} {
```

- [ ] **Step 3: Update README.md**

Add to the commands table:
```markdown
| `gcli metrics <ds> [pattern]` | metric name discovery (Prometheus-type) |
| `gcli labels <ds> [metric]` / `gcli values <ds> <label>` | label browsing |
| `gcli tables <ds>` / `gcli columns <ds> <t>` | SQL schema introspection |
| `gcli alert <name>` | one alert's definition + current state |
| `gcli panels <uid> [--queries]` | dashboard panel query mining |
```
Update Install section:
```markdown
cd grafana-cli
make install        # builds and installs /usr/local/bin/gcli
# or: make build    # binary at ./gcli
```
Add design-note bullet:
```markdown
- **Metrics discovery** reads through the datasource proxy (`/api/datasources/proxy/uid/:uid/api/v1/...`) — GET-only, read-only.
- **Provisioning export**: `--export` strips server-assigned id/version and the meta envelope.
- **Shell completion**: `gcli completion bash|zsh|fish`.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v && make build`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/help.txt grafana-cli/internal/cmd/help_test.go grafana-cli/README.md
git commit -m "docs(gcli): help + README — install, completion, v2 commands"
```

---

## Self-Review

**Spec coverage (v2 scope):**
- Metrics discovery (metrics/labels/values) → Tasks 1–2. ✓
- SQL introspection (tables/columns) → Task 3. ✓
- Alert detail (ruler merge, gap #1) → Task 4. ✓
- Panel mining → Task 5. ✓
- Gap #2 provisioning export → Task 6. ✓
- Gap #3 annotations source → Task 7. ✓
- Gap #4 datasources --json → Task 8. ✓
- Gap #5 multi-query → Task 9. ✓
- Gap #6 help install section → Task 13. ✓
- Housekeeping: --version + dead code → Task 10; Makefile → Task 11; ledger minors → Task 12; docs → Task 13. ✓

**Placeholder scan:** none — every task carries runnable code and expected output.

**Type consistency:** `ProxyGet`/`parseLabelNames` defined T1, consumed T2; `requirePromType` defined T2, metrics.go refactored to use it (noted inline); `validateIdent`/`quoteIdent` local to T3; `AlertDetail` T4; `PanelInfo`/`Panels`/`datasourceString` T5; `DatasourcesRaw` T8; `buildQueryReqs` T9; `toolVersion` T10; `runCommandErr`/`resetAllFlags` defined T1 (refactor of existing helper, runCommand delegates) and reused T2/T3/T9. flag vars (`flagMetricsLimit`, `flagMetricsPattern`, `flagPanelsQueries`) declared in query.go vars block and reset in resetAllFlags — both noted in T1/T5. `lastCfg` read INSIDE run() closures (correct: set before fn executes). ✓
