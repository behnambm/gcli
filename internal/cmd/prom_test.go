// === Behavioral Contract: `gcli prom <ds> <promql>` ===
// - Without --step: sends an instant query ({expr, instant:true}, no range key)
// - With --step: sends a range query ({expr, instant:false, range:true, interval:step})
// - Rejects non-Prometheus-type datasources with a clear error
// - Renders the returned metric frames
package cmd

import (
	"strings"
	"testing"
)

func TestPromCommand_withoutStep_sendsInstantQuery(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "prom", "Universal", "count(up)")

	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "count(up)" {
		t.Errorf("expr = %v", q0["expr"])
	}
	if q0["instant"] != true {
		t.Errorf("instant = %v, want true without --step", q0["instant"])
	}
	if _, hasRange := q0["range"]; hasRange {
		t.Errorf("instant query must not set range: %v", q0)
	}
}

func TestPromCommand_withStep_sendsRangeQuery(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "prom", "Universal", "rate(http_requests_total[5m])", "--step", "1m")

	q0 := firstQueryObject(t, gotBody)
	if q0["range"] != true || q0["interval"] != "1m" {
		t.Errorf("range payload = %v", q0)
	}
	if q0["instant"] != false {
		t.Errorf("instant = %v, want false with --step", q0["instant"])
	}
}

func TestPromCommand_nonPrometheusDatasource_isRejected(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "prom", "Billing", "SELECT 1")

	if err == nil {
		t.Fatal("want error for non-prometheus datasource")
	}
	if !strings.Contains(err.Error(), "not a Prometheus-type") {
		t.Errorf("err = %v, want type explanation", err)
	}
}

func TestPromCommand_rendersReturnedValues(t *testing.T) {
	resp := `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Value","type":"number","labels":{"job":"api"}}]},"data":{"values":[[2173]]}}]}}}`
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse, "/api/ds/query": resp}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "prom", "Universal", "count(up)")

	if !strings.Contains(out, "2173") || !strings.Contains(out, "Value{job=api}") {
		t.Errorf("output = %q, want rendered values with labels", out)
	}
}

func TestPromCommand_defaultDatasourceUsedWhenArgOmitted(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Universal")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	runCommand(t, "--profile", "p", "prom", "count(up)")

	if gotBody == nil {
		t.Fatal("no ds/query request received")
	}
	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "count(up)" {
		t.Errorf("expr = %v", q0["expr"])
	}
}

func TestPromCommand_noArgsWithDefault_errorsCleanly(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Universal")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	_, err := runCommandErr(t, "--profile", "p", "prom")

	if err == nil || !strings.Contains(err.Error(), "promql") {
		t.Errorf("err = %v, want missing-promql error", err)
	}
}

func TestPromCommand_noArgNoDefault_errors(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "prom")

	if err == nil || !strings.Contains(err.Error(), "defaultDatasource") {
		t.Errorf("err = %v, want defaultDatasource hint", err)
	}
}
