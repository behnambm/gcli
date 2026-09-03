// === Behavioral Contract: api.Client.Capabilities ===
//   - Probes each command group once: auth, datasources, query, dashboards,
//     alerts, annotations, datasource-health
//   - Each probe reports OK / DENIED / ERROR:
//   - OK: the probe's request succeeds
//   - DENIED: the probe got an HTTP 403/404 (permission-hidden or missing)
//   - ERROR: anything else (500, network, decode failures)
//   - Capabilities never returns an error itself — every outcome lands in
//     the per-group statuses (the command must exit 0)
//   - The query probe runs a trivial instant query against the first
//     prometheus-type datasource; when none is visible it reports ERROR
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

const capsDatasources = `[{"uid":"u1","name":"Universal","type":"prometheus","url":"x","isDefault":true}]`

func TestCapabilities_allGroupsOK_reportsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/org":
			w.Write([]byte(`{"id":1,"name":"Acme"}`))
		case "/api/datasources":
			w.Write([]byte(capsDatasources))
		case "/api/search":
			w.Write([]byte(`[]`))
		case "/api/prometheus/grafana/api/v1/rules":
			w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
		case "/api/annotations":
			w.Write([]byte(`[]`))
		case "/api/datasources/uid/u1/health":
			w.Write([]byte(`{"status":"OK"}`))
		case "/api/ds/query":
			w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	caps, err := c.Capabilities(context.Background())

	if err != nil {
		t.Fatalf("Capabilities must never return an error: %v", err)
	}
	byGroup := map[string]Capability{}
	for _, cp := range caps {
		byGroup[cp.Group] = cp
	}
	for _, g := range []string{"auth", "query", "datasources", "dashboards", "alerts", "annotations", "datasource-health"} {
		if byGroup[g].Status != "OK" {
			t.Errorf("group %s = %+v, want OK", g, byGroup[g])
		}
	}
}

func TestCapabilities_allEndpointsHidden_reportsDENIED(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/org" {
			w.Write([]byte(`{"id":1}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	caps, err := c.Capabilities(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range caps {
		switch cp.Group {
		case "auth":
			if cp.Status != "OK" {
				t.Errorf("auth = %+v, want OK", cp)
			}
		default:
			if cp.Status != "DENIED" {
				t.Errorf("group %s = %+v, want DENIED", cp.Group, cp)
			}
			if cp.Detail == "" {
				t.Errorf("group %s DENIED without detail", cp.Group)
			}
		}
	}
}

func TestCapabilities_queryProbe_withoutPrometheusDatasource_reportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/org":
			w.Write([]byte(`{"id":1}`))
		case "/api/datasources":
			w.Write([]byte(`[{"uid":"u1","name":"Billing","type":"grafana-postgresql-datasource","url":"b"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	caps, err := c.Capabilities(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range caps {
		if cp.Group == "query" && cp.Status != "ERROR" {
			t.Errorf("query without prometheus datasource = %+v, want ERROR", cp)
		}
	}
}

func TestCapabilities_queryProbeFailure_reportsErrorNotDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/org":
			w.Write([]byte(`{"id":1}`))
		case "/api/datasources":
			w.Write([]byte(capsDatasources))
		case "/api/ds/query":
			http.Error(w, "boom", 500)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	caps, err := c.Capabilities(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range caps {
		if cp.Group == "query" && cp.Status != "ERROR" {
			t.Errorf("query probe 500 = %+v, want ERROR", cp)
		}
	}
}
