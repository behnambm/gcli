package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

// TestAgainstRecordedFixtures replays real anonymized responses captured
// from a live Grafana instance (2026-08-30) and asserts the client parses them.
func TestAgainstRecordedFixtures(t *testing.T) {
	fixtures := map[string]string{
		"/api/health":      "health.json",
		"/api/datasources": "datasources.json",
		"/api/search":      "search.json",
		"/api/annotations": "annotations.json",
	}
	// ds/query responses keyed by exact body marker — serve by checking body content:
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ds/query" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			q := body["queries"].([]any)[0].(map[string]any)
			file := ""
			switch {
			case q["rawSql"] != nil:
				file = "postgres-table.json"
			case q["queryType"] == "stats":
				file = "vlogs-stats.json"
			case q["queryType"] == "range":
				file = "vlogs-range.json"
			case q["instant"] == true:
				file = "prom-instant.json"
			default:
				file = "prom-range.json"
			}
			serveFixture(w, file)
			return
		}
		if f, ok := fixtures[r.URL.Path+"?"+r.URL.RawQuery]; ok {
			serveFixture(w, f)
			return
		}
		if f, ok := fixtures[r.URL.Path]; ok {
			serveFixture(w, f)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 10 * time.Second})

	dss, err := c.Datasources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dss) < 10 {
		t.Errorf("datasources = %d, want >= 10 from live capture", len(dss))
	}

	// prometheus instant
	res, err := c.DSQuery(context.Background(), "prometheus", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "03hIm2zGz"}, Body: map[string]any{"expr": "count(up)", "instant": true}}}, "now-5m", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) == 0 {
		t.Error("prom instant: no frames")
	}

	// vlogs range
	res, err = c.DSQuery(context.Background(), "victoriametrics-logs-datasource", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "victoriametrics-logs-datasource", UID: "cf3iebh4uz1fkc"}, Body: map[string]any{"expr": "*", "queryType": "range", "limit": 5}}}, "now-1h", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) == 0 || len(res.Frames[0].Columns) != 3 {
		t.Errorf("vlogs range frames = %+v", res.Frames)
	}

	// postgres
	res, err = c.DSQuery(context.Background(), "grafana-postgresql-datasource", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "grafana-postgresql-datasource", UID: "fc1ae488-f63a-4658-8dcc-94b70f4c15c8"}, Body: map[string]any{"rawSql": "SELECT 1 AS one", "format": "table"}}}, "now-5m", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) == 0 {
		t.Error("postgres: no frames")
	}

	// annotations parse
	anns, err := c.Annotations(context.Background(), 0, 9999999999999, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) == 0 {
		t.Error("annotations: empty")
	}
}

func serveFixture(w http.ResponseWriter, name string) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		http.Error(w, "fixture missing: "+name, 500)
		return
	}
	w.Write(b)
}
