package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/behnambm/gcli/internal/frames"
)

type Options struct {
	Output   string
	NoColor  bool
	Color    bool // ANSI allowed (TTY + !NoColor)
	FrameIdx int
}

const (
	cellWidth  = 40
	timeLayout = "2006-01-02 15:04:05"
	jsonNull   = "null"
)

func Render(w io.Writer, res frames.Result, opts Options) error {
	switch opts.Output {
	case "table":
		return renderTable(w, res, opts)
	case "json":
		return renderJSON(w, res)
	case "csv":
		return renderCSV(w, res, opts.FrameIdx)
	default:
		return fmt.Errorf("invalid output %q", opts.Output)
	}
}

func renderTable(w io.Writer, res frames.Result, opts Options) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	if len(res.Frames) > 1 {
		fmt.Fprintf(tw, "frames: %d\t\n\n", len(res.Frames))
	}
	for i, f := range res.Frames {
		if len(res.Frames) > 1 {
			fmt.Fprintf(tw, "Frame %d: %s (refId %s)\t\n", i, f.Name, f.RefID)
		}
		headers := make([]string, len(f.Columns))
		for j, c := range f.Columns {
			headers[j] = c.DisplayName()
		}
		fmt.Fprintln(tw, strings.Join(headers, "\t"))
		rowCount := 0
		for _, c := range f.Columns {
			if len(c.Values) > rowCount {
				rowCount = len(c.Values)
			}
		}
		for r := 0; r < rowCount; r++ {
			cells := make([]string, len(f.Columns))
			for j, c := range f.Columns {
				if r < len(c.Values) {
					cells[j] = formatCell(c.Values[r])
				} else {
					cells[j] = ""
				}
			}
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if i < len(res.Frames)-1 {
			fmt.Fprintln(tw)
		}
	}
	return tw.Flush()
}

func formatCell(v any) string {
	switch t := v.(type) {
	case nil:
		return jsonNull
	case time.Time:
		return t.Local().Format(timeLayout)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return truncateRunes(t, cellWidth)
	default:
		b, _ := json.Marshal(t)
		return truncateRunes(string(b), cellWidth)
	}
}

func renderJSON(w io.Writer, res frames.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func renderCSV(w io.Writer, res frames.Result, frameIdx int) error {
	if frameIdx < 0 || frameIdx >= len(res.Frames) {
		return fmt.Errorf("frame %d out of range: result has %d frames", frameIdx, len(res.Frames))
	}
	f := res.Frames[frameIdx]
	cw := csv.NewWriter(w)
	headers := make([]string, len(f.Columns))
	for j, c := range f.Columns {
		headers[j] = c.DisplayName()
	}
	if err := cw.Write(headers); err != nil {
		return err
	}
	rowCount := 0
	for _, c := range f.Columns {
		if len(c.Values) > rowCount {
			rowCount = len(c.Values)
		}
	}
	for r := 0; r < rowCount; r++ {
		row := make([]string, len(f.Columns))
		for j, c := range f.Columns {
			if r < len(c.Values) {
				row[j] = formatCell(c.Values[r])
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func Colorize(s, color string, enabled bool) string {
	if !enabled {
		return s
	}
	var code string
	switch color {
	case "red":
		code = "31"
	case "green":
		code = "32"
	case "yellow":
		code = "33"
	default:
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
