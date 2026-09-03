// === Behavioral Contract: `gcli panels <dashboard-uid>` ===
//   - Fetches the dashboard JSON and flattens its panels, recursing into
//     type=="row" panels, rendering Title/Type/Datasource/Queries per panel
//   - Datasource renders object form {type,uid} as "type (uid)" and the
//     string form as the bare type name
//   - --queries prints one row per non-empty query expr (Panel/Query columns)
//   - -o json emits the raw []PanelInfo document
package cmd

import (
	"strings"
	"testing"
)

const accountDashboardResp = `{"dashboard":{"uid":"account","title":"Account","panels":[{"id":1,"title":"CPU","type":"timeseries","datasource":{"type":"prometheus","uid":"u1"},"targets":[{"refId":"A","expr":"sum(rate(cpu[5m]))"}]},{"id":2,"title":"Row","type":"row","panels":[{"id":3,"title":"Nested CPU","type":"timeseries","datasource":"prometheus","targets":[{"refId":"A","expr":"sum(rate(nested[5m]))"}]}]}]},"meta":{}}`

func TestPanelsCommand_flattensRows_listsNestedPanels(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": accountDashboardResp}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "panels", "account")

	for _, want := range []string{"CPU", "Nested CPU", "prometheus (u1)", "prometheus"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestPanelsCommand_queriesFlag_printsExprs(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": accountDashboardResp}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "panels", "account", "--queries")

	for _, want := range []string{"sum(rate(cpu[5m]))", "sum(rate(nested[5m]))"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing expr %q", out, want)
		}
	}
}

func TestPanelsCommand_jsonOutput_emitsPanelInfo(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": accountDashboardResp}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "panels", "account", "-o", "json")

	for _, want := range []string{`"title"`, `"Nested CPU"`, `"datasource"`, `"prometheus (u1)"`, `"queries"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}
