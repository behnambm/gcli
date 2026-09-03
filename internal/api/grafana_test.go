// === Behavioral Contract: api.Client datasource/dashboard/annotation/health accessors ===
//   - Datasources(): returns datasources sorted default-first, then by name;
//     HTTP errors propagate
//   - ResolveDatasource(): exact UID match wins over case-insensitive name
//     match; unknown inputs error with the available names listed
//   - SearchDashboards(): passes the query through the search endpoint;
//     empty query omits the parameter
//   - DashboardJSON(): returns the raw dashboard payload untouched
//   - Annotations(): sends from/to/limit, repeated tags params, and
//     dashboardUID only when given
//   - Health(): returns the Grafana version plus one status per datasource;
//     per-datasource HTTP errors become "denied (HTTP n)" entries WITHOUT
//     aborting the probe; non-HTTP errors abort
//   - Version(): returns version/commit/database from /api/health
//   - Panels(): flattens dashboard panels, recursing into type=="row";
//     datasource from panel.datasource (string form "type" or object form
//     {type,uid} → "type (uid)") falling back to the first target's
//     datasource; queries are the non-empty target exprs
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

func grafanaServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if body, ok := routes[key]; ok {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
}

func TestDatasources_sortsDefaultFirstThenByName(t *testing.T) {
	resp := `[
		{"uid":"u3","name":"zeta","type":"prometheus","url":"z"},
		{"uid":"u1","name":"alpha","type":"prometheus","url":"a","isDefault":true},
		{"uid":"u2","name":"mid","type":"prometheus","url":"m"}
	]`
	srv := grafanaServer(t, map[string]string{"/api/datasources": resp})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	dss, err := c.Datasources(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(dss) != 3 {
		t.Fatalf("len = %d, want 3", len(dss))
	}
	if !dss[0].IsDefault || dss[0].Name != "alpha" {
		t.Errorf("default datasource must sort first: %+v", dss[0])
	}
	if dss[1].Name != "mid" || dss[2].Name != "zeta" {
		t.Errorf("non-default must sort by name: %+v", dss)
	}
}

func TestResolveDatasource_byExactUID(t *testing.T) {
	resp := `[{"uid":"03hIm2zGz","name":"Universal","type":"prometheus","url":"u"},{"uid":"cf3iebh4uz1fkc","name":"Logs","type":"victoriametrics-logs-datasource","url":"v"}]`
	srv := grafanaServer(t, map[string]string{"/api/datasources": resp})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	ds, err := c.ResolveDatasource(context.Background(), "cf3iebh4uz1fkc")

	if err != nil {
		t.Fatal(err)
	}
	if ds.Name != "Logs" {
		t.Errorf("ds = %+v", ds)
	}
}

func TestResolveDatasource_byCaseInsensitiveName(t *testing.T) {
	resp := `[{"uid":"03hIm2zGz","name":"Universal","type":"prometheus","url":"u"}]`
	srv := grafanaServer(t, map[string]string{"/api/datasources": resp})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	ds, err := c.ResolveDatasource(context.Background(), "universal")

	if err != nil {
		t.Fatal(err)
	}
	if ds.UID != "03hIm2zGz" {
		t.Errorf("ds = %+v", ds)
	}
}

func TestResolveDatasource_unknown_listsAvailableNames(t *testing.T) {
	resp := `[{"uid":"u1","name":"Universal","type":"prometheus","url":"u"}]`
	srv := grafanaServer(t, map[string]string{"/api/datasources": resp})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.ResolveDatasource(context.Background(), "nope")

	if err == nil || !strings.Contains(err.Error(), "Universal") {
		t.Errorf("err = %v, want hint listing available names", err)
	}
}

func TestSearchDashboards_escapesQueryParameter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	if _, err := c.SearchDashboards(context.Background(), "pay & bill"); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "pay & bill" {
		t.Errorf("query param = %q, want round-tripped value", gotQuery)
	}
}

func TestSearchDashboards_emptyQuery_omitsParameter(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	if _, err := c.SearchDashboards(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawQuery, "query=") {
		t.Errorf("raw query = %q, want no query param", rawQuery)
	}
}

func TestDashboardJSON_returnsRawPayloadUntouched(t *testing.T) {
	payload := `{"dashboard":{"uid":"abc","title":"T"},"meta":{"x":1}}`
	srv := grafanaServer(t, map[string]string{"/api/dashboards/uid/abc": payload})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	raw, err := c.DashboardJSON(context.Background(), "abc")

	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != payload {
		t.Errorf("raw = %s, want payload untouched", raw)
	}
}

func TestAnnotations_buildsFilteredRequest(t *testing.T) {
	var gotValues url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotValues = r.URL.Query()
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.Annotations(context.Background(), 1000, 2000, []string{"deploy", "release"}, "abc")

	if err != nil {
		t.Fatal(err)
	}
	if gotValues.Get("from") != "1000" || gotValues.Get("to") != "2000" {
		t.Errorf("from/to = %v/%v", gotValues.Get("from"), gotValues.Get("to"))
	}
	if gotValues.Get("dashboardUID") != "abc" {
		t.Errorf("dashboardUID = %q", gotValues.Get("dashboardUID"))
	}
	if len(gotValues["tags"]) != 2 || gotValues["tags"][0] != "deploy" || gotValues["tags"][1] != "release" {
		t.Errorf("tags = %v, want repeated params", gotValues["tags"])
	}
}

func TestHealth_returnsVersionAndPerDatasourceStatus(t *testing.T) {
	routes := map[string]string{
		"/api/health":                    `{"version":"10.4.3","commit":"abc"}`,
		"/api/datasources":               `[{"uid":"u1","name":"Universal","type":"prometheus","url":"u"},{"uid":"u2","name":"Vlog","type":"victoriametrics-logs-datasource","url":"v"}]`,
		"/api/datasources/uid/u1/health": `{"status":"OK","message":"up"}`,
		"/api/datasources/uid/u2/health": `{"status":"Error","message":"down"}`,
	}
	srv := grafanaServer(t, routes)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	version, stats, err := c.Health(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if version != "10.4.3" {
		t.Errorf("version = %q", version)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats[0].Status != "OK" || stats[1].Status != "Error" {
		t.Errorf("stats = %+v", stats)
	}
}

func TestHealth_perDatasourceHTTPError_markedDeniedAndContinues(t *testing.T) {
	routes := map[string]string{
		"/api/health":                    `{"version":"10.4.3"}`,
		"/api/datasources":               `[{"uid":"u1","name":"Universal","type":"prometheus","url":"u"},{"uid":"u2","name":"Hidden","type":"prometheus","url":"h"}]`,
		"/api/datasources/uid/u1/health": `{"status":"OK","message":"up"}`,
	}
	// u2's health route absent → 404
	srv := grafanaServer(t, routes)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, stats, err := c.Health(context.Background())

	if err != nil {
		t.Fatalf("per-datasource denial must not abort the probe: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want both datasources reported", stats)
	}
	var denied *HealthStatus
	for i := range stats {
		if stats[i].Name == "Hidden" {
			denied = &stats[i]
		}
	}
	if denied == nil || !strings.HasPrefix(denied.Status, "denied (HTTP 404)") {
		t.Errorf("denied entry = %+v, want Hidden marked denied (HTTP 404)", denied)
	}
}

func TestPanels_flattensRowsAndExtractsFields(t *testing.T) {
	payload := `{"dashboard":{"panels":[{"title":"CPU","type":"timeseries","datasource":{"type":"prometheus","uid":"u1"},"targets":[{"refId":"A","expr":"sum(rate(cpu[5m]))"}]},{"title":"Row","type":"row","panels":[{"title":"Nested CPU","type":"timeseries","datasource":"prometheus","targets":[{"refId":"A","expr":"sum(rate(nested[5m]))"}]}]}]}}`
	srv := grafanaServer(t, map[string]string{"/api/dashboards/uid/account": payload})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	pis, err := c.Panels(context.Background(), "account")

	if err != nil {
		t.Fatal(err)
	}
	if len(pis) != 2 {
		t.Fatalf("panels = %+v, want 2 (row itself not listed)", pis)
	}
	if pis[0].Title != "CPU" || pis[0].Type != "timeseries" || pis[0].Datasource != "prometheus (u1)" {
		t.Errorf("panel[0] = %+v", pis[0])
	}
	if len(pis[0].Queries) != 1 || pis[0].Queries[0] != "sum(rate(cpu[5m]))" {
		t.Errorf("panel[0] queries = %+v", pis[0].Queries)
	}
	if pis[1].Title != "Nested CPU" || pis[1].Datasource != "prometheus" {
		t.Errorf("panel[1] = %+v", pis[1])
	}
	if len(pis[1].Queries) != 1 || pis[1].Queries[0] != "sum(rate(nested[5m]))" {
		t.Errorf("panel[1] queries = %+v", pis[1].Queries)
	}
}

func TestPanels_missingPanelDatasource_fallsBackToFirstTarget(t *testing.T) {
	payload := `{"dashboard":{"panels":[{"title":"No DS","type":"timeseries","targets":[{"refId":"A","expr":"up","datasource":{"type":"prometheus","uid":"u2"}}]}]}}`
	srv := grafanaServer(t, map[string]string{"/api/dashboards/uid/account": payload})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	pis, err := c.Panels(context.Background(), "account")

	if err != nil {
		t.Fatal(err)
	}
	if len(pis) != 1 {
		t.Fatalf("panels = %+v", pis)
	}
	if pis[0].Datasource != "prometheus (u2)" {
		t.Errorf("datasource = %q, want fallback to target datasource", pis[0].Datasource)
	}
	if len(pis[0].Queries) != 1 || pis[0].Queries[0] != "up" {
		t.Errorf("queries = %+v", pis[0].Queries)
	}
}

func TestPanels_emptyTargetExprs_skipped(t *testing.T) {
	payload := `{"dashboard":{"panels":[{"title":"Mixed","type":"timeseries","datasource":"prometheus","targets":[{"refId":"A","expr":"up"},{"refId":"B","expr":""},{"refId":"C","expr":"up2"}]}]}}`
	srv := grafanaServer(t, map[string]string{"/api/dashboards/uid/account": payload})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	pis, err := c.Panels(context.Background(), "account")

	if err != nil {
		t.Fatal(err)
	}
	if len(pis) != 1 {
		t.Fatalf("panels = %+v", pis)
	}
	if len(pis[0].Queries) != 2 || pis[0].Queries[0] != "up" || pis[0].Queries[1] != "up2" {
		t.Errorf("queries = %+v, want only non-empty exprs", pis[0].Queries)
	}
}

func TestVersion_returnsBuildFields(t *testing.T) {
	srv := grafanaServer(t, map[string]string{"/api/health": `{"version":"10.4.3","commit":"0bfd547","database":"ok"}`})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	v, err := c.Version(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if v["version"] != "10.4.3" || v["commit"] != "0bfd547" || v["database"] != "ok" {
		t.Errorf("v = %v", v)
	}
}
