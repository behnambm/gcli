// === Behavioral Contract: `gcli alert <name>` ===
//   - Matches a single alert rule by title or alertname (case-insensitive)
//     from /api/ruler/grafana/api/v1/rules, merging current state from the
//     unified rules endpoint
//   - Table output renders Key/Value rows: Name, Folder, Expr, For,
//     Severity, State, ActiveAt, Annotations
//   - -o json emits the raw AlertDetail document
//   - Unknown name errors with "not found"
package cmd

import (
	"strings"
	"testing"
)

const rulerDetailCmdResp = `{"Acme Pay":[{"name":"PostgreSQL Service","interval":"1m","rules":[{"expr":"sum by(pod) (pg_stat_activity_count{metrics-zeta-cluster=\"metrics-service\"}) > 900","for":"1m","labels":{"severity":"warning","target":"acme-credit"},"annotations":{"description":"PostgreSQL service has more than 900 connections"},"grafana_alert":{"id":41,"title":"High DB Connections [Metrics Zeta]"}}]}]}`

const rulesDetailCmdResp = `{"status":"success","data":{"groups":[{"name":"g","file":"Acme Pay","rules":[{"name":"High DB Connections [Metrics Zeta]","state":"inactive","alerts":[{"labels":{"alertname":"High DB Connections [Metrics Zeta]","grafana_folder":"Acme Pay"},"state":"Alerting","activeAt":"2026-08-30T10:00:00Z"}]}]}]}}`

func alertDetailRoutes() map[string]string {
	return map[string]string{
		"/api/ruler/grafana/api/v1/rules":      rulerDetailCmdResp,
		"/api/prometheus/grafana/api/v1/rules": rulesDetailCmdResp,
	}
}

func TestAlertCommand_printsDetailKeyValues(t *testing.T) {
	srv := fakeGrafana(t, alertDetailRoutes(), nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "alert", "high db connections [Metrics Zeta]")

	for _, want := range []string{"High DB Connections [Metrics Zeta]", "Acme Pay", "Expr", "For", "Severity", "warning", "State", "Alerting", "ActiveAt", "description"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestAlertCommand_jsonOutput_emitsRawDetail(t *testing.T) {
	srv := fakeGrafana(t, alertDetailRoutes(), nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "alert", "high db connections [Metrics Zeta]", "-o", "json")

	for _, want := range []string{`"name"`, `"High DB Connections [Metrics Zeta]"`, `"folder"`, `"expr"`, `"severity"`, `"warning"`, `"state"`, `"Alerting"`, `"activeAt"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

func TestAlertCommand_unknownName_errorsNotFound(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/ruler/grafana/api/v1/rules": `{}`}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "alert", "nope")

	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found", err)
	}
}
