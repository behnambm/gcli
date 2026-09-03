// === Behavioral Contract: `gcli metrics <datasource> [pattern]` ===
//   - Resolves the named datasource and rejects any non-Prometheus type
//     (prometheus | victoriametrics-datasource) with a clear error
//   - Lists metric names by proxying GET /api/v1/label/__name__/values through
//     the datasource proxy, passing --limit as ?limit=
//   - Filters names case-insensitively by --pattern, or by the positional
//     pattern when --pattern is empty
//   - Renders the names as a single "Metric" column
package cmd

import (
	"strings"
	"testing"
)

const defaultLabelValuesResponse = `{"status":"success","data":["up","node_memory_MemTotal_bytes","http_requests_total"]}`

func TestMetricsCommand_noPattern_listsAllNames(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/label/__name__/values": defaultLabelValuesResponse,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "metrics", "Universal")

	for _, want := range []string{"up", "node_memory_MemTotal_bytes", "http_requests_total"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing metric %q", out, want)
		}
	}
}

func TestMetricsCommand_positionalPattern_filtersCaseInsensitively(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/label/__name__/values": defaultLabelValuesResponse,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "metrics", "Universal", "UP")

	if !strings.Contains(out, "up") {
		t.Errorf("output = %q, want case-insensitive match for metric name", out)
	}
	if strings.Contains(out, "node_memory") || strings.Contains(out, "http_requests") {
		t.Errorf("pattern filter failed:\n%s", out)
	}
}

func TestMetricsCommand_flagPattern_filters(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/label/__name__/values": defaultLabelValuesResponse,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "metrics", "Universal", "--pattern", "mem")

	if !strings.Contains(out, "node_memory_MemTotal_bytes") {
		t.Errorf("output = %q, want metric matching --pattern", out)
	}
	if strings.Contains(out, "http_requests_total") {
		t.Errorf("--pattern filter failed:\n%s", out)
	}
}

func TestMetricsCommand_nonPromDatasource_rejected(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "metrics", "Billing")

	if err == nil {
		t.Fatal("want error for non-prometheus datasource")
	}
	if !strings.Contains(err.Error(), "not a Prometheus-type") {
		t.Errorf("err = %v, want type explanation", err)
	}
}
