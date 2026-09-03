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
	if !isHidden(err3) {
		// a real failure (500/network) must not be masked as DENIED downstream
		return nil, err3
	}
	return nil, fmt.Errorf("no alerts endpoint accessible: rules %w; v2 statuses %w; legacy %w", err, err2, err3)
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
	State  string      `json:"state"`
	Name   string      `json:"name"`
	Alerts []ruleAlert `json:"alerts"`
}
type ruleAlert struct {
	Labels   map[string]string `json:"labels"`
	State    string            `json:"state"`
	ActiveAt string            `json:"activeAt"`
	Value    string            `json:"value"`
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
		rows = append(rows, AlertRow{Name: a.Name, State: state, Core: legacyCore(state)})
	}
	return rows, nil
}

// legacyCore maps Grafana <9 legacy lowercase states to the canonical Core
// values ("Alerting"/"Pending"/"NoData"/"Error") so --firing and color coding
// work uniformly across the fallback chain.
func legacyCore(state string) string {
	switch state {
	case "ok", "paused":
		return "Normal"
	case "alerting":
		return "Alerting"
	case "pending":
		return "Pending"
	case "no_data":
		return "NoData"
	case "unknown":
		return "Error"
	default:
		return state
	}
}

// coreState strips " (Error)" / " (NoData)" suffixes: "Normal (Error)" → "Normal".
func coreState(s string) string {
	if i := strings.Index(s, " ("); i > 0 {
		return s[:i]
	}
	return s
}

type AlertDetail struct {
	Name        string            `json:"name"`
	Folder      string            `json:"folder"`
	Expr        string            `json:"expr"`
	For         string            `json:"for"`
	Severity    string            `json:"severity"`
	Annotations map[string]string `json:"annotations,omitempty"`
	State       string            `json:"state,omitempty"`
	ActiveAt    string            `json:"activeAt,omitempty"`
}

type rulerGroup struct {
	Name  string      `json:"name"`
	Rules []rulerRule `json:"rules"`
}
type rulerRule struct {
	Expr         string            `json:"expr"`
	For          string            `json:"for"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	GrafanaAlert struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"grafana_alert"`
}

// AlertDetail returns one alert's rule definition (ruler endpoint) merged
// with its current state (rules endpoint). Name matches the rule title or
// the alertname label, case-insensitively.
func (c *Client) AlertDetail(ctx context.Context, name string) (AlertDetail, error) {
	var env map[string][]rulerGroup
	if err := c.Get(ctx, "/api/ruler/grafana/api/v1/rules", &env); err != nil {
		return AlertDetail{}, err
	}
	var d AlertDetail
	for folder, groups := range env {
		for _, g := range groups {
			for _, r := range g.Rules {
				if !strings.EqualFold(r.GrafanaAlert.Title, name) && !strings.EqualFold(r.Labels["alertname"], name) {
					continue
				}
				d = AlertDetail{
					Name:        r.GrafanaAlert.Title,
					Folder:      folder,
					Expr:        r.Expr,
					For:         r.For,
					Severity:    r.Labels["severity"],
					Annotations: r.Annotations,
				}
				if d.Name == "" {
					d.Name = r.Labels["alertname"]
				}
				// merge current state
				rows, aerr := c.Alerts(ctx)
				if aerr == nil {
					for _, row := range rows {
						if strings.EqualFold(row.Name, d.Name) {
							d.State = row.State
							d.ActiveAt = row.ActiveAt
							break
						}
					}
				}
				return d, nil
			}
		}
	}
	return AlertDetail{}, fmt.Errorf("alert %q not found", name)
}
