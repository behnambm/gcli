// === Behavioral Contract: api.Client (Get/Post/HTTPError) ===
//   - Every request carries Authorization: Bearer <token>
//   - Basic base64(user:pass) when User/Pass set and Token empty (Bearer wins)
//   - X-Grafana-Org-Id sent when OrgID is set
//   - POST sends JSON bodies with Content-Type application/json
//   - Successful responses decode into the caller's target value
//   - Non-2xx responses become *HTTPError carrying status, endpoint, and body
//   - Hint(): 401 → token invalid; 403 → role lacks permission; 404 →
//     missing-or-permission-hidden; plugin.notRegistered body → dedicated hint
//   - ExitCode(): 401→3, 403/404→4, everything else→2
//   - Verbose dumps redact all secrets (token and/or pass) from request headers and response bodies
//   - Empty 2xx bodies decode to nothing (no error)
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

func TestGet_sendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]string{"version": "10.4.3"})
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "glsa_secret", Timeout: 5 * time.Second})
	var out map[string]string
	err := c.Get(context.Background(), "/api/health", &out)

	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if gotAuth != "Bearer glsa_secret" {
		t.Errorf("Authorization = %q, want Bearer glsa_secret", gotAuth)
	}
	if out["version"] != "10.4.3" {
		t.Errorf("out = %v", out)
	}
}

func TestPost_sendsJSONBody(t *testing.T) {
	var gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	var out map[string]string
	err := c.Post(context.Background(), "/api/ds/query", map[string]any{"queries": []any{}}, &out)

	if err != nil {
		t.Fatalf("Post returned error: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if len(gotBody["queries"].([]any)) != 0 {
		t.Errorf("body = %v", gotBody)
	}
}

func TestGet_emptySuccessBody_decodesNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	var out map[string]any
	if err := c.Get(context.Background(), "/api/x", &out); err != nil {
		t.Fatalf("204 must not be an error: %v", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nil", out)
	}
}

func TestHTTPError_401_hasAuthHintAndExitCode3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	var out any
	err := c.Get(context.Background(), "/api/org", &out)

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.StatusCode != 401 || he.ExitCode() != 3 {
		t.Errorf("401 mapping: %+v exit=%d", he, he.ExitCode())
	}
	if !strings.Contains(he.Hint(), "invalid") {
		t.Errorf("401 hint = %q", he.Hint())
	}
	if !strings.Contains(err.Error(), "/api/org") {
		t.Errorf("error must name the endpoint: %v", err)
	}
}

func TestHTTPError_403_hasPermissionHintAndExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", 403)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	err := c.Get(context.Background(), "/api/x", nil)

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.ExitCode() != 4 || !strings.Contains(he.Hint(), "permission") {
		t.Errorf("403 mapping: exit=%d hint=%q", he.ExitCode(), he.Hint())
	}
}

func TestHTTPError_404_mentionsPermissionHiding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	err := c.Get(context.Background(), "/api/v2/alerts/statuses", nil)

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.ExitCode() != 4 {
		t.Errorf("exit = %d, want 4", he.ExitCode())
	}
	if !strings.Contains(he.Hint(), "permissions hide it") {
		t.Errorf("404 hint = %q", he.Hint())
	}
}

func TestHTTPError_500_hasExitCode2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	err := c.Get(context.Background(), "/api/x", nil)

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.ExitCode() != 2 || he.Hint() != "" {
		t.Errorf("500 mapping: exit=%d hint=%q", he.ExitCode(), he.Hint())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error must include response body: %v", err)
	}
}

func TestHTTPError_pluginNotRegistered_getsDedicatedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Plugin not registered","messageId":"plugin.notRegistered","statusCode":404}`))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	err := c.Get(context.Background(), "/api/ds/query", nil)

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if !strings.Contains(he.Hint(), "not registered") {
		t.Errorf("hint = %q, want plugin-not-registered hint", he.Hint())
	}
}

func TestVerbose_redactsTokenFromDumps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"echo":"glsa_supersecret"}`))
	}))
	defer srv.Close()

	var buf strings.Builder
	c := NewClient(config.Config{URL: srv.URL, Token: "glsa_supersecret", Timeout: 5 * time.Second, Verbose: true})
	c.LogW = &buf
	var out any
	if err := c.Get(context.Background(), "/x", &out); err != nil {
		t.Fatal(err)
	}
	dump := buf.String()
	if strings.Contains(dump, "glsa_supersecret") {
		t.Errorf("verbose dump leaks token:\n%s", dump)
	}
	if !strings.Contains(dump, "REDACTED") {
		t.Errorf("verbose dump lacks redaction marker:\n%s", dump)
	}
}

func TestRequest_unreachableServer_returnsError(t *testing.T) {
	c := NewClient(config.Config{URL: "http://127.0.0.1:1", Token: "t", Timeout: time.Second})

	err := c.Get(context.Background(), "/api/health", nil)

	if err == nil {
		t.Fatal("want error for unreachable server")
	}
	var he *HTTPError
	if errors.As(err, &he) {
		t.Errorf("network failure must not be *HTTPError: %v", err)
	}
}

func TestNewClient_basicAuthWhenUserPassSet(t *testing.T) {
	c := NewClient(config.Config{URL: "http://x", User: "alice", Pass: "s3cret", Timeout: 5 * time.Second})
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if c.AuthHeader != want {
		t.Errorf("AuthHeader = %q, want %q", c.AuthHeader, want)
	}
}

func TestNewClient_bearerWinsOverBasicWhenTokenSet(t *testing.T) {
	c := NewClient(config.Config{URL: "http://x", Token: "glsa_t", User: "alice", Pass: "s3cret", Timeout: 5 * time.Second})
	if c.AuthHeader != "Bearer glsa_t" {
		t.Errorf("AuthHeader = %q, want bearer", c.AuthHeader)
	}
}

func TestClientDo_sendsOrgHeaderWhenSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Grafana-Org-Id"); got != "7" {
			t.Errorf("X-Grafana-Org-Id = %q, want 7", got)
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	c := NewClient(config.Config{URL: srv.URL, Token: "glsa_secret", OrgID: "7", Timeout: 5 * time.Second})
	var out map[string]any
	if err := c.Get(context.Background(), "/api/health", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestVerboseDump_redactsPass(t *testing.T) {
	var buf bytes.Buffer
	c := NewClient(config.Config{URL: "http://127.0.0.1:1", User: "alice", Pass: "s3cret", Timeout: 5 * time.Second, Verbose: true})
	c.LogW = &buf
	_ = c.Get(context.Background(), "/api/health", nil)
	if strings.Contains(buf.String(), "YWxpY2U6czNjcmV0") {
		t.Errorf("verbose dump leaks basic-auth credential:\n%s", buf.String())
	}
}
