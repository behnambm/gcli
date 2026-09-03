// === Behavioral Contract: timeparse.ParseToEpochMS(s, now) ===
//   - "now" returns the reference instant in epoch milliseconds
//   - "now-<n><unit>" shifts back by n of s/m/h/d/w
//   - "now-<n><unit>/<unit>" shifts back, then truncates to the unit boundary (UTC)
//   - "now/w" returns the start (Monday 00:00 UTC) of the current week
//   - Absolute RFC3339 timestamps parse to their epoch milliseconds
//   - Leading/trailing whitespace is ignored
//   - Empty input, malformed relative forms, unsupported truncations, and
//     overflowing durations all return an error
package timeparse

import (
	"testing"
	"time"
)

func TestParseToEpochMS_relativeShifts_returnShiftedEpochMilliseconds(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want int64
	}{
		{"now-30s", now.Add(-30 * time.Second).UnixMilli()},
		{"now-5m", now.Add(-5 * time.Minute).UnixMilli()},
		{"now-1h", now.Add(-time.Hour).UnixMilli()},
		{"now-1d", now.Add(-24 * time.Hour).UnixMilli()},
		{"now-2w", now.Add(-14 * 24 * time.Hour).UnixMilli()},
		{"now-0h", now.UnixMilli()},
	}
	for _, tc := range cases {
		got, err := ParseToEpochMS(tc.in, now)
		if err != nil {
			t.Errorf("ParseToEpochMS(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseToEpochMS(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseToEpochMS_truncatedRelative_truncatesToUnitBoundary(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 30, 45, 0, time.UTC)
	cases := []struct {
		in   string
		want int64
	}{
		// now-1d/d = start of day of (now - 1d)
		{"now-1d/d", time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC).UnixMilli()},
		// now-1h/h = start of hour of (now - 1h)
		{"now-1h/h", time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC).UnixMilli()},
		// now-1m/m = start of minute of (now - 1m)
		{"now-1m/m", time.Date(2026, 8, 30, 11, 29, 0, 0, time.UTC).UnixMilli()},
		// now-1w/w = Monday 00:00 of the week containing (now - 1w)
		{"now-1w/w", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC).UnixMilli()},
	}
	for _, tc := range cases {
		got, err := ParseToEpochMS(tc.in, now)
		if err != nil {
			t.Errorf("ParseToEpochMS(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseToEpochMS(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseToEpochMS_weekStart_returnsMondayMidnight(t *testing.T) {
	// 2026-08-30 is a Sunday; week start is Monday 2026-08-24.
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC).UnixMilli()

	got, err := ParseToEpochMS("now/w", now)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("ParseToEpochMS(now/w) = %d, want %d", got, want)
	}
}

func TestParseToEpochMS_absoluteRFC3339_returnsThatInstant(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	got, err := ParseToEpochMS("2026-08-01T00:00:00Z", now)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestParseToEpochMS_whitespace_isIgnored(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	got, err := ParseToEpochMS("  now-1h  ", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != now.Add(-time.Hour).UnixMilli() {
		t.Errorf("got %d, want shifted epoch", got)
	}
}

func TestParseToEpochMS_invalidInputs_returnError(t *testing.T) {
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	for _, in := range []string{
		"", "now-", "now-1x", "now-1", "yesterday",
		"now-1h/foo", "now/d", "now--1h", "now-1h extra",
		"2026-08-01", // not RFC3339 (missing time)
	} {
		if _, err := ParseToEpochMS(in, now); err == nil {
			t.Errorf("ParseToEpochMS(%q): want error, got nil", in)
		}
	}
}

func TestParseToEpochMS_weekStart_withNonUTCLocation_truncatesInUTC(t *testing.T) {
	// Reference in Tehran (+03:30), Sunday 2026-08-30 04:30 local = 2026-08-30T01:00Z.
	loc := time.FixedZone("Tehran", 3*3600+1800)
	now := time.Date(2026, 8, 30, 4, 30, 0, 0, loc)
	// Monday of that week, 00:00 UTC.
	want := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC).UnixMilli()

	got, err := ParseToEpochMS("now/w", now)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("ParseToEpochMS(now/w) = %d, want %d", got, want)
	}
}

func TestParseToEpochMS_overflowingDuration_returnsError(t *testing.T) {
	if _, err := ParseToEpochMS("now-99999999999999999999h", time.Now()); err == nil {
		t.Fatal("want error for overflowing duration")
	}
}
