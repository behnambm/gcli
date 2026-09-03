package frames

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Column struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Values []any             `json:"values"`
}

type Frame struct {
	RefID   string   `json:"refId"`
	Name    string   `json:"name,omitempty"`
	Columns []Column `json:"columns"`
}

type Meta struct {
	Datasource string `json:"datasource"`
	Query      string `json:"query,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	DurationMS int64  `json:"duration_ms"`
}

type Result struct {
	Meta   Meta    `json:"meta"`
	Frames []Frame `json:"frames"`
}

// RawFrame mirrors the Grafana dataplane frame JSON.
type RawFrame struct {
	Schema rawSchema `json:"schema"`
	Data   rawData   `json:"data"`
}

type rawSchema struct {
	RefID  string     `json:"refId"`
	Name   string     `json:"name"`
	Fields []rawField `json:"fields"`
}

type rawField struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Type   string            `json:"type"`
}

type rawData struct {
	Values [][]any `json:"values"`
}

func Normalize(raws []RawFrame) []Frame {
	out := make([]Frame, 0, len(raws))
	for _, r := range raws {
		f := Frame{RefID: r.Schema.RefID, Name: r.Schema.Name}
		for i, fld := range r.Schema.Fields {
			col := Column{Name: fld.Name, Labels: fld.Labels}
			if i < len(r.Data.Values) {
				col.Values = normalizeValues(r.Data.Values[i], fld.Type)
			}
			f.Columns = append(f.Columns, col)
		}
		out = append(out, f)
	}
	return out
}

func normalizeValues(vals []any, typ string) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
		if typ == "time" && v != nil {
			if ms, ok := toFloat64(v); ok {
				out[i] = time.UnixMilli(int64(ms)).UTC()
			}
		}
	}
	return out
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func (c Column) DisplayName() string {
	if len(c.Labels) == 0 {
		return c.Name
	}
	keys := make([]string, 0, len(c.Labels))
	for k := range c.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, c.Labels[k]))
	}
	return fmt.Sprintf("%s{%s}", c.Name, strings.Join(parts, ","))
}
