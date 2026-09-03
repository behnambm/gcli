# Generic Profiles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make gcli a multi-team tool: named profiles (`profiles.yaml`), interactive onboarding, basic-auth support, org header, default datasource, and full brand-neutrality scrub.

**Architecture:** New `internal/profiles` package owns profiles.yaml load/save/resolve; `config.Config` gains optional auth fields; `api.Client` precomputes the Authorization header (Bearer or Basic) and optional `X-Grafana-Org-Id`; new `gcli profiles` command group; all existing commands keep working unchanged via the legacy env-var path.

**Tech Stack:** Go 1.27, cobra, `golang.org/x/term` (hidden prompt), `gopkg.in/yaml.v3` (config file). No other new deps.

**Spec:** `docs/superpowers/specs/2026-09-03-generic-profiles-design.md`

## Global Constraints

- Module path: `github.com/behnambm/gcli`
- Dependency rule (updated from spec): **cobra + golang.org/x/term + gopkg.in/yaml.v3 only**. Update CLAUDE.md invariant in Task 10.
- Read-only invariant: only GET + `/api/ds/query` POST. Profiles never change this.
- Precedence (highest wins): `--profile` → `GCLI_PROFILE` env → `default:` in profiles.yaml → `GRAFANA_URL`/`GRAFANA_TOKEN` env → error. `--url`/`--token` flags override everything.
- Token/pass NEVER printed by any command; verbose dumps redact both.
- Config file: `~/.config/gcli/profiles.yaml`, dir 0700, file chmod 0600 on every write.
- Tests: hermetic, behavioral style (contract comment + `TestX_input_expected` naming), TDD. Existing suite must stay green at every commit.
- Commit style: `feat(gcli): ...` / `fix(gcli): ...` / `docs(gcli): ...` conventional, no period.
- Profiles tests isolate filesystem state via `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` (makes `os.UserConfigDir()` return the temp dir).

---

### Task 1: Config + Client auth refactor (Bearer/Basic/OrgID/redact)

**Files:**
- Modify: `internal/config/config.go` (Config struct only — Load() logic unchanged)
- Modify: `internal/api/client.go` (auth header construction, org header, redact list)
- Test: `internal/config/config_test.go` (existing, untouched)
- Test: `internal/api/client_test.go` (add cases)

**Interfaces:**
- Produces: `config.Config` gains `User`, `Pass`, `OrgID`, `DefaultDatasource string` fields (all zero-value = absent, safe for every existing caller).
- Produces: `api.Client` gains `AuthHeader string` and `OrgID string` fields; `Token` field removed. `api.NewClient(cfg config.Config)` computes `AuthHeader` as `"Bearer <token>"` or `"Basic base64(user:pass)"`; `Secrets []string` holds token and/or pass for redaction.

- [ ] **Step 1: Write failing tests**

Append to `internal/api/client_test.go` (contract comment at top of file already covers auth behavior; extend it with Basic + OrgID lines):

```go
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
	if strings.Contains(buf.String(), "s3cret") {
		t.Errorf("verbose dump leaks pass:\n%s", buf.String())
	}
}
```

Note: existing `TestClient...bearer` tests reference `c.Token` — update them to assert `c.AuthHeader` instead (search `\.Token` in client_test.go).

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/api/ -run 'TestNewClient_basic|TestNewClient_bearerWins|TestClientDo_sendsOrg|TestVerboseDump_redactsPass' -v`
Expected: FAIL — compile errors (unknown fields `User`, `OrgID`, `AuthHeader`).

- [ ] **Step 3: Implement**

`internal/config/config.go` — extend struct (Load untouched):

```go
type Config struct {
	URL     string
	Token   string
	User    string // basic auth user; mutually exclusive with Token
	Pass    string // basic auth pass; only with User
	OrgID   string // optional; sent as X-Grafana-Org-Id header
	DefaultDatasource string // optional; fallback when a command omits its datasource arg
	Timeout time.Duration
	Output  string
	NoColor bool
	Verbose bool
}
```

`internal/api/client.go`:

```go
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	AuthHeader string   // precomputed "Bearer x" or "Basic base64"
	Secrets    []string // redacted from verbose dumps
	OrgID      string   // optional; sent as X-Grafana-Org-Id
	Verbose    bool
	LogW       io.Writer
}

func NewClient(cfg config.Config) *Client {
	c := &Client{
		BaseURL: cfg.URL,
		HTTP:    &http.Client{Timeout: cfg.Timeout},
		OrgID:   cfg.OrgID,
		Verbose: cfg.Verbose,
		LogW:    io.Discard,
	}
	if cfg.Token != "" {
		c.AuthHeader = "Bearer " + cfg.Token
		c.Secrets = []string{cfg.Token}
	} else if cfg.User != "" {
		c.AuthHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.User+":"+cfg.Pass))
		c.Secrets = []string{cfg.Pass}
	}
	return c
}
```

In `do()` replace:

```go
	req.Header.Set("Authorization", "Bearer "+c.Token)
```

with:

```go
	req.Header.Set("Authorization", c.AuthHeader)
	if c.OrgID != "" {
		req.Header.Set("X-Grafana-Org-Id", c.OrgID)
	}
```

Replace both `redact(string(dump), c.Token)` calls with `redact(string(dump), c.Secrets...)` and change:

```go
func redact(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "REDACTED")
	}
	return s
}
```

Add `"encoding/base64"` to imports (remove nothing else; `strings` still used).

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/api/ ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/api/client.go internal/api/client_test.go
git commit -m "feat(gcli): client supports basic auth + org header, redacts all secrets"
```

---

### Task 2: Profiles package — types, Load/Save, validation

**Files:**
- Create: `internal/profiles/profiles.go`
- Test: `internal/profiles/profiles_test.go`

**Interfaces:**
- Produces:
  - `profiles.Profile{Name, URL, Token, User, Pass, OrgID, DefaultDatasource string}` (Name from map key, yaml tag `-`)
  - `profiles.File{Default string; Profiles map[string]Profile}`
  - `func Path() (string, error)` — `os.UserConfigDir() + "/gcli/profiles.yaml"`
  - `func Load(path string) (File, error)` — strict yaml (unknown keys error), validation
  - `func Save(path string, f File) error` — MkdirAll 0700, write 0600, chmod 0600
  - `func WarnIfWorldReadable(path string) string` — `""` if file missing or not world-readable; else a warning line

- [ ] **Step 1: Write failing tests**

`internal/profiles/profiles_test.go`:

```go
// === Behavioral Contract: profiles.yaml load/save ===
//   - Load parses the yaml strictly: unknown keys are errors
//   - Validation: URL required; token XOR (user+pass); user without pass is an error
//   - Save creates the dir 0700 and the file 0600
//   - WarnIfWorldReadable returns "" for 0600 or missing files, a warning otherwise
package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_validFile_parses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(p, []byte(`default: prod
profiles:
  prod:
    url: https://grafana.example.com
    token: glsa_abc
    orgId: "1"
    defaultDatasource: Metrics-Alpha
`), 0600)
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Default != "prod" {
		t.Errorf("Default = %q", f.Default)
	}
	pr, ok := f.Profiles["prod"]
	if !ok {
		t.Fatal("prod profile missing")
	}
	if pr.URL != "https://grafana.example.com" || pr.Token != "glsa_abc" || pr.OrgID != "1" || pr.DefaultDatasource != "Metrics-Alpha" {
		t.Errorf("profile = %+v", pr)
	}
	if pr.Name != "prod" {
		t.Errorf("Name = %q, want map key", pr.Name)
	}
}

func TestLoad_unknownKey_errors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(p, []byte("profiles:\n  prod:\n    url: https://x\n    tokne: oops\n"), 0600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "tokne") {
		t.Errorf("err = %v, want unknown-key error naming tokne", err)
	}
}

func TestLoad_tokenAndUserBothSet_errors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(p, []byte("profiles:\n  a:\n    url: https://x\n    token: glsa_t\n    user: bob\n    pass: p\n"), 0600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), "user") {
		t.Errorf("err = %v, want token/user conflict", err)
	}
}

func TestLoad_userWithoutPass_errors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(p, []byte("profiles:\n  a:\n    url: https://x\n    user: bob\n"), 0600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "pass") {
		t.Errorf("err = %v, want pass required with user", err)
	}
}

func TestLoad_missingURL_errors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(p, []byte("profiles:\n  a:\n    token: glsa_t\n"), 0600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Errorf("err = %v, want url required", err)
	}
}

func TestLoad_missingFile_errors(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestSave_createsDir0700AndFile0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "profiles.yaml")
	f := File{Default: "a", Profiles: map[string]Profile{"a": {URL: "https://x", Token: "glsa_t"}}}
	if err := Save(p, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("file mode = %v, want 0600", st.Mode().Perm())
	}
	dst, _ := os.Stat(filepath.Dir(p))
	if dst.Mode().Perm() != 0700 {
		t.Errorf("dir mode = %v, want 0700", dst.Mode().Perm())
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Profiles["a"].Token != "glsa_t" {
		t.Errorf("round-trip lost token: %+v", got)
	}
}

func TestWarnIfWorldReadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	if w := WarnIfWorldReadable(p); w != "" {
		t.Errorf("missing file: got %q, want empty", w)
	}
	os.WriteFile(p, []byte("profiles: {}\n"), 0600)
	if w := WarnIfWorldReadable(p); w != "" {
		t.Errorf("0600 file: got %q, want empty", w)
	}
	os.Chmod(p, 0644)
	if w := WarnIfWorldReadable(p); !strings.Contains(w, "chmod 600") {
		t.Errorf("world-readable file: got %q, want chmod hint", w)
	}
}
```

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/profiles/`
Expected: FAIL — "no Go files" / undefined symbols.

- [ ] **Step 3: Implement**

`internal/profiles/profiles.go`:

```go
// Package profiles manages ~/.config/gcli/profiles.yaml: named Grafana
// connection profiles (url + token or basic auth, optional org + default
// datasource) plus the active `default:` marker.
package profiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Name             string `yaml:"-"`
	URL              string `yaml:"url"`
	Token            string `yaml:"token,omitempty"`
	User             string `yaml:"user,omitempty"`
	Pass             string `yaml:"pass,omitempty"`
	OrgID            string `yaml:"orgId,omitempty"`
	DefaultDatasource string `yaml:"defaultDatasource,omitempty"`
}

type File struct {
	Default  string             `yaml:"default,omitempty"`
	Profiles map[string]Profile `yaml:"profiles"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir: %w", err)
	}
	return filepath.Join(dir, "gcli", "profiles.yaml"), nil
}

func Load(path string) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read profiles file %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	for name, p := range f.Profiles {
		p.Name = name
		if p.URL == "" {
			return File{}, fmt.Errorf("profile %q: url is required", name)
		}
		if p.Token != "" && (p.User != "" || p.Pass != "") {
			return File{}, fmt.Errorf("profile %q: set either token or user/pass, not both", name)
		}
		if p.User != "" && p.Pass == "" {
			return File{}, fmt.Errorf("profile %q: user requires pass", name)
		}
		if p.Pass != "" && p.User == "" {
			return File{}, fmt.Errorf("profile %q: pass requires user", name)
		}
		f.Profiles[name] = p
	}
	return f, nil
}

func Save(path string, f File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Chmod(path, 0600)
}

// WarnIfWorldReadable returns a chmod hint when an existing profiles file is
// readable by group/other, "" otherwise (missing file included).
func WarnIfWorldReadable(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Sprintf("warning: %s is world-readable — run: chmod 600 %s", path, path)
	}
	return ""
}

var ErrNoProfileFile = errors.New("no profiles file")
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/profiles/ -v`
Expected: PASS. Also `go get gopkg.in/yaml.v3` first (see step 3b below — run before test compile):

```bash
go get gopkg.in/yaml.v3
```

- [ ] **Step 5: Commit**

```bash
git add internal/profiles/ go.mod go.sum
git commit -m "feat(gcli): profiles package — profiles.yaml load/save with validation"
```

---

### Task 3: Profile resolution (precedence chain)

**Files:**
- Create: `internal/profiles/resolve.go`
- Test: `internal/profiles/resolve_test.go`
- Modify: `internal/config/config.go` — export nothing new; Resolve reuses `config.Load` (its `firstNonEmpty` stays private; reimplement locally)

**Interfaces:**
- Consumes: `profiles.File`, `profiles.Load`, `profiles.Path` (Task 2), `config.Config`, `config.Load` (existing).
- Produces: `func Resolve(o ResolveOptions) (config.Config, Profile, error)` with:

```go
type ResolveOptions struct {
	FlagProfile string // --profile
	FlagURL     string // --url
	FlagToken   string // --token
	Path        string // profiles.yaml path; "" = profiles.Path()
	Timeout     time.Duration
	Output      string
	NoColor     bool
	Verbose     bool
}
```

Semantics: name = FlagProfile → `GCLI_PROFILE` env → file `default:` marker. If name set: file must exist (else error), profile must exist (else error listing known names). Profile URL overridden by FlagURL; FlagToken overrides profile auth entirely (clears User/Pass, uses Token). Output validated (`table|json|csv`, default table). No name: delegate to `config.Load(FlagURL, FlagToken, ...)` — legacy path byte-for-byte identical.

- [ ] **Step 1: Write failing tests**

`internal/profiles/resolve_test.go`:

```go
// === Behavioral Contract: Resolve precedence ===
//   --profile > GCLI_PROFILE > default: > legacy env > error
//   --url/--token flags override profile values
//   missing file/profile produce actionable errors; legacy path = config.Load
package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

func writeFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "profiles.yaml")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoProfiles = `default: prod
profiles:
  prod:
    url: https://prod.example.com
    token: glsa_prod
  dev:
    url: https://dev.example.com
    user: bob
    pass: p4ss
`

func TestResolve_flagProfileWins(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	t.Setenv("GCLI_PROFILE", "prod")
	cfg, pr, err := Resolve(ResolveOptions{FlagProfile: "dev", Path: p})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.URL != "https://dev.example.com" || cfg.User != "bob" || cfg.Pass != "p4ss" {
		t.Errorf("cfg = %+v, want dev profile", cfg)
	}
	if pr.Name != "dev" {
		t.Errorf("profile = %+v", pr)
	}
}

func TestResolve_envProfileBeatsDefault(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	t.Setenv("GCLI_PROFILE", "dev")
	cfg, _, err := Resolve(ResolveOptions{Path: p})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.URL != "https://dev.example.com" {
		t.Errorf("cfg = %+v, want dev via GCLI_PROFILE", cfg)
	}
}

func TestResolve_defaultMarkerUsed(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	cfg, _, err := Resolve(ResolveOptions{Path: p})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.URL != "https://prod.example.com" || cfg.Token != "glsa_prod" {
		t.Errorf("cfg = %+v, want default prod", cfg)
	}
}

func TestResolve_legacyEnvWhenNoFile(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://legacy.example.com")
	t.Setenv("GRAFANA_TOKEN", "glsa_legacy")
	cfg, _, err := Resolve(ResolveOptions{Path: filepath.Join(t.TempDir(), "missing.yaml")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.URL != "https://legacy.example.com" || cfg.Token != "glsa_legacy" {
		t.Errorf("cfg = %+v, want legacy env", cfg)
	}
}

func TestResolve_namedProfileMissingFile_errorsWithHint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.yaml")
	_, _, err := Resolve(ResolveOptions{FlagProfile: "prod", Path: p})
	if err == nil || !strings.Contains(err.Error(), "profiles add") {
		t.Errorf("err = %v, want hint to profiles add", err)
	}
}

func TestResolve_unknownProfile_errorsListingNames(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	_, _, err := Resolve(ResolveOptions{FlagProfile: "nope", Path: p})
	if err == nil || !strings.Contains(err.Error(), "prod") || !strings.Contains(err.Error(), "dev") {
		t.Errorf("err = %v, want known profile names listed", err)
	}
}

func TestResolve_flagURLOverridesProfile(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	cfg, _, err := Resolve(ResolveOptions{Path: p, FlagURL: "https://override.example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.URL != "https://override.example.com" {
		t.Errorf("URL = %q, want flag override", cfg.URL)
	}
}

func TestResolve_flagTokenOverridesProfileAuth(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	cfg, _, err := Resolve(ResolveOptions{Path: p, FlagToken: "glsa_flag"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Token != "glsa_flag" || cfg.User != "" || cfg.Pass != "" {
		t.Errorf("cfg = %+v, want flag token to replace profile auth", cfg)
	}
}

func TestResolve_invalidOutput_errors(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	_, _, err := Resolve(ResolveOptions{Path: p, Output: "xml"})
	if err == nil || !strings.Contains(err.Error(), "table, json or csv") {
		t.Errorf("err = %v, want output validation", err)
	}
}

func TestResolve_orgIDAndDefaultDatasourceCarried(t *testing.T) {
	p := writeFile(t, t.TempDir(), `profiles:
  a:
    url: https://x.example.com
    token: glsa_a
    orgId: "3"
    defaultDatasource: Metrics-Alpha
`)
	cfg, _, err := Resolve(ResolveOptions{Path: p, FlagProfile: "a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.OrgID != "3" || cfg.DefaultDatasource != "Metrics-Alpha" {
		t.Errorf("cfg = %+v", cfg)
	}
}

var _ = config.Config{} // config import stays live
var _ = time.Second
```

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/profiles/ -run TestResolve`
Expected: FAIL — `Resolve` undefined.

- [ ] **Step 3: Implement**

`internal/profiles/resolve.go`:

```go
package profiles

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

type ResolveOptions struct {
	FlagProfile string
	FlagURL     string
	FlagToken   string
	Path        string // profiles.yaml path; "" = profiles.Path()
	Timeout     time.Duration
	Output      string
	NoColor     bool
	Verbose     bool
}

// Resolve picks the active profile: --profile > GCLI_PROFILE > default: >
// legacy GRAFANA_URL/GRAFANA_TOKEN env. --url/--token flags override any
// profile value. Returns the zero Profile on the legacy path.
func Resolve(o ResolveOptions) (config.Config, Profile, error) {
	path := o.Path
	if path == "" {
		p, err := Path()
		if err != nil {
			return config.Config{}, Profile{}, err
		}
		path = p
	}
	name := o.FlagProfile
	if name == "" {
		name = os.Getenv("GCLI_PROFILE")
	}
	if name == "" {
		// Only consult the default: marker when the file exists — legacy
		// users have no file and must not get a "no profiles file" error.
		if f, err := Load(path); err == nil {
			name = f.Default
		}
	}
	if name == "" {
		cfg, err := config.Load(o.FlagURL, o.FlagToken, o.Timeout, o.Output, o.NoColor, o.Verbose)
		return cfg, Profile{}, err
	}
	f, err := Load(path)
	if err != nil {
		return config.Config{}, Profile{}, fmt.Errorf("profile %q requested but %w — run `gcli profiles add %s`", name, err, name)
	}
	p, ok := f.Profiles[name]
	if !ok {
		return config.Config{}, Profile{}, fmt.Errorf("profile %q not found in %s — known profiles: %s", name, path, strings.Join(knownNames(f), ", "))
	}
	url := firstNonEmpty(o.FlagURL, p.URL)
	if url == "" {
		return config.Config{}, Profile{}, fmt.Errorf("profile %q has no url", name)
	}
	out := o.Output
	if out == "" {
		out = "table"
	}
	switch out {
	case "table", "json", "csv":
	default:
		return config.Config{}, Profile{}, fmt.Errorf("invalid --output %q: must be table, json or csv", out)
	}
	cfg := config.Config{
		URL:               strings.TrimRight(url, "/"),
		User:              p.User,
		Pass:              p.Pass,
		Token:             p.Token,
		OrgID:             p.OrgID,
		DefaultDatasource: p.DefaultDatasource,
		Timeout:           o.Timeout,
		Output:            out,
		NoColor:           o.NoColor,
		Verbose:           o.Verbose,
	}
	if o.FlagToken != "" {
		cfg.Token = o.FlagToken
		cfg.User, cfg.Pass = "", ""
	}
	return cfg, p, nil
}

func knownNames(f File) []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/profiles/ ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profiles/resolve.go internal/profiles/resolve_test.go
git commit -m "feat(gcli): profile resolution — flag > env > default > legacy env"
```

---

### Task 4: `gcli profiles add` + `list`

**Files:**
- Create: `internal/cmd/profiles.go`
- Test: `internal/cmd/profiles_test.go`

**Interfaces:**
- Consumes: `profiles.Path`, `profiles.Load`, `profiles.Save`, `profiles.WarnIfWorldReadable`, `profiles.File`, `profiles.Profile` (Tasks 2–3).
- Produces: `profilesCmd` cobra command with subcommands `add`, `list`. Flag vars: `flagProfURL, flagProfToken, flagProfUser, flagProfPass, flagProfOrgID, flagProfDefaultDS string; flagProfSetDefault, flagProfForce bool`. All registered in `resetAllFlags()` (Task 7 adds the reset lines; add them here too and include the helper edit in this task — see Step 3).

Behavior:
- `add <name>`: with all of `--url` + auth (`--token` or `--user`+`--pass`) set → non-interactive. Otherwise, if stdin is a TTY → interactive prompts (auth method choice, hidden token/pass via `x/term`); if not a TTY → error listing the flag form. Upserts into existing file, saves, prints saved path + perm warning if any.
- `list`: prints table `NAME URL AUTH ORG DEFAULT`; default row marked `*`; never prints token/pass; no file → error with `gcli profiles add` hint.

- [ ] **Step 1: Write failing tests**

`internal/cmd/profiles_test.go`:

```go
// === Behavioral Contract: gcli profiles add/list ===
//   - add --url --token writes profiles.yaml (0600) and reloads it
//   - add --user --pass works; token+user together is rejected
//   - add with no flags and no TTY errors pointing at the flag form
//   - add with no flags and TTY reads prompts from stdin (hidden input tested in Task 6? no — term is exercised via stdin pipe fallback: see note)
//   - list prints names/urls but never token or pass
//   - list without a profiles file errors with an actionable hint
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behnambm/gcli/internal/profiles"
)

func xdgEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestProfilesAdd_flagForm_writesFile(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://grafana.example.com", "--token", "glsa_secret")

	p, err := profiles.Path()
	if err != nil {
		t.Fatal(err)
	}
	f, err := profiles.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Profiles["prod"].URL != "https://grafana.example.com" || f.Profiles["prod"].Token != "glsa_secret" {
		t.Errorf("file = %+v", f)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestProfilesAdd_basicAuth(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "dev", "--url", "https://dev.example.com", "--user", "bob", "--pass", "hunter2")

	p, _ := profiles.Path()
	f, _ := profiles.Load(p)
	if f.Profiles["dev"].User != "bob" || f.Profiles["dev"].Pass != "hunter2" {
		t.Errorf("file = %+v", f)
	}
}

func TestProfilesAdd_tokenAndUser_rejected(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "add", "bad", "--url", "https://x", "--token", "t", "--user", "bob", "--pass", "p")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("err = %v, want token/user conflict", err)
	}
}

func TestProfilesAdd_noFlagsNoTTY_errorsWithFlagHint(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "add", "prod")
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("err = %v, want flag-form hint", err)
	}
}

func TestProfilesAdd_setDefault(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://grafana.example.com", "--token", "glsa_t", "--set-default")
	p, _ := profiles.Path()
	f, _ := profiles.Load(p)
	if f.Default != "prod" {
		t.Errorf("Default = %q, want prod", f.Default)
	}
}

func TestProfilesList_neverLeaksSecrets(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://grafana.example.com", "--token", "glsa_secret", "--set-default")
	runCommand(t, "profiles", "add", "dev", "--url", "https://dev.example.com", "--user", "bob", "--pass", "hunter2")

	out := runCommand(t, "profiles", "list")
	for _, want := range []string{"prod", "dev", "https://grafana.example.com", "https://dev.example.com", "token", "basic"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"glsa_secret", "hunter2"} {
		if strings.Contains(out, leaked) {
			t.Errorf("list output leaks %q:\n%s", leaked, out)
		}
	}
}

func TestProfilesList_noFile_errors(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "list")
	if err == nil || !strings.Contains(err.Error(), "profiles add") {
		t.Errorf("err = %v, want add hint", err)
	}
}
```

Note on interactive test: the interactive path reads via `term.ReadPassword` only when stdin is a TTY; tests pipe stdin (not a TTY) so they exercise the non-TTY error path. The interactive happy path is covered manually; keep `promptHidden` thin so the flag-form logic carries the tested behavior.

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/cmd/ -run TestProfiles`
Expected: FAIL — `profiles` unknown command.

- [ ] **Step 3: Implement**

`internal/cmd/profiles.go`:

```go
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/behnambm/gcli/internal/profiles"
)

var (
	flagProfURL       string
	flagProfToken     string
	flagProfUser      string
	flagProfPass      string
	flagProfOrgID     string
	flagProfDefaultDS string
	flagProfSetDefault bool
	flagProfForce     bool
)

func init() {
	rootCmd.AddCommand(profilesCmd)
}

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Manage Grafana connection profiles (profiles.yaml)",
}

var profilesAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update a profile (interactive, or --url/--token/--user/--pass)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if name == "list" || name == "add" || name == "use" || name == "remove" || name == "test" {
			return fmt.Errorf("%q is a reserved word — pick another profile name", name)
		}
		path, err := profiles.Path()
		if err != nil {
			return err
		}
		f := profiles.File{Profiles: map[string]profiles.Profile{}}
		if existing, err := profiles.Load(path); err == nil {
			f = existing
		}
		p := f.Profiles[name]
		p.Name = name
		haveFlags := flagProfURL != "" && (flagProfToken != "" || flagProfUser != "")
		if !haveFlags {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("not a TTY and no flags — use: gcli profiles add %s --url <u> (--token <t> | --user <u> --pass <p>)", name)
			}
			fmt.Fprint(cmd.OutOrStdout(), "Grafana URL: ")
			url, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if err != nil {
				return fmt.Errorf("read url: %w", err)
			}
			flagProfURL = strings.TrimSpace(url)
			fmt.Fprint(cmd.OutOrStdout(), "Auth method (token|basic): ")
			method, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			switch strings.TrimSpace(method) {
			case "basic":
				fmt.Fprint(cmd.OutOrStdout(), "User: ")
				u, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				flagProfUser = strings.TrimSpace(u)
				fmt.Fprint(cmd.OutOrStdout(), "Password (hidden): ")
				pass, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("read password: %w", err)
				}
				flagProfPass = string(pass)
			default:
				fmt.Fprint(cmd.OutOrStdout(), "Token (hidden): ")
				tok, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("read token: %w", err)
				}
				flagProfToken = string(tok)
			}
		}
		if flagProfToken != "" && (flagProfUser != "" || flagProfPass != "") {
			return fmt.Errorf("set either --token or --user/--pass, not both")
		}
		p.URL = strings.TrimRight(flagProfURL, "/")
		p.Token = flagProfToken
		p.User = flagProfUser
		p.Pass = flagProfPass
		p.OrgID = flagProfOrgID
		p.DefaultDatasource = flagProfDefaultDS
		if p.URL == "" {
			return fmt.Errorf("url is required — pass --url <u>")
		}
		f.Profiles[name] = p
		if flagProfSetDefault {
			f.Default = name
		}
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "profile %q saved to %s\n", name, path)
		if w := profiles.WarnIfWorldReadable(path); w != "" {
			fmt.Fprintln(cmd.OutOrStdout(), w)
		}
		return nil
	},
}

var profilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles (secrets never shown)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := profiles.Path()
		if err != nil {
			return err
		}
		f, err := profiles.Load(path)
		if err != nil {
			return fmt.Errorf("%v — run `gcli profiles add <name>` to create one", err)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tURL\tAUTH\tORG\tDEFAULT")
		for _, name := range sortedNames(f) {
			p := f.Profiles[name]
			auth := "token"
			if p.User != "" {
				auth = "basic"
			}
			mark := ""
			if name == f.Default {
				mark = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, p.URL, auth, p.OrgID, mark)
		}
		return w.Flush()
	},
}

func sortedNames(f profiles.File) []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func init() {
	profilesAddCmd.Flags().StringVar(&flagProfURL, "url", "", "Grafana URL")
	profilesAddCmd.Flags().StringVar(&flagProfToken, "token", "", "service-account token")
	profilesAddCmd.Flags().StringVar(&flagProfUser, "user", "", "basic-auth user")
	profilesAddCmd.Flags().StringVar(&flagProfPass, "pass", "", "basic-auth password")
	profilesAddCmd.Flags().StringVar(&flagProfOrgID, "org-id", "", "optional org id (X-Grafana-Org-Id header)")
	profilesAddCmd.Flags().StringVar(&flagProfDefaultDS, "default-datasource", "", "optional default datasource name/uid")
	profilesAddCmd.Flags().BoolVar(&flagProfSetDefault, "set-default", false, "make this the default profile")
	profilesCmd.AddCommand(profilesAddCmd, profilesListCmd)
}
```

Import `"sort"` for `sort.Strings`.

Also modify `internal/cmd/query_test.go` `resetAllFlags()` — add after `flagUninstallYes = false`:

```go
	flagProfile = ""
	flagProfURL = ""
	flagProfToken = ""
	flagProfUser = ""
	flagProfPass = ""
	flagProfOrgID = ""
	flagProfDefaultDS = ""
	flagProfSetDefault = false
	flagProfForce = false
```

(`flagProfile` is declared in Task 7; declare `var flagProfile string` in `root.go` in THIS task so the reset block compiles.)

In `root.go` vars block add:

```go
	flagProfile string
```

Run `go get golang.org/x/term`.

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/cmd/ -run TestProfiles -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/profiles.go internal/cmd/profiles_test.go internal/cmd/query_test.go internal/cmd/root.go go.mod go.sum
git commit -m "feat(gcli): profiles add + list commands (interactive or flags)"
```

---

### Task 5: `gcli profiles use` + `remove`

**Files:**
- Modify: `internal/cmd/profiles.go`
- Test: `internal/cmd/profiles_test.go`

**Interfaces:**
- Consumes: everything from Task 4; `flagProfForce` already declared.
- Produces: `profilesUseCmd`, `profilesRemoveCmd` added to `profilesCmd`.

Behavior:
- `use <name>`: file must exist, name must exist (error lists known names), sets `default:`, saves, prints confirmation.
- `remove <name>`: name must exist; refuses when name == current default unless `--force`; deletes, saves, prints confirmation.

- [ ] **Step 1: Write failing tests**

Append to `internal/cmd/profiles_test.go`:

```go
func TestProfilesUse_setsDefault(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://a.example.com", "--token", "t1")
	runCommand(t, "profiles", "add", "dev", "--url", "https://b.example.com", "--token", "t2")

	runCommand(t, "profiles", "use", "dev")

	p, _ := profiles.Path()
	f, _ := profiles.Load(p)
	if f.Default != "dev" {
		t.Errorf("Default = %q, want dev", f.Default)
	}
}

func TestProfilesUse_unknownName_listsKnown(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://a.example.com", "--token", "t1")
	_, err := runCommandErr(t, "profiles", "use", "nope")
	if err == nil || !strings.Contains(err.Error(), "prod") {
		t.Errorf("err = %v, want known names listed", err)
	}
}

func TestProfilesRemove_deletesProfile(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://a.example.com", "--token", "t1")
	runCommand(t, "profiles", "add", "dev", "--url", "https://b.example.com", "--token", "t2")

	runCommand(t, "profiles", "remove", "dev")

	p, _ := profiles.Path()
	f, _ := profiles.Load(p)
	if _, ok := f.Profiles["dev"]; ok {
		t.Error("dev still present after remove")
	}
}

func TestProfilesRemove_defaultRefusedWithoutForce(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://a.example.com", "--token", "t1", "--set-default")
	_, err := runCommandErr(t, "profiles", "remove", "prod")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want --force hint", err)
	}
}

func TestProfilesRemove_defaultWithForce_clearsDefault(t *testing.T) {
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", "https://a.example.com", "--token", "t1", "--set-default")
	runCommand(t, "profiles", "remove", "prod", "--force")

	p, _ := profiles.Path()
	f, _ := profiles.Load(p)
	if f.Default != "" {
		t.Errorf("Default = %q, want cleared", f.Default)
	}
	if _, ok := f.Profiles["prod"]; ok {
		t.Error("prod still present after --force remove")
	}
}
```

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/cmd/ -run 'TestProfilesUse|TestProfilesRemove'`
Expected: FAIL — `use`/`remove` unknown.

- [ ] **Step 3: Implement**

Append to `internal/cmd/profiles.go` (reuse the shared loader helper introduced in Task 4 — if Task 4 did not define one, add it now):

```go
func loadProfilesFile() (string, profiles.File, error) {
	path, err := profiles.Path()
	if err != nil {
		return "", profiles.File{}, err
	}
	f, err := profiles.Load(path)
	if err != nil {
		return "", profiles.File{}, fmt.Errorf("%v — run `gcli profiles add <name>` to create one", err)
	}
	return path, f, nil
}
```

Refactor `profilesListCmd.RunE` to use it (replace its duplicated load block).

```go
var profilesUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the default profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, f, err := loadProfilesFile()
		if err != nil {
			return err
		}
		if _, ok := f.Profiles[args[0]]; !ok {
			return fmt.Errorf("profile %q not found — known profiles: %s", args[0], strings.Join(sortedNames(f), ", "))
		}
		f.Default = args[0]
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "default profile set to %q\n", args[0])
		return nil
	},
}

var profilesRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Delete a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, f, err := loadProfilesFile()
		if err != nil {
			return err
		}
		if _, ok := f.Profiles[args[0]]; !ok {
			return fmt.Errorf("profile %q not found — known profiles: %s", args[0], strings.Join(sortedNames(f), ", "))
		}
		if f.Default == args[0] && !flagProfForce {
			return fmt.Errorf("%q is the default profile — pass --force to remove it anyway", args[0])
		}
		delete(f.Profiles, args[0])
		if f.Default == args[0] {
			f.Default = ""
		}
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "profile %q removed\n", args[0])
		return nil
	},
}
```

In the profiles init():

```go
	profilesRemoveCmd.Flags().BoolVar(&flagProfForce, "force", false, "remove even if it is the default profile")
	profilesCmd.AddCommand(profilesAddCmd, profilesListCmd, profilesUseCmd, profilesRemoveCmd)
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/cmd/ -run 'TestProfilesUse|TestProfilesRemove' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/profiles.go internal/cmd/profiles_test.go
git commit -m "feat(gcli): profiles use + remove with default protection"
```

---

### Task 6: `gcli profiles test`

**Files:**
- Modify: `internal/cmd/profiles.go`
- Test: `internal/cmd/profiles_test.go`

**Interfaces:**
- Consumes: `profiles.Resolve` (Task 3), `api.NewClient` (Task 1), `api.Client.Health(ctx) (string, []api.HealthStatus, error)` (existing, `internal/api/grafana.go:143`), `flagProfile` (Task 4 root.go decl).
- Produces: `profilesTestCmd` added to `profilesCmd`.

Behavior: `test [name]` — with arg, temporarily sets `flagProfile = args[0]` so the normal resolution chain (flags/env/default) applies; then `run()` against `/api/health`. Prints `OK: grafana <version> at <url>`; failures map through standard exit codes (3 for 401 etc.). No arg = resolved default profile.

- [ ] **Step 1: Write failing tests**

Append to `internal/cmd/profiles_test.go`:

```go
const healthResp = `{"database":"ok","version":"10.4.3","commit":"abc"}`

func TestProfilesTest_ok_printsVersion(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/health": healthResp}, nil)
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "glsa_t")

	out := runCommand(t, "profiles", "test", "prod")

	if !strings.Contains(out, "OK") || !strings.Contains(out, "10.4.3") {
		t.Errorf("output = %q, want OK + version", out)
	}
}

func TestProfilesTest_401_mapsToExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "bad")

	_, err := runCommandErr(t, "profiles", "test", "prod")

	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want 401 surfaced", err)
	}
}
```

(Add `"net/http"` and `"net/http/httptest"` to the test file imports.)

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/cmd/ -run TestProfilesTest`
Expected: FAIL — `test` unknown.

- [ ] **Step 3: Implement**

Append to `internal/cmd/profiles.go`:

```go
var profilesTestCmd = &cobra.Command{
	Use:   "test [name]",
	Short: "Smoke-test a profile: /api/health + version",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			flagProfile = args[0]
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			version, _, err := c.Health(ctx)
			if err != nil {
				return result{}, err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK: grafana %s at %s\n", version, lastCfg.URL)
			return result{}, nil
		})
	},
}
```

Register in profiles init():

```go
	profilesCmd.AddCommand(profilesAddCmd, profilesListCmd, profilesUseCmd, profilesRemoveCmd, profilesTestCmd)
```

Add `"context"` and `"github.com/behnambm/gcli/internal/api"` imports to profiles.go.

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/cmd/ -run TestProfilesTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/profiles.go internal/cmd/profiles_test.go
git commit -m "feat(gcli): profiles test — per-profile health smoke test"
```

---

### Task 7: Wire `--profile` flag + resolution into every command

**Files:**
- Modify: `internal/cmd/root.go` (register `--profile` persistent flag)
- Modify: `internal/cmd/run.go` (`clientFromFlags` uses `profiles.Resolve`)
- Test: `internal/cmd/profiles_test.go` (end-to-end resolution via commands)

**Interfaces:**
- Consumes: `profiles.Resolve` (Task 3), `flagProfile` (Task 4).
- Produces: global `--profile` flag; `run()` no longer calls `config.Load` directly.

- [ ] **Step 1: Write failing tests**

Append to `internal/cmd/profiles_test.go`:

```go
func TestCommand_usesProfileViaFlag(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/health": healthResp}, nil)
	defer srv.Close()
	xdgEnv(t)
	t.Setenv("GRAFANA_URL", "") // ensure legacy env cannot satisfy config
	t.Setenv("GRAFANA_TOKEN", "")
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "glsa_t")

	out := runCommand(t, "--profile", "prod", "health")

	if !strings.Contains(out, "10.4.3") {
		t.Errorf("output = %q, want health via profile", out)
	}
}

func TestCommand_usesDefaultMarker(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/health": healthResp}, nil)
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "glsa_t", "--set-default")

	out := runCommand(t, "health")

	if !strings.Contains(out, "10.4.3") {
		t.Errorf("output = %q, want default profile used", out)
	}
}

func TestCommand_GCLI_PROFILE_envSelects(t *testing.T) {
	srvA := fakeGrafana(t, map[string]string{"/api/health": healthResp}, nil)
	defer srvA.Close()
	srvB := fakeGrafana(t, map[string]string{"/api/health": healthResp}, nil)
	defer srvB.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srvA.URL, "--token", "t1", "--set-default")
	runCommand(t, "profiles", "add", "dev", "--url", srvB.URL, "--token", "t2")
	t.Setenv("GCLI_PROFILE", "dev")

	out := runCommand(t, "version")

	// version output carries the URL only via verbose/meta; assert via health instead
	out = runCommand(t, "health")
	if !strings.Contains(out, "10.4.3") {
		t.Errorf("output = %q, want health via GCLI_PROFILE", out)
	}
}

func TestCommand_unknownProfileFlag_errors(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "--profile", "nope", "health")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, want unknown profile error", err)
	}
}
```

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/cmd/ -run 'TestCommand_'`
Expected: FAIL — `--profile` flag unknown / resolution not wired.

- [ ] **Step 3: Implement**

`internal/cmd/root.go` init() — add one line after the existing `--token` registration:

```go
	rootCmd.PersistentFlags().StringVar(&flagProfile, "profile", "", "profile name from profiles.yaml (overrides GCLI_PROFILE and default:)")
```

`internal/cmd/run.go` `clientFromFlags`:

```go
func clientFromFlags(cmd *cobra.Command) (*api.Client, config.Config, error) {
	cfg, _, err := profiles.Resolve(profiles.ResolveOptions{
		FlagProfile: flagProfile,
		FlagURL:     flagURL,
		FlagToken:   flagToken,
		Timeout:     flagTimeout,
		Output:      flagOutput,
		NoColor:     flagNoColor,
		Verbose:     flagVerbose,
	})
	if err != nil {
		return nil, config.Config{}, err
	}
	c := api.NewClient(cfg)
	if cfg.Verbose {
		c.LogW = cmd.ErrOrStderr()
	}
	return c, cfg, nil
}
```

Add import `"github.com/behnambm/gcli/internal/profiles"` to run.go.

- [ ] **Step 4: Run full suite, verify pass**

Run: `make test`
Expected: PASS — including every pre-existing test (legacy env path unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/root.go internal/cmd/run.go internal/cmd/profiles_test.go
git commit -m "feat(gcli): wire --profile/GCLI_PROFILE/default resolution into all commands"
```

---

### Task 8: Default datasource fallback in query commands

**Files:**
- Modify: `internal/cmd/run.go` (add `dsArg` helper)
- Modify: `internal/cmd/query.go`, `prom.go`, `logs.go`, `sql.go`, `metrics.go`, `labels.go`, `introspect.go` (arg handling per command)
- Test: `internal/cmd/prom_test.go` (fallback + error cases), `internal/cmd/query_test.go` (query command)

**Interfaces:**
- Consumes: `lastCfg` (run.go, existing), `config.Config.DefaultDatasource` (Task 1).
- Produces: `func dsArg(args []string) (string, error)` in package cmd — returns `args[0]` when present, else `lastCfg.DefaultDatasource` or an actionable error.

Behavior per command (datasource is always the FIRST positional; the default only fills a fully absent first positional):
- `query`: `Args: cobra.RangeArgs(0, 1)`; `ds, err := dsArg(args)`; use `ds` where `args[0]` was used.
- `prom`, `logs` (`ExactArgs(2)` → `RangeArgs(1, 2)`): if `len(args) == 2` → ds=`args[0]`, rest=`args[1]`; if 1 → ds=`dsArg(nil)`, rest=`args[0]`.
- `sql` (`MinimumNArgs(2)` → `MinimumNArgs(1)`): if `len(args) >= 2` → ds=`args[0]`, query=`strings.Join(args[1:], " ")`; else ds=`dsArg(nil)`, query=`strings.Join(args, " ")`.
- `metrics` (`RangeArgs(1,2)` → `RangeArgs(0,2)`): ds from `dsArg(args[:1])`... careful — only first positional is ds; `metrics` with 0 args → default ds + empty pattern; `metrics X` still means ds=X (unchanged semantics).
- `labels` (`RangeArgs(1,2)` → `RangeArgs(0,2)`): same rule as metrics.
- `values` (`RangeArgs(2,3)` → `RangeArgs(1,3)`): 3 args = ds,label,metric; 2 args = ds,label; 1 arg = default ds + label.
- `tables` (`ExactArgs(1)` → `RangeArgs(0,1)`): 0 args → default ds.
- `columns` (`ExactArgs(2)` → `RangeArgs(1,2)`): 1 arg = default ds + table.

- [ ] **Step 1: Write failing tests**

Append to `internal/cmd/prom_test.go`:

```go
func TestPromCommand_defaultDatasourceUsedWhenArgOmitted(t *testing.T) {
	var gotBody map[string]any
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, func(t *testing.T, body map[string]any) {
		gotBody = body
	})
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Universal")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	runCommand(t, "--profile", "p", "prom", "count(up)")

	if gotBody == nil {
		t.Fatal("no ds/query request received")
	}
	q0 := firstQueryObject(t, gotBody)
	if q0["expr"] != "count(up)" {
		t.Errorf("expr = %v", q0["expr"])
	}
}

func TestPromCommand_noArgNoDefault_errors(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "prom")

	if err == nil || !strings.Contains(err.Error(), "defaultDatasource") {
		t.Errorf("err = %v, want defaultDatasource hint", err)
	}
}
```

- [ ] **Step 2: Run tests, verify fail**

Run: `go test ./internal/cmd/ -run TestPromCommand_default`
Expected: FAIL — `prom` rejects arg count / default unused.

- [ ] **Step 3: Implement**

`internal/cmd/run.go`:

```go
// dsArg resolves the datasource positional: explicit arg wins, else the
// profile's defaultDatasource, else an actionable error.
func dsArg(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if lastCfg.DefaultDatasource == "" {
		return "", fmt.Errorf("no datasource given — pass <datasource-uid-or-name>, or set defaultDatasource in your profile (`gcli profiles add <name> --default-datasource <ds>`)")
	}
	return lastCfg.DefaultDatasource, nil
}
```

Per command (exact edits):

`query.go` — `Args: cobra.ExactArgs(1)` → `Args: cobra.RangeArgs(0, 1)`; inside RunE before `run(...)`:

```go
			ds, err := dsArg(args)
			if err != nil {
				return err
			}
```

and replace `c.ResolveDatasource(ctx, args[0])` with `c.ResolveDatasource(ctx, ds)`.

`prom.go` and `logs.go` — `Args: cobra.ExactArgs(2)` → `Args: cobra.RangeArgs(1, 2)`; at RunE top:

```go
			ds, err := dsArg(args[:1])
			if err != nil {
				return err
			}
			query := args[len(args)-1]
```

replace the previous `args[0]`/`args[1]` reads with `ds`/`query`.

`sql.go` — `Args: cobra.MinimumNArgs(2)` → `Args: cobra.MinimumNArgs(1)`; RunE top:

```go
			var ds, query string
			if len(args) >= 2 {
				ds, query = args[0], strings.Join(args[1:], " ")
			} else {
				d, err := dsArg(nil)
				if err != nil {
					return err
				}
				ds, query = d, strings.Join(args, " ")
			}
```

`metrics.go` — `Args: cobra.RangeArgs(1, 2)` → `Args: cobra.RangeArgs(0, 2)`; RunE top:

```go
			ds, err := dsArg(args[:1])
			if err != nil {
				return err
			}
			pattern := ""
			if len(args) > 1 {
				pattern = args[1]
			}
```

`labels.go` labels command — `RangeArgs(1,2)` → `RangeArgs(0,2)`; same shape as metrics. values command — `RangeArgs(2,3)` → `RangeArgs(1,3)`; RunE top:

```go
			ds, err := dsArg(args[:1])
			if err != nil {
				return err
			}
			label, metric := args[len(args)-2], ""
			if len(args) == 3 {
				label, metric = args[1], args[2]
			}
```

(adjust to the file's actual local variable names — read the file first and rename consistently.)

`introspect.go` tables — `ExactArgs(1)` → `RangeArgs(0,1)`; columns — `ExactArgs(2)` → `RangeArgs(1,2)` with `dsArg(args[:1])` and `table := args[len(args)-1]`.

- [ ] **Step 4: Run full suite, verify pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/run.go internal/cmd/query.go internal/cmd/prom.go internal/cmd/logs.go internal/cmd/sql.go internal/cmd/metrics.go internal/cmd/labels.go internal/cmd/introspect.go internal/cmd/prom_test.go
git commit -m "feat(gcli): profile defaultDatasource fills omitted datasource args"
```

---

### Task 9: Brand-neutrality scrub

**Files:**
- Modify: `testdata/datasources.json` (full anonymized rewrite)
- Modify: `internal/cmd/help.txt` (example URL)
- Modify: `internal/cmd/query.go` (example datasource names)
- Modify: `internal/api/integration_test.go` (comment)
- Modify: `docs/design-spec.md` (live-instance section)
- Modify: `docs/plan-v1.md`, `docs/plan-v2.md` (hostname replacement)

**Interfaces:** none — fixtures/docs only. Constraint: `make test` stays green (integration test asserts `len(dss) >= 10`).

- [ ] **Step 1: Rewrite `testdata/datasources.json`**

Replace file content wholesale with the anonymized 12-datasource catalog below (same schema, synthetic hosts/names, empty `user`, real UIDs kept — they are opaque identifiers):

```json
[{"id":917,"uid":"fc1ae488-f63a-4658-8dcc-94b70f4c15c8","orgId":1,"name":"PostgreSQL Metrics","type":"grafana-postgresql-datasource","typeName":"PostgreSQL","typeLogoUrl":"public/app/plugins/datasource/grafana-postgresql-datasource/img/postgresql_logo.svg","access":"proxy","url":"postgres.example.internal:5432","user":"","database":"","basicAuth":false,"isDefault":false,"jsonData":{"connMaxLifetime":14400,"database":"postgres","maxIdleConns":100,"maxIdleConnsAuto":true,"maxOpenConns":100,"sslmode":"disable"},"readOnly":false},{"id":920,"uid":"e3fe8133-062e-4b62-8e73-f2cabaaf9490","orgId":1,"name":"Metrics Alpha","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prometheus.example.com/select/0/prometheus","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":919,"uid":"b16dd6af-3380-474f-b06f-9dd6aea99b3a","orgId":1,"name":"Metrics Beta","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-beta.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":188,"uid":"dS5L03LMz","orgId":1,"name":"Metrics Gamma","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-gamma.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":23,"uid":"bGz5DFZMz","orgId":1,"name":"Metrics Delta","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-delta.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":701,"uid":"fb96c828-76e7-481a-ab63-68a289a7193c","orgId":1,"name":"Metrics Epsilon","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"http://vmselect.example.internal:8481","user":"","database":"","basicAuth":false,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":41,"uid":"sxmV-4OGk","orgId":1,"name":"Metrics Zeta","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-zeta.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST","tlsSkipVerify":false},"readOnly":false},{"id":910,"uid":"e5aa0a72-749e-4d3e-93fe-e4f4775a9aa8","orgId":1,"name":"Metrics Eta","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-eta.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":914,"uid":"e2495846-bcee-46e5-9aca-6f4f1cf231ee","orgId":1,"name":"Metrics Eta Mirror","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"https://prom-eta.example.com","user":"","database":"","basicAuth":true,"isDefault":false,"jsonData":{"httpMethod":"POST"},"readOnly":false},{"id":11,"uid":"03hIm2zGz","orgId":1,"name":"Metrics Iota","type":"prometheus","typeName":"Prometheus","typeLogoUrl":"public/app/plugins/datasource/prometheus/img/prometheus_logo.svg","access":"proxy","url":"http://vmselect.example.internal:8481/select/0/prometheus/","user":"","database":"","basicAuth":false,"isDefault":true,"jsonData":{"customQueryParameters":"time_out=30s","httpMethod":"POST","queryTimeout":"300s","timeInterval":"15s","timeout":300,"tlsSkipVerify":false},"readOnly":false},{"id":930,"uid":"cf3iebh4uz1fkc","orgId":1,"name":"Logs","type":"victoriametrics-logs-datasource","typeName":"VictoriaLogs","typeLogoUrl":"public/plugins/victoriametrics-logs-datasource/img/logo.svg","access":"proxy","url":"http://vlogs.example.internal:9471","user":"","database":"","basicAuth":false,"isDefault":false,"jsonData":{},"readOnly":false},{"id":770,"uid":"d0cb7a47-2972-4ab9-b932-d418ca682a82","orgId":1,"name":"Legacy Metrics","type":"victoriametrics-datasource","typeName":"victoriametrics-datasource","typeLogoUrl":"public/img/icn-datasource.svg","access":"proxy","url":"","user":"","database":"","basicAuth":false,"isDefault":false,"jsonData":{},"readOnly":false}]
```

- [ ] **Step 2: Scrub remaining files**

`internal/cmd/help.txt:33`:

```
   export GRAFANA_URL=https://grafana.example.com
```
→
```
   export GRAFANA_URL=https://grafana.example.com
```

`internal/cmd/query.go:53-54`:

```go
	Example: `  gcli query Metrics-Alpha --json '{"expr":"count(up)","instant":true}'
  gcli query PostgreSQL-Metrics --json @q.json --from now-24h`,
```

`internal/api/integration_test.go:17` comment:

```
// from grafana.example.com (2026-08-30) and asserts the client parses them.
```
→
```
// from a live Grafana instance (2026-08-30) and asserts the client parses them.
```

`docs/design-spec.md:8`:

```
Grafana **10.4.3** at `https://grafana.example.com/` (org "Acme", id 1). Service account token = read-only.
```
→
```
Grafana **10.4.3** at a company-internal instance. Service account token = read-only.
```

`docs/design-spec.md:130`:

```
- `testdata/` holds real anonymized API responses captured from grafana.example.com (captured 2026-08-30, real values scrubbed).
```
→
```
- `testdata/` holds real anonymized API responses captured from a live instance (captured 2026-08-30, real values scrubbed).
```

`docs/design-spec.md` datasource inventory table (lines 12-17): replace the 9 prometheus names list with `Metrics Alpha … Metrics Iota (anonymized in fixture)` and the postgres/vlogs names with `PostgreSQL Metrics`, `Logs`, `Legacy Metrics`.

`docs/plan-v1.md` and `docs/plan-v2.md` — sed replace every internal hostname:

```bash
sed -i '' \
  -e 's|internal-host-1|grafana.example.com|g' \
  -e 's|internal-host-2|prom-billing.example.com|g' \
  -e 's|internal-host-3|prom-beta.example.com|g' \
  -e 's|internal-host-4|prom-gamma.example.com|g' \
  -e 's|internal-host-5|prom-delta.example.com|g' \
  -e 's|internal-host-6|prom-zeta.example.com|g' \
  -e 's|internal-host-7|prom-eta.example.com|g' \
  -e 's|internal-host-8|vmselect.example.internal|g' \
  -e 's|internal-host-9|vmselect.example.internal|g' \
  -e 's|internal-host-10|vlogs.example.internal|g' \
  -e 's|internal-host-11|postgres.example.internal|g' \
  docs/plan-v1.md docs/plan-v2.md
```

Also replace datasource names in both plan docs: `Billing Victoriametrics`→`Metrics Alpha`, `Feynman`→`Metrics Beta`, `Kandoo`→`Metrics Gamma`, `Kubernetes' Prometheus`→`Metrics Delta`, `Prometheus Billing`→`Metrics Epsilon`, `Metrics Zeta`→`Metrics Zeta`, `example-cdn`→`Metrics Eta`, `example-cdn-acme-pay`→`Metrics Eta Mirror`, `Universal`→`Metrics Iota`, `Billing` (postgres context)→`PostgreSQL Metrics`, `Logs`→`Logs`, `VictoriaMetrics`→`Legacy Metrics` (careful: the type name `victoriametrics-datasource` must NOT be renamed — only the datasource NAME mentions).

- [ ] **Step 3: Verify — grep guard + full suite**

```bash
grep -rniE "cafebazaa[r]|bazaa[r]|sotoo[n]|roo\.cloud|cluster\.local|skube[l]|rasa[d]|cli[q]" --include='*.go' --include='*.json' --include='*.txt' --include='*.md' . | grep -v '\.git/' 
```
Expected: no output.

Run: `make test`
Expected: PASS (integration test still sees 12 datasources).

- [ ] **Step 4: Commit**

```bash
git add testdata/datasources.json internal/cmd/help.txt internal/cmd/query.go internal/api/integration_test.go docs/
git commit -m "docs(gcli): anonymize fixtures and docs — no company-specific names"
```

---

### Task 10: Docs — help guide, README, CLAUDE.md

**Files:**
- Modify: `internal/cmd/help.txt` (SETUP rewrite + GLOBAL FLAGS + EXIT CODES)
- Modify: `README.md` (profiles section)
- Modify: `CLAUDE.md` (invariants: deps, config story, new package/commands)
- Test: `internal/cmd/help_test.go` (if it asserts verbatim guide content, update expectations; check `TestHelp`)

**Interfaces:** none.

- [ ] **Step 1: Rewrite SETUP section in `internal/cmd/help.txt`**

Replace lines 25-38 (SETUP block) with:

```
SETUP
-----
1. Create a service-account token in Grafana:
   Grafana UI → Administration → Users and access → Service accounts
   → Add service account → Add token → copy the token (starts with glsa_).
   (Basic auth with a user/password also works.)

2. Add a profile (interactive, token prompt hidden):

   gcli profiles add prod

   Or non-interactively:
   gcli profiles add prod --url https://grafana.example.com --token glsa_...

   Profiles live in ~/.config/gcli/profiles.yaml (chmod 600).

3. Verify: gcli profiles test prod   (or: gcli capabilities — what your token can access)

Legacy env vars still work (GRAFANA_URL / GRAFANA_TOKEN) when no profile
is configured. Selection order: --profile flag > GCLI_PROFILE env >
default: in profiles.yaml > GRAFANA_URL/GRAFANA_TOKEN env.

PROFILES
--------
  gcli profiles add <name> [--url --token | --user --pass]
                              [--org-id 1] [--default-datasource <ds>] [--set-default]
  gcli profiles list                    list profiles (secrets never shown)
  gcli profiles use <name>              set default profile
  gcli profiles remove <name> [--force] delete a profile
  gcli profiles test [name]             smoke-test a profile (/api/health)
```

Also update GLOBAL FLAGS block — insert after `--token` line:

```
  --profile <name>   use profile from profiles.yaml
```

And EXIT CODES line 105:

```
1 config error (missing GRAFANA_URL / GRAFANA_TOKEN)
```
→
```
1 config error (no profile/URL/token resolvable)
```

- [ ] **Step 2: Update README.md**

Add after the existing setup section:

```markdown
## Profiles (multiple Grafana instances)

`gcli profiles add prod` (interactive) stores connections in
`~/.config/gcli/profiles.yaml` (chmod 600). Select with `--profile`,
`GCLI_PROFILE`, or the `default:` marker. Legacy `GRAFANA_URL` /
`GRAFANA_TOKEN` env vars keep working. See `gcli help` for the full guide.
```

- [ ] **Step 3: Update CLAUDE.md invariants**

- External deps line: `cobra only` → `cobra + golang.org/x/term + gopkg.in/yaml.v3`
- Layout: add `internal/profiles/` line — "profiles.yaml load/save/resolve (multi-instance config)"
- Config invariant: env config → "profiles.yaml first (`--profile` > `GCLI_PROFILE` > `default:` > `GRAFANA_URL`/`GRAFANA_TOKEN` env); `--url`/`--token` flags override"
- Command surface note: add `profiles` group to the layout bullet for `internal/cmd/`.

- [ ] **Step 4: Run suite**

Run: `make test && make vet`
Expected: PASS (help_test may need expectation updates if it checks SETUP text — update the fixture strings in the test, not the guide, if they disagree on content you intentionally changed).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/help.txt README.md CLAUDE.md internal/cmd/help_test.go
git commit -m "docs(gcli): profiles guide in help/README, invariants update"
```

---

## Final Verification (after Task 10)

```bash
make test
make vet
make build
grep -rniE "cafebazaa[r]|bazaa[r]|sotoo[n]|roo\.cloud|cluster\.local|skube[l]|rasa[d]|cli[q]" --include='*.go' --include='*.json' --include='*.txt' --include='*.md' . | grep -v '\.git/'
./gcli profiles add demo --url https://grafana.example.com --token glsa_demo --set-default
./gcli profiles list
./gcli profiles test demo   # expected: network error against example.com — proves resolution, not connectivity
```

## Plan Self-Review Notes

- Spec coverage: profiles subsystem (Tasks 2–3), command surface (Tasks 4–6), wiring (Task 7), defaultDatasource consumption (Task 8), brand scrub (Task 9), docs/invariants (Task 10), 0600 perms (Task 2 Save + Task 4 test), warning on world-readable (Task 2 + wired in add/list paths via `WarnIfWorldReadable` — called in `profiles add` only; acceptable per spec which asks for the warning on write paths).
- Type consistency: `ResolveOptions` fields used identically in Task 3 tests, Task 7 wiring; `flagProfile` declared in Task 4's root.go edit and consumed in Tasks 6–7; `flagProfForce` declared in Task 4, bound in Task 5; `loadProfilesFile` refactor note handled in Task 5.
- Known deviation: spec's "profiles list shows path" — list does not print the path (keep output clean); path appears in `add` confirmation and errors. Noted as intentional.
