package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/behnambm/gcli/internal/config"
)

type Client struct {
	BaseURL    string
	HTTP       *http.Client
	AuthHeader string   // precomputed "Bearer x" or "Basic base64"
	Secrets    []string // redacted from verbose dumps
	OrgID      string   // optional; sent as X-Grafana-Org-Id
	Verbose    bool
	LogW       io.Writer // verbose dumps; nil = io.Discard
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
		c.Secrets = []string{cfg.Pass, c.AuthHeader}
	}
	return c
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
	req.Header.Set("Authorization", c.AuthHeader)
	if c.OrgID != "" {
		req.Header.Set("X-Grafana-Org-Id", c.OrgID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Verbose {
		dump, _ := httputil.DumpRequestOut(req, true)
		fmt.Fprintf(c.LogW, "--- request ---\n%s\n", redact(string(dump), c.Secrets...))
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
		fmt.Fprintf(c.LogW, "--- response %d ---\n%s\n", resp.StatusCode, redact(string(respBody), c.Secrets...))
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

func redact(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "REDACTED")
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
