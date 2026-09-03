package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
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

// DatasourcesRaw returns the full /api/datasources payload untouched.
func (c *Client) DatasourcesRaw(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/datasources", &raw); err != nil {
		return nil, err
	}
	return raw, nil
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

type Annotation struct {
	ID        int64    `json:"id"`
	AlertID   int64    `json:"alertId"`
	AlertName string   `json:"alertName"`
	Login     string   `json:"login"`
	TimeMS    int64    `json:"time"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags"`
	NewState  string   `json:"newState"`
	PrevState string   `json:"prevState"`
	Source    string   `json:"-"` // derived: login → alertName → "-"
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
	for i := range anns {
		switch {
		case anns[i].Login != "":
			anns[i].Source = anns[i].Login
		case anns[i].AlertID != 0 && anns[i].AlertName != "":
			anns[i].Source = "alert: " + anns[i].AlertName
		case anns[i].AlertID != 0:
			anns[i].Source = "alert"
		default:
			anns[i].Source = "-"
		}
	}
	return anns, nil
}

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

type PanelInfo struct {
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Datasource string   `json:"datasource"`
	Queries    []string `json:"queries"`
}

type dashboardModel struct {
	Dashboard struct {
		Panels []rawPanel `json:"panels"`
	} `json:"dashboard"`
}

type rawPanel struct {
	Title      string            `json:"title"`
	Type       string            `json:"type"`
	Datasource json.RawMessage   `json:"datasource"`
	Targets    []json.RawMessage `json:"targets"`
	Panels     []rawPanel        `json:"panels"`
}

// Panels flattens a dashboard's panels (recurse into rows) and extracts
// each panel's datasource and query expressions.
func (c *Client) Panels(ctx context.Context, dashUID string) ([]PanelInfo, error) {
	raw, err := c.DashboardJSON(ctx, dashUID)
	if err != nil {
		return nil, err
	}
	var m dashboardModel
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse dashboard JSON: %w", err)
	}
	var out []PanelInfo
	flattenPanels(m.Dashboard.Panels, &out)
	return out, nil
}

func flattenPanels(panels []rawPanel, out *[]PanelInfo) {
	for _, p := range panels {
		if p.Type == "row" {
			flattenPanels(p.Panels, out)
			continue
		}
		pi := PanelInfo{Title: p.Title, Type: p.Type, Datasource: datasourceString(p.Datasource)}
		for _, t := range p.Targets {
			var tgt struct {
				Expr       string          `json:"expr"`
				Datasource json.RawMessage `json:"datasource"`
			}
			if err := json.Unmarshal(t, &tgt); err != nil {
				continue
			}
			if pi.Datasource == "" {
				pi.Datasource = datasourceString(tgt.Datasource)
			}
			if tgt.Expr != "" {
				pi.Queries = append(pi.Queries, tgt.Expr)
			}
		}
		*out = append(*out, pi)
	}
}

// datasourceString renders a Grafana datasource ref (string form "type",
// or object form {"type":..,"uid":..}) as "type" or "type (uid)".
func datasourceString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var o struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.Type == "" {
		return ""
	}
	if o.UID != "" {
		return o.Type + " (" + o.UID + ")"
	}
	return o.Type
}
