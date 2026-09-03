// === Behavioral Contract: `gcli alerts` ===
//   - Renders one row per alert instance: Name, State, Severity, Folder, ActiveAt
//   - --firing filters to Alerting/Pending/NoData/Error core states
//   - With --no-color, states print plainly (colors are render.Colorize's
//     job, covered in the render package)
package cmd

import (
	"strings"
	"testing"
)

const rulesCmdResponse = `{"status":"success","data":{"groups":[{"name":"g","file":"Acme Pay","rules":[{"state":"inactive","name":"High DB Connections","alerts":[{"labels":{"alertname":"High DB Connections","grafana_folder":"Acme Pay","severity":"warning"},"state":"Alerting","activeAt":"2026-08-30T10:00:00Z"},{"labels":{"alertname":"Idle Alert","grafana_folder":"Acme Pay","severity":"info"},"state":"Normal","activeAt":""}]}]}]}}`

func TestAlertsCommand_rendersRowsWithStateAndSeverity(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/prometheus/grafana/api/v1/rules": rulesCmdResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "alerts")

	for _, want := range []string{"High DB Connections", "Alerting", "warning", "Acme Pay", "Idle Alert", "Normal"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestAlertsCommand_firingFilter_showsOnlyActiveStates(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/prometheus/grafana/api/v1/rules": rulesCmdResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "alerts", "--firing")

	if !strings.Contains(out, "High DB Connections") {
		t.Errorf("output = %q, want the Alerting instance", out)
	}
	if strings.Contains(out, "Idle Alert") {
		t.Errorf("output = %q, Normal instances must be filtered out", out)
	}
}

func TestAlertsCommand_jsonOutput_emitsMetaAndFrames(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/prometheus/grafana/api/v1/rules": rulesCmdResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "alerts", "-o", "json")

	for _, want := range []string{`"meta"`, `"frames"`, `"High DB Connections"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}
