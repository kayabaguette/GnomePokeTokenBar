package usage

import "time"

// DayLayout is the day-key format, yyyy-MM-dd.
const DayLayout = "2006-01-02"

const (
	// BlockWindow is the rolling window of the active block (LocalUsageReader.swift:18).
	BlockWindow = 5 * time.Hour
	// RecentUseWindow keeps a provider used within it visible on idle days (:21).
	RecentUseWindow = 7 * 24 * time.Hour
	// RecentDays is the length of the rolling daily series (:2145).
	RecentDays = 21
)

// DayKey formats t as a day key in t's own location.
func DayKey(t time.Time) string { return t.Format(DayLayout) }

// midnight returns the first instant of the civil day (y, m, d) in loc. d may overflow; it is
// normalized by the calendar, so day arithmetic never assumes 86,400-second days. Where
// midnight falls in a DST gap, time.Date normalizes backwards into the previous day
// (America/Santiago, America/Havana); the day then starts at the zone transition.
func midnight(y int, m time.Month, d int, loc *time.Location) time.Time {
	cy, cm, cd := time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Date()
	t := time.Date(cy, cm, cd, 0, 0, 0, 0, loc)
	if t.Day() != cd {
		_, end := t.ZoneBounds()
		t = end
	}
	return t
}

// StartOfDay returns the first instant of t's local day.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return midnight(y, m, d, t.Location())
}

// StartOfWeek returns the first instant of the week containing t, weeks starting on first.
// Upstream takes the locale's first weekday; the helper receives it as a setting.
func StartOfWeek(t time.Time, first time.Weekday) time.Time {
	y, m, d := t.Date()
	back := (int(t.Weekday()) - int(first) + 7) % 7
	return midnight(y, m, d-back, t.Location())
}

// StartOfMonth returns the first instant of t's month.
func StartOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return midnight(y, m, 1, t.Location())
}

// StartOfRollingWindow returns the first instant of the first day of a days-long window
// ending on t's day (LocalUsageReader.swift:2151-2158).
func StartOfRollingWindow(t time.Time, days int) time.Time {
	y, m, d := t.Date()
	return midnight(y, m, d-(max(1, days)-1), t.Location())
}

// EnrichmentScanStart is the mtime lower bound of the enrichment scan: the earliest start of
// every window derived from it (block, recent use, week, month, rolling series). Logs are
// append-only, so a file last modified before this cannot hold an entry inside any window
// (LocalUsageReader.swift:2168-2174).
func EnrichmentScanStart(now time.Time, weekStart time.Weekday) time.Time {
	start := StartOfMonth(now)
	for _, t := range []time.Time{
		StartOfWeek(now, weekStart),
		now.Add(-BlockWindow),
		now.Add(-RecentUseWindow),
		StartOfRollingWindow(now, RecentDays),
	} {
		if t.Before(start) {
			start = t
		}
	}
	return start
}
