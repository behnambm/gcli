// === Behavioral Contract: `gcli logs <ds> <logsql>` ===
//   - Sends {expr, queryType, limit} to the VictoriaLogs datasource
//   - limit serializes as a JSON NUMBER (upstream silently ignores strings)
//   - --mode selects instant|range|stats; invalid mode errors before any
//     network call
//   - Renders returned log frames
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogsCommand_sendsNumericLimit(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "logs", "vlog", `{app="acme-pay"} |= "error"`, "--limit", "25")

	q0 := firstQueryObject(t, gotBody)
	if q0["queryType"] != "range" {
		t.Errorf("queryType = %v, want range default", q0["queryType"])
	}
	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), `"limit":25`) {
		t.Errorf("limit must serialize as JSON number: %s", raw)
	}
}

func TestLogsCommand_statsMode_sendsStatsQueryType(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "logs", "vlog", "* | stats count() rows", "--mode", "stats")

	q0 := firstQueryObject(t, gotBody)
	if q0["queryType"] != "stats" {
		t.Errorf("queryType = %v, want stats", q0["queryType"])
	}
	if q0["expr"] != "* | stats count() rows" {
		t.Errorf("expr = %v", q0["expr"])
	}
}

func TestLogsCommand_invalidMode_failsBeforeNetwork(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "logs", "vlog", "*", "--mode", "bogus")

	if err == nil || !strings.Contains(err.Error(), "invalid --mode") {
		t.Fatalf("err = %v, want invalid --mode error", err)
	}
}

func TestLogsCommand_rendersLogLines(t *testing.T) {
	resp := `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Time","type":"time"},{"name":"Line","type":"string"}]},"data":{"values":[[1788080773997],["error: db down"]]}}]}}}`
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse, "/api/ds/query": resp}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "logs", "vlog", "*")

	if !strings.Contains(out, "error: db down") {
		t.Errorf("output = %q, want log line rendered", out)
	}
}
