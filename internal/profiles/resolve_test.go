// === Behavioral Contract: Resolve precedence ===
//
//	--profile > GCLI_PROFILE > default: > legacy env > error
//	--url/--token flags override profile values
//	missing file/profile produce actionable errors; legacy path = config.Load
//	a file that exists but fails to parse errors (no legacy fallthrough)
//	unknown/missing-profile errors wrap ErrNotFound
package profiles

import (
	"errors"
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
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound-wrapped", err)
	}
}

func TestResolve_unknownProfile_errorsListingNames(t *testing.T) {
	p := writeFile(t, t.TempDir(), twoProfiles)
	_, _, err := Resolve(ResolveOptions{FlagProfile: "nope", Path: p})
	if err == nil || !strings.Contains(err.Error(), "prod") || !strings.Contains(err.Error(), "dev") {
		t.Errorf("err = %v, want known profile names listed", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound-wrapped", err)
	}
}

func TestResolve_corruptFileWithNoName_errors(t *testing.T) {
	t.Setenv("GCLI_PROFILE", "")
	// Legacy env is populated to prove the corrupt file is NOT silently
	// bypassed: the error must win over the legacy fallthrough.
	t.Setenv("GRAFANA_URL", "https://legacy.example.com")
	t.Setenv("GRAFANA_TOKEN", "glsa_legacy")
	p := writeFile(t, t.TempDir(), "profiles: [unclosed\n")

	cfg, _, err := Resolve(ResolveOptions{Path: p})
	if err == nil {
		t.Fatalf("Resolve = %+v, want corrupt-file error (not legacy fallthrough)", cfg)
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want parse error surfaced", err)
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
