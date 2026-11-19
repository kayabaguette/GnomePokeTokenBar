package usage

import (
	"fmt"
	"testing"
	"time"
)

// Fixtures of ActivityVisualizationTests.swift:220-254: zone America/Los_Angeles.

// seriesThrough is series(through:): days 1..DD of that month at 1,000 tokens.
func seriesThrough(today string) []DailyUsage {
	var out []DailyUsage
	for d := 1; d <= int(today[8]-'0')*10+int(today[9]-'0'); d++ {
		out = append(out, day(fmt.Sprintf("%s-%02d", today[:7], d), 1000))
	}
	return out
}

// ledgerMonths is ledger(months:): a ledger that has recorded every day of the months, at 1,000 a day.
func ledgerMonths(months ...string) Ledger {
	var l Ledger
	for _, m := range months {
		var n int
		fmt.Sscanf(m[5:], "%d", &n)
		days := map[int]int{2: 28, 4: 30, 6: 30, 9: 30, 11: 30}[n]
		if days == 0 {
			days = 31
		}
		l.Merge(seriesThrough(fmt.Sprintf("%s-%02d", m, days)))
	}
	return l
}

func heatmapAt(t *testing.T, l Ledger, series []DailyUsage, today string, ws time.Weekday) Heatmap {
	t.Helper()
	la := zone(t, "America/Los_Angeles")
	now, err := time.ParseInLocation(DayLayout, today, la)
	if err != nil {
		t.Fatal(err)
	}
	return MakeHeatmap(NewHistory(l, series), now.Add(12*time.Hour), ws)
}

// cells lists the (date, tokens, inRange) of the grid in index order, dates by civil addition.
type cell struct {
	date     string
	tokens   int64
	outRange bool
}

func cellsOf(t *testing.T, h Heatmap, today string) []cell {
	t.Helper()
	start, err := time.Parse(DayLayout, h.Start)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]cell, HeatmapCells)
	for i := range out {
		d := start.AddDate(0, 0, i).Format(DayLayout)
		out[i] = cell{d, h.Tokens[i], d < h.First || d > today}
	}
	return out
}

// ActivityVisualizationTests.swift:259-273 testGridIsAlwaysSixteenWeeksEndingWithTodaysWeek.
func TestHeatmapIsAlwaysSixteenWeeksEndingWithTodaysWeek(t *testing.T) {
	l := ledgerMonths("2026-06", "2026-07", "2026-08", "2026-09", "2026-10")
	for _, c := range []struct {
		today string
		ws    time.Weekday
	}{{"2026-10-01", time.Monday}, {"2026-10-19", time.Monday}, {"2026-11-03", time.Sunday}, {"2026-10-31", time.Sunday}} {
		h := heatmapAt(t, l, nil, c.today, c.ws)
		cells := cellsOf(t, h, c.today)
		if len(h.Tokens) != 112 {
			t.Fatalf("%s: %d tokens", c.today, len(h.Tokens))
		}
		inLastColumn := false
		for _, x := range cells[105:] {
			inLastColumn = inLastColumn || x.date == c.today
		}
		if !inLastColumn {
			t.Errorf("%s: the last column is not today's week", c.today)
		}
		last, seen := "", map[string]bool{}
		for _, x := range cells {
			if !x.outRange {
				last = x.date
			}
			seen[x.date] = true
		}
		if last != c.today {
			t.Errorf("%s: last in-range day is %s", c.today, last)
		}
		if len(seen) != 112 {
			t.Errorf("%s: a day repeats or is missing", c.today)
		}
		if start, _ := time.Parse(DayLayout, h.Start); start.Weekday() != c.ws {
			t.Errorf("%s: start %s is a %v, not a %v", c.today, h.Start, start.Weekday(), c.ws)
		}
	}
}

// ActivityVisualizationTests.swift:277-285 testTheFirstOfTheMonthKeepsLastMonthsDays.
func TestTheFirstOfTheMonthKeepsLastMonthsDays(t *testing.T) {
	h := heatmapAt(t, ledgerMonths("2026-08", "2026-09"), []DailyUsage{day("2026-10-01", 1000)}, "2026-10-01", time.Monday)
	used := map[string]bool{}
	for _, x := range cellsOf(t, h, "2026-10-01") {
		if !x.outRange && x.tokens > 0 {
			used[x.date] = true
		}
	}
	if !used["2026-09-30"] || !used["2026-10-01"] {
		t.Error("last month vanished on the 1st")
	}
	// Derived: Aug 1 to Sep 30 (61 days) plus Oct 1; the window starts 2026-06-15 and first is 08-01.
	if len(used) != 62 || h.Start != "2026-06-15" || h.First != "2026-08-01" {
		t.Errorf("used %d, start %s, first %s", len(used), h.Start, h.First)
	}
}

// ActivityVisualizationTests.swift:290-298 testDaysBeforeRecordingBeganAreOutsideTheRange.
func TestDaysBeforeRecordingBeganAreOutsideTheRange(t *testing.T) {
	h := heatmapAt(t, Ledger{}, seriesThrough("2026-10-19"), "2026-10-19", time.Monday)
	cells := cellsOf(t, h, "2026-10-19")
	first, out930 := "", false
	for _, x := range cells {
		if !x.outRange && first == "" {
			first = x.date
		}
		out930 = out930 || x.date == "2026-09-30" && x.outRange
	}
	if first != "2026-10-01" || h.First != "2026-10-01" || !out930 {
		t.Errorf("first in range %s (first %s): September was not recorded; 09-30 outside = %v", first, h.First, out930)
	}
	if len(cells)/7 != HeatmapWeeks {
		t.Errorf("%d columns", len(cells)/7)
	}
}

// ActivityVisualizationTests.swift:303-310 testLiveSeriesWinsWhenTheLedgerTrails.
func TestLiveSeriesWinsWhenTheLedgerTrails(t *testing.T) {
	var l Ledger
	l.Merge([]DailyUsage{day("2026-10-19", 500)})
	tokensOn := func(series int64) int64 {
		h := heatmapAt(t, l, []DailyUsage{day("2026-10-19", series)}, "2026-10-19", time.Monday)
		for _, x := range cellsOf(t, h, "2026-10-19") {
			if x.date == "2026-10-19" {
				return x.tokens
			}
		}
		return -1
	}
	if got := tokensOn(2000); got != 2000 {
		t.Errorf("series 2000 gives %d", got)
	}
	if got := tokensOn(100); got != 500 {
		t.Errorf("series 100 gives %d: a day never goes down", got)
	}
}

// ActivityVisualizationTests.swift:105-137 testGridStaysContiguousAcrossDSTInEveryWeekStartAndDirection:
// both week starts, both transition months, plus the fall-back trap (Sunday-first, the week of 2026-11-01).
func TestHeatmapStaysContiguousAcrossDST(t *testing.T) {
	l := ledgerMonths("2026-03", "2026-11")
	for _, ws := range []time.Weekday{time.Sunday, time.Monday} {
		for _, today := range []string{"2026-03-31", "2026-11-30", "2026-11-04", "2026-03-10"} {
			h := heatmapAt(t, l, nil, today, ws)
			cells := cellsOf(t, h, today)
			seen := map[string]bool{}
			for i, x := range cells {
				if seen[x.date] {
					t.Fatalf("ws=%v %s: %s repeats, the grid slipped a day", ws, today, x.date)
				}
				seen[x.date] = true
				if i > 0 {
					prev, _ := time.Parse(DayLayout, cells[i-1].date)
					if want := prev.AddDate(0, 0, 1).Format(DayLayout); x.date != want {
						t.Fatalf("ws=%v %s: %s -> %s breaks the sequence", ws, today, cells[i-1].date, x.date)
					}
				}
			}
		}
	}
}

// Derived: protocol §5.2 Heatmap. Days after today are 0 even if the history holds a stray value,
// first falls back to today when nothing was ever recorded, and start is a weekStart weekday for
// every weekday of today.
func TestHeatmapFieldsAndFallbacks(t *testing.T) {
	l := Ledger{TokensByDay: map[string]int64{"2026-10-21": 999, "2026-10-19": 7}}
	h := heatmapAt(t, l, nil, "2026-10-19", time.Monday)
	if h.First != "2026-10-19" {
		t.Errorf("first = %s, want today when recordedSince is empty", h.First)
	}
	cells := cellsOf(t, h, "2026-10-19")
	if cells[111].tokens != 0 || cells[111].date != "2026-10-25" {
		t.Errorf("last cell %+v: a future day is 0", cells[111])
	}
	if cells[105].date != "2026-10-19" || cells[105].tokens != 7 {
		t.Errorf("today's week starts %+v", cells[105])
	}
	// first never precedes start, even for a ledger that reaches back further.
	old := ledgerMonths("2025-01")
	if h := heatmapAt(t, old, nil, "2026-10-19", time.Monday); h.First != h.Start {
		t.Errorf("first %s, start %s", h.First, h.Start)
	}
	for d := 1; d <= 7; d++ {
		today := fmt.Sprintf("2026-10-%02d", 11+d) // Mon 12 .. Sun 18
		hm := heatmapAt(t, Ledger{}, nil, today, time.Wednesday)
		if s, _ := time.Parse(DayLayout, hm.Start); s.Weekday() != time.Wednesday {
			t.Errorf("%s: start %s is a %v", today, hm.Start, s.Weekday())
		}
	}
}
