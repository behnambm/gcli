package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ProxyGet performs a read-only GET through the Grafana datasource proxy
// (path is appended to /api/datasources/proxy/uid/<uid>) and returns the
// raw JSON response body.
func (c *Client) ProxyGet(ctx context.Context, dsUID, path string, params url.Values) (json.RawMessage, error) {
	q := ""
	if len(params) > 0 {
		q = "?" + params.Encode()
	}
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/datasources/proxy/uid/"+url.PathEscape(dsUID)+path+q, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ParseLabelNames parses the Prometheus-compatible label API envelope:
// {"status":"success","data":["a","b"]}.
func ParseLabelNames(raw json.RawMessage) ([]string, error) {
	var env struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse label API response: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("label API returned status %q", env.Status)
	}
	return env.Data, nil
}
