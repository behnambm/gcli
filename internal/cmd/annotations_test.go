// === Behavioral Contract: `gcli annotations` ===
// - Renders Time/Source/State/Text/Tags rows from the annotations API
// - Source is derived client-side: login → alertName → alert → "-"
// - Alert-linked annotations show PrevState→NewState in the State column
// - --tags sends repeated tags params; --dashboard sends dashboardUID
// - --from/--to are converted to epoch milliseconds
package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const annotationsResponse = `[
	{"id":3833414,"alertId":0,"time":1788067792043,"text":"deploy v1.2.3","tags":["deploy"],"newState":"","prevState":"","login":"deployer"},
	{"id":3833413,"alertId":9,"time":1788067731528,"text":"alert transition","tags":[],"newState":"Pending","prevState":"Normal","alertName":"High CPU"}
]`

func TestAnnotationsCommand_rendersRowsAndStateTransitions(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/annotations": annotationsResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "annotations")

	for _, want := range []string{"deploy v1.2.3", "deploy", "Normal→Pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestAnnotationsSourceColumn(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/annotations": annotationsResponse}, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	out := runCommand(t, "annotations")

	if !strings.Contains(out, "Source") || !strings.Contains(out, "deployer") {
		t.Errorf("output = %q, want Source column with login", out)
	}
}

func TestAnnotationsCommand_sendsTagAndDashboardFilters(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/annotations" {
			gotQuery = r.URL.RawQuery
			w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "annotations", "--tags", "deploy,release", "--dashboard", "abc")

	for _, want := range []string{"tags=deploy", "tags=release", "dashboardUID=abc", "from=", "to="} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("annotations query %q missing %q", gotQuery, want)
		}
	}
}
