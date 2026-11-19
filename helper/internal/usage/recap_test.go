package usage

import (
	"slices"
	"testing"
	"time"
)

// Fixtures of UsageRecapTests.swift:7-48: UTC, weeks start on Monday, now = Friday 2026-09-18 12:00.
func recapNow() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }

func recapOf(h History, scope RecapScope, offset int) Recap {
	now := recapNow()
	return MakeRecap(h, NewRecapPeriod(scope, now, time.Monday).Shifted(offset), now)
}

// ledgerOf is UsageRecapTests' ledger(since:, days): a ledger after Merge([day(since, 0)] + days).
func ledgerOf(since string, days map[string]int64) History {
	var series []DailyUsage
	for k, v := range days {
		series = append(series, day(k, v))
	}
	return NewHistory(ledgerSince(since, series...), nil)
}

func bucketKeys(r Recap) []string {
	var out []string
	for _, b := range r.Buckets {
		out = append(out, b.Key)
	}
	return out
}

func hasData(r Recap) []bool {
	var out []bool
	for _, b := range r.Buckets {
		out = append(out, b.HasData)
	}
	return out
}

// UsageRecapTests.swift:200-224 testPeriods... (Monday-first, Sunday-first, shifted).
func TestRecapPeriods(t *testing.T) {
	now := recapNow()
	mon := NewRecapPeriod(ScopeWeek, now, time.Monday)
	if !mon.Start.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)) || !mon.End.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Monday-first week = %v .. %v", mon.Start, mon.End)
	}
	if sun := NewRecapPeriod(ScopeWeek, now, time.Sunday); !sun.Start.Equal(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Sunday-first week starts %v", sun.Start)
	}
	// "31 March - 1 month" is February, not March 3.
	mar := NewRecapPeriod(ScopeMonth, time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Monday).Shifted(-1)
	if !mar.Start.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) || !mar.End.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month before 2026-03-31 = %v .. %v", mar.Start, mar.End)
	}
	year := NewRecapPeriod(ScopeYear, now, time.Monday).Shifted(-1)
	if !year.Start.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) || !year.End.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("year before now = %v .. %v", year.Start, year.End)
	}
}

// UsageRecapTests.swift:227-247 testEachScopeDrawsItsOwnMeters.
func TestEachScopeDrawsItsOwnMeters(t *testing.T) {
	l := ledgerOf("2026-01-01", map[string]int64{"2026-09-02": 10, "2026-09-17": 20, "2026-08-30": 5})

	week := recapOf(l, ScopeWeek, 0)
	if k := bucketKeys(week); len(k) != 7 || k[0] != "2026-09-14" {
		t.Errorf("week buckets = %v", k)
	}
	var current []string
	for _, b := range week.Buckets {
		if b.IsCurrent {
			current = append(current, b.Key)
		}
	}
	if !slices.Equal(current, []string{"2026-09-18"}) {
		t.Errorf("current buckets = %v", current)
	}
	if n := len(recapOf(l, ScopeMonth, 0).Buckets); n != 30 {
		t.Errorf("September has %d buckets", n)
	}
	if n := len(recapOf(l, ScopeMonth, -7).Buckets); n != 28 {
		t.Errorf("February 2026 has %d buckets", n)
	}
	year := recapOf(l, ScopeYear, 0)
	if len(year.Buckets) != 12 || year.Buckets[8].Tokens != 30 || year.Buckets[7].Tokens != 5 || !year.Buckets[8].IsCurrent || year.Total != 35 {
		t.Errorf("year = %+v total %d", year.Buckets, year.Total)
	}
	if year.Buckets[0].Key != "2026-01-01" || year.Buckets[11].Key != "2026-12-01" {
		t.Errorf("year bucket keys are the first of each month: %v", bucketKeys(year))
	}
	// Derived: start and end are inclusive day keys.
	if week.Start != "2026-09-14" || week.End != "2026-09-20" || year.Start != "2026-01-01" || year.End != "2026-12-31" {
		t.Errorf("ranges: week %s..%s, year %s..%s", week.Start, week.End, year.Start, year.End)
	}
}

// UsageRecapTests.swift:249-259 testFutureAndUnrecordedDaysAreNotZeros.
func TestFutureAndUnrecordedDaysAreNotZeros(t *testing.T) {
	week := recapOf(ledgerOf("2026-09-16", map[string]int64{"2026-09-17": 50}), ScopeWeek, 0)
	if got := hasData(week); !slices.Equal(got, []bool{false, false, true, true, true, false, false}) {
		t.Errorf("hasData = %v: Mon-Tue before the ledger, Sat-Sun still ahead", got)
	}
	if week.ActiveDays != 1 || week.CountedDays != 3 {
		t.Errorf("active %d, counted %d", week.ActiveDays, week.CountedDays)
	}
	year := recapOf(ledgerOf("2026-09-01", nil), ScopeYear, 0)
	want := append(append(make([]bool, 8), true), false, false, false)
	if got := hasData(year); !slices.Equal(got, want) {
		t.Errorf("year hasData = %v", got)
	}
}

// UsageRecapTests.swift:261-272 testARunningPeriodIsComparedOverTheSameDays.
func TestARunningPeriodIsComparedOverTheSameDays(t *testing.T) {
	l := ledgerOf("2026-08-01", map[string]int64{"2026-09-10": 100, "2026-08-18": 40, "2026-08-19": 1000})
	month := recapOf(l, ScopeMonth, 0)
	if !month.IsInProgress || month.PreviousTotal == nil || *month.PreviousTotal != 40 {
		t.Fatalf("in progress %v, previous %v: the 19th of August is past the 18th", month.IsInProgress, month.PreviousTotal)
	}
	// Derived: total 100 against 40 is the Swift delta 1.5.
	if month.Total != 100 || float64(month.Total-*month.PreviousTotal)/float64(*month.PreviousTotal) != 1.5 {
		t.Errorf("total %d", month.Total)
	}
}

// UsageRecapTests.swift:274-282 testAFinishedPeriodIsComparedWhole.
func TestAFinishedPeriodIsComparedWhole(t *testing.T) {
	l := ledgerOf("2026-07-01", map[string]int64{"2026-08-05": 300, "2026-07-31": 100})
	august := recapOf(l, ScopeMonth, -1)
	if august.IsInProgress || august.PreviousTotal == nil || *august.PreviousTotal != 100 {
		t.Fatalf("in progress %v, previous %v", august.IsInProgress, august.PreviousTotal)
	}
	// Derived: 300 against 100 is the Swift delta 2.0.
	if august.Total != 300 || float64(august.Total-*august.PreviousTotal)/float64(*august.PreviousTotal) != 2.0 {
		t.Errorf("total %d", august.Total)
	}
}

// UsageRecapTests.swift:283-289 testAHalfCoveredPreviousPeriodIsNotAComparison.
func TestAHalfCoveredPreviousPeriodIsNotAComparison(t *testing.T) {
	week := recapOf(ledgerOf("2026-09-09", map[string]int64{"2026-09-15": 150, "2026-09-10": 50}), ScopeWeek, 0)
	if week.PreviousTotal != nil {
		t.Errorf("previousTotal = %d: the ledger starts on the Wednesday of the previous week", *week.PreviousTotal)
	}
}

// UsageRecapTests.swift:291-298 testTheBestStreakStaysInsideThePeriod.
func TestTheBestStreakStaysInsideThePeriod(t *testing.T) {
	days := map[string]int64{}
	for _, d := range []string{"11", "12", "13", "14", "15", "16", "18"} {
		days["2026-09-"+d] = 5
	}
	l := ledgerOf("2026-09-01", days)
	if got := recapOf(l, ScopeWeek, 0).BestStreak; got != 3 {
		t.Errorf("week bestStreak = %d: Monday 14 to Wednesday 16, the days before the week do not count", got)
	}
	if got := recapOf(l, ScopeMonth, 0).BestStreak; got != 6 {
		t.Errorf("month bestStreak = %d", got)
	}
}

// UsageRecapTests.swift:310-317 testGoingBackStopsWhereTheLedgerStarts.
func TestGoingBackStopsWhereTheLedgerStarts(t *testing.T) {
	l := ledgerOf("2026-09-01", map[string]int64{"2026-09-02": 10})
	for _, c := range []struct {
		scope  RecapScope
		offset int
		want   bool
		why    string
	}{
		{ScopeMonth, 0, false, "August was never recorded"},
		{ScopeWeek, -1, true, "the previous week (31 Aug - 6 Sep) holds 1 September"},
		{ScopeWeek, -2, false, ""},
		{ScopeYear, 0, false, ""},
	} {
		if got := recapOf(l, c.scope, c.offset).CanGoBack; got != c.want {
			t.Errorf("%s offset %d canGoBack = %v, want %v %s", c.scope, c.offset, got, c.want, c.why)
		}
	}
}

// UsageRecapTests.swift:320-330 testAnEmptyLedgerStillDrawsThePeriod.
func TestAnEmptyLedgerStillDrawsThePeriod(t *testing.T) {
	week := recapOf(NewHistory(Ledger{}, nil), ScopeWeek, 0)
	if len(week.Buckets) != 7 || slices.Contains(hasData(week), true) || week.Total != 0 || week.BestDay != nil ||
		week.PreviousTotal != nil || week.CanGoBack || week.ActiveDays != 0 || week.CountedDays != 0 {
		t.Errorf("empty recap = %+v", week)
	}
	// Derived: the empty recap still marshals its buckets as an array, never null.
	if week.Buckets == nil {
		t.Error("buckets is nil")
	}
}

// Derived: the port's tie rule (protocol §7.5) is the earliest day, and a zero day is never best.
func TestBestDayTiesGoToTheEarliestDay(t *testing.T) {
	l := ledgerOf("2026-09-01", map[string]int64{"2026-09-14": 70, "2026-09-16": 90, "2026-09-17": 90, "2026-09-15": 0})
	week := recapOf(l, ScopeWeek, 0)
	if week.BestDay == nil || week.BestDay.Key != "2026-09-16" || week.BestDay.Tokens != 90 {
		t.Errorf("bestDay = %+v", week.BestDay)
	}
	if week.ActiveDays != 3 || week.CountedDays != 5 {
		t.Errorf("active %d, counted %d", week.ActiveDays, week.CountedDays)
	}
}

// Derived from protocol §7.5: the live series wins over a trailing ledger, and its first day is
// part of recordedSince, so a day it covers is known and a ledger-less day before it is not.
func TestRecapOverlaysTheLiveSeries(t *testing.T) {
	var l Ledger
	l.Merge([]DailyUsage{day("2026-09-10", 0), day("2026-09-17", 500)})
	series := []DailyUsage{day("2026-09-01", 0), day("2026-09-17", 200), day("2026-09-18", 40)}
	h := NewHistory(l, series)
	if h.RecordedSince() != "2026-09-01" {
		t.Errorf("recordedSince = %s, want the earlier of 09-10 and 09-01", h.RecordedSince())
	}
	week := recapOf(h, ScopeWeek, 0)
	if week.Total != 540 { // 500 from the ledger (it wins over 200) + 40 live
		t.Errorf("total = %d", week.Total)
	}
	// Without a ledger the series alone sets the coverage.
	only := NewHistory(Ledger{}, series)
	if only.RecordedSince() != "2026-09-01" || !only.Known("2026-09-02") || only.Known("2026-08-31") {
		t.Errorf("series-only coverage: since %s", only.RecordedSince())
	}
	if NewHistory(Ledger{}, nil).RecordedSince() != "" || NewHistory(Ledger{}, nil).Known("2026-09-02") {
		t.Error("an empty history vouches for nothing")
	}
}

// Derived: a year compares by position in the year; a finished year compares whole; previousTotal
// stays null when the ledger starts inside the compared range.
func TestYearComparison(t *testing.T) {
	l := ledgerOf("2025-01-01", map[string]int64{"2025-01-05": 7, "2025-09-18": 3, "2025-09-19": 1000, "2026-01-02": 20})
	year := recapOf(l, ScopeYear, 0)
	if year.PreviousTotal == nil || *year.PreviousTotal != 10 { // Jan 1 .. Sep 18 (262 days) of 2025
		t.Errorf("previous = %v", year.PreviousTotal)
	}
	if !year.CanGoBack || recapOf(l, ScopeYear, -1).CanGoBack {
		t.Errorf("canGoBack this %v, 2025 %v", year.CanGoBack, recapOf(l, ScopeYear, -1).CanGoBack)
	}
	if y25 := recapOf(l, ScopeYear, -1); y25.IsInProgress || y25.Total != 1010 || y25.PreviousTotal != nil || y25.End != "2025-12-31" {
		t.Errorf("2025 = %+v", y25)
	}
	// A past period has no current bucket and every elapsed month has data.
	for _, b := range recapOf(l, ScopeYear, -1).Buckets {
		if b.IsCurrent || !b.HasData {
			t.Errorf("2025 bucket %+v", b)
		}
	}
}

// Go-only DST cases (docs/spec/recap-heatmap.md §5.3 item 13): periods start at local midnights
// across both 2026 transitions and have the right number of buckets.
func TestRecapAcrossDST(t *testing.T) {
	la := zone(t, "America/Los_Angeles")
	for _, c := range []struct {
		now     time.Time
		scope   RecapScope
		offset  int
		buckets int
	}{
		{at(la, 2026, 3, 10, 12), ScopeWeek, 0, 7},
		{at(la, 2026, 3, 10, 12), ScopeWeek, -2, 7},
		{at(la, 2026, 3, 20, 12), ScopeWeek, -1, 7},
		{at(la, 2026, 11, 4, 12), ScopeWeek, 0, 7},
		{at(la, 2026, 11, 10, 12), ScopeWeek, -1, 7},
		{at(la, 2026, 3, 20, 12), ScopeMonth, 0, 31},
		{at(la, 2026, 11, 20, 12), ScopeMonth, 0, 30},
		{at(la, 2026, 9, 18, 12), ScopeMonth, -7, 28},
		{at(la, 2026, 11, 20, 12), ScopeYear, 0, 12},
	} {
		for _, ws := range []time.Weekday{time.Sunday, time.Monday} {
			p := NewRecapPeriod(c.scope, c.now, ws).Shifted(c.offset)
			if p.Start.Hour() != 0 || p.Start.Minute() != 0 || p.End.Hour() != 0 || p.End.Minute() != 0 {
				t.Errorf("%v %s %d ws=%v: period %v .. %v is not midnight to midnight", c.now, c.scope, c.offset, ws, p.Start, p.End)
			}
			r := MakeRecap(NewHistory(Ledger{}, nil), p, c.now)
			if len(r.Buckets) != c.buckets {
				t.Errorf("%v %s %d ws=%v: %d buckets, want %d", c.now, c.scope, c.offset, ws, len(r.Buckets), c.buckets)
			}
			if !slices.IsSorted(bucketKeys(r)) {
				t.Errorf("bucket keys out of order: %v", bucketKeys(r))
			}
		}
	}
	// The week of the spring-forward day is 167 hours long and still 7 distinct days.
	p := NewRecapPeriod(ScopeWeek, at(la, 2026, 3, 10, 12), time.Sunday)
	if got := p.End.Sub(p.Start); got != 167*time.Hour {
		t.Errorf("spring-forward week = %v", got)
	}
	if k := p.Days(); len(k) != 7 || k[0] != "2026-03-08" || k[6] != "2026-03-14" {
		t.Errorf("days = %v", k)
	}
}
