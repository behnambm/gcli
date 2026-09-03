// === Behavioral Contract: `gcli dashboards` ===
//   - Bare search renders Title/UID/Type/Folder rows from the search API
//   - --get <uid> prints the raw dashboard JSON envelope verbatim (pure
//     JSON, no table chrome)
//   - --get + --export <path> writes the dashboard JSON to the file and
//     prints "written: <path>"; the exported file is in provisioning format
//     (dashboard object at top level, no id/version/meta)
//   - --export without --get fails with a clear error
package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const searchResponse = `[{"title":"Account","uid":"8GbEch5Mz","type":"dash-db","folderTitle":"Acme Pay"}]`

const dashboardResponse = `{"dashboard":{"uid":"account","title":"Account","id":5,"version":29},"meta":{"folderTitle":"Acme Pay"}}`

func TestDashboardsCommand_search_rendersDashboardRows(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/search": searchResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "dashboards", "account")

	for _, want := range []string{"Account", "8GbEch5Mz", "dash-db", "Acme Pay"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestDashboardsCommand_get_printsRawJSONOnly(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": dashboardResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "dashboards", "--get", "account")

	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--get must print pure JSON, got: %q (%v)", out, err)
	}
	if doc["dashboard"] == nil {
		t.Errorf("dashboard JSON missing dashboard key: %s", out)
	}
}

func TestDashboardsCommand_export_writesFileAndConfirms(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/account.json"
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": dashboardResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "dashboards", "--get", "account", "--export", path)

	if !strings.Contains(out, "written: "+path) {
		t.Errorf("output = %q, want written confirmation", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exported file missing: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("exported file is not JSON: %v", err)
	}
}

func TestDashboardsExportProvisioningFormat(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/dashboards/uid/account": dashboardResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	dir := t.TempDir()
	path := dir + "/account.json"
	out := runCommand(t, "dashboards", "--get", "account", "--export", path)

	if !strings.Contains(out, "written:") {
		t.Fatalf("output = %q", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("exported file is not JSON: %v", err)
	}
	if _, ok := doc["title"]; !ok {
		t.Errorf("export must contain dashboard fields at top level: %s", b)
	}
	for _, key := range []string{"id", "version", "meta"} {
		if _, ok := doc[key]; ok {
			t.Errorf("provisioning export must not contain %q: %s", key, b)
		}
	}
}

func TestDashboardsCommand_exportWithoutGet_fails(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/search": searchResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	_, err := runCommandErr(t, "dashboards", "--export", "x.json")

	if err == nil || !strings.Contains(err.Error(), "--export requires --get") {
		t.Fatalf("err = %v, want --export requires --get error", err)
	}
}
