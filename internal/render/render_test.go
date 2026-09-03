// === Behavioral Contract: render.Render(w, res, opts) + render.Colorize ===
//   - "table": header row = DisplayName per column; one row per value row;
//     times in local "2006-01-02 15:04:05"; nil as "null"; floats compact;
//     long strings truncated with "…"-style ellipsis; multi-frame results
//     carry a frame header per frame
//   - "json": one indented JSON doc of {meta, frames} with columnar values
//     (time.Time RFC3339)
//   - "csv": RFC-4180 quoting via encoding/csv; header = DisplayName; the
//     FrameIdx-th frame is selected; out-of-range (negative or beyond)
//     returns an error instead of panicking
//   - Unknown output format returns an error
//   - Colorize: colors red/green/yellow with ANSI codes when enabled,
//     returns input unchanged when disabled or for unknown colors
package render

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behnambm/gcli/internal/frames"
)

func sampleResult() frames.Result {
	return frames.Result{
		Meta: frames.Meta{Datasource: "prometheus", Query: "count(up)", From: "now-5m", To: "now", DurationMS: 42},
		Frames: []frames.Frame{{
			RefID: "A",
			Columns: []frames.Column{
				{Name: "Time", Values: []any{time.Date(2026, 8, 30, 9, 5, 30, 0, time.UTC)}},
				{Name: "Value", Labels: map[string]string{"job": "api"}, Values: []any{float64(2173)}},
			},
		}},
	}
}

func TestRender_table_printsHeadersValuesAndLabeledNames(t *testing.T) {
	var b strings.Builder

	err := Render(&b, sampleResult(), Options{Output: "table"})

	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	out := b.String()
	for _, want := range []string{"Time", "Value{job=api}", "2026-08-30", "2173"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRender_table_formatsNilFloatAndLongStrings(t *testing.T) {
	long := strings.Repeat("x", 60)
	res := frames.Result{
		Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
			{Name: "Nil", Values: []any{nil}},
			{Name: "F", Values: []any{float64(0.5)}},
			{Name: "S", Values: []any{long}},
		}}},
	}
	var b strings.Builder

	if err := Render(&b, res, Options{Output: "table"}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "null") {
		t.Errorf("nil must render as null:\n%s", out)
	}
	if !strings.Contains(out, "0.5") {
		t.Errorf("float must render compact:\n%s", out)
	}
	if strings.Contains(out, long) {
		t.Errorf("long string must be truncated:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("x", 40)+"...") {
		t.Errorf("truncated string missing:\n%s", out)
	}
}

func TestRender_table_multipleFrames_labelsEachFrame(t *testing.T) {
	res := frames.Result{
		Frames: []frames.Frame{
			{RefID: "A", Name: "rows", Columns: []frames.Column{{Name: "V", Values: []any{float64(1)}}}},
			{RefID: "B", Columns: []frames.Column{{Name: "V", Values: []any{float64(2)}}}},
		},
	}
	var b strings.Builder

	if err := Render(&b, res, Options{Output: "table"}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"frames: 2", "Frame 0: rows (refId A)", "Frame 1:  (refId B)"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-frame output missing %q:\n%s", want, out)
		}
	}
}

func TestRender_json_emitsMetaAndFramesDocument(t *testing.T) {
	var b strings.Builder

	err := Render(&b, sampleResult(), Options{Output: "json"})

	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	var doc struct {
		Meta struct {
			Datasource string `json:"datasource"`
			Query      string `json:"query"`
			From       string `json:"from"`
			To         string `json:"to"`
			DurationMS int64  `json:"duration_ms"`
		} `json:"meta"`
		Frames []struct {
			RefID   string `json:"refId"`
			Columns []struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
				Values []any             `json:"values"`
			} `json:"columns"`
		} `json:"frames"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if doc.Meta.Datasource != "prometheus" || doc.Meta.Query != "count(up)" || doc.Meta.DurationMS != 42 {
		t.Errorf("meta = %+v", doc.Meta)
	}
	if len(doc.Frames) != 1 || doc.Frames[0].RefID != "A" {
		t.Errorf("frames = %+v", doc.Frames)
	}
	if doc.Frames[0].Columns[1].Labels["job"] != "api" {
		t.Errorf("labels lost in JSON: %+v", doc.Frames[0].Columns[1])
	}
}

func TestRender_csv_writesHeaderAndRowsForSelectedFrame(t *testing.T) {
	var b strings.Builder

	err := Render(&b, sampleResult(), Options{Output: "csv", FrameIdx: 0})

	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("csv rows = %d, want header + 1 data row: %v", len(records), records)
	}
	if records[0][0] != "Time" || records[0][1] != "Value{job=api}" {
		t.Errorf("csv header = %v", records[0])
	}
	if !strings.Contains(records[1][1], "2173") {
		t.Errorf("csv data = %v", records[1])
	}
}

func TestRender_csv_quotesValuesContainingSeparators(t *testing.T) {
	res := frames.Result{
		Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
			{Name: "S", Values: []any{"a,b", `say "hi"`}},
		}}},
	}
	var b strings.Builder

	if err := Render(&b, res, Options{Output: "csv"}); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("quoted values must round-trip through a CSV reader: %v", err)
	}
	if records[1][0] != "a,b" || records[2][0] != `say "hi"` {
		t.Errorf("quoted values corrupted: %v", records)
	}
}

func TestRender_csv_selectsFrameByIndex(t *testing.T) {
	res := frames.Result{
		Frames: []frames.Frame{
			{RefID: "A", Columns: []frames.Column{{Name: "V", Values: []any{float64(1)}}}},
			{RefID: "B", Columns: []frames.Column{{Name: "V", Values: []any{float64(2)}}}},
		},
	}
	var b strings.Builder

	if err := Render(&b, res, Options{Output: "csv", FrameIdx: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "2") || strings.Contains(b.String(), "1\n") {
		t.Errorf("csv output = %q, want only second frame", b.String())
	}
}

func TestRender_csv_outOfRangeFrameIdx_returnsError(t *testing.T) {
	var b strings.Builder

	for _, idx := range []int{-1, 5} {
		err := Render(&b, sampleResult(), Options{Output: "csv", FrameIdx: idx})
		if err == nil {
			t.Errorf("FrameIdx %d: want error, got nil", idx)
		}
	}
}

func TestRender_unknownOutput_returnsError(t *testing.T) {
	var b strings.Builder

	err := Render(&b, sampleResult(), Options{Output: "yaml"})

	if err == nil {
		t.Fatal("want error for unknown output format")
	}
}

func TestColorize_disabledOrUnknown_returnsInputUnchanged(t *testing.T) {
	for _, tc := range []struct {
		s, color string
		enabled  bool
	}{
		{"Alerting", "red", false},
		{"Normal", "green", false},
		{"Pending", "yellow", false},
		{"x", "mauve", true},
	} {
		if got := Colorize(tc.s, tc.color, tc.enabled); got != tc.s {
			t.Errorf("Colorize(%q, %q, %v) = %q, want input unchanged", tc.s, tc.color, tc.enabled, got)
		}
	}
}

func TestColorize_enabled_wrapsWithAnsiCodes(t *testing.T) {
	cases := map[string]string{"red": "\x1b[31m", "green": "\x1b[32m", "yellow": "\x1b[33m"}
	for color, prefix := range cases {
		got := Colorize("Alerting", color, true)
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, "\x1b[0m") {
			t.Errorf("Colorize(_, %q, true) = %q, want %q...%q", color, got, prefix, "\x1b[0m")
		}
	}
}
