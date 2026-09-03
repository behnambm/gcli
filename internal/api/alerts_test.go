// === Behavioral Contract: api.Client.Alerts ===
//   - Primary source: /api/prometheus/grafana/api/v1/rules — one row per
//     alert instance; Name from alertname label (rule name fallback);
//     Severity/Folder from labels; Core strips " (Error)"/" (NoData)"
//   - When rules is hidden (403/404): falls back to /api/v2/alerts/statuses
//   - When both are hidden: falls back to legacy /api/alerts, mapping
//     lowercase legacy states to canonical Core values
//   - Non-hidden failures (401/500/network) propagate immediately — never
//     masked as "not accessible"
//   - When everything is hidden: error names all three endpoints
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

func alertsServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
}

const rulesResp = `{"status":"success","data":{"groups":[{"name":"PostgreSQL Service","file":"Acme Pay","rules":[{"state":"inactive","name":"High DB Connections","alerts":[{"labels":{"alertname":"High DB Connections","grafana_folder":"Acme Pay","severity":"warning"},"state":"Normal (Error)","activeAt":"2026-07-11T13:46:00Z","value":""},{"labels":{"alertname":"High DB Connections","grafana_folder":"Acme Pay","severity":"critical","pod":"svc8-0"},"state":"Alerting","activeAt":"2026-08-29T10:00:00Z","value":"1000"}]}]}]}}`

const rulerDetailResp = `{"Acme Pay":[{"name":"PostgreSQL Service","interval":"1m","rules":[{"expr":"sum by(pod) (pg_stat_activity_count{metrics-zeta-cluster=\"metrics-service\"}) > 900","for":"1m","labels":{"severity":"warning","target":"acme-credit"},"annotations":{"description":"PostgreSQL service has more than 900 connections"},"grafana_alert":{"id":41,"title":"High DB Connections [Metrics Zeta]"}}]}]}`

const rulesDetailResp = `{"status":"success","data":{"groups":[{"name":"g","file":"Acme Pay","rules":[{"name":"High DB Connections [Metrics Zeta]","state":"inactive","alerts":[{"labels":{"alertname":"High DB Connections [Metrics Zeta]","grafana_folder":"Acme Pay"},"state":"Alerting","activeAt":"2026-08-30T10:00:00Z"}]}]}]}}`

func TestAlerts_fromRulesEndpoint_returnsOneRowPerInstance(t *testing.T) {
	srv := alertsServer(t, map[string]string{"/api/prometheus/grafana/api/v1/rules": rulesResp})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	rows, err := c.Alerts(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	r0 := rows[0]
	if r0.Name != "High DB Connections" || r0.State != "Normal (Error)" || r0.Core != "Normal" {
		t.Errorf("row0 = %+v", r0)
	}
	if r0.Severity != "warning" || r0.Folder != "Acme Pay" || r0.ActiveAt == "" {
		t.Errorf("row0 = %+v", r0)
	}
	r1 := rows[1]
	if r1.Core != "Alerting" || r1.Severity != "critical" {
		t.Errorf("row1 = %+v", r1)
	}
}

func TestAlerts_rulesHidden_fallsBackToV2Statuses(t *testing.T) {
	routes := map[string]string{
		"/api/v2/alerts/statuses": `{"statuses":[{"labels":{"alertname":"DiskFull","severity":"critical"},"state":"Alerting"}]}`,
	}
	srv := alertsServer(t, routes) // rules path absent → 404
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	rows, err := c.Alerts(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "DiskFull" || rows[0].Core != "Alerting" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestAlerts_rulesAndV2Hidden_fallsBackToLegacy(t *testing.T) {
	routes := map[string]string{
		"/api/alerts": `[{"id":1,"name":"Legacy alert","state":"alerting"},{"id":2,"name":"Paused alert","state":"paused"}]`,
	}
	srv := alertsServer(t, routes)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	rows, err := c.Alerts(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Name != "Legacy alert" || rows[0].Core != "Alerting" {
		t.Errorf("row0 = %+v", rows[0])
	}
	if rows[1].Core != "Normal" {
		t.Errorf("legacy paused must map to Normal: %+v", rows[1])
	}
}

func TestAlerts_unauthorized_propagatesImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.Alerts(context.Background())

	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 401 {
		t.Fatalf("err = %v, want 401 *HTTPError (not masked)", err)
	}
}

func TestAlerts_legacyRealFailure_propagatesNotMasked(t *testing.T) {
	// rules and v2 hidden, legacy returns a real 500 — the caller must see
	// the 500, not a "no endpoint accessible" wrap that downstream
	// permission probes would misread as DENIED.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/alerts" {
			http.Error(w, "boom", 500)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.Alerts(context.Background())

	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 500 {
		t.Fatalf("err = %v, want unwrapped *HTTPError 500", err)
	}
}

func TestAlerts_allEndpointsHidden_combinedErrorNamesThem(t *testing.T) {
	srv := alertsServer(t, nil) // all three absent → 404
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.Alerts(context.Background())

	if err == nil {
		t.Fatal("want error when every endpoint is hidden")
	}
	for _, ep := range []string{"rules", "v2 statuses", "legacy"} {
		if !strings.Contains(err.Error(), ep) {
			t.Errorf("error must name all endpoints, missing %q: %v", ep, err)
		}
	}
}

// === Behavioral Contract: api.Client.AlertDetail ===
//   - Fetches the ruler definition from /api/ruler/grafana/api/v1/rules
//     (map keyed by folder); matches by grafana_alert.title OR
//     labels.alertname, case-insensitively
//   - Returns Name (title, falling back to alertname), Folder, Expr, For,
//     Severity, Annotations from the ruler definition
//   - Merges current State and ActiveAt from Alerts() rows matched by name
//   - Returns error "alert %q not found" when no rule matches
func TestAlertDetail_fromRuler_mergesDefinitionAndState(t *testing.T) {
	routes := map[string]string{
		"/api/ruler/grafana/api/v1/rules":      rulerDetailResp,
		"/api/prometheus/grafana/api/v1/rules": rulesDetailResp,
	}
	srv := alertsServer(t, routes)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	d, err := c.AlertDetail(context.Background(), "high db connections [Metrics Zeta]")

	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "High DB Connections [Metrics Zeta]" || d.Folder != "Acme Pay" {
		t.Errorf("detail = %+v", d)
	}
	if d.Expr == "" || d.For != "1m" || d.Severity != "warning" {
		t.Errorf("detail = %+v", d)
	}
	if d.Annotations["description"] == "" {
		t.Errorf("annotations lost: %+v", d)
	}
	if d.State != "Alerting" || d.ActiveAt == "" {
		t.Errorf("state merge failed: %+v", d)
	}
}

func TestAlertDetail_notFound_returnsError(t *testing.T) {
	srv := alertsServer(t, map[string]string{"/api/ruler/grafana/api/v1/rules": `{}`})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.AlertDetail(context.Background(), "nope")

	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found", err)
	}
}

func TestCoreState_stripsErrorSuffix(t *testing.T) {
	cases := map[string]string{
		"Normal":           "Normal",
		"Normal (Error)":   "Normal",
		"NoData (Error)":   "NoData",
		"Alerting":         "Alerting",
		"Pending (NoData)": "Pending",
	}
	for in, want := range cases {
		if got := coreState(in); got != want {
			t.Errorf("coreState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLegacyCore_mapsGrafanaLegacyStates(t *testing.T) {
	cases := map[string]string{
		"ok": "Normal", "paused": "Normal",
		"alerting": "Alerting", "pending": "Pending",
		"no_data": "NoData", "unknown": "Error",
		"custom": "custom",
	}
	for in, want := range cases {
		if got := legacyCore(in); got != want {
			t.Errorf("legacyCore(%q) = %q, want %q", in, got, want)
		}
	}
}
