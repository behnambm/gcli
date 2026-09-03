package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseToEpochMS parses Grafana-style times ("now", "now-1h", "now-1d/d",
// "now/w", RFC3339 absolute) into epoch milliseconds.
func ParseToEpochMS(s string, now time.Time) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty time string")
	}
	if s == "now" {
		return now.UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), nil
	}
	if strings.HasPrefix(s, "now/") {
		unit := strings.TrimPrefix(s, "now/")
		if unit != "w" {
			return 0, fmt.Errorf("unsupported truncation %q: only now/w is supported", s)
		}
		// Grafana weeks start Monday.
		wd := (int(now.Weekday()) + 6) % 7
		start := now.AddDate(0, 0, -wd).Truncate(24 * time.Hour)
		return start.UnixMilli(), nil
	}
	re := regexp.MustCompile(`^now-(\d+)([smhdw])(?:/([smhdw]))?$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid time %q: use now, now-<n><s|m|h|d|w>[/<unit>], or RFC3339", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: duration overflows", s)
	}
	base := now.Add(-time.Duration(n) * unitDuration(m[2]))
	if m[3] != "" {
		base = truncateTo(base, m[3])
	}
	return base.UnixMilli(), nil
}

func unitDuration(u string) time.Duration {
	switch u {
	case "s":
		return time.Second
	case "m":
		return time.Minute
	case "h":
		return time.Hour
	case "d":
		return 24 * time.Hour
	case "w":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

func truncateTo(t time.Time, unit string) time.Time {
	utc := t.UTC()
	y, mo, d := utc.Date()
	switch unit {
	case "w":
		wd := (int(utc.Weekday()) + 6) % 7
		return time.Date(y, mo, d-wd, 0, 0, 0, 0, time.UTC)
	case "d":
		return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	case "h":
		return time.Date(y, mo, d, utc.Hour(), 0, 0, 0, time.UTC)
	case "m":
		return time.Date(y, mo, d, utc.Hour(), utc.Minute(), 0, 0, time.UTC)
	default:
		return utc.Truncate(time.Second)
	}
}
