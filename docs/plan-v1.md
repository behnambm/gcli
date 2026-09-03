# gcli — Grafana CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `gcli`, a generic read-only CLI for the company Grafana (grafana.example.com, Grafana 10.4.3) supporting all its datasource types (Prometheus-style metrics, VictoriaLogs, PostgreSQL) plus Grafana-internal data (datasources, dashboards, alerts, annotations, health).

**Architecture:** Single Go binary (cobra CLI). One generic engine POSTs to `/api/ds/query` for any datasource; thin helpers (`prom`, `logs`, `sql`) build payloads. All responses normalize to a columnar `frames.Result` shape; three renderers (table/json/csv) consume only that shape. Grafana-internal commands hit read-only REST endpoints. RBAC-aware error mapping tolerates limited and full-permission tokens.

**Tech Stack:** Go 1.27 (stdlib `net/http`, `encoding/json`, `text/tabwriter`, `encoding/csv`), `github.com/spf13/cobra` (only external dep). No other deps.

**Spec:** [design-spec.md](design-spec.md) — the plan argues from the spec; read it first.

## Global Constraints

- Module path: `gcli`, Go 1.27, dir `grafana-cli/` at repo root.
- External deps: `cobra` ONLY. Tables = `text/tabwriter`, CSV = `encoding/csv`, no color libs (raw ANSI only, gated on TTY).
- Env vars: `GRAFANA_URL`, `GRAFANA_TOKEN` (required, no defaults). Flags `--url`/`--token` override.
- Token never logged; `--verbose` dumps redact `Authorization` header value.
- Exit codes: 1 config, 2 network/HTTP-other, 3 HTTP 401, 4 HTTP 403/404, 5 query error.
- `/api/ds/query` payload: datasource identity goes INSIDE each query object (`queries[].datasource.{type,uid}`); endpoint path takes `?ds_type=<type>`.
- VictoriaLogs `limit` must serialize as JSON number (string is silently ignored upstream → 1000).
- Datasource referenced by uid or case-insensitive name (resolution via `/api/datasources`).
- JSON parsers everywhere must ignore unknown fields (full-permission tokens may surface extra API fields).
- Time range syntax: Grafana relative (`now-1h`, `now-1d/d`) + RFC3339 absolute; ds/query accepts these strings as-is.
- Commits: conventional style (`feat(gcli): ...`, `fix(gcli): ...`), never push.
- All tests hermetic: `httptest` fakes, no live Grafana required.

---

### Task 1: Module scaffold + config loading

**Files:**
- Create: `grafana-cli/go.mod`
- Create: `grafana-cli/internal/config/config.go`
- Test: `grafana-cli/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{URL, Token string; Timeout time.Duration; Output string; NoColor, Verbose bool}`; `config.Load(flagURL, flagToken string, timeout time.Duration, output string, noColor, verbose bool) (Config, error)`; errors `ErrMissingURL`, `ErrMissingToken` (sentinel, `errors.Is`-compatible).

- [ ] **Step 1: Create module**

```bash
mkdir -p grafana-cli/internal/config
```

`grafana-cli/go.mod`:
```
module gcli

go 1.27
```

- [ ] **Step 2: Write failing test**

`grafana-cli/internal/config/config_test.go`:
```go
package config

import (
	"errors"
	"testing"
	"time"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://grafana.example.com/")
	t.Setenv("GRAFANA_TOKEN", "glsa_secret")
	cfg, err := Load("", "", 30*time.Second, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://grafana.example.com" {
		t.Errorf("URL = %q, want trailing slash trimmed", cfg.URL)
	}
	if cfg.Token != "glsa_secret" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.Output != "table" {
		t.Errorf("Output = %q, want default table", cfg.Output)
	}
}

func TestLoadFlagOverridesEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://env.example.com")
	t.Setenv("GRAFANA_TOKEN", "env-token")
	cfg, err := Load("https://flag.example.com", "flag-token", 0, "json", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://flag.example.com" || cfg.Token != "flag-token" {
		t.Errorf("flags must override env: %+v", cfg)
	}
	if cfg.Output != "json" || !cfg.NoColor || !cfg.Verbose {
		t.Errorf("misc flags not applied: %+v", cfg)
	}
}

func TestLoadMissingVars(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")
	_, err := Load("", "", 0, "", false, false)
	if !errors.Is(err, ErrMissingURL) {
		t.Errorf("err = %v, want ErrMissingURL", err)
	}
	t.Setenv("GRAFANA_URL", "https://x.example.com")
	_, err = Load("", "", 0, "", false, false)
	if !errors.Is(err, ErrMissingToken) {
		t.Errorf("err = %v, want ErrMissingToken", err)
	}
}

func TestLoadRejectsBadOutput(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://x.example.com")
	t.Setenv("GRAFANA_TOKEN", "t")
	_, err := Load("", "", 0, "yaml", false, false)
	if err == nil {
		t.Fatal("want error for output=yaml")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/config/`
Expected: FAIL — "undefined: Load" / "undefined: ErrMissingURL"

- [ ] **Step 4: Implement**

`grafana-cli/internal/config/config.go`:
```go
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var (
	ErrMissingURL   = errors.New("GRAFANA_URL is not set")
	ErrMissingToken = errors.New("GRAFANA_TOKEN is not set")
)

type Config struct {
	URL     string
	Token   string
	Timeout time.Duration
	Output  string
	NoColor bool
	Verbose bool
}

func Load(flagURL, flagToken string, timeout time.Duration, output string, noColor, verbose bool) (Config, error) {
	url := firstNonEmpty(flagURL, os.Getenv("GRAFANA_URL"))
	token := firstNonEmpty(flagToken, os.Getenv("GRAFANA_TOKEN"))
	if url == "" {
		return Config{}, fmt.Errorf("%w — export GRAFANA_URL=https://your-grafana or pass --url", ErrMissingURL)
	}
	if token == "" {
		return Config{}, fmt.Errorf("%w — create a service-account token in Grafana and export it, or pass --token", ErrMissingToken)
	}
	if output == "" {
		output = "table"
	}
	switch output {
	case "table", "json", "csv":
	default:
		return Config{}, fmt.Errorf("invalid --output %q: must be table, json or csv", output)
	}
	return Config{
		URL:     strings.TrimRight(url, "/"),
		Token:   token,
		Timeout: timeout,
		Output:  output,
		NoColor: noColor,
		Verbose: verbose,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/config/ -v`
Expected: PASS (4 tests)

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/go.mod grafana-cli/internal/config/
git commit -m "feat(gcli): module scaffold + env-based config loading"
```

---

### Task 2: HTTP client core (auth, errors, redaction)

**Files:**
- Create: `grafana-cli/internal/api/client.go`
- Test: `grafana-cli/internal/api/client_test.go`

**Interfaces:**
- Consumes: `config.Config` from Task 1.
- Produces: `api.NewClient(cfg config.Config) *Client`; `(*Client).Get(ctx, path string, out any) error`; `(*Client).Post(ctx, path string, body any, out any) error`; `type HTTPError struct{ StatusCode int; Endpoint, Body string }` with methods `Error() string`, `Hint() string`, `ExitCode() int`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/api/client_test.go`:
```go
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

	"gcli/internal/config"
)

func TestGetSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]string{"version": "10.4.3"})
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "glsa_secret", Timeout: 5 * time.Second})
	var out map[string]string
	if err := c.Get(context.Background(), "/api/health", &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer glsa_secret" {
		t.Errorf("Authorization = %q, want Bearer glsa_secret", gotAuth)
	}
	if out["version"] != "10.4.3" {
		t.Errorf("out = %v", out)
	}
}

func TestPostSendsJSON(t *testing.T) {
	var gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	var out map[string]string
	err := c.Post(context.Background(), "/api/ds/query?ds_type=prometheus", map[string]any{"queries": []any{}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if len(gotBody["queries"].([]any)) != 0 {
		t.Errorf("body = %v", gotBody)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v2/alerts/statuses") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})

	var out any
	err := c.Get(context.Background(), "/api/v2/alerts/statuses", &out)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.StatusCode != 404 || he.ExitCode() != 4 {
		t.Errorf("404 mapping: %+v exit=%d", he, he.ExitCode())
	}
	if !strings.Contains(he.Hint(), "token permissions hide it") {
		t.Errorf("404 hint = %q", he.Hint())
	}

	err = c.Get(context.Background(), "/api/org", &out)
	if !errors.As(err, &he) || he.ExitCode() != 3 {
		t.Fatalf("401 mapping: %v", err)
	}
	if !strings.Contains(he.Hint(), "invalid") {
		t.Errorf("401 hint = %q", he.Hint())
	}
}

func TestVerboseRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
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

func TestPluginNotRegisteredHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Plugin not registered","messageId":"plugin.notRegistered","statusCode":404}`))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	var out any
	err := c.Get(context.Background(), "/api/ds/query", &out)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(he.Hint(), "not registered") {
		t.Errorf("hint = %q, want plugin-not-registered hint", he.Hint())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/`
Expected: FAIL — "undefined: NewClient"

- [ ] **Step 3: Implement**

`grafana-cli/internal/api/client.go`:
```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"

	"gcli/internal/config"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   string
	Verbose bool
	LogW    io.Writer // verbose dumps; nil = io.Discard
}

func NewClient(cfg config.Config) *Client {
	return &Client{
		BaseURL: cfg.URL,
		HTTP:    &http.Client{Timeout: cfg.Timeout},
		Token:   cfg.Token,
		Verbose: cfg.Verbose,
		LogW:    io.Discard,
	}
}

type HTTPError struct {
	StatusCode int
	Endpoint   string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d: %s", e.Endpoint, e.StatusCode, truncate(e.Body, 300))
}

func (e *HTTPError) Hint() string {
	if strings.Contains(e.Body, "plugin.notRegistered") {
		return "datasource plugin not registered — the datasource is broken or was removed on this Grafana"
	}
	switch e.StatusCode {
	case 401:
		return "token is invalid, expired, or revoked — check GRAFANA_TOKEN"
	case 403:
		return "token role lacks permission for this endpoint — needs a Grafana role with the matching RBAC permission"
	case 404:
		return "endpoint missing: either this Grafana version lacks it, or token permissions hide it (Grafana returns 404 for both)"
	default:
		return ""
	}
}

func (e *HTTPError) ExitCode() int {
	switch e.StatusCode {
	case 401:
		return 3
	case 403, 404:
		return 4
	default:
		return 2
	}
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Verbose {
		dump, _ := httputil.DumpRequestOut(req, true)
		fmt.Fprintf(c.LogW, "--- request ---\n%s\n", redact(string(dump), c.Token))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if c.Verbose {
		fmt.Fprintf(c.LogW, "--- response %d ---\n%s\n", resp.StatusCode, redact(string(respBody), c.Token))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Endpoint: path, Body: string(respBody)}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "REDACTED")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/api/ -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/api/
git commit -m "feat(gcli): http client with auth header, RBAC-aware errors, token redaction"
```

---

### Task 3: Time range parser

**Files:**
- Create: `grafana-cli/internal/timeparse/timeparse.go`
- Test: `grafana-cli/internal/timeparse/timeparse_test.go`

**Interfaces:**
- Produces: `timeparse.ParseToEpochMS(s string, now time.Time) (int64, error)`

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/timeparse/timeparse_test.go`:
```go
package timeparse

import (
	"testing"
	"time"
)

func TestParseToEpochMS(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want int64
	}{
		{"now", now.UnixMilli()},
		{"now-1h", now.Add(-time.Hour).UnixMilli()},
		{"now-5m", now.Add(-5 * time.Minute).UnixMilli()},
		{"now-30s", now.Add(-30 * time.Second).UnixMilli()},
		{"now-1d", now.Add(-24 * time.Hour).UnixMilli()},
		{"now-2w", now.Add(-14 * 24 * time.Hour).UnixMilli()},
		{"2026-08-01T00:00:00Z", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli()},
	}
	for _, tc := range cases {
		got, err := ParseToEpochMS(tc.in, now)
		if err != nil {
			t.Errorf("ParseToEpochMS(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseToEpochMS(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseToEpochMSTruncation(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 30, 45, 0, time.UTC)
	// now-1d/d = start of day of (now - 1d)
	want := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC).UnixMilli()
	got, err := ParseToEpochMS("now-1d/d", now)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ParseToEpochMS(now-1d/d) = %d, want %d", got, want)
	}
	// now/w = start of current week (Monday, UTC)
	wantWeek := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	gotWeek, err := ParseToEpochMS("now/w", now)
	if err != nil {
		t.Fatal(err)
	}
	if gotWeek != wantWeek {
		t.Errorf("ParseToEpochMS(now/w) = %d, want %d", gotWeek, wantWeek)
	}
}

func TestParseToEpochMSErrors(t *testing.T) {
	now := time.Now()
	for _, in := range []string{"", "now-1x", "yesterday", "now-", "now-1h/foo"} {
		if _, err := ParseToEpochMS(in, now); err == nil {
			t.Errorf("ParseToEpochMS(%q): want error", in)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/timeparse/`
Expected: FAIL — "undefined: ParseToEpochMS"

- [ ] **Step 3: Implement**

`grafana-cli/internal/timeparse/timeparse.go`:
```go
package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseToEpochMS parses Grafana-style times ("now", "now-1h", "now-1d/d",
// "now/w", RFC3339 absolute) into epoch milliseconds.
func ParseToEpochMS(s string, now time.Time) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty time string")
	}
	if s == "now" {
		return now.UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), nil
	}
	if strings.HasPrefix(s, "now/") {
		unit := strings.TrimPrefix(s, "now/")
		if unit != "w" {
			return 0, fmt.Errorf("unsupported truncation %q: only now/w is supported", s)
		}
		// Grafana weeks start Monday.
		wd := (int(now.Weekday()) + 6) % 7
		start := now.AddDate(0, 0, -wd).Truncate(24 * time.Hour)
		return start.UnixMilli(), nil
	}
	re := regexp.MustCompile(`^now-(\d+)([smhdw])(?:/([smhdw]))?$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid time %q: use now, now-<n><s|m|h|d|w>[/<unit>], or RFC3339", s)
	}
	n, _ := strconv.Atoi(m[1])
	base := now.Add(-time.Duration(n) * unitDuration(m[2]))
	if m[3] != "" {
		base = truncateTo(base, m[3])
	}
	return base.UnixMilli(), nil
}

func unitDuration(u string) time.Duration {
	switch u {
	case "s":
		return time.Second
	case "m":
		return time.Minute
	case "h":
		return time.Hour
	case "d":
		return 24 * time.Hour
	case "w":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

func truncateTo(t time.Time, unit string) time.Time {
	utc := t.UTC()
	y, mo, d := utc.Date()
	switch unit {
	case "w":
		wd := (int(utc.Weekday()) + 6) % 7
		return time.Date(y, mo, d-wd, 0, 0, 0, 0, time.UTC)
	case "d":
		return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	case "h":
		return time.Date(y, mo, d, utc.Hour(), 0, 0, 0, time.UTC)
	case "m":
		return time.Date(y, mo, d, utc.Hour(), utc.Minute(), 0, 0, time.UTC)
	default:
		return utc.Truncate(time.Second)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/timeparse/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/timeparse/
git commit -m "feat(gcli): grafana-style relative time parser"
```

---

### Task 4: Frame normalization (dataplane → columnar)

**Files:**
- Create: `grafana-cli/internal/frames/frames.go`
- Test: `grafana-cli/internal/frames/frames_test.go`

**Interfaces:**
- Produces: `frames.Column{Name string; Labels map[string]string; Values []any}`; `frames.Frame{RefID, Name string; Columns []Column}`; `frames.Meta{Datasource, Query, From, To string; DurationMS int64}`; `frames.Result{Meta Meta; Frames []Frame}`; `frames.RawFrame` (exported JSON mirror of dataplane frame); `frames.Normalize(raws []RawFrame) []Frame`; `func (c Column) DisplayName() string` (Name + `{k=v,...}` sorted labels suffix when labels non-empty).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/frames/frames_test.go`:
```go
package frames

import (
	"testing"
	"time"
)

func TestNormalizePrometheusVector(t *testing.T) {
	raw := []RawFrame{{
		Schema: rawSchema{
			RefID: "A",
			Fields: []rawField{
				{Name: "Time", Type: "time"},
				{Name: "Value", Type: "number", Labels: map[string]string{"job": "api", "instance": "pod-1"}},
			},
		},
		Data: rawData{Values: [][]any{{float64(1788080730946)}, {float64(2173)}}},
	}}
	got := Normalize(raw)
	if len(got) != 1 || len(got[0].Columns) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].RefID != "A" {
		t.Errorf("RefID = %q", got[0].RefID)
	}
	ts, ok := got[0].Columns[0].Values[0].(time.Time)
	if !ok {
		t.Fatalf("Time value = %T, want time.Time", got[0].Columns[0].Values[0])
	}
	if ts.UTC().Format("2006-01-02T15:04:05Z") != "2026-08-30T09:05:30Z" {
		t.Errorf("time = %v", ts)
	}
	if got[0].Columns[1].Values[0] != float64(2173) {
		t.Errorf("value = %v", got[0].Columns[1].Values[0])
	}
	want := "Value{instance=pod-1,job=api}"
	if got[0].Columns[1].DisplayName() != want {
		t.Errorf("DisplayName = %q, want %q", got[0].Columns[1].DisplayName(), want)
	}
}

func TestNormalizeVLogsStream(t *testing.T) {
	raw := []RawFrame{{
		Schema: rawSchema{
			RefID: "A",
			Fields: []rawField{
				{Name: "Time", Type: "time"},
				{Name: "Line", Type: "string"},
				{Name: "labels", Type: "other"},
			},
		},
		Data: rawData{Values: [][]any{
			{float64(1788080773997), float64(1788080773996)},
			{"error: db down", "request ok"},
			{`{"app":"acme-pay"}`, `{"app":"acme-pay"}`},
		}},
	}}
	got := Normalize(raw)
	f := got[0]
	if len(f.Columns) != 3 || len(f.Columns[0].Values) != 2 {
		t.Fatalf("got %+v", got)
	}
	if f.Columns[1].Values[0] != "error: db down" {
		t.Errorf("line = %v", f.Columns[1].Values[0])
	}
	if _, ok := f.Columns[0].Values[0].(time.Time); !ok {
		t.Errorf("Time value not converted: %T", f.Columns[0].Values[0])
	}
}

func TestNormalizeSQLTable(t *testing.T) {
	raw := []RawFrame{{
		Schema: rawSchema{RefID: "A", Fields: []rawField{{Name: "one", Type: "number"}}},
		Data:   rawData{Values: [][]any{{float64(1)}}},
	}}
	got := Normalize(raw)
	if got[0].Columns[0].Name != "one" || got[0].Columns[0].Values[0] != float64(1) {
		t.Errorf("got %+v", got)
	}
	if got[0].Columns[0].DisplayName() != "one" {
		t.Errorf("DisplayName without labels = %q, want plain name", got[0].Columns[0].DisplayName())
	}
}

func TestNormalizeNilTime(t *testing.T) {
	raw := []RawFrame{{
		Schema: rawSchema{RefID: "A", Fields: []rawField{{Name: "Time", Type: "time"}}},
		Data:   rawData{Values: [][]any{{nil}}},
	}}
	got := Normalize(raw)
	if got[0].Columns[0].Values[0] != nil {
		t.Errorf("nil time must stay nil, got %v", got[0].Columns[0].Values[0])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/frames/`
Expected: FAIL — "undefined: RawFrame"

- [ ] **Step 3: Implement**

`grafana-cli/internal/frames/frames.go`:
```go
package frames

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Column struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Values []any             `json:"values"`
}

type Frame struct {
	RefID   string   `json:"refId"`
	Name    string   `json:"name,omitempty"`
	Columns []Column `json:"columns"`
}

type Meta struct {
	Datasource string `json:"datasource"`
	Query      string `json:"query,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	DurationMS int64  `json:"duration_ms"`
}

type Result struct {
	Meta   Meta    `json:"meta"`
	Frames []Frame `json:"frames"`
}

// RawFrame mirrors the Grafana dataplane frame JSON.
type RawFrame struct {
	Schema rawSchema `json:"schema"`
	Data   rawData   `json:"data"`
}

type rawSchema struct {
	RefID  string     `json:"refId"`
	Name   string     `json:"name"`
	Fields []rawField `json:"fields"`
}

type rawField struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Type   string            `json:"type"`
}

type rawData struct {
	Values [][]any `json:"values"`
}

func Normalize(raws []RawFrame) []Frame {
	out := make([]Frame, 0, len(raws))
	for _, r := range raws {
		f := Frame{RefID: r.Schema.RefID, Name: r.Schema.Name}
		for i, fld := range r.Schema.Fields {
			col := Column{Name: fld.Name, Labels: fld.Labels}
			if i < len(r.Data.Values) {
				col.Values = normalizeValues(r.Data.Values[i], fld.Type)
			}
			f.Columns = append(f.Columns, col)
		}
		out = append(out, f)
	}
	return out
}

func normalizeValues(vals []any, typ string) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
		if typ == "time" && v != nil {
			if ms, ok := toFloat64(v); ok {
				out[i] = time.UnixMilli(int64(ms)).UTC()
			}
		}
	}
	return out
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func (c Column) DisplayName() string {
	if len(c.Labels) == 0 {
		return c.Name
	}
	keys := make([]string, 0, len(c.Labels))
	for k := range c.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, c.Labels[k]))
	}
	return fmt.Sprintf("%s{%s}", c.Name, strings.Join(parts, ","))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/frames/ -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/frames/
git commit -m "feat(gcli): dataplane frame normalization with display names"
```

---

### Task 5: Renderers (table / json / csv)

**Files:**
- Create: `grafana-cli/internal/render/render.go`
- Test: `grafana-cli/internal/render/render_test.go`

**Interfaces:**
- Consumes: `frames.Result` from Task 4.
- Produces: `render.Options{Output string; NoColor bool; Color bool; FrameIdx int}`; `render.Render(w io.Writer, res frames.Result, opts Options) error`; `render.Colorize(s, color string) string` (color ∈ green/red/yellow; returns plain s when !Color).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/render/render_test.go`:
```go
package render

import (
	"strings"
	"testing"
	"time"

	"gcli/internal/frames"
)

func sampleResult() frames.Result {
	return frames.Result{
		Meta: frames.Meta{Datasource: "prometheus", Query: "count(up)", From: "now-5m", To: "now", DurationMS: 42},
		Frames: []frames.Frame{{
			RefID: "A",
			Columns: []frames.Column{
				{Name: "Time", Values: []any{time.Date(2026, 8, 30, 9, 5, 30, 0, time.UTC)}},
				{Name: "Value", Labels: map[string]string{"job": "api"}, Values: []any{float64(2173)}},
			},
		}},
	}
}

func TestRenderTable(t *testing.T) {
	var b strings.Builder
	err := Render(&b, sampleResult(), Options{Output: "table"})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Time", "Value{job=api}", "2026-08-30", "2173"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderJSON(t *testing.T) {
	var b strings.Builder
	err := Render(&b, sampleResult(), Options{Output: "json"})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{`"meta"`, `"datasource": "prometheus"`, `"query": "count(up)"`, `"frames"`, `"refId": "A"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCSV(t *testing.T) {
	var b strings.Builder
	err := Render(&b, sampleResult(), Options{Output: "csv", FrameIdx: 0})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want header + 1 row:\n%s", len(lines), b.String())
	}
	if !strings.Contains(lines[0], "Time") || !strings.Contains(lines[0], "Value{job=api}") {
		t.Errorf("csv header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "2173") {
		t.Errorf("csv row = %q", lines[1])
	}
}

func TestRenderCSVFrameOutOfRange(t *testing.T) {
	var b strings.Builder
	err := Render(&b, sampleResult(), Options{Output: "csv", FrameIdx: 5})
	if err == nil {
		t.Fatal("want error for FrameIdx out of range")
	}
}

func TestColorize(t *testing.T) {
	if got := Colorize("Alerting", "red", false); got != "Alerting" {
		t.Errorf("NoColor: %q", got)
	}
	if got := Colorize("Alerting", "red", true); !strings.HasPrefix(got, "\x1b[31m") {
		t.Errorf("colored = %q, want ANSI prefix", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/render/`
Expected: FAIL — "undefined: Render"

- [ ] **Step 3: Implement**

`grafana-cli/internal/render/render.go`:
```go
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"gcli/internal/frames"
)

type Options struct {
	Output   string
	NoColor  bool
	Color    bool // ANSI allowed (TTY + !NoColor)
	FrameIdx int
}

const (
	cellWidth     = 40
	timeLayout    = "2006-01-02 15:04:05"
	defaultWidth  = 120
	jsonNull      = "null"
)

func Render(w io.Writer, res frames.Result, opts Options) error {
	switch opts.Output {
	case "table":
		return renderTable(w, res, opts)
	case "json":
		return renderJSON(w, res)
	case "csv":
		return renderCSV(w, res, opts.FrameIdx)
	default:
		return fmt.Errorf("invalid output %q", opts.Output)
	}
}

func renderTable(w io.Writer, res frames.Result, opts Options) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	if len(res.Frames) > 1 {
		fmt.Fprintf(tw, "frames: %d\t\n\n", len(res.Frames))
	}
	for i, f := range res.Frames {
		if len(res.Frames) > 1 {
			fmt.Fprintf(tw, "Frame %d: %s (refId %s)\t\n", i, f.Name, f.RefID)
		}
		headers := make([]string, len(f.Columns))
		for j, c := range f.Columns {
			headers[j] = c.DisplayName()
		}
		fmt.Fprintln(tw, strings.Join(headers, "\t"))
		rowCount := 0
		for _, c := range f.Columns {
			if len(c.Values) > rowCount {
				rowCount = len(c.Values)
			}
		}
		for r := 0; r < rowCount; r++ {
			cells := make([]string, len(f.Columns))
			for j, c := range f.Columns {
				if r < len(c.Values) {
					cells[j] = formatCell(c.Values[r])
				} else {
					cells[j] = ""
				}
			}
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if i < len(res.Frames)-1 {
			fmt.Fprintln(tw)
		}
	}
	return tw.Flush()
}

func formatCell(v any) string {
	switch t := v.(type) {
	case nil:
		return jsonNull
	case time.Time:
		return t.Local().Format(timeLayout)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return truncateRunes(t, cellWidth)
	default:
		b, _ := json.Marshal(t)
		return truncateRunes(string(b), cellWidth)
	}
}

func renderJSON(w io.Writer, res frames.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func renderCSV(w io.Writer, res frames.Result, frameIdx int) error {
	if frameIdx >= len(res.Frames) {
		return fmt.Errorf("frame %d out of range: result has %d frames", frameIdx, len(res.Frames))
	}
	f := res.Frames[frameIdx]
	cw := csv.NewWriter(w)
	headers := make([]string, len(f.Columns))
	for j, c := range f.Columns {
		headers[j] = c.DisplayName()
	}
	if err := cw.Write(headers); err != nil {
		return err
	}
	rowCount := 0
	for _, c := range f.Columns {
		if len(c.Values) > rowCount {
			rowCount = len(c.Values)
		}
	}
	for r := 0; r < rowCount; r++ {
		row := make([]string, len(f.Columns))
		for j, c := range f.Columns {
			if r < len(c.Values) {
				row[j] = formatCell(c.Values[r])
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func Colorize(s, color string, enabled bool) string {
	if !enabled {
		return s
	}
	var code string
	switch color {
	case "red":
		code = "31"
	case "green":
		code = "32"
	case "yellow":
		code = "33"
	default:
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/render/ -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/render/
git commit -m "feat(gcli): table/json/csv renderers over normalized frames"
```

---

### Task 6: DSQuery engine (payload builder + envelope)

**Files:**
- Create: `grafana-cli/internal/api/dsquery.go`
- Test: `grafana-cli/internal/api/dsquery_test.go`

**Interfaces:**
- Consumes: `Client.Get/Post` from Task 2, `frames` from Task 4.
- Produces: `api.DatasourceRef{Type, UID string}`; `api.DSQueryReq{RefID string; Datasource DatasourceRef; Body map[string]any}`; `api.QueryError{RefID, Source, Msg string}` with `Error() string`; `(*Client).DSQuery(ctx, dsType string, queries []DSQueryReq, from, to string) (frames.Result, error)`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/api/dsquery_test.go`:
```go
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

	"gcli/internal/config"
	"gcli/internal/frames"
)

func TestDSQueryBuildsPayload(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{}})
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	_, err := c.DSQuery(context.Background(), "prometheus",
		[]DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "uid-1"},
			Body: map[string]any{"expr": "up", "instant": true}}},
		"now-5m", "now")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "ds_type=prometheus") {
		t.Errorf("path = %q, want ds_type=prometheus", gotPath)
	}
	qs, ok := gotBody["queries"].([]any)
	if !ok || len(qs) != 1 {
		t.Fatalf("queries = %v", gotBody["queries"])
	}
	q0 := qs[0].(map[string]any)
	ds, ok := q0["datasource"].(map[string]any)
	if !ok || ds["uid"] != "uid-1" || ds["type"] != "prometheus" {
		t.Errorf("datasource must be INSIDE query object: %v", q0)
	}
	if q0["refId"] != "A" || q0["expr"] != "up" {
		t.Errorf("query object = %v", q0)
	}
	if gotBody["from"] != "now-5m" || gotBody["to"] != "now" {
		t.Errorf("from/to = %v/%v", gotBody["from"], gotBody["to"])
	}
}

func TestDSQueryParsesFrames(t *testing.T) {
	resp := `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Time","type":"time"},{"name":"Value","type":"number","labels":{"job":"api"}}]},"data":{"values":[[1788080730946],[2173]]}}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	res, err := c.DSQuery(context.Background(), "prometheus",
		[]DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "u"}}}, "now-5m", "now")
	if err != nil {
		t.Fatal(err)
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

func TestDSQueryPerRefIDError(t *testing.T) {
	resp := `{"results":{"A":{"error":"failed to make http request: dial tcp: connect: connection refused","errorSource":"downstream","status":500,"frames":[]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(resp))
	}))
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

func TestDSQueryMixedSuccessAndError(t *testing.T) {
	resp := `{"results":{"A":{"error":"boom","errorSource":"plugin","status":500,"frames":[]},"B":{"status":200,"frames":[{"schema":{"refId":"B","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	res, err := c.DSQuery(context.Background(), "x", []DSQueryReq{
		{RefID: "A", Datasource: DatasourceRef{Type: "x", UID: "1"}},
		{RefID: "B", Datasource: DatasourceRef{Type: "x", UID: "2"}},
	}, "now", "now")
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *QueryError", err)
	}
	if len(res.Frames) != 1 || res.Frames[0].RefID != "B" {
		t.Errorf("good frames must still be returned: %+v", res.Frames)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/`
Expected: FAIL — "undefined: DSQuery"

- [ ] **Step 3: Implement**

`grafana-cli/internal/api/dsquery.go`:
```go
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gcli/internal/frames"
)

type DatasourceRef struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type DSQueryReq struct {
	RefID      string
	Datasource DatasourceRef
	Body       map[string]any
}

type QueryError struct {
	RefID  string
	Source string
	Msg    string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("query %s failed (%s): %s", e.RefID, e.Source, e.Msg)
}

// DSQuery runs queries against /api/ds/query and returns normalized frames.
// On per-refId failures it returns the successful frames plus the joined
// QueryErrors — callers render what succeeded and surface the error.
func (c *Client) DSQuery(ctx context.Context, dsType string, queries []DSQueryReq, from, to string) (frames.Result, error) {
	payload := struct {
		Queries []map[string]any `json:"queries"`
		From    string           `json:"from"`
		To      string           `json:"to"`
	}{From: from, To: to}
	for _, q := range queries {
		m := map[string]any{}
		for k, v := range q.Body {
			m[k] = v
		}
		m["refId"] = q.RefID
		m["datasource"] = q.Datasource
		payload.Queries = append(payload.Queries, m)
	}

	start := time.Now()
	var env struct {
		Results map[string]struct {
			Status      int                `json:"status"`
			Error       string             `json:"error"`
			ErrorSource string             `json:"errorSource"`
			Frames      []frames.RawFrame  `json:"frames"`
		} `json:"results"`
	}
	err := c.Post(ctx, "/api/ds/query?ds_type="+dsType, payload, &env)
	if err != nil {
		return frames.Result{}, err
	}

	res := frames.Result{Meta: frames.Meta{
		Datasource: dsType,
		From:       from,
		To:         to,
		DurationMS: time.Since(start).Milliseconds(),
	}}
	var errs []error
	for _, q := range queries {
		r, ok := env.Results[q.RefID]
		if !ok {
			continue
		}
		if r.Error != "" {
			errs = append(errs, &QueryError{RefID: q.RefID, Source: r.ErrorSource, Msg: r.Error})
			continue
		}
		res.Frames = append(res.Frames, frames.Normalize(r.Frames)...)
	}
	return res, errors.Join(errs...)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/api/ -v`
Expected: PASS (9 tests total across client + dsquery)

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/api/
git commit -m "feat(gcli): ds/query engine — payload builder, envelope, per-refId errors"
```

---

### Task 7: CLI scaffold + datasources command + resolution

**Files:**
- Create: `grafana-cli/main.go`
- Create: `grafana-cli/internal/cmd/root.go`
- Create: `grafana-cli/internal/cmd/run.go`
- Create: `grafana-cli/internal/cmd/datasources.go`
- Create: `grafana-cli/internal/api/grafana.go`
- Test: `grafana-cli/internal/api/grafana_test.go`

**Interfaces:**
- Produces: `cmd.Execute()` (main entry); `api.Datasource{Name, UID, Type, URL string; IsDefault bool}`; `(*Client).Datasources(ctx) ([]Datasource, error)`; `(*Client).ResolveDatasource(ctx, nameOrUID string) (Datasource, error)` (exact uid match first, then case-insensitive name; error lists available names).
- Internal: `run(cmd *cobra.Command, fn func(ctx context.Context, c *api.Client) (result, error)) error`; `type result struct { res frames.Result; raw []byte }`; `exitCode(err error) int`; `hintOf(err error) string`.

- [ ] **Step 1: Add cobra dep**

```bash
cd grafana-cli && go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write failing tests**

`grafana-cli/internal/api/grafana_test.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gcli/internal/config"
)

func TestDatasources(t *testing.T) {
	resp := `[{"id":1,"uid":"03hIm2zGz","name":"Metrics Iota","type":"prometheus","url":"http://vm","isDefault":true},{"id":2,"uid":"cf3iebh4uz1fkc","name":"Logs","type":"victoriametrics-logs-datasource","url":"http://vlog"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasources" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	dss, err := c.Datasources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dss) != 2 || dss[0].Name != "Metrics Iota" || !dss[0].IsDefault {
		t.Errorf("dss = %+v", dss)
	}
}

func TestResolveDatasourceByUIDAndName(t *testing.T) {
	resp := `[{"uid":"03hIm2zGz","name":"Metrics Iota","type":"prometheus","url":"u","isDefault":true},{"uid":"cf3iebh4uz1fkc","name":"Logs","type":"victoriametrics-logs-datasource","url":"v"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})

	byUID, err := c.ResolveDatasource(context.Background(), "cf3iebh4uz1fkc")
	if err != nil {
		t.Fatal(err)
	}
	if byUID.Name != "Logs" {
		t.Errorf("by uid = %+v", byUID)
	}
	byName, err := c.ResolveDatasource(context.Background(), "logs")
	if err != nil {
		t.Fatal(err)
	}
	if byName.UID != "cf3iebh4uz1fkc" {
		t.Errorf("by name = %+v", byName)
	}
	_, err = c.ResolveDatasource(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "Metrics Iota") {
		t.Errorf("unknown datasource err = %v, want hint listing names", err)
	}
}
```

`grafana-cli/internal/cmd/run_test.go`:
```go
package cmd

import (
	"errors"
	"testing"

	"gcli/internal/api"
)

func TestExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{errors.New("plain"), 2},
		{&api.HTTPError{StatusCode: 401}, 3},
		{&api.HTTPError{StatusCode: 403}, 4},
		{&api.HTTPError{StatusCode: 404}, 4},
		{&api.HTTPError{StatusCode: 500}, 2},
		{&api.QueryError{RefID: "A"}, 5},
	}
	for _, tc := range cases {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestHintOf(t *testing.T) {
	if got := hintOf(&api.HTTPError{StatusCode: 401, Body: ""}); got == "" {
		t.Error("401 must have hint")
	}
	if got := hintOf(&api.QueryError{RefID: "A", Msg: "x"}); got == "" {
		t.Error("QueryError must have hint")
	}
	if got := hintOf(errors.New("plain")); got != "" {
		t.Errorf("plain error hint = %q, want empty", got)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/ ./internal/cmd/`
Expected: FAIL — "undefined: Datasources" / "no Go files" (cmd pkg missing)

- [ ] **Step 4: Implement API side**

`grafana-cli/internal/api/grafana.go`:
```go
package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Datasource struct {
	Name      string `json:"name"`
	UID       string `json:"uid"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	IsDefault bool   `json:"isDefault"`
}

func (c *Client) Datasources(ctx context.Context) ([]Datasource, error) {
	var dss []Datasource
	if err := c.Get(ctx, "/api/datasources", &dss); err != nil {
		return nil, err
	}
	sort.SliceStable(dss, func(i, j int) bool {
		if dss[i].IsDefault != dss[j].IsDefault {
			return dss[i].IsDefault
		}
		return dss[i].Name < dss[j].Name
	})
	return dss, nil
}

func (c *Client) ResolveDatasource(ctx context.Context, nameOrUID string) (Datasource, error) {
	dss, err := c.Datasources(ctx)
	if err != nil {
		return Datasource{}, err
	}
	for _, ds := range dss {
		if ds.UID == nameOrUID {
			return ds, nil
		}
	}
	for _, ds := range dss {
		if strings.EqualFold(ds.Name, nameOrUID) {
			return ds, nil
		}
	}
	names := make([]string, len(dss))
	for i, ds := range dss {
		names[i] = ds.Name
	}
	return Datasource{}, fmt.Errorf("datasource %q not found — available: %s", nameOrUID, strings.Join(names, ", "))
}
```

- [ ] **Step 5: Implement cmd scaffold + datasources command**

`grafana-cli/internal/cmd/root.go`:
```go
package cmd

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagURL     string
	flagToken   string
	flagOutput  string
	flagTimeout time.Duration
	flagNoColor bool
	flagVerbose bool
)

var rootCmd = &cobra.Command{
	Use:           "gcli",
	Short:         "Read-only Grafana CLI",
	Long:          "gcli reads metrics, logs, SQL and Grafana state from a Grafana instance. Run `gcli help` for the full guide.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagURL, "url", "", "Grafana URL (overrides GRAFANA_URL)")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "service-account token (overrides GRAFANA_TOKEN)")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "output format: table|json|csv (default table)")
	rootCmd.PersistentFlags().DurationVar(&flagTimeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable ANSI colors")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "dump HTTP requests/responses (token redacted)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrln("error:", err)
		if hint := hintOf(err); hint != "" {
			rootCmd.PrintErrln("hint:", hint)
		}
		os.Exit(exitCode(err))
	}
}
```

`grafana-cli/internal/cmd/run.go`:
```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/config"
	"gcli/internal/frames"
	"gcli/internal/render"
)

type result struct {
	res frames.Result
	raw []byte // printed verbatim instead of rendered
}

func run(cmd *cobra.Command, fn func(ctx context.Context, c *api.Client) (result, error)) error {
	client, cfg, err := clientFromFlags(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
	defer cancel()
	r, err := fn(ctx, client)
	if r.raw != nil {
		fmt.Fprintln(cmd.OutOrStdout(), string(r.raw))
		return err
	}
	if len(r.res.Frames) > 0 || r.res.Meta.Datasource != "" {
		if rerr := render.Render(cmd.OutOrStdout(), r.res, outputOptions(cfg)); rerr != nil {
			return rerr
		}
	}
	return err
}

func clientFromFlags(cmd *cobra.Command) (*api.Client, config.Config, error) {
	cfg, err := config.Load(flagURL, flagToken, flagTimeout, flagOutput, flagNoColor, flagVerbose)
	if err != nil {
		return nil, config.Config{}, err
	}
	c := api.NewClient(cfg)
	if cfg.Verbose {
		c.LogW = cmd.ErrOrStderr()
	}
	return c, cfg, nil
}

func outputOptions(cfg config.Config) render.Options {
	return render.Options{
		Output:  cfg.Output,
		NoColor: cfg.NoColor,
		Color:   !cfg.NoColor && isTTY(os.Stdout),
	}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func exitCode(err error) int {
	var he *api.HTTPError
	if errors.As(err, &he) {
		return he.ExitCode()
	}
	var qe *api.QueryError
	if errors.As(err, &qe) {
		return 5
	}
	if errors.Is(err, config.ErrMissingURL) || errors.Is(err, config.ErrMissingToken) {
		return 1
	}
	return 2
}

func hintOf(err error) string {
	var he *api.HTTPError
	if errors.As(err, &he) {
		return he.Hint()
	}
	var qe *api.QueryError
	if errors.As(err, &qe) {
		return "query failed — check the query syntax for this datasource type"
	}
	return ""
}
```

`grafana-cli/internal/cmd/datasources.go`:
```go
package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(datasourcesCmd)
}

var datasourcesCmd = &cobra.Command{
	Use:   "datasources",
	Short: "List datasources this token can access",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dss, err := c.Datasources(ctx)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Name", Values: namesOf(dss)},
					{Name: "UID", Values: uidsOf(dss)},
					{Name: "Type", Values: typesOf(dss)},
					{Name: "URL", Values: urlsOf(dss)},
					{Name: "Default", Values: defaultsOf(dss)},
				}}},
			}}, nil
		})
	},
}

func namesOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.Name
	}
	return out
}
func uidsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.UID
	}
	return out
}
func typesOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.Type
	}
	return out
}
func urlsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = d.URL
	}
	return out
}
func defaultsOf(dss []api.Datasource) []any {
	out := make([]any, len(dss))
	for i, d := range dss {
		out[i] = ""
		if d.IsDefault {
			out[i] = "yes"
		}
	}
	return out
}
```

`grafana-cli/main.go`:
```go
package main

import "gcli/internal/cmd"

func main() {
	cmd.Execute()
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v && go build ./...`
Expected: PASS all; build succeeds

- [ ] **Step 7: Smoke test against live instance (optional but useful)**

```bash
cd grafana-cli && GRAFANA_URL=https://grafana.example.com GRAFANA_TOKEN=$TOKEN go run . datasources
```

- [ ] **Step 8: Commit**

```bash
git add grafana-cli/main.go grafana-cli/internal/cmd/ grafana-cli/internal/api/grafana.go grafana-cli/go.mod grafana-cli/go.sum
git commit -m "feat(gcli): cli scaffold, datasources command, uid/name resolution"
```

---

### Task 8: `query` command (generic raw JSON)

**Files:**
- Create: `grafana-cli/internal/cmd/query.go`
- Test: `grafana-cli/internal/cmd/query_test.go`

**Interfaces:**
- Consumes: `run()`, `ResolveDatasource`, `DSQuery` from Task 7/6.
- Flags: `--json <string|@file>`, `--from` (default `now-1h`), `--to` (default `now`). Global query flags live on this command and are duplicated on prom/logs/sql (cobra flags are per-command).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/query_test.go`:
```go
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeGrafana serves datasources list + a ds/query echo asserting payload shape.
func fakeGrafana(t *testing.T, onQuery func(t *testing.T, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources":
			w.Write([]byte(`[{"uid":"u1","name":"Metrics Iota","type":"prometheus","url":"x","isDefault":true}]`))
		case "/api/ds/query":
			if onQuery != nil {
				onQuery(t, r)
			}
			w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"Value","type":"number"}]},"data":{"values":[[1]]}}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestQueryCommandSendsRawJSON(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	buf := runCommand(t, "query", "Metrics Iota", "--json", `{"expr":"up","instant":true}`)
	_ = buf
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["expr"] != "up" || q0["instant"] != true {
		t.Errorf("query object = %v", q0)
	}
	ds := q0["datasource"].(map[string]any)
	if ds["uid"] != "u1" {
		t.Errorf("datasource uid not injected: %v", ds)
	}
}

func TestQueryCommandFileInput(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/q.json"
	os.WriteFile(path, []byte(`{"expr":"up"}`), 0o644)
	srv := fakeGrafana(t, nil)
	defer srv.Close()

	setEnv(t, srv.URL)
	buf := runCommand(t, "query", "Metrics Iota", "--json", "@"+path)
	if !strings.Contains(buf, "1") {
		t.Errorf("output = %q", buf)
	}
}

func setEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("GRAFANA_URL", url)
	t.Setenv("GRAFANA_TOKEN", "test-token")
}

func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	// cobra persists flag values across Execute calls in one process — reset
	// every shared flag to its default so tests stay order-independent.
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

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"--no-color"}, args...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("command %v failed: %v", args, err)
	}
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	return buf.String()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestQueryCommand`
Expected: FAIL — "unknown command \"query\""

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/query.go`:
```go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gcli/internal/api"
)

var (
	flagQueryJSON string
	flagFrom      string
	flagTo        string
)

func init() {
	queryCmd.Flags().StringVar(&flagQueryJSON, "json", "", "raw query-object JSON, or @file.json")
	queryCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time (now-1h, RFC3339)")
	queryCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	_ = queryCmd.MarkFlagRequired("json")
	rootCmd.AddCommand(queryCmd)
}

var queryCmd = &cobra.Command{
	Use:   "query <datasource-uid-or-name>",
	Short: "Run a raw datasource query (works with ANY datasource type)",
	Example: `  gcli query Metrics Iota --json '{"expr":"count(up)","instant":true}'
  gcli query PostgreSQL Metrics --json @q.json --from now-24h`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readBody(flagQueryJSON)
		if err != nil {
			return err
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				return result{}, fmt.Errorf("invalid --json: %w", err)
			}
			req := api.DSQueryReq{
				RefID:      "A",
				Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID},
				Body:       body,
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{req}, flagFrom, flagTo)
			return result{res: res}, err
		})
	},
}

func readBody(s string) (string, error) {
	if strings.HasPrefix(s, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return "", fmt.Errorf("read --json file: %w", err)
		}
		return string(b), nil
	}
	return s, nil
}
```

Note: `queryCmd.MarkFlagRequired("json")` returns an error — handle:
```go
	if err := queryCmd.MarkFlagRequired("json"); err != nil {
		panic(err)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/
git commit -m "feat(gcli): generic query command — raw JSON for any datasource"
```

---

### Task 9: `prom` command

**Files:**
- Create: `grafana-cli/internal/cmd/prom.go`
- Test: `grafana-cli/internal/cmd/prom_test.go`

**Interfaces:**
- Consumes: `run()`, `ResolveDatasource`, `DSQuery`.
- Flags: `--step <duration>` (empty = instant query), `--from`, `--to`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/prom_test.go`:
```go
package cmd

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPromInstantQuery(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "prom", "Metrics Iota", "count(up)")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["expr"] != "count(up)" || q0["instant"] != true {
		t.Errorf("instant payload = %v", q0)
	}
	if _, hasRange := q0["range"]; hasRange {
		t.Errorf("instant query must not set range: %v", q0)
	}
}

func TestPromRangeQuery(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "prom", "Metrics Iota", "rate(http_requests_total[5m])", "--step", "1m")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["range"] != true || q0["interval"] != "1m" {
		t.Errorf("range payload = %v", q0)
	}
	if q0["instant"] != false {
		t.Errorf("range query instant must be false: %v", q0)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestProm`
Expected: FAIL — "unknown command \"prom\""

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/prom.go`:
```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"gcli/internal/api"
)

var flagStep string

func init() {
	promCmd.Flags().StringVar(&flagStep, "step", "", "range query step (e.g. 1m, 5m, 1h); empty = instant query")
	promCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	promCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(promCmd)
}

var promCmd = &cobra.Command{
	Use:     "prom <datasource> <promql>",
	Short:   "Run a PromQL query against a Prometheus-type datasource",
	Example: `  gcli prom Metrics Iota 'sum(rate(http_requests_total[5m])) by (job)' --step 1m
  gcli prom Metrics Zeta 'count(up)'`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			if ds.Type != "prometheus" && ds.Type != "victoriametrics-datasource" {
				return result{}, fmt.Errorf("datasource %q has type %q, not a Prometheus-type datasource", ds.Name, ds.Type)
			}
			body := map[string]any{
				"expr":    args[1],
				"instant": flagStep == "",
			}
			if flagStep != "" {
				body["range"] = true
				body["interval"] = flagStep
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = args[1]
			return result{res: res}, err
		})
	},
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/prom.go grafana-cli/internal/cmd/prom_test.go
git commit -m "feat(gcli): prom command — instant/range PromQL queries"
```

---

### Task 10: `logs` command (VictoriaLogs)

**Files:**
- Create: `grafana-cli/internal/cmd/logs.go`
- Test: `grafana-cli/internal/cmd/logs_test.go`

**Interfaces:**
- Consumes: `run()`, `ResolveDatasource`, `DSQuery`.
- Flags: `--mode instant|range|stats` (default `range`), `--limit` int (default 50), `--from`, `--to`.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/logs_test.go`:
```go
package cmd

import (
	"encoding/json"
	"testing"
)

func TestLogsQueryPayload(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "logs", "vlog", `{app="acme-pay"} |= "error"`, "--limit", "25")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["queryType"] != "range" {
		t.Errorf("queryType = %v", q0["queryType"])
	}
	// CRITICAL gotcha: limit must serialize as JSON number, not string.
	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), `"limit":25`) {
		t.Errorf("limit must be a JSON number: %s", raw)
	}
}

func TestLogsStatsMode(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "logs", "vlog", "* | stats count() rows", "--mode", "stats")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["queryType"] != "stats" {
		t.Errorf("queryType = %v", q0["queryType"])
	}
}

func TestLogsRejectsBadMode(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	old := rootCmd.SilenceErrors // keep silent; command must error
	_ = old
	rootCmd.SetArgs([]string{"logs", "vlog", "*", "--mode", "bogus"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("want error for --mode bogus")
	}
}
```

(fakeGrafana serves datasource uid u1 named Metrics Iota — the logs test uses name "vlog": extend fakeGrafana's datasources list to include `{"uid":"u2","name":"vlog","type":"victoriametrics-logs-datasource"}`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestLogs`
Expected: FAIL — "unknown command \"logs\""

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/logs.go`:
```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"gcli/internal/api"
)

var (
	flagLogsMode  string
	flagLogsLimit int
)

func init() {
	logsCmd.Flags().StringVar(&flagLogsMode, "mode", "range", "query mode: instant|range|stats")
	logsCmd.Flags().IntVar(&flagLogsLimit, "limit", 50, "max log lines (sent as JSON number — string is silently ignored upstream)")
	logsCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	logsCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:     "logs <datasource> <logsql>",
	Short:   "Query a VictoriaLogs datasource (LogsQL)",
	Example: `  gcli logs "Logs" '{app="acme-pay"} |= "error"' --limit 100
  gcli logs "Logs" '* | stats by (app) count() rows' --mode stats`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch flagLogsMode {
		case "instant", "range", "stats":
		default:
			return fmt.Errorf("invalid --mode %q: must be instant, range or stats", flagLogsMode)
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			body := map[string]any{
				"expr":      args[1],
				"queryType": flagLogsMode,
				"limit":     flagLogsLimit,
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = args[1]
			return result{res: res}, err
		})
	},
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/logs.go grafana-cli/internal/cmd/logs_test.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): logs command — VictoriaLogs LogsQL with numeric limit gotcha"
```

---

### Task 11: `sql` command

**Files:**
- Create: `grafana-cli/internal/cmd/sql.go`
- Test: `grafana-cli/internal/cmd/sql_test.go`

**Interfaces:**
- Consumes: `run()`, `ResolveDatasource`, `DSQuery`, `timeparse.ParseToEpochMS`.
- Flags: `--from`, `--to`. Query = all args after datasource joined with spaces.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/sql_test.go`:
```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLCommand(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "sql", "PostgreSQL Metrics", "SELECT", "count(*)", "FROM", "users")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	if q0["rawSql"] != "SELECT count(*) FROM users" {
		t.Errorf("rawSql = %v", q0["rawSql"])
	}
	if q0["format"] != "table" {
		t.Errorf("format = %v", q0["format"])
	}
}

func TestSQLTimeMacros(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, func(t *testing.T, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
	})
	defer srv.Close()

	setEnv(t, srv.URL)
	runCommand(t, "sql", "PostgreSQL Metrics", "SELECT", "*", "FROM", "t", "WHERE", "ts", "BETWEEN", "$__timeFrom", "AND", "$__timeTo",
		"--from", "2026-08-01T00:00:00Z", "--to", "2026-08-02T00:00:00Z")
	q0 := gotBody["queries"].([]any)[0].(map[string]any)
	sql := q0["rawSql"].(string)
	if strings.Contains(sql, "$__timeFrom") || strings.Contains(sql, "$__timeTo") {
		t.Errorf("macros not substituted: %s", sql)
	}
	if !strings.Contains(sql, "1785542400000") { // 2026-08-01T00:00:00Z epoch ms
		t.Errorf("expected epoch ms for $__timeFrom: %s", sql)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestSQL`
Expected: FAIL — "unknown command \"sql\""

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/sql.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/timeparse"
)

func init() {
	sqlCmd.Flags().StringVar(&flagFrom, "from", "now-1h", "start time")
	sqlCmd.Flags().StringVar(&flagTo, "to", "now", "end time")
	rootCmd.AddCommand(sqlCmd)
}

var sqlCmd = &cobra.Command{
	Use:     "sql <datasource> <query...>",
	Short:   "Run a SQL query against a SQL datasource (PostgreSQL etc.)",
	Example: `  gcli sql PostgreSQL Metrics 'SELECT count(*) FROM invoices'
  gcli sql PostgreSQL Metrics 'SELECT * FROM events WHERE ts BETWEEN $__timeFrom AND $__timeTo' --from now-24h`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		q := strings.Join(args[1:], " ")
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			ds, err := c.ResolveDatasource(ctx, args[0])
			if err != nil {
				return result{}, err
			}
			sql, err := substituteMacros(q)
			if err != nil {
				return result{}, err
			}
			body := map[string]any{
				"rawSql": sql,
				"format": "table",
			}
			res, err := c.DSQuery(ctx, ds.Type, []api.DSQueryReq{{RefID: "A", Datasource: api.DatasourceRef{Type: ds.Type, UID: ds.UID}, Body: body}}, flagFrom, flagTo)
			res.Meta.Query = q
			return result{res: res}, err
		})
	},
}

func substituteMacros(q string) (string, error) {
	if !strings.Contains(q, "$__timeFrom") && !strings.Contains(q, "$__timeTo") {
		return q, nil
	}
	now := time.Now()
	fromMS, err := timeparse.ParseToEpochMS(flagFrom, now)
	if err != nil {
		return "", fmt.Errorf("--from: %w", err)
	}
	toMS, err := timeparse.ParseToEpochMS(flagTo, now)
	if err != nil {
		return "", fmt.Errorf("--to: %w", err)
	}
	q = strings.ReplaceAll(q, "$__timeFrom", strconv.FormatInt(fromMS, 10))
	q = strings.ReplaceAll(q, "$__timeTo", strconv.FormatInt(toMS, 10))
	return q, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/sql.go grafana-cli/internal/cmd/sql_test.go
git commit -m "feat(gcli): sql command with \$__timeFrom/\$__timeTo macro substitution"
```

---

### Task 12: `dashboards` command

**Files:**
- Create: `grafana-cli/internal/cmd/dashboards.go`
- Create: `grafana-cli/internal/api/dashboards.go` (part of grafana.go is fine — add functions there)
- Test: `grafana-cli/internal/cmd/dashboards_test.go`

**Interfaces:**
- Produces: `api.Dashboard{Title, UID, Type, FolderTitle string}`; `(*Client).SearchDashboards(ctx, query string) ([]Dashboard, error)`; `(*Client).DashboardJSON(ctx, uid string) (json.RawMessage, error)`.
- Flags: `--get <uid>` (print full dashboard JSON), `--export <path>` (write dashboard JSON to file).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/dashboards_test.go`:
```go
package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDashboardsSearch(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	// extend fake: serve /api/search
	// (see Step 3 — fakeGrafana gains a search branch returning two dashboards)

	setEnv(t, srv.URL)
	out := runCommand(t, "dashboards", "account")
	if !strings.Contains(out, "Account") || !strings.Contains(out, "dash-db") {
		t.Errorf("search output = %q", out)
	}
}

func TestDashboardsGetRawJSON(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "dashboards", "--get", "account")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--get must print raw dashboard JSON, got: %q (%v)", out, err)
	}
	if doc["dashboard"] == nil {
		t.Errorf("dashboard JSON missing dashboard key: %s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestDashboards`
Expected: FAIL — "unknown command \"dashboards\""

- [ ] **Step 3: Implement API + command + fake extension**

API (append to `grafana-cli/internal/api/grafana.go`; extend its imports with `"encoding/json"`, `"net/url"`):
```go
type Dashboard struct {
	Title       string `json:"title"`
	UID         string `json:"uid"`
	Type        string `json:"type"`
	FolderTitle string `json:"folderTitle"`
}

func (c *Client) SearchDashboards(ctx context.Context, query string) ([]Dashboard, error) {
	var dbs []Dashboard
	path := "/api/search?limit=5000"
	if query != "" {
		path += "&query=" + url.QueryEscape(query)
	}
	if err := c.Get(ctx, path, &dbs); err != nil {
		return nil, err
	}
	return dbs, nil
}

func (c *Client) DashboardJSON(ctx context.Context, uid string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/dashboards/uid/"+url.PathEscape(uid), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
```

Command `grafana-cli/internal/cmd/dashboards.go`:
```go
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
)

var (
	flagDashGet    string
	flagDashExport string
)

func init() {
	dashboardsCmd.Flags().StringVar(&flagDashGet, "get", "", "fetch full JSON of dashboard by uid")
	dashboardsCmd.Flags().StringVar(&flagDashExport, "export", "", "write dashboard JSON to file (provisioning format)")
	rootCmd.AddCommand(dashboardsCmd)
}

var dashboardsCmd = &cobra.Command{
	Use:     "dashboards [search-query]",
	Short:   "Search dashboards, or fetch full JSON with --get",
	Example: `  gcli dashboards account
  gcli dashboards --get 8GbEch5Mz
  gcli dashboards --get 8GbEch5Mz --export account.json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagDashGet != "" {
			return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
				raw, err := c.DashboardJSON(ctx, flagDashGet)
				if err != nil {
					return result{}, err
				}
				if flagDashExport != "" {
					var indented bytes.Buffer
					if err := json.Indent(&indented, raw, "", "  "); err != nil {
						return result{}, err
					}
					if err := os.WriteFile(flagDashExport, indented.Bytes(), 0o644); err != nil {
						return result{}, err
					}
					return result{raw: []byte("written: " + flagDashExport)}, nil
				}
				var indented bytes.Buffer
				if err := json.Indent(&indented, raw, "", "  "); err != nil {
					return result{}, err
				}
				return result{raw: indented.Bytes()}, nil
			})
		}
		q := ""
		if len(args) == 1 {
			q = args[0]
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			dbs, err := c.SearchDashboards(ctx, q)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Title", Values: dbsTitles(dbs)},
					{Name: "UID", Values: dbsUIDs(dbs)},
					{Name: "Type", Values: dbsTypes(dbs)},
					{Name: "Folder", Values: dbsFolders(dbs)},
				}}},
			}}, nil
		})
	},
}

func dbsTitles(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.Title
	}
	return out
}
func dbsUIDs(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.UID
	}
	return out
}
func dbsTypes(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.Type
	}
	return out
}
func dbsFolders(dbs []api.Dashboard) []any {
	out := make([]any, len(dbs))
	for i, d := range dbs {
		out[i] = d.FolderTitle
	}
	return out
}
```

Extend `fakeGrafana` in `query_test.go` (rename file role: it's the shared test helper) with:
```go
case "/api/search":
	w.Write([]byte(`[{"title":"Account","uid":"8GbEch5Mz","type":"dash-db","folderTitle":"Acme Pay"}]`))
case "/api/dashboards/uid/account":
	w.Write([]byte(`{"dashboard":{"uid":"account","title":"Account"},"meta":{"folderTitle":"Acme Pay"}}`))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/dashboards.go grafana-cli/internal/cmd/dashboards_test.go grafana-cli/internal/cmd/query_test.go grafana-cli/internal/api/grafana.go
git commit -m "feat(gcli): dashboards command — search, raw get, export"
```

---

### Task 13: `alerts` command (rules + ruler merge, fallback chain)

**Files:**
- Create: `grafana-cli/internal/api/alerts.go`
- Create: `grafana-cli/internal/cmd/alerts.go`
- Test: `grafana-cli/internal/api/alerts_test.go`
- Test: `grafana-cli/internal/cmd/alerts_test.go`

**Interfaces:**
- Produces: `api.AlertRow{Name, State, Core, Severity, Folder, ActiveAt string}`; `(*Client).Alerts(ctx) ([]AlertRow, error)` (fallback chain: rules → v2 statuses → legacy, first success wins).
- Flags: `--firing` (filter to Core ∈ Alerting/Pending/NoData/Error).

- [ ] **Step 1: Write failing tests**

`grafana-cli/internal/api/alerts_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gcli/internal/config"
)

func TestAlertsFromRulesEndpoint(t *testing.T) {
	resp := `{"status":"success","data":{"groups":[{"name":"PostgreSQL Service","file":"Acme Pay","rules":[{"state":"inactive","name":"High DB Connections","alerts":[{"labels":{"alertname":"High DB Connections","grafana_folder":"Acme Pay","severity":"warning"},"state":"Normal (Error)","activeAt":"2026-07-11T13:46:00Z","value":""},{"labels":{"alertname":"High DB Connections","grafana_folder":"Acme Pay","severity":"critical","pod":"svc8-0"},"state":"Alerting","activeAt":"2026-08-29T10:00:00Z","value":"1000"}]}]}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	rows, err := c.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	r0 := rows[0]
	if r0.Name != "High DB Connections" || r0.State != "Normal (Error)" || r0.Core != "Normal" {
		t.Errorf("row0 = %+v", r0)
	}
	if r0.Severity != "warning" || r0.Folder != "Acme Pay" {
		t.Errorf("row0 labels = %+v", r0)
	}
	r1 := rows[1]
	if r1.Core != "Alerting" || r1.Severity != "critical" {
		t.Errorf("row1 = %+v", r1)
	}
}

func TestAlertsFallsBackToV2Statuses(t *testing.T) {
	// rules endpoint RBAC-denied (404), v2 statuses works
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/prometheus/grafana/api/v1/rules":
			http.NotFound(w, r)
		case "/api/v2/alerts/statuses":
			w.Write([]byte(`{"statuses":[{"labels":{"alertname":"DiskFull","severity":"critical"},"state":"Alerting"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
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

func TestAlertsFallsBackToLegacy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/prometheus/grafana/api/v1/rules", "/api/v2/alerts/statuses":
			http.NotFound(w, r)
		case "/api/alerts":
			w.Write([]byte(`[{"id":1,"name":"Legacy alert","state":"alerting"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(config.Config{URL: srv.URL, Token: "t", Timeout: 5 * time.Second})
	rows, err := c.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Legacy alert" || rows[0].Core != "Alerting" {
		t.Errorf("rows = %+v", rows)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/ -run TestAlerts`
Expected: FAIL — "undefined: AlertRow"

- [ ] **Step 3: Implement API**

`grafana-cli/internal/api/alerts.go`:
```go
package api

import (
	"context"
	"fmt"
	"strings"
)

type AlertRow struct {
	Name     string
	State    string // as reported, e.g. "Normal (Error)"
	Core     string // "Normal", "Alerting", "Pending", "NoData", "Error"
	Severity string
	Folder   string
	ActiveAt string
}

// Alerts returns current alert instances. Chain: unified rules endpoint
// (works for limited tokens) → v2 statuses → legacy /api/alerts.
func (c *Client) Alerts(ctx context.Context) ([]AlertRow, error) {
	rows, err := c.alertsFromRules(ctx)
	if err == nil {
		return rows, nil
	}
	if !isHidden(err) {
		return nil, err // 401/network: real problem, don't mask
	}
	rows, err2 := c.alertsFromV2Statuses(ctx)
	if err2 == nil {
		return rows, nil
	}
	if !isHidden(err2) {
		return nil, err2
	}
	rows, err3 := c.alertsFromLegacy(ctx)
	if err3 == nil {
		return rows, nil
	}
	return nil, fmt.Errorf("no alerts endpoint accessible: rules %v; v2 statuses %v; legacy %v", err, err2, err3)
}

func isHidden(err error) bool {
	he, ok := err.(*HTTPError)
	return ok && (he.StatusCode == 403 || he.StatusCode == 404)
}

type ruleGroup struct {
	Name  string     `json:"name"`
	File  string     `json:"file"`
	Rules []ruleItem `json:"rules"`
}
type ruleItem struct {
	State   string      `json:"state"`
	Name    string      `json:"name"`
	Alerts  []ruleAlert `json:"alerts"`
}
type ruleAlert struct {
	Labels      map[string]string `json:"labels"`
	State       string            `json:"state"`
	ActiveAt    string            `json:"activeAt"`
	Value       string            `json:"value"`
}

func (c *Client) alertsFromRules(ctx context.Context) ([]AlertRow, error) {
	var env struct {
		Data struct {
			Groups []ruleGroup `json:"groups"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/api/prometheus/grafana/api/v1/rules", &env); err != nil {
		return nil, err
	}
	var rows []AlertRow
	for _, g := range env.Data.Groups {
		for _, rule := range g.Rules {
			for _, a := range rule.Alerts {
				name := a.Labels["alertname"]
				if name == "" {
					name = rule.Name
				}
				rows = append(rows, AlertRow{
					Name:     name,
					State:    a.State,
					Core:     coreState(a.State),
					Severity: a.Labels["severity"],
					Folder:   a.Labels["grafana_folder"],
					ActiveAt: a.ActiveAt,
				})
			}
		}
	}
	return rows, nil
}

func (c *Client) alertsFromV2Statuses(ctx context.Context) ([]AlertRow, error) {
	var env struct {
		Statuses []struct {
			Labels map[string]string `json:"labels"`
			State  string            `json:"state"`
		} `json:"statuses"`
	}
	if err := c.Get(ctx, "/api/v2/alerts/statuses", &env); err != nil {
		return nil, err
	}
	rows := make([]AlertRow, 0, len(env.Statuses))
	for _, s := range env.Statuses {
		rows = append(rows, AlertRow{
			Name:     s.Labels["alertname"],
			State:    s.State,
			Core:     coreState(s.State),
			Severity: s.Labels["severity"],
			Folder:   s.Labels["grafana_folder"],
		})
	}
	return rows, nil
}

func (c *Client) alertsFromLegacy(ctx context.Context) ([]AlertRow, error) {
	var env []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := c.Get(ctx, "/api/alerts", &env); err != nil {
		return nil, err
	}
	rows := make([]AlertRow, 0, len(env))
	for _, a := range env {
		state := a.State
		core := state
		if state == "ok" || state == "paused" {
			core = "Normal"
		}
		rows = append(rows, AlertRow{Name: a.Name, State: state, Core: core})
	}
	return rows, nil
}

// coreState strips " (Error)" / " (NoData)" suffixes: "Normal (Error)" → "Normal".
func coreState(s string) string {
	if i := strings.Index(s, " ("); i > 0 {
		return s[:i]
	}
	return s
}
```

- [ ] **Step 4: Implement command**

`grafana-cli/internal/cmd/alerts.go`:
```go
package cmd

import (
	"context"
	"sort"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
	"gcli/internal/render"
)

var flagFiring bool

func init() {
	alertsCmd.Flags().BoolVar(&flagFiring, "firing", false, "only show Alerting/Pending/NoData/Error alerts")
	rootCmd.AddCommand(alertsCmd)
}

var alertsCmd = &cobra.Command{
	Use:     "alerts",
	Short:   "Current alert states (unified alerting)",
	Example: "  gcli alerts --firing",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			rows, err := c.Alerts(ctx)
			if err != nil {
				return result{}, err
			}
			if flagFiring {
				rows = filterFiring(rows)
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Name", Values: alertNames(rows)},
					{Name: "State", Values: alertStates(rows, color)},
					{Name: "Severity", Values: alertSeverities(rows)},
					{Name: "Folder", Values: alertFolders(rows)},
					{Name: "ActiveAt", Values: alertActiveAts(rows)},
				}}},
			}}, nil
		})
	},
}

func filterFiring(rows []api.AlertRow) []api.AlertRow {
	out := rows[:0]
	for _, r := range rows {
		switch r.Core {
		case "Alerting", "Pending", "NoData", "Error":
			out = append(out, r)
		}
	}
	return out
}

func alertStates(rows []api.AlertRow, color bool) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		c := "green"
		switch r.Core {
		case "Alerting", "NoData", "Error":
			c = "red"
		case "Pending":
			c = "yellow"
		}
		out[i] = render.Colorize(r.State, c, color)
	}
	return out
}
```

Required `run.go` addition (this task): set `lastCfg` so commands can reach the resolved config for coloring:

```go
var lastCfg config.Config

func run(cmd *cobra.Command, fn func(ctx context.Context, c *api.Client) (result, error)) error {
	client, cfg, err := clientFromFlags(cmd)
	if err != nil {
		return err
	}
	lastCfg = cfg
	...
}
```

The rest of the column helpers:
```go
func alertNames(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
func alertSeverities(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Severity
	}
	return out
}
func alertFolders(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.Folder
	}
	return out
}
func alertActiveAts(rows []api.AlertRow) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r.ActiveAt
	}
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/api/alerts.go grafana-cli/internal/cmd/alerts.go grafana-cli/internal/cmd/run.go grafana-cli/internal/api/alerts_test.go
git commit -m "feat(gcli): alerts command — rules/v2/legacy fallback chain, firing filter"
```

---

### Task 14: `annotations` command

**Files:**
- Create: `grafana-cli/internal/cmd/annotations.go`
- Add to `grafana-cli/internal/api/grafana.go`: annotations fetch
- Test: `grafana-cli/internal/cmd/annotations_test.go`

**Interfaces:**
- Produces: `api.Annotation{ID, AlertID int64; TimeMS int64; Text string; Tags []string; NewState, PrevState string}`; `(*Client).Annotations(ctx, fromMS, toMS int64, tags []string, dashUID string) ([]Annotation, error)`.
- Flags: `--dashboard <uid>`, `--tags <a,b,c>`, `--from` (default `now-24h`), `--to` (default `now`).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/annotations_test.go`:
```go
package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func TestAnnotationsCommand(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "annotations")
	if !strings.Contains(out, "deploy") {
		t.Errorf("output = %q", out)
	}
}

var lastAnnotationsQuery string

func TestAnnotationsSendsFilters(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	runCommand(t, "annotations", "--tags", "deploy,release", "--dashboard", "abc")
	for _, want := range []string{"tags=deploy", "tags=release", "dashboardUID=abc"} {
		if !strings.Contains(lastAnnotationsQuery, want) {
			t.Errorf("annotations query %q missing %q", lastAnnotationsQuery, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestAnnotations`
Expected: FAIL — "unknown command \"annotations\""

- [ ] **Step 3: Implement**

API (append to `grafana-cli/internal/api/grafana.go`; extend its imports with `"net/url"`, `"strconv"`):
```go
type Annotation struct {
	ID        int64    `json:"id"`
	AlertID   int64    `json:"alertId"`
	TimeMS    int64    `json:"time"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags"`
	NewState  string   `json:"newState"`
	PrevState string   `json:"prevState"`
}

func (c *Client) Annotations(ctx context.Context, fromMS, toMS int64, tags []string, dashUID string) ([]Annotation, error) {
	params := url.Values{}
	params.Set("from", strconv.FormatInt(fromMS, 10))
	params.Set("to", strconv.FormatInt(toMS, 10))
	params.Set("limit", "500")
	for _, t := range tags {
		params.Add("tags", t)
	}
	if dashUID != "" {
		params.Set("dashboardUID", dashUID)
	}
	var anns []Annotation
	if err := c.Get(ctx, "/api/annotations?"+params.Encode(), &anns); err != nil {
		return nil, err
	}
	return anns, nil
}
```

Command `grafana-cli/internal/cmd/annotations.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
	"gcli/internal/timeparse"
)

var (
	flagAnnTags      string
	flagAnnDashboard string
	flagAnnFrom      string
	flagAnnTo        string
)

func init() {
	annotationsCmd.Flags().StringVar(&flagAnnTags, "tags", "", "comma-separated tags")
	annotationsCmd.Flags().StringVar(&flagAnnDashboard, "dashboard", "", "filter by dashboard uid")
	annotationsCmd.Flags().StringVar(&flagAnnFrom, "from", "now-24h", "start time")
	annotationsCmd.Flags().StringVar(&flagAnnTo, "to", "now", "end time")
	rootCmd.AddCommand(annotationsCmd)
}

var annotationsCmd = &cobra.Command{
	Use:     "annotations",
	Short:   "Read annotations (incl. alert state changes)",
	Example: `  gcli annotations --tags deploy --from now-7d
  gcli annotations --dashboard GKmhl2-Mk`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		now := time.Now()
		fromMS, err := timeparse.ParseToEpochMS(flagAnnFrom, now)
		if err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		toMS, err := timeparse.ParseToEpochMS(flagAnnTo, now)
		if err != nil {
			return fmt.Errorf("--to: %w", err)
		}
		var tags []string
		if flagAnnTags != "" {
			for _, t := range strings.Split(flagAnnTags, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			anns, err := c.Annotations(ctx, fromMS, toMS, tags, flagAnnDashboard)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", From: flagAnnFrom, To: flagAnnTo},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Time", Values: annTimes(anns)},
					{Name: "State", Values: annStates(anns)},
					{Name: "Text", Values: annTexts(anns)},
					{Name: "Tags", Values: annTagsOf(anns)},
				}}},
			}}, nil
		})
	},
}

func annTimes(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = time.UnixMilli(a.TimeMS).UTC()
	}
	return out
}
func annStates(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		if a.AlertID != 0 && (a.NewState != "" || a.PrevState != "") {
			out[i] = a.PrevState + "→" + a.NewState
		} else {
			out[i] = ""
		}
	}
	return out
}
func annTexts(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = a.Text
	}
	return out
}
func annTagsOf(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = strings.Join(a.Tags, ",")
	}
	return out
}
```

Extend `fakeGrafana`:
```go
case "/api/annotations":
	lastAnnotationsQuery = r.URL.RawQuery
	w.Write([]byte(`[{"id":3833414,"alertId":0,"time":1788067792043,"text":"deploy v1.2.3","tags":["deploy"],"newState":"","prevState":""}]`))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/annotations.go grafana-cli/internal/cmd/annotations_test.go grafana-cli/internal/cmd/query_test.go grafana-cli/internal/api/grafana.go
git commit -m "feat(gcli): annotations command with tag/dashboard/time filters"
```

---

### Task 15: `health` + `version` commands

**Files:**
- Create: `grafana-cli/internal/cmd/health.go`
- Create: `grafana-cli/internal/cmd/version.go`
- Add to `grafana-cli/internal/api/grafana.go`: health + version fetches
- Test: `grafana-cli/internal/cmd/health_test.go`

**Interfaces:**
- Produces: `api.HealthStatus{Name, Type, Status, Message string}`; `(*Client).Health(ctx) (version string, stats []HealthStatus, err error)`; `(*Client).Version(ctx) (map[string]string, error)` (keys version/commit/database from `/api/health`).

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/health_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestHealthCommand(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "health")
	if !strings.Contains(out, "Metrics Iota") || !strings.Contains(out, "OK") {
		t.Errorf("output = %q", out)
	}
}

func TestVersionCommand(t *testing.T) {
	srv := fakeGrafana(t, nil)
	defer srv.Close()
	setEnv(t, srv.URL)
	out := runCommand(t, "version")
	if !strings.Contains(out, "10.4.3") {
		t.Errorf("output = %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run "TestHealth|TestVersion"`
Expected: FAIL — "unknown command \"health\""

- [ ] **Step 3: Implement API**

Append to `grafana-cli/internal/api/grafana.go`:
```go
type HealthStatus struct {
	Name    string
	Type    string
	Status  string
	Message string
}

func (c *Client) Health(ctx context.Context) (string, []HealthStatus, error) {
	var h struct {
		Version string `json:"version"`
	}
	if err := c.Get(ctx, "/api/health", &h); err != nil {
		return "", nil, err
	}
	dss, err := c.Datasources(ctx)
	if err != nil {
		return "", nil, err
	}
	stats := make([]HealthStatus, 0, len(dss))
	for _, ds := range dss {
		st := HealthStatus{Name: ds.Name, Type: ds.Type}
		var hres struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := c.Get(ctx, "/api/datasources/uid/"+url.PathEscape(ds.UID)+"/health", &hres); err != nil {
			if he, ok := err.(*HTTPError); ok {
				st.Status = fmt.Sprintf("denied (HTTP %d)", he.StatusCode)
				st.Message = he.Hint()
				stats = append(stats, st)
				continue
			}
			return "", nil, err
		}
		st.Status = hres.Status
		st.Message = hres.Message
		stats = append(stats, st)
	}
	return h.Version, stats, nil
}

func (c *Client) Version(ctx context.Context) (map[string]string, error) {
	var h struct {
		Version  string `json:"version"`
		Commit   string `json:"commit"`
		Database string `json:"database"`
	}
	if err := c.Get(ctx, "/api/health", &h); err != nil {
		return nil, err
	}
	return map[string]string{"version": h.Version, "commit": h.Commit, "database": h.Database}, nil
}
```

- [ ] **Step 4: Implement commands**

`grafana-cli/internal/cmd/health.go`:
```go
package cmd

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
	"gcli/internal/render"
)

func init() {
	rootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Grafana health + per-datasource health probe",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			version, stats, err := c.Health(ctx)
			if err != nil {
				return result{}, err
			}
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", Query: "grafana " + version},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Datasource", Values: hsNames(stats)},
					{Name: "Type", Values: hsTypes(stats)},
					{Name: "Status", Values: hsStatuses(stats, color)},
					{Name: "Message", Values: hsMessages(stats)},
				}}},
			}}, nil
		})
	},
}

func hsNames(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}
func hsTypes(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Type
	}
	return out
}
func hsStatuses(s []api.HealthStatus, color bool) []any {
	out := make([]any, len(s))
	for i, x := range s {
		c := "green"
		if strings.HasPrefix(x.Status, "denied") {
			c = "yellow"
		} else if x.Status != "OK" {
			c = "red"
		}
		out[i] = render.Colorize(x.Status, c, color)
	}
	return out
}
func hsMessages(s []api.HealthStatus) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x.Message
	}
	return out
}
```

`grafana-cli/internal/cmd/version.go`:
```go
package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Grafana version + build info",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			v, err := c.Version(ctx)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Key", Values: []any{"version", "commit", "database"}},
					{Name: "Value", Values: []any{v["version"], v["commit"], v["database"]}},
				}}},
			}}, nil
		})
	},
}
```

Extend `fakeGrafana`:
```go
case "/api/health":
	w.Write([]byte(`{"version":"10.4.3","commit":"0bfd547","database":"ok"}`))
case "/api/datasources/uid/u1/health":
	w.Write([]byte(`{"status":"OK","message":"Successfully queried"}`))
```

(Add `"strings"` import to health.go.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/cmd/health.go grafana-cli/internal/cmd/version.go grafana-cli/internal/cmd/health_test.go grafana-cli/internal/api/grafana.go grafana-cli/internal/cmd/query_test.go
git commit -m "feat(gcli): health + version commands"
```

---

### Task 16: `capabilities` command (permission probe)

**Files:**
- Create: `grafana-cli/internal/api/capabilities.go`
- Create: `grafana-cli/internal/cmd/capabilities.go`
- Test: `grafana-cli/internal/api/capabilities_test.go`

**Interfaces:**
- Produces: `api.Capability{Group, Status, Detail string}` (Status ∈ OK/DENIED/ERROR); `(*Client).Capabilities(ctx) ([]Capability, error)` — always exits 0 at command level.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/api/capabilities_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gcli/internal/config"
)

func TestCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/org":
			w.Write([]byte(`{"id":1,"name":"Acme"}`))
		case "/api/datasources":
			w.Write([]byte(`[{"uid":"u1","name":"Metrics Iota","type":"prometheus","url":"x","isDefault":true}]`))
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
		t.Fatal(err)
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

func TestCapabilitiesDenied(t *testing.T) {
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
		if cp.Group == "auth" && cp.Status != "OK" {
			t.Errorf("auth = %+v", cp)
		}
		if cp.Group == "alerts" && cp.Status != "DENIED" {
			t.Errorf("alerts = %+v, want DENIED", cp)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/api/ -run TestCapabilities`
Expected: FAIL — "undefined: Capabilities"

- [ ] **Step 3: Implement API**

`grafana-cli/internal/api/capabilities.go`:
```go
package api

import (
	"context"
	"fmt"
)

type Capability struct {
	Group  string
	Status string // OK, DENIED, ERROR
	Detail string
}

func (c *Client) Capabilities(ctx context.Context) ([]Capability, error) {
	var caps []Capability

	probe := func(group string, fn func() error) {
		err := fn()
		cp := Capability{Group: group, Status: "OK"}
		if err != nil {
			if he, ok := err.(*HTTPError); ok && (he.StatusCode == 403 || he.StatusCode == 404) {
				cp.Status = "DENIED"
				cp.Detail = fmt.Sprintf("HTTP %d — %s", he.StatusCode, he.Hint())
			} else if err != nil {
				cp.Status = "ERROR"
				cp.Detail = err.Error()
			}
		}
		caps = append(caps, cp)
	}

	probe("auth", func() error {
		var org map[string]any
		return c.Get(ctx, "/api/org", &org)
	})
	probe("datasources", func() error {
		_, err := c.Datasources(ctx)
		return err
	})
	probe("query", func() error {
		dss, err := c.Datasources(ctx)
		if err != nil {
			return err
		}
		for _, ds := range dss {
			if ds.Type == "prometheus" {
				_, err := c.DSQuery(ctx, "prometheus", []DSQueryReq{{
					RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: ds.UID},
					Body: map[string]any{"expr": "1", "instant": true},
				}}, "now-5m", "now")
				return err
			}
		}
		return fmt.Errorf("no prometheus-type datasource visible to this token")
	})
	probe("dashboards", func() error {
		_, err := c.SearchDashboards(ctx, "")
		return err
	})
	probe("alerts", func() error {
		_, err := c.Alerts(ctx)
		return err
	})
	probe("annotations", func() error {
		_, err := c.Annotations(ctx, 0, 1, nil, "")
		return err
	})
	probe("datasource-health", func() error {
		dss, err := c.Datasources(ctx)
		if err != nil {
			return err
		}
		if len(dss) == 0 {
			return fmt.Errorf("no datasources visible")
		}
		var hres map[string]any
		return c.Get(ctx, "/api/datasources/uid/"+dss[0].UID+"/health", &hres)
	})
	return caps, nil
}
```

- [ ] **Step 4: Implement command**

`grafana-cli/internal/cmd/capabilities.go`:
```go
package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"gcli/internal/api"
	"gcli/internal/frames"
	"gcli/internal/render"
)

func init() {
	rootCmd.AddCommand(capabilitiesCmd)
}

var capabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Probe what this token can access (per command group)",
	Long:  "Runs one cheap probe per command group and reports OK / DENIED / ERROR. DENIED means the token role lacks the RBAC permission (or the endpoint is missing on this Grafana). Exit code is always 0.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			caps, err := c.Capabilities(ctx)
			if err != nil {
				return result{}, err
			}
			color := outputOptions(lastCfg).Color
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana"},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Group", Values: capGroups(caps)},
					{Name: "Status", Values: capStatuses(caps, color)},
					{Name: "Detail", Values: capDetails(caps)},
				}}},
			}}, nil
		})
	},
}

func capGroups(caps []api.Capability) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		out[i] = cp.Group
	}
	return out
}
func capStatuses(caps []api.Capability, color bool) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		c := "green"
		if cp.Status == "DENIED" {
			c = "yellow"
		} else if cp.Status == "ERROR" {
			c = "red"
		}
		out[i] = render.Colorize(cp.Status, c, color)
	}
	return out
}
func capDetails(caps []api.Capability) []any {
	out := make([]any, len(caps))
	for i, cp := range caps {
		out[i] = cp.Detail
	}
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add grafana-cli/internal/api/capabilities.go grafana-cli/internal/cmd/capabilities.go grafana-cli/internal/api/capabilities_test.go
git commit -m "feat(gcli): capabilities command — per-group permission probe"
```

---

### Task 17: `help` command with embedded guide

**Files:**
- Create: `grafana-cli/internal/cmd/help.go`
- Create: `grafana-cli/internal/cmd/help.txt`
- Test: `grafana-cli/internal/cmd/help_test.go`

**Interfaces:**
- Consumes: cobra root from Task 7.
- Produces: `gcli help` (full guide); `gcli help <command>` → cobra per-command help.

- [ ] **Step 1: Write failing test**

`grafana-cli/internal/cmd/help_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestHelpCommandPrintsGuide(t *testing.T) {
	setEnv(t, "http://localhost:1") // config not needed for help
	_ = rootCmd
	out := runCommand(t, "help")
	for _, want := range []string{"GRAFANA_URL", "GRAFANA_TOKEN", "gcli prom", "gcli logs", "gcli sql", "gcli query", "gcli alerts", "gcli capabilities", "exit codes", "service-account"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestHelpCommandForSubcommand(t *testing.T) {
	setEnv(t, "http://localhost:1")
	out := runCommand(t, "help", "prom")
	if !strings.Contains(out, "promql") && !strings.Contains(out, "PromQL") {
		t.Errorf("per-command help = %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd grafana-cli && go test ./internal/cmd/ -run TestHelp`
Expected: FAIL — help output lacks the guide content

- [ ] **Step 3: Implement**

`grafana-cli/internal/cmd/help.go`:
```go
package cmd

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed help.txt
var helpText string

func init() {
	rootCmd.SetHelpCommand(helpCmd)
}

var helpCmd = &cobra.Command{
	Use:   "help [command]",
	Short: "Full setup and usage guide",
	Long:  "Prints the complete gcli guide: setup, env vars, token creation, every command with examples, output formats, exit codes, permission notes. With a command name, shows that command's help.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			for _, c := range rootCmd.Commands() {
				if c.Name() == args[0] {
					return c.Help()
				}
			}
			return fmt.Errorf("unknown command %q — run `gcli help` for the full guide", args[0])
		}
		fmt.Fprint(cmd.OutOrStdout(), helpText)
		return nil
	},
}
```

`grafana-cli/internal/cmd/help.txt` (exact content):
```
GCLI — read-only Grafana CLI
============================

gcli reads metrics, logs, SQL and Grafana state from a Grafana instance.
Works with limited (personal) tokens and full-permission (devops) tokens.

SETUP
-----
1. Create a service-account token in Grafana:
   Grafana UI → Administration → Users and access → Service accounts
   → Add service account → Add token → copy the token (starts with glsa_).

2. Export the config (put in ~/.zshrc or shell profile):

   export GRAFANA_URL=https://grafana.example.com
   export GRAFANA_TOKEN=glsa_...

   Or pass per-invocation: gcli --url <u> --token <t> <command>

3. Verify: gcli health   (or: gcli capabilities — shows what the token can access)

COMMANDS
--------
Query datasources:
  gcli query <ds> --json '<raw query JSON>'   generic: ANY datasource type,
                                              raw /api/ds/query object
                                              (use --json @file.json for big queries)
  gcli prom <ds> '<promql>' [--step 1m]       Prometheus metrics
                                              (no --step = instant query)
  gcli logs <ds> '<logsql>' [--limit 100]     VictoriaLogs logs
                                              [--mode instant|range|stats]
  gcli sql <ds> '<sql>' [--from now-24h]      SQL datasources
                                              ($__timeFrom/$__timeTo macros
                                              substituted when --from/--to given)

Grafana state:
  gcli datasources                            list datasources (name, uid, type)
  gcli dashboards [query]                     search dashboards
  gcli dashboards --get <uid> [--export f]    full dashboard JSON
  gcli alerts [--firing]                      current alert states
  gcli annotations [--tags a,b] [--dashboard <uid>]
  gcli health                                 grafana + per-datasource health
  gcli version                                grafana version/build
  gcli capabilities                           per-group permission probe

Datasources are referenced by uid or name (case-insensitive):
  gcli prom Metrics Iota 'count(up)'
  gcli prom 03hIm2zGz 'count(up)'

GLOBAL FLAGS
------------
  --url <url>        override GRAFANA_URL
  --token <token>    override GRAFANA_TOKEN
  -o, --output       table | json | csv   (default table; json = jq-friendly)
  --timeout          30s default
  --no-color         disable ANSI colors
  -v, --verbose      dump HTTP requests/responses (token redacted)

TIME RANGES
-----------
Grafana-style: now, now-1h, now-5m, now-1d, now-2w, now-1d/d (day-truncated),
now/w (start of week), or absolute RFC3339 (2026-08-01T00:00:00Z).
Default range: now-1h → now (annotations: now-24h → now).

OUTPUT FORMATS
--------------
table   aligned columns, local-time timestamps, long strings truncated
json    {"meta":{...}, "frames":[{refId, columns:[{name, labels, values}]}]}
csv     first frame only; --frame N for others

EXIT CODES
----------
0 success
1 config error (missing GRAFANA_URL / GRAFANA_TOKEN)
2 network/HTTP error
3 HTTP 401 — token invalid, expired, or revoked
4 HTTP 403/404 — permission denied (or endpoint missing on this Grafana)
5 query error (per-refId failure from the datasource)

PERMISSIONS
-----------
Tokens vary by role. Grafana returns 404 for endpoints the token's role
cannot see — gcli reports "permission denied" hints for both cases.
Run `gcli capabilities` to see exactly what your token can do.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./internal/cmd/ -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add grafana-cli/internal/cmd/help.go grafana-cli/internal/cmd/help.txt grafana-cli/internal/cmd/help_test.go
git commit -m "feat(gcli): help command with embedded setup + usage guide"
```

---

### Task 18: Live fixture capture + integration tests

**Files:**
- Create: `grafana-cli/testdata/` (11 fixtures: health, datasources, search, annotations, rules, prom-instant, prom-range, vlogs-range, vlogs-stats, postgres-table, v2-statuses-response.txt)
- Create: `grafana-cli/internal/api/integration_test.go`

**Interfaces:**
- Consumes: everything. No new exported API.

- [ ] **Step 1: Capture live fixtures (anonymized)**

```bash
cd grafana-cli && mkdir -p testdata && TOKEN="<real token>"; BASE="https://grafana.example.com"
curl -s "$BASE/api/health" -H "Authorization: Bearer $TOKEN" > testdata/health.json
curl -s "$BASE/api/datasources" -H "Authorization: Bearer $TOKEN" > testdata/datasources.json
curl -s "$BASE/api/search?limit=3" -H "Authorization: Bearer $TOKEN" > testdata/search.json
curl -s "$BASE/api/annotations?limit=3" -H "Authorization: Bearer $TOKEN" > testdata/annotations.json
curl -s "$BASE/api/prometheus/grafana/api/v1/rules" -H "Authorization: Bearer $TOKEN" > testdata/rules.json
curl -s -X POST "$BASE/api/ds/query?ds_type=prometheus" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"03hIm2zGz"},"expr":"count(up)","instant":true}],"from":"now-5m","to":"now"}' > testdata/prom-instant.json
curl -s -X POST "$BASE/api/ds/query?ds_type=prometheus" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"03hIm2zGz"},"expr":"count(up)","range":true,"interval":"1m"}],"from":"now-5m","to":"now"}' > testdata/prom-range.json
curl -s -X POST "$BASE/api/ds/query?ds_type=victoriametrics-logs-datasource" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"queries":[{"refId":"A","datasource":{"type":"victoriametrics-logs-datasource","uid":"cf3iebh4uz1fkc"},"expr":"*","queryType":"range","limit":5}],"from":"now-1h","to":"now"}' > testdata/vlogs-range.json
curl -s -X POST "$BASE/api/ds/query?ds_type=victoriametrics-logs-datasource" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"queries":[{"refId":"A","datasource":{"type":"victoriametrics-logs-datasource","uid":"cf3iebh4uz1fkc"},"expr":"* | stats count() rows","queryType":"stats"}],"from":"now-1h","to":"now"}' > testdata/vlogs-stats.json
curl -s -X POST "$BASE/api/ds/query" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"queries":[{"refId":"A","datasource":{"type":"grafana-postgresql-datasource","uid":"fc1ae488-f63a-4658-8dcc-94b70f4c15c8"},"rawSql":"SELECT 1 AS one","format":"table"}],"from":"now-5m","to":"now"}' > testdata/postgres-table.json
curl -s -w "\nHTTP:%{http_code}" "$BASE/api/v2/alerts/statuses" -H "Authorization: Bearer $TOKEN" > testdata/v2-statuses-response.txt
```

Scrub: replace real values. For prom/vlogs/postgres fixtures, replace `data.values` arrays with small synthetic numbers via jq; keep schema intact. For rules.json keep only `groups[0].rules[0]` (truncate with jq `.data.groups[0].rules |= .[:1]` and remove email/labels noise). For datasources.json keep all — internal DNS names already in repo docs. Verify no fixture contains the token: `grep -ri "glsa_" testdata/ || echo clean`.

- [ ] **Step 2: Write API integration test serving fixtures**

`grafana-cli/internal/api/integration_test.go`:
```go
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

	"gcli/internal/config"
)

// TestAgainstRecordedFixtures replays real anonymized responses captured
// from grafana.example.com (2026-08-30) and asserts the client parses them.
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
	res, err := c.DSQuery(context.Background(), "prometheus", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: "03hIm2zGz"}}}, "now-5m", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) == 0 {
		t.Error("prom instant: no frames")
	}

	// vlogs range
	res, err = c.DSQuery(context.Background(), "victoriametrics-logs-datasource", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "victoriametrics-logs-datasource", UID: "cf3iebh4uz1fkc"}}}, "now-1h", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) == 0 || len(res.Frames[0].Columns) != 3 {
		t.Errorf("vlogs range frames = %+v", res.Frames)
	}

	// postgres
	res, err = c.DSQuery(context.Background(), "grafana-postgresql-datasource", []DSQueryReq{{RefID: "A", Datasource: DatasourceRef{Type: "grafana-postgresql-datasource", UID: "fc1ae488"}}}, "now-5m", "now")
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
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `cd grafana-cli && go test ./... -v`
Expected: PASS — fixtures parse end-to-end

- [ ] **Step 4: Commit**

```bash
git add grafana-cli/testdata/ grafana-cli/internal/api/integration_test.go
git commit -m "test(gcli): live anonymized fixtures + replay integration test"
```

---

### Task 19: README + build instructions

**Files:**
- Create: `grafana-cli/README.md`

- [ ] **Step 1: Write README**

`grafana-cli/README.md`:
```markdown
# gcli — read-only Grafana CLI

Generic CLI for reading data from the company Grafana (grafana.example.com, Grafana 10.4.3). Reads metrics (Prometheus/VictoriaMetrics), logs (VictoriaLogs), SQL (PostgreSQL) and Grafana state (datasources, dashboards, alerts, annotations). Read-only — no write endpoints.

## Install

```bash
cd grafana-cli
go build -o gcli .
# or for teams: go install gcli@latest (after repo is published)
```

## Setup

```bash
export GRAFANA_URL=https://grafana.example.com
export GRAFANA_TOKEN=glsa_...   # service-account token (UI: Administration → Service accounts)
```

Run `gcli help` for the full embedded guide (setup, every command with examples, output formats, exit codes, permission notes).

## Commands

| Command | Purpose |
|---------|---------|
| `gcli query <ds> --json '...'` | raw `/api/ds/query` object — ANY datasource type |
| `gcli prom <ds> '<promql>' [--step 1m]` | Prometheus instant/range queries |
| `gcli logs <ds> '<logsql>' [--mode range\|instant\|stats]` | VictoriaLogs (LogsQL) |
| `gcli sql <ds> '<sql>'` | SQL datasources, `$__timeFrom/$__timeTo` macros |
| `gcli datasources` | list datasources (uid/name/type/url) |
| `gcli dashboards [q]` / `--get <uid>` / `--export f` | search / fetch / export dashboards |
| `gcli alerts [--firing]` | alert states (unified alerting, fallback chain) |
| `gcli annotations [--tags --dashboard --from --to]` | annotations incl. alert state changes |
| `gcli health` | Grafana + per-datasource health |
| `gcli version` | Grafana version/build |
| `gcli capabilities` | what this token can access, per group |

Global flags: `--url`, `--token`, `-o table|json|csv`, `--timeout`, `--no-color`, `-v`.

## Design notes (state doc)

- **Generic engine**: everything goes through `/api/ds/query` (Grafana's unified datasource proxy). Datasource identity must live inside each query object; path takes `?ds_type=<type>`. The `query` command is the escape hatch for any future datasource.
- **Datasource inventory** (live, 2026-08-30): 9 prometheus-type (all VictoriaMetrics backends; default = Metrics Iota), 1 postgres (PostgreSQL Metrics), 1 victoriametrics-logs (Logs), 1 dead victoriametrics-datasource (plugin not registered — queries against it fail with a dedicated hint).
- **VictoriaLogs gotcha**: `limit` must be a JSON number — the plugin silently ignores string values (defaults to 1000).
- **Alerts**: `/api/v2/alerts/statuses` returns 404 for limited tokens (RBAC hides it). Primary endpoint = `/api/prometheus/grafana/api/v1/rules`; fallbacks v2 statuses → legacy `/api/alerts`.
- **Errors**: per-refId query errors come back as HTTP 200 with `results.<refId>.error` — the engine inspects results, not just status codes.
- **Permissions**: tokens vary by role. Grafana returns 404 for both "endpoint missing" and "permission hidden". `gcli capabilities` diagnoses; error hints explain.
- **Testing**: hermetic — `httptest` fakes + anonymized recorded fixtures in `testdata/` (captured 2026-08-30). No live Grafana needed. `go test ./...`.
- Spec: `docs/design-spec.md`. Plan: `docs/plan-v1.md`.
```

- [ ] **Step 2: Final verification**

Run: `cd grafana-cli && go vet ./... && go test ./... && go build -o gcli .`
Expected: vet clean, all tests pass, binary builds.

- [ ] **Step 3: Manual smoke against live instance**

```bash
cd grafana-cli
export GRAFANA_URL=https://grafana.example.com GRAFANA_TOKEN=<real token>
./gcli health
./gcli prom Metrics Iota 'count(up)'
./gcli logs "Logs" '* | stats count() rows' --mode stats
./gcli sql PostgreSQL Metrics 'SELECT 1 AS one'
./gcli alerts --firing
./gcli capabilities
```

- [ ] **Step 4: Commit**

```bash
git add grafana-cli/README.md
git commit -m "docs(gcli): README — install, usage, design notes, gotchas"
```

---

## Self-Review

**Spec coverage:**
- Command surface (query/prom/logs/sql/datasources/dashboards/alerts/annotations/health/version/capabilities/help) → Tasks 7–17. ✓
- Generic core + helpers → Tasks 6, 8–11. ✓
- Datasource-in-query-object gotcha → Task 6 payload builder + test. ✓
- Numeric `limit` gotcha → Task 10 test asserts JSON number. ✓
- Per-refId error envelope → Task 6. ✓
- plugin.notRegistered hint → Task 2. ✓
- Alerts fallback chain + state suffix stripping → Task 13. ✓
- Permission variance + capabilities → Tasks 2 (hints), 16. ✓
- Env-only config → Task 1. ✓
- Table/json/csv + meta+frames shape → Tasks 4–5. ✓
- Time parser incl. `now-1d/d`, `now/w` → Task 3. ✓
- Macro substitution in sql → Task 11. ✓
- help command with full guide → Task 17. ✓
- Fixtures + hermetic tests → Tasks 18. ✓
- README as state doc → Task 19. ✓
- Non-goals respected: no writes, no streaming, no profiles, no caching. ✓

**Placeholder scan:** none — every task carries runnable code and expected test output.

**Type consistency:** `frames.Result`/`Meta`/`Frame`/`Column` shapes consistent Tasks 4→19; `run()`/`result{res,raw}` signature consistent Tasks 7→16; `lastCfg` addition noted inline in Task 13 (edit `run.go`); `api.Client` methods referenced across tasks all defined (Get/Post T2, DSQuery T6, Datasources/Resolve T7, SearchDashboards/DashboardJSON T12, Annotations T14, Health/Version T15, Alerts T13, Capabilities T16). `fakeGrafana` helper extended in Tasks 12/14/15 — each extension shows the exact branch to add. ✓
