// === Behavioral Contract: api.Client.DSQuery ===
//   - Sends POST to /api/ds/query?ds_type=<type> with {"queries":[...],"from","to"}
//   - Each query object carries refId and datasource{type,uid} INSIDE it;
//     body fields merge at the same level; explicit datasource/refId in the
//     request override any colliding body keys
//   - Response results are keyed by refId; frames normalize via frames.Normalize
//   - Per-refId errors ({error, errorSource}) become *QueryError with refId,
//     source, and message; successful frames are still returned alongside
//   - Query results for refIds absent from the response contribute nothing
//     (no frames, no error)
//   - Meta carries the datasource type, from/to, and a positive duration
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

func dsqueryServer(t *testing.T, resp string, onReq func(t *testing.T, path string, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onReq != nil {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			onReq(t, r.URL.String(), body)
		}
		w.Write([]byte(resp))
	}))
}

func TestDSQuery_buildsDatasourceInsideEachQueryObject(t *testing.T) {
	srv := dsqueryServer(t, `{"results":{}}`, func(t *testing.T, path string, body map[string]any) {
		if !strings.Contains(path, "ds_type=prometheus") {
			t.Errorf("path = %q, want ds_type=prometheus", path)
		}
		qs, ok := body["queries"].([]any)
		if !ok || len(qs) != 1 {
			t.Fatalf("queries = %v", body["queries"])
		}
		q0 := qs[0].(map[string]any)
		ds, ok := q0["datasource"].(map[string]any)
		if !ok || ds["uid"] != "uid-1" || ds["type"] != "prometheus" {
			t.Errorf("datasource must be inside the query object: %v", q0)
		}
		if q0["refId"] != "A" || q0["expr"] != "up" {
			t.Errorf("query object = %v", q0)
		}
		if body["from"] != "now-5m" || body["to"] != "now" {
			t.Errorf("from/to = %v/%v", body["from"], body["to"])
		}
	})
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.DSQuery(context.Background(), "prometheus",
		[]DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "uid-1"},
			Body: map[string]any{"expr": "up", "instant": true}}},
		"now-5m", "now")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDSQuery_returnsNormalizedFramesAndMeta(t *testing.T) {
	resp := `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Time","type":"time"},{"name":"Value","type":"number","labels":{"job":"api"}}]},"data":{"values":[[1788080730946],[2173]]}}]}}}`
	srv := dsqueryServer(t, resp, nil)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	res, err := c.DSQuery(context.Background(), "prometheus",
		[]DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "u"}}},
		"now-5m", "now")

	if err != nil {
		t.Fatalf("DSQuery returned error: %v", err)
	}
	if res.Meta.Datasource != "prometheus" || res.Meta.From != "now-5m" || res.Meta.DurationMS <= 0 {
		t.Errorf("meta = %+v", res.Meta)
	}
	if len(res.Frames) != 1 || len(res.Frames[0].Columns) != 2 {
		t.Errorf("frames = %+v", res.Frames)
	}
	if res.Frames[0].Columns[1].DisplayName() != "Value{job=api}" {
		t.Errorf("display = %q", res.Frames[0].Columns[1].DisplayName())
	}
}

func TestDSQuery_perRefIDError_becomesQueryError(t *testing.T) {
	resp := `{"results":{"A":{"error":"failed to make http request: dial tcp: connect: connection refused","errorSource":"downstream","status":500,"frames":[]}}}`
	srv := dsqueryServer(t, resp, nil)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.DSQuery(context.Background(), "victoriametrics-logs-datasource",
		[]DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "victoriametrics-logs-datasource", UID: "u"}}},
		"now-1h", "now")

	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *QueryError", err)
	}
	if qe.RefID != "A" || qe.Source != "downstream" {
		t.Errorf("queryError = %+v", qe)
	}
	if !strings.Contains(qe.Error(), "connection refused") {
		t.Errorf("error string = %q", qe.Error())
	}
}

func TestDSQuery_mixedSuccessAndError_returnsFramesAlongsideError(t *testing.T) {
	resp := `{"results":{"A":{"error":"boom","errorSource":"plugin","status":500,"frames":[]},"B":{"status":200,"frames":[{"schema":{"refId":"B","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`
	srv := dsqueryServer(t, resp, nil)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	res, err := c.DSQuery(context.Background(), "x", []DSQueryReq{
		{RefID: "A", Datasource: DatasourceRef{Type: "x", UID: "1"}},
		{RefID: "B", Datasource: DatasourceRef{Type: "x", UID: "2"}},
	}, "now", "now")

	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *QueryError for refId A", err)
	}
	if len(res.Frames) != 1 || res.Frames[0].RefID != "B" {
		t.Errorf("good frames must still be returned: %+v", res.Frames)
	}
}

func TestDSQuery_missingRefIDInResponse_contributesNothing(t *testing.T) {
	resp := `{"results":{"B":{"status":200,"frames":[{"schema":{"refId":"B","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`
	srv := dsqueryServer(t, resp, nil)
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	res, err := c.DSQuery(context.Background(), "x", []DSQueryReq{
		{RefID: "A", Datasource: DatasourceRef{Type: "x", UID: "1"}},
		{RefID: "B", Datasource: DatasourceRef{Type: "x", UID: "2"}},
	}, "now", "now")

	if err != nil {
		t.Fatalf("absent refId must not be an error: %v", err)
	}
	if len(res.Frames) != 1 || res.Frames[0].RefID != "B" {
		t.Errorf("frames = %+v", res.Frames)
	}
}
