// === Behavioral Contract: `gcli labels <datasource> [metric]` ===
//   - Resolves the named datasource and rejects any non-Prometheus type
//     (prometheus | victoriametrics-datasource)
//   - Lists label names by proxying GET /api/v1/labels through the datasource
//     proxy, sending the optional metric as ?match[]=
//   - Renders the names as a single "Label" column

// === Behavioral Contract: `gcli values <datasource> <label> [metric]` ===
//   - Resolves the named datasource and rejects any non-Prometheus type
//   - Lists label values by proxying GET /api/v1/label/<label>/values through
//     the datasource proxy, sending the optional metric as ?match[]=
//   - Renders the values as a single column named after the label
package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLabelsCommand_noMetric_listsLabelNames(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/labels": `{"status":"success","data":["job","instance"]}`,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "labels", "Universal")

	for _, want := range []string{"job", "instance"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing label %q", out, want)
		}
	}
}

func TestLabelsCommand_withMetric_sendsMatchQuery(t *testing.T) {
	var gotMatch []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources":
			w.Write([]byte(defaultDatasourcesResponse))
		case "/api/datasources/proxy/uid/u1/api/v1/labels":
			gotMatch = r.URL.Query()["match[]"]
			w.Write([]byte(`{"status":"success","data":["job","instance"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setEnv(t, srv.URL)

	runCommand(t, "labels", "Universal", "up")

	if len(gotMatch) != 1 || gotMatch[0] != "up" {
		t.Errorf("match[] = %v, want [up]", gotMatch)
	}
}

func TestLabelsCommand_nonPromDatasource_rejected(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "labels", "Billing")

	if err == nil || !strings.Contains(err.Error(), "not a Prometheus-type") {
		t.Fatalf("err = %v, want type explanation", err)
	}
}

func TestValuesCommand_listsLabelValues(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/label/job/values": `{"status":"success","data":["api","worker"]}`,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "values", "Universal", "job")

	if !strings.Contains(out, "api") {
		t.Errorf("output = %q, missing value %q", out, "api")
	}
}

func TestValuesCommand_withMetric_sendsMatchQuery(t *testing.T) {
	var gotMatch []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources":
			w.Write([]byte(defaultDatasourcesResponse))
		case "/api/datasources/proxy/uid/u1/api/v1/label/job/values":
			gotMatch = r.URL.Query()["match[]"]
			w.Write([]byte(`{"status":"success","data":["api","worker"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setEnv(t, srv.URL)

	runCommand(t, "values", "Universal", "job", "up")

	if len(gotMatch) != 1 || gotMatch[0] != "up" {
		t.Errorf("match[] = %v, want [up]", gotMatch)
	}
}

func TestValuesCommand_oneArg_usesDefaultDatasource(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/datasources/proxy/uid/u1/api/v1/label/job/values": `{"status":"success","data":["api","worker"]}`,
	}, nil)
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Universal")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	out := runCommand(t, "--profile", "p", "values", "job")

	if !strings.Contains(out, "api") {
		t.Errorf("output = %q, missing value %q", out, "api")
	}
}

func TestValuesCommand_nonPromDatasource_rejected(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "values", "Billing", "job")

	if err == nil || !strings.Contains(err.Error(), "not a Prometheus-type") {
		t.Fatalf("err = %v, want type explanation", err)
	}
}
