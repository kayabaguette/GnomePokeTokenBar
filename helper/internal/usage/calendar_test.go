package usage

import (
	"fmt"
	"testing"
	"time"
	_ "time/tzdata" // hermetic zones: tests never depend on the machine's zone database
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, loc)
}

func TestEnrichmentScanStartCoversAllWindows(t *testing.T) {
	for _, name := range []string{"Europe/Paris", "America/Los_Angeles", "Asia/Seoul"} {
		loc := zone(t, name)
		for _, ws := range []time.Weekday{time.Monday, time.Sunday} {
			// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1104
			var now time.Time
			for m := time.January; m <= time.December; m++ {
				c := at(loc, 2026, m, 1, 2)
				if StartOfWeek(c, ws).Before(StartOfMonth(c)) {
					now = c
					break
				}
			}
			if now.IsZero() {
				t.Fatalf("%s/%v: no month whose week straddles its first day", name, ws)
			}
			scan := EnrichmentScanStart(now, ws)
			for label, bound := range map[string]time.Time{
				"month": StartOfMonth(now), "week": StartOfWeek(now, ws), "block": now.Add(-BlockWindow),
				"recent use": now.Add(-RecentUseWindow), "rolling": StartOfRollingWindow(now, RecentDays),
			} {
				if scan.After(bound) {
					t.Errorf("%s/%v: scan start %v clips the %s window starting %v", name, ws, scan, label, bound)
				}
			}
			if !scan.Before(StartOfMonth(now)) {
				t.Errorf("%s/%v: early in the month the scan must start before the month", name, ws)
			}
			// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1127
			late := at(loc, 2026, 7, 28, 12)
			if got := EnrichmentScanStart(late, ws); !got.Equal(StartOfMonth(late)) {
				t.Errorf("%s/%v: late-month scan start = %v; want the month start", name, ws, got)
			}
			// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1131
			mid := at(loc, 2026, 7, 20, 12)
			if got := EnrichmentScanStart(mid, ws); !got.Equal(StartOfRollingWindow(mid, RecentDays)) {
				t.Errorf("%s/%v: mid-month scan start = %v; want the 21-day start", name, ws, got)
			}
		}
	}
}

func TestCalendarStarts(t *testing.T) {
	utc := time.UTC
	paris := zone(t, "Europe/Paris")
	cases := []struct {
		name string
		got  time.Time
		want time.Time
	}{
		// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:203 (Friday 2026-09-18, Monday weeks)
		{"monday week", StartOfWeek(at(utc, 2026, 9, 18, 12), time.Monday), at(utc, 2026, 9, 14, 0)},
		// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:208 (Sunday weeks)
		{"sunday week", StartOfWeek(at(utc, 2026, 9, 18, 12), time.Sunday), at(utc, 2026, 9, 13, 0)},
		// Go-only: today is the week start itself.
		{"week starts today", StartOfWeek(at(utc, 2026, 9, 14, 12), time.Monday), at(utc, 2026, 9, 14, 0)},
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:928 (month start is the 1st)
		{"month", StartOfMonth(at(paris, 2026, 7, 2, 15)), at(paris, 2026, 7, 1, 0)},
		// Go-only: a week crossing a year boundary.
		{"week across new year", StartOfWeek(at(paris, 2027, 1, 2, 9), time.Monday), at(paris, 2026, 12, 28, 0)},
		// Go-only: rolling window, days clamped to at least 1.
		{"rolling 1 day", StartOfRollingWindow(at(paris, 2026, 3, 1, 9), 0), at(paris, 2026, 3, 1, 0)},
		{"rolling 21 days over February", StartOfRollingWindow(at(paris, 2026, 3, 1, 9), 21), at(paris, 2026, 2, 9, 0)},
	}
	for _, c := range cases {
		if !c.got.Equal(c.want) {
			t.Errorf("%s: %v; want %v", c.name, c.got, c.want)
		}
	}
}

// time.Date normalizes a midnight that falls in a DST gap backwards into the previous day in
// some zones. The start of such a day is the transition, and its key is still that day.
func TestStartOfDayAtAMidnightDSTGap(t *testing.T) {
	cases := []struct {
		zone    string
		y       int
		m       time.Month
		d       int
		wantUTC string
	}{
		// Go-only: Chile springs forward at 24:00 on 2025-09-06 (00:00 -04 becomes 01:00 -03).
		{"America/Santiago", 2025, 9, 7, "2025-09-07T04:00:00Z"},
		// Go-only: Cuba springs forward at 00:00 on 2026-03-08 (00:00 CST becomes 01:00 CDT).
		{"America/Havana", 2026, 3, 8, "2026-03-08T05:00:00Z"},
	}
	for _, c := range cases {
		loc := zone(t, c.zone)
		noon := at(loc, c.y, c.m, c.d, 12)
		got := StartOfDay(noon)
		if got.UTC().Format(time.RFC3339) != c.wantUTC || DayKey(got) != DayKey(noon) {
			t.Errorf("%s: StartOfDay = %v (key %s); want %s on %s", c.zone, got, DayKey(got), c.wantUTC, DayKey(noon))
		}
		// The series through that day still lists it exactly once.
		s := DailySeries(nil, at(loc, c.y, c.m, c.d-2, 12), at(loc, c.y, c.m, c.d+1, 12))
		var keys []string
		for _, d := range s {
			keys = append(keys, d.Day)
		}
		want := []string{
			fmt.Sprintf("%04d-%02d-%02d", c.y, c.m, c.d-2), fmt.Sprintf("%04d-%02d-%02d", c.y, c.m, c.d-1),
			fmt.Sprintf("%04d-%02d-%02d", c.y, c.m, c.d), fmt.Sprintf("%04d-%02d-%02d", c.y, c.m, c.d+1),
		}
		if fmt.Sprint(keys) != fmt.Sprint(want) {
			t.Errorf("%s: series keys %v; want %v", c.zone, keys, want)
		}
	}
}
