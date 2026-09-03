// === Behavioral Contract: `gcli datasources` ===
// - Renders Name/UID/Type/URL/Default rows, default datasource first
// - -o json emits the full raw datasources payload array (jsonData kept)
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDatasourcesCommand_rendersAllDatasources(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "datasources")

	for _, want := range []string{"Universal", "u1", "prometheus", "yes", "vlog", "Billing"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestDatasourcesJSONFullPayload(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "datasources", "-o", "json")

	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %q (%v)", out, err)
	}
	if len(arr) == 0 {
		t.Fatal("empty array")
	}
	if _, ok := arr[0]["jsonData"]; !ok {
		t.Errorf("full payload must keep jsonData field:\n%s", out)
	}
	if strings.Contains(out, `"frames"`) {
		t.Errorf("normalized frames leaked into full-payload mode:\n%s", out)
	}
}
