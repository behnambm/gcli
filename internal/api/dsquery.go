package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/behnambm/gcli/internal/frames"
)

type DatasourceRef struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type DSQueryReq struct {
	RefID      string
	Datasource DatasourceRef
	Body       map[string]any
}

type QueryError struct {
	RefID  string
	Source string
	Msg    string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("query %s failed (%s): %s", e.RefID, e.Source, e.Msg)
}

// DSQuery runs queries against /api/ds/query and returns normalized frames.
// On per-refId failures it returns the successful frames plus the joined
// QueryErrors — callers render what succeeded and surface the error.
func (c *Client) DSQuery(ctx context.Context, dsType string, queries []DSQueryReq, from, to string) (frames.Result, error) {
	payload := struct {
		Queries []map[string]any `json:"queries"`
		From    string           `json:"from"`
		To      string           `json:"to"`
	}{From: from, To: to}
	for _, q := range queries {
		m := map[string]any{}
		for k, v := range q.Body {
			m[k] = v
		}
		m["refId"] = q.RefID
		m["datasource"] = q.Datasource
		payload.Queries = append(payload.Queries, m)
	}

	start := time.Now()
	var env struct {
		Results map[string]struct {
			Status      int               `json:"status"`
			Error       string            `json:"error"`
			ErrorSource string            `json:"errorSource"`
			Frames      []frames.RawFrame `json:"frames"`
		} `json:"results"`
	}
	err := c.Post(ctx, "/api/ds/query?ds_type="+dsType, payload, &env)
	if err != nil {
		return frames.Result{}, err
	}

	elapsed := time.Since(start).Milliseconds()
	if elapsed <= 0 {
		elapsed = 1
	}
	res := frames.Result{Meta: frames.Meta{
		Datasource: dsType,
		From:       from,
		To:         to,
		DurationMS: elapsed,
	}}
	var errs []error
	for _, q := range queries {
		r, ok := env.Results[q.RefID]
		if !ok {
			continue
		}
		if r.Error != "" {
			errs = append(errs, &QueryError{RefID: q.RefID, Source: r.ErrorSource, Msg: r.Error})
			continue
		}
		res.Frames = append(res.Frames, frames.Normalize(r.Frames)...)
	}
	return res, errors.Join(errs...)
}
