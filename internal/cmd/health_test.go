// === Behavioral Contract: `gcli health` + `gcli version` ===
//   - health renders one row per datasource with its probe status
//   - A datasource whose health endpoint is denied still appears, marked
//     "denied (HTTP n)"
//   - version renders version/commit/database from /api/health
package cmd

import (
	"strings"
	"testing"
)

func healthRoutes() map[string]string {
	return map[string]string{
		"/api/health":                    `{"version":"10.4.3","commit":"0bfd547","database":"ok"}`,
		"/api/datasources":               defaultDatasourcesResponse,
		"/api/datasources/uid/u1/health": `{"status":"OK","message":"Successfully queried"}`,
	}
}

func TestHealthCommand_rendersPerDatasourceStatus(t *testing.T) {
	srv := fakeGrafana(t, healthRoutes(), nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "health")

	for _, want := range []string{"Universal", "OK", "vlog", "Billing"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestHealthCommand_deniedDatasource_markedDenied(t *testing.T) {
	routes := healthRoutes()
	delete(routes, "/api/datasources/uid/u2/health") // never existed → 404
	srv := fakeGrafana(t, routes, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "health")

	if !strings.Contains(out, "denied (HTTP 404)") {
		t.Errorf("output = %q, want denied marker for inaccessible health", out)
	}
	if !strings.Contains(out, "Universal") {
		t.Errorf("output = %q, probe must continue past denied datasources", out)
	}
}

func TestVersionCommand_rendersBuildFields(t *testing.T) {
	srv := fakeGrafana(t, healthRoutes(), nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "version")

	for _, want := range []string{"version", "10.4.3", "commit", "0bfd547", "database", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}
