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
	URL               string
	Token             string
	User              string // basic auth user; mutually exclusive with Token
	Pass              string // basic auth pass; only with User
	OrgID             string // optional; sent as X-Grafana-Org-Id header
	DefaultDatasource string // optional; fallback when a command omits its datasource arg
	Timeout           time.Duration
	Output            string
	NoColor           bool
	Verbose           bool
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
