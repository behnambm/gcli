package api

import (
	"context"
	"errors"
	"fmt"
)

type Capability struct {
	Group  string
	Status string // OK, DENIED, ERROR
	Detail string
}

func (c *Client) Capabilities(ctx context.Context) ([]Capability, error) {
	var caps []Capability

	probe := func(group string, fn func() error) {
		err := fn()
		cp := Capability{Group: group, Status: "OK"}
		if err != nil {
			var he *HTTPError
			if errors.As(err, &he) && (he.StatusCode == 403 || he.StatusCode == 404) {
				cp.Status = "DENIED"
				cp.Detail = fmt.Sprintf("HTTP %d — %s", he.StatusCode, he.Hint())
			} else {
				cp.Status = "ERROR"
				cp.Detail = err.Error()
			}
		}
		caps = append(caps, cp)
	}

	probe("auth", func() error {
		var org map[string]any
		return c.Get(ctx, "/api/org", &org)
	})
	probe("datasources", func() error {
		_, err := c.Datasources(ctx)
		return err
	})
	probe("query", func() error {
		dss, err := c.Datasources(ctx)
		if err != nil {
			return err
		}
		for _, ds := range dss {
			if ds.Type == "prometheus" {
				_, err := c.DSQuery(ctx, "prometheus", []DSQueryReq{{
					RefID: "A", Datasource: DatasourceRef{Type: "prometheus", UID: ds.UID},
					Body: map[string]any{"expr": "1", "instant": true},
				}}, "now-5m", "now")
				return err
			}
		}
		return fmt.Errorf("no prometheus-type datasource visible to this token")
	})
	probe("dashboards", func() error {
		_, err := c.SearchDashboards(ctx, "")
		return err
	})
	probe("alerts", func() error {
		_, err := c.Alerts(ctx)
		return err
	})
	probe("annotations", func() error {
		_, err := c.Annotations(ctx, 0, 1, nil, "")
		return err
	})
	probe("datasource-health", func() error {
		dss, err := c.Datasources(ctx)
		if err != nil {
			return err
		}
		if len(dss) == 0 {
			return fmt.Errorf("no datasources visible")
		}
		var hres map[string]any
		return c.Get(ctx, "/api/datasources/uid/"+dss[0].UID+"/health", &hres)
	})
	return caps, nil
}
