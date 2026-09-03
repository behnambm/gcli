// === Shared test infrastructure for cmd package ===
// fakeGrafana: httptest double serving per-path fixture bodies plus an
// optional ds/query request inspector. runCommandErr resets all shared
// flag vars before each Execute (cobra persists flag values in-process),
// so tests are order-independent. runCommand fails the test on error.

// === Behavioral Contract: `gcli query <ds> --json <payload>` ===
//   - Accepts one raw query object or an array of query objects, sending each
//     to the datasource unchanged
//   - Injects the resolved datasource (type AND uid) and a refId into each
//     query on the wire: "A" for a single object, kept user refId if present,
//     else "Q<n>" for array elements; a per-element datasource is kept
//   - Accepts @file.json input, reading the payload from disk
//   - Renders the returned frames to stdout
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/behnambm/gcli/internal/api"
)

// fakeGrafana serves the given fixture bodies keyed by request path. When
// onDSQuery is set, ds/query requests are decoded and passed to it before
// the fixture response is written. Unknown paths get 404.
func fakeGrafana(t *testing.T, routes map[string]string, onDSQuery func(t *testing.T, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ds/query" {
			if onDSQuery != nil {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode ds/query body: %v", err)
				}
				onDSQuery(t, body)
			}
			if body, ok := routes["/api/ds/query"]; ok {
				w.Write([]byte(body))
				return
			}
			w.Write([]byte(defaultDSQueryResponse))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
}

const defaultDSQueryResponse = `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`

const defaultDatasourcesResponse = `[{"uid":"u1","name":"Universal","type":"prometheus","url":"x","isDefault":true,"jsonData":{"httpMethod":"POST"}},{"uid":"u2","name":"vlog","type":"victoriametrics-logs-datasource"},{"uid":"u3","name":"Billing","type":"grafana-postgresql-datasource"}]`

func setEnv(t *testing.T, url string) {
	t.Helper()
	// run() resolves config through profiles.Resolve, which consults the
	// user's real profiles.yaml (default: marker beats legacy env). Isolate
	// the config dir so a host-machine profiles file cannot hijack these
	// legacy-env tests, and clear GCLI_PROFILE for the same reason.
	xdgEnv(t)
	t.Setenv("GRAFANA_URL", url)
	t.Setenv("GRAFANA_TOKEN", "test-token")
	t.Setenv("GCLI_PROFILE", "")
}

// resetAllFlags restores every shared flag var to its default so cobra's
// in-process flag persistence cannot leak state between tests.
func resetAllFlags() {
	flagStep = ""
	flagLogsMode = "range"
	flagLogsLimit = 50
	flagFrom = "now-1h"
	flagTo = "now"
	flagQueryJSON = ""
	flagFiring = false
	flagDashGet = ""
	flagDashExport = ""
	flagAnnTags = ""
	flagAnnDashboard = ""
	flagAnnFrom = "now-24h"
	flagAnnTo = "now"
	flagOutput = ""
	flagVerbose = false
	flagNoColor = false
	flagFrame = 0
	flagMetricsLimit = 200
	flagMetricsPattern = ""
	flagPanelsQueries = false
	flagUninstallYes = false
	flagProfile = ""
	flagProfURL = ""
	flagProfToken = ""
	flagProfUser = ""
	flagProfPass = ""
	flagProfOrgID = ""
	flagProfDefaultDS = ""
	flagProfSetDefault = false
	flagProfForce = false
}

func runCommandErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"--no-color"}, args...))
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	return buf.String(), err
}

// runCommand executes the CLI and fails the test on any error.
func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCommandErr(t, args...)
	if err != nil {
		t.Fatalf("command %v failed: %v", args, err)
	}
	return out
}

func firstQueryObject(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	qs, ok := body["queries"].([]any)
	if !ok || len(qs) == 0 {
		t.Fatalf("queries = %v", body["queries"])
	}
	q0, ok := qs[0].(map[string]any)
	if !ok {
		t.Fatalf("query object = %v", qs[0])
	}
	return q0
}

func TestQueryCommand_injectsDatasourceAndRefID(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "query", "Universal", "--json", `{"expr":"up","instant":true}`)

	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "up" || q0["instant"] != true {
		t.Errorf("query object = %v, want user payload preserved", q0)
	}
	if q0["refId"] != "A" {
		t.Errorf("refId = %v, want A injected", q0["refId"])
	}
	ds, ok := q0["datasource"].(map[string]any)
	if !ok {
		t.Fatalf("datasource missing: %v", q0)
	}
	if ds["uid"] != "u1" || ds["type"] != "prometheus" {
		t.Errorf("datasource = %v, want resolved type AND uid injected", ds)
	}
}

func TestQueryCommand_fileInput_readsPayloadFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/q.json"
	if err := os.WriteFile(path, []byte(`{"expr":"up"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "query", "Universal", "--json", "@"+path)

	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "up" {
		t.Errorf("query object = %v, want file content as payload", q0)
	}
}

func TestQueryCommand_missingJSONFlag_failsWithoutNetwork(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "query", "Universal")

	if err == nil {
		t.Fatal("want error when --json is required and missing")
	}
}

func TestQueryCommand_invalidJSON_returnsError(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "query", "Universal", "--json", "{not json")

	if err == nil || !strings.Contains(err.Error(), "invalid --json") {
		t.Fatalf("err = %v, want invalid --json error", err)
	}
}

func TestQueryCommand_unknownDatasource_listsAvailable(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "query", "nope", "--json", `{"expr":"up"}`)

	if err == nil || !strings.Contains(err.Error(), "Universal") {
		t.Fatalf("err = %v, want available names listed", err)
	}
}

func TestQueryCommandArrayInput(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "query", "Universal", "--json", `[{"expr":"up","refId":"A"},{"expr":"count(up)","refId":"B","datasource":{"type":"prometheus","uid":"u2"}}]`)

	qs, ok := gotBody["queries"].([]any)
	if !ok {
		t.Fatalf("queries = %v", gotBody["queries"])
	}
	if len(qs) != 2 {
		t.Fatalf("queries = %d, want 2", len(qs))
	}
	q0 := qs[0].(map[string]any)
	if q0["refId"] != "A" || q0["expr"] != "up" {
		t.Errorf("q0 = %v", q0)
	}
	ds0 := q0["datasource"].(map[string]any)
	if ds0["uid"] != "u1" {
		t.Errorf("q0 datasource uid not injected: %v", ds0)
	}
	q1 := qs[1].(map[string]any)
	ds1 := q1["datasource"].(map[string]any)
	if ds1["uid"] != "u2" {
		t.Errorf("q1 user datasource must be kept: %v", ds1)
	}
}

func TestQueryCommand_defaultDatasourceUsedWhenArgOmitted(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Universal")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	runCommand(t, "--profile", "p", "query", "--json", `{"expr":"count(up)"}`)

	if gotBody == nil {
		t.Fatal("no ds/query request received")
	}
	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "count(up)" {
		t.Errorf("expr = %v", q0["expr"])
	}
}

func TestBuildQueryReqsSingleKeepsRefA(t *testing.T) {
	ds := api.Datasource{Type: "prometheus", UID: "u1", Name: "Universal"}
	reqs, err := buildQueryReqs([]byte(`{"expr":"up"}`), ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].RefID != "A" || reqs[0].Datasource.UID != "u1" {
		t.Errorf("reqs = %+v", reqs)
	}
}
