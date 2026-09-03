// === Behavioral Contract: `gcli sql <ds> <query...>` ===
//   - Joins all args after the datasource with spaces into rawSql
//   - Sends {rawSql, format:"table"}
//   - Substitutes $__timeFrom/$__timeTo with epoch milliseconds from
//     --from/--to only when those macros appear in the query
//   - Renders the returned table frames
package cmd

import (
	"strings"
	"testing"
)

func TestSQLCommand_joinsArgsIntoRawSQL(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "sql", "Billing", "SELECT", "count(*)", "FROM", "users")

	q0 := firstQueryObject(t, gotBody)
	if q0["rawSql"] != "SELECT count(*) FROM users" {
		t.Errorf("rawSql = %v", q0["rawSql"])
	}
	if q0["format"] != "table" {
		t.Errorf("format = %v", q0["format"])
	}
}

func TestSQLCommand_substitutesTimeMacros(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "sql", "Billing",
		"SELECT", "*", "FROM", "t", "WHERE", "ts", "BETWEEN", "$__timeFrom", "AND", "$__timeTo",
		"--from", "2026-08-01T00:00:00Z", "--to", "2026-08-02T00:00:00Z")

	sql := firstQueryObject(t, gotBody)["rawSql"].(string)
	if strings.Contains(sql, "$__timeFrom") || strings.Contains(sql, "$__timeTo") {
		t.Errorf("macros not substituted: %s", sql)
	}
	if !strings.Contains(sql, "1785542400000") { // 2026-08-01T00:00:00Z epoch ms
		t.Errorf("expected epoch ms for $__timeFrom: %s", sql)
	}
}

func TestSQLCommand_withoutMacros_leavesQueryUntouched(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "sql", "Billing", "SELECT", "1")

	sql := firstQueryObject(t, gotBody)["rawSql"].(string)
	if sql != "SELECT 1" {
		t.Errorf("rawSql = %q, want untouched", sql)
	}
}

func TestSQLCommand_rendersTableFrames(t *testing.T) {
	resp := `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"one","type":"number"}]},"data":{"values":[[1]]}}]}}}`
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse, "/api/ds/query": resp}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "sql", "Billing", "SELECT", "1", "AS", "one")

	if !strings.Contains(out, "one") || !strings.Contains(out, "1") {
		t.Errorf("output = %q, want table with column name and value", out)
	}
}
