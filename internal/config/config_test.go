// === Behavioral Contract: config.Load(flagURL, flagToken, timeout, output, noColor, verbose) ===
// - Reads GRAFANA_URL and GRAFANA_TOKEN from the environment when flags are empty
// - Command-line flags override environment values
// - Trailing slashes are trimmed from the URL
// - Missing URL yields an error that errors.Is matches ErrMissingURL
// - Missing token yields an error that errors.Is matches ErrMissingToken
// - Empty output defaults to "table"
// - Output outside table|json|csv is rejected with an error
// - Timeout, NoColor, and Verbose pass through unchanged
package config

import (
	"errors"
	"testing"
	"time"
)

func TestLoad_withEnvVarsSet_populatesConfig(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://grafana.example.com/")
	t.Setenv("GRAFANA_TOKEN", "glsa_secret")

	cfg, err := Load("", "", 30*time.Second, "", false, false)

	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.URL != "https://grafana.example.com" {
		t.Errorf("URL = %q, want trailing slash trimmed", cfg.URL)
	}
	if cfg.Token != "glsa_secret" {
		t.Errorf("Token = %q, want value from environment", cfg.Token)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want pass-through value", cfg.Timeout)
	}
	if cfg.Output != "table" {
		t.Errorf("Output = %q, want default \"table\"", cfg.Output)
	}
	if cfg.NoColor || cfg.Verbose {
		t.Errorf("NoColor=%v Verbose=%v, want false defaults", cfg.NoColor, cfg.Verbose)
	}
}

func TestLoad_withFlags_overridesEnvironment(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://env.example.com")
	t.Setenv("GRAFANA_TOKEN", "env-token")

	cfg, err := Load("https://flag.example.com", "flag-token", 0, "json", true, true)

	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.URL != "https://flag.example.com" {
		t.Errorf("URL = %q, want flag value", cfg.URL)
	}
	if cfg.Token != "flag-token" {
		t.Errorf("Token = %q, want flag value", cfg.Token)
	}
	if cfg.Output != "json" {
		t.Errorf("Output = %q, want flag value", cfg.Output)
	}
	if !cfg.NoColor || !cfg.Verbose {
		t.Errorf("NoColor=%v Verbose=%v, want true from flags", cfg.NoColor, cfg.Verbose)
	}
}

func TestLoad_withMissingURL_returnsErrMissingURL(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "t")

	_, err := Load("", "", 0, "", false, false)

	if !errors.Is(err, ErrMissingURL) {
		t.Fatalf("err = %v, want ErrMissingURL", err)
	}
}

func TestLoad_withMissingToken_returnsErrMissingToken(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://x.example.com")
	t.Setenv("GRAFANA_TOKEN", "")

	_, err := Load("", "", 0, "", false, false)

	if !errors.Is(err, ErrMissingToken) {
		t.Fatalf("err = %v, want ErrMissingToken", err)
	}
}

func TestLoad_withMissingBoth_reportsURLFirst(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	_, err := Load("", "", 0, "", false, false)

	if !errors.Is(err, ErrMissingURL) {
		t.Fatalf("err = %v, want ErrMissingURL to win when both are missing", err)
	}
}

func TestLoad_withUnsupportedOutput_returnsError(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://x.example.com")
	t.Setenv("GRAFANA_TOKEN", "t")

	_, err := Load("", "", 0, "yaml", false, false)

	if err == nil {
		t.Fatal("want error for output=yaml")
	}
}

func TestLoad_withFlagURLOnly_stillNeedsTokenFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "env-token")

	cfg, err := Load("https://flag.example.com", "", 0, "", false, false)

	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.URL != "https://flag.example.com" || cfg.Token != "env-token" {
		t.Errorf("cfg = %+v, want mixed flag+env", cfg)
	}
}
