// === Behavioral Contract: `gcli capabilities` ===
// - Renders one row per probed group with OK/DENIED/ERROR status
// - Succeeds (exit 0) even when groups are DENIED — diagnosis, not a failure
package cmd

import (
	"strings"
	"testing"
)

func capabilitiesRoutes() map[string]string {
	return map[string]string{
		"/api/org":                             `{"id":1,"name":"Acme"}`,
		"/api/datasources":                     defaultDatasourcesResponse,
		"/api/search":                          `[]`,
		"/api/prometheus/grafana/api/v1/rules": `{"status":"success","data":{"groups":[]}}`,
		"/api/annotations":                     `[]`,
		"/api/datasources/uid/u1/health":       `{"status":"OK"}`,
		"/api/ds/query":                        defaultDSQueryResponse,
	}
}

func TestCapabilitiesCommand_reportsAllGroups(t *testing.T) {
	srv := fakeGrafana(t, capabilitiesRoutes(), nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "capabilities")

	for _, want := range []string{"auth", "query", "datasources", "dashboards", "alerts", "annotations", "datasource-health", "OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestCapabilitiesCommand_deniedGroups_stillExitZero(t *testing.T) {
	routes := map[string]string{"/api/org": `{"id":1}`} // everything else 404
	srv := fakeGrafana(t, routes, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out, err := runCommandErr(t, "capabilities")

	if err != nil {
		t.Fatalf("capabilities must exit 0 even when groups are denied: %v", err)
	}
	if !strings.Contains(out, "DENIED") {
		t.Errorf("output = %q, want DENIED markers", out)
	}
}
