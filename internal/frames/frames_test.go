// === Behavioral Contract: frames.Normalize(raws) + Column.DisplayName() ===
//   - Every dataplane schema field becomes a column carrying that field's name and labels
//   - Column order follows field order
//   - Time-typed values (epoch milliseconds, any numeric JSON form) become UTC time.Time
//   - Non-time values pass through unchanged, including nil
//   - nil time values stay nil (no panic, no conversion)
//   - Fields with no matching values row produce an empty column (nil Values)
//   - RefID and Name propagate from the schema onto the frame
//   - DisplayName: bare name without labels; with labels, name{k=v,...} with
//     labels sorted alphabetically (deterministic regardless of map order)
package frames

import (
	"encoding/json"
	"testing"
	"time"
)

func rawFrameWith(fields []rawField, values [][]any, refID, name string) RawFrame {
	return RawFrame{
		Schema: rawSchema{RefID: refID, Name: name, Fields: fields},
		Data:   rawData{Values: values},
	}
}

func TestNormalize_prometheusVector_producesTimeAndLabeledValueColumns(t *testing.T) {
	raw := []RawFrame{rawFrameWith(
		[]rawField{
			{Name: "Time", Type: "time"},
			{Name: "Value", Type: "number", Labels: map[string]string{"job": "api", "instance": "pod-1"}},
		},
		[][]any{{float64(1788080730946)}, {float64(2173)}},
		"A", "",
	)}

	got := Normalize(raw)

	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1", len(got))
	}
	f := got[0]
	if f.RefID != "A" {
		t.Errorf("RefID = %q, want A", f.RefID)
	}
	if len(f.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(f.Columns))
	}
	ts, ok := f.Columns[0].Values[0].(time.Time)
	if !ok {
		t.Fatalf("Time value = %T, want time.Time", f.Columns[0].Values[0])
	}
	if ts.UTC().Format("2006-01-02T15:04:05Z") != "2026-08-30T09:05:30Z" {
		t.Errorf("time = %v, want 2026-08-30T09:05:30Z", ts)
	}
	if f.Columns[1].Values[0] != float64(2173) {
		t.Errorf("value = %v, want 2173", f.Columns[1].Values[0])
	}
	if f.Columns[1].Labels["job"] != "api" {
		t.Errorf("labels = %v", f.Columns[1].Labels)
	}
}

func TestNormalize_victoriaLogsStream_producesTimeLineAndLabelsColumns(t *testing.T) {
	raw := []RawFrame{rawFrameWith(
		[]rawField{
			{Name: "Time", Type: "time"},
			{Name: "Line", Type: "string"},
			{Name: "labels", Type: "other"},
		},
		[][]any{
			{float64(1788080773997), float64(1788080773996)},
			{"error: db down", "request ok"},
			{`{"app":"acme-pay"}`, `{"app":"acme-pay"}`},
		},
		"A", "",
	)}

	got := Normalize(raw)

	f := got[0]
	if len(f.Columns) != 3 {
		t.Fatalf("columns = %d, want 3", len(f.Columns))
	}
	if len(f.Columns[0].Values) != 2 || len(f.Columns[1].Values) != 2 {
		t.Fatalf("rows misaligned: %+v", f.Columns)
	}
	if f.Columns[1].Values[0] != "error: db down" {
		t.Errorf("line = %v", f.Columns[1].Values[0])
	}
	if _, ok := f.Columns[0].Values[0].(time.Time); !ok {
		t.Errorf("Time value not converted: %T", f.Columns[0].Values[0])
	}
	if _, ok := f.Columns[2].Values[0].(string); !ok {
		t.Errorf("labels value = %T, want raw string passthrough", f.Columns[2].Values[0])
	}
}

func TestNormalize_sqlTable_preservesTypedValues(t *testing.T) {
	raw := []RawFrame{rawFrameWith(
		[]rawField{{Name: "one", Type: "number"}},
		[][]any{{float64(1)}},
		"A", "",
	)}

	got := Normalize(raw)

	if got[0].Columns[0].Name != "one" || got[0].Columns[0].Values[0] != float64(1) {
		t.Errorf("got %+v", got)
	}
}

func TestNormalize_timeValues_acceptAllNumericJSONForms(t *testing.T) {
	raw := []RawFrame{rawFrameWith(
		[]rawField{
			{Name: "a", Type: "time"},
			{Name: "b", Type: "time"},
			{Name: "c", Type: "time"},
		},
		[][]any{
			{json.Number("1788080730946")},
			{int64(1788080730946)},
			{nil},
		},
		"A", "",
	)}

	got := Normalize(raw)

	want := time.UnixMilli(1788080730946).UTC()
	for i, col := range got[0].Columns[:2] {
		ts, ok := col.Values[0].(time.Time)
		if !ok || !ts.Equal(want) {
			t.Errorf("column %d = %v (%T), want %v", i, col.Values[0], col.Values[0], want)
		}
	}
	if got[0].Columns[2].Values[0] != nil {
		t.Errorf("nil time must stay nil, got %v", got[0].Columns[2].Values[0])
	}
}

func TestNormalize_fieldWithoutValues_producesEmptyColumn(t *testing.T) {
	raw := []RawFrame{rawFrameWith(
		[]rawField{{Name: "Time", Type: "time"}, {Name: "Value", Type: "number"}},
		[][]any{{float64(1)}},
		"A", "",
	)}

	got := Normalize(raw)

	if len(got[0].Columns) != 2 {
		t.Fatalf("columns = %d, want 2 (schema fields preserved)", len(got[0].Columns))
	}
	if got[0].Columns[1].Values != nil {
		t.Errorf("column without data = %v, want nil", got[0].Columns[1].Values)
	}
}

func TestNormalize_multipleFrames_preservesOrderAndNames(t *testing.T) {
	raw := []RawFrame{
		rawFrameWith([]rawField{{Name: "V", Type: "number"}}, [][]any{{float64(1)}}, "A", "rows"),
		rawFrameWith([]rawField{{Name: "V", Type: "number"}}, [][]any{{float64(2)}}, "B", ""),
	}

	got := Normalize(raw)

	if len(got) != 2 || got[0].RefID != "A" || got[1].RefID != "B" {
		t.Fatalf("frames = %+v", got)
	}
	if got[0].Name != "rows" {
		t.Errorf("frame name = %q, want rows", got[0].Name)
	}
}

func TestColumnDisplayName_withLabels_sortsLabelKeys(t *testing.T) {
	col := Column{
		Name:   "Value",
		Labels: map[string]string{"zeta": "1", "alpha": "2", "mid": "3"},
	}

	got := col.DisplayName()

	want := "Value{alpha=2,mid=3,zeta=1}"
	if got != want {
		t.Errorf("DisplayName = %q, want %q (labels sorted)", got, want)
	}
}

func TestColumnDisplayName_withoutLabels_returnsBareName(t *testing.T) {
	col := Column{Name: "one"}

	if got := col.DisplayName(); got != "one" {
		t.Errorf("DisplayName = %q, want bare name", got)
	}
}
