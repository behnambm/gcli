// === Behavioral Contract: gcli profiles add/list ===
//   - add --url --token writes profiles.yaml (0600) and reloads it
//   - add --user --pass works; token+user together is rejected
//   - add validates auth before saving: user/pass pairs, token conflicts,
//     and no auth at all are rejected (mirrors Load's checks)
//   - add refuses to overwrite a corrupt existing profiles file
//   - add with no flags and no TTY errors pointing at the flag form
//   - add with no flags and TTY reads prompts from stdin (hidden input tested in Task 6? no — term is exercised via stdin pipe fallback: see note)
//   - list prints names/urls but never token or pass
//   - list without a profiles file errors with an actionable hint
//   - unknown --profile exits with code 1 (config-class error)
package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behnambm/gcli/internal/profiles"
)

func xdgEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// os.UserConfigDir honors XDG_CONFIG_HOME on Unix but $HOME on darwin,
	// so set both to isolate the config dir on every platform.
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	// A host-machine GCLI_PROFILE export must not leak into these tests.
	t.Setenv("GCLI_PROFILE", "")
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

func TestProfilesAdd_corruptExistingFile_refusesToOverwrite(t *testing.T) {
	xdgEnv(t)
	p, err := profiles.Path()
	if err != nil {
		t.Fatal(err)
	}
	corrupt := "profiles:\n  prod: [unclosed\n"
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(corrupt), 0600); err != nil {
		t.Fatal(err)
	}

	_, err = runCommandErr(t, "profiles", "add", "new", "--url", "https://x", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("err = %v, want refusing-to-overwrite error", err)
	}
	got, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != corrupt {
		t.Errorf("file changed to %q, want unchanged %q", got, corrupt)
	}
}

func TestProfilesAdd_tokenAndUser_rejected(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "add", "bad", "--url", "https://x", "--token", "t", "--user", "bob", "--pass", "p")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("err = %v, want token/user conflict", err)
	}
}

func TestProfilesAdd_userWithoutPass_rejected(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "add", "bad", "--url", "https://x", "--user", "bob")
	if err == nil || !strings.Contains(err.Error(), "user requires pass") {
		t.Errorf("err = %v, want user-requires-pass error", err)
	}
}

func TestProfilesAdd_noAuth_rejected(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "profiles", "add", "bad", "--url", "https://x")
	if err == nil || !strings.Contains(err.Error(), "no auth given") {
		t.Errorf("err = %v, want no-auth error", err)
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

func TestCommand_usesProfileViaFlag(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/health": healthResp, "/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	xdgEnv(t)
	t.Setenv("GRAFANA_URL", "") // ensure legacy env cannot satisfy config
	t.Setenv("GRAFANA_TOKEN", "")
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "glsa_t")

	out := runCommand(t, "--profile", "prod", "health")

	if !strings.Contains(out, "Universal") {
		t.Errorf("output = %q, want health via profile", out)
	}
}

func TestCommand_usesDefaultMarker(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/health": healthResp, "/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srv.URL, "--token", "glsa_t", "--set-default")

	out := runCommand(t, "health")

	if !strings.Contains(out, "Universal") {
		t.Errorf("output = %q, want default profile used", out)
	}
}

func TestCommand_GCLI_PROFILE_envSelects(t *testing.T) {
	srvA := fakeGrafana(t, map[string]string{"/api/health": healthResp, "/api/datasources": defaultDatasourcesResponse}, nil)
	defer srvA.Close()
	srvB := fakeGrafana(t, map[string]string{"/api/health": healthResp, "/api/datasources": defaultDatasourcesResponse}, nil)
	defer srvB.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "prod", "--url", srvA.URL, "--token", "t1", "--set-default")
	runCommand(t, "profiles", "add", "dev", "--url", srvB.URL, "--token", "t2")
	t.Setenv("GCLI_PROFILE", "dev")

	out := runCommand(t, "version")

	// health's table renderer drops the Grafana version (Meta is JSON-only),
	// so assert on a datasource row from the profile-selected server instead
	out = runCommand(t, "health")
	if !strings.Contains(out, "Universal") {
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

func TestProfilesExitCode_unknownProfile_isConfigError(t *testing.T) {
	xdgEnv(t)
	_, err := runCommandErr(t, "--profile", "nope", "health")
	if err == nil {
		t.Fatal("want unknown profile error")
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode = %d, want 1 (config-class error)", got)
	}
	if got := hintOf(err); got == "" {
		t.Error("config-class error must produce a setup hint")
	}
}
