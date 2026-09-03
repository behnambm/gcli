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
