package usage

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func entryAt(id string, date time.Time, model string, in, out int64) Entry {
	return Entry{ID: id, Date: date, LocalDay: DayKey(date), Model: model, Input: in, Output: out}
}

func dayKeys(s []DailyUsage) []string {
	var keys []string
	for _, d := range s {
		keys = append(keys, d.Day)
	}
	return keys
}

func TestDaily(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	when := at(paris, 2026, 6, 30, 12)
	entries := []Entry{
		entryAt("A", when, "claude-opus-4-8", mtok, 0),
		entryAt("B", when, "claude-sonnet-5-5", 0, 100),
		entryAt("S", when, "<synthetic>", 0, 0),
	}
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:117
	d := Daily(entries, DayKey(when))
	if d == nil || d.Tokens != 1_000_100 || math.Abs(d.Cost.USD-5.001) > 1e-9 {
		t.Fatalf("Daily = %+v; want 1,000,100 tokens and $5.001", d)
	}
	// Go-only: the per-model breakdown sums back to the day.
	if d.Models["claude-opus-4-8"].Tokens != mtok || d.Models["claude-sonnet-5-5"].Tokens != 100 || d.Models["<synthetic>"].Tokens != 0 {
		t.Errorf("Models = %+v", d.Models)
	}
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:120
	if Daily(entries, "2000-01-01") != nil {
		t.Error("a day without entries must be nil")
	}
	// Go-only: a day holding only zero-token records is nil (LocalUsageReader.swift:2025).
	if Daily([]Entry{entries[2]}, DayKey(when)) != nil {
		t.Error("a synthetic-only day must be nil")
	}
}

func TestRollingDailySeries(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	now := at(paris, 2026, 10, 15, 0)
	entries := []Entry{
		entryAt("e1", at(paris, 2026, 10, 14, 0), "test", 100, 50),
		entryAt("e2", at(paris, 2026, 10, 15, 0), "test", 200, 80),
		entryAt("e0", at(paris, 2026, 9, 1, 0), "test", 500, 50),
	}
	cases := []struct {
		days        int
		first, last string
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1254
		{7, "2026-10-09", "2026-10-15"},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1263
		{14, "2026-10-02", "2026-10-15"},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1268
		{21, "2026-09-25", "2026-10-15"},
	}
	for _, c := range cases {
		s := DailySeries(entries, StartOfRollingWindow(now, c.days), now)
		if len(s) != c.days || s[0].Day != c.first || s[len(s)-1].Day != c.last {
			t.Errorf("%d days: %v", c.days, dayKeys(s))
		}
	}
	s := DailySeries(entries, StartOfRollingWindow(now, 7), now)
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1258
	if s[6].Tokens != 280 || s[5].Day != "2026-10-14" || s[5].Tokens != 150 || s[0].Tokens != 0 {
		t.Errorf("7-day values: %+v", s)
	}
}

func TestMonthSeries(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	now := at(paris, 2026, 7, 10, 12)
	op := func(id string, d time.Time, out int64) Entry { return entryAt(id, d, "claude-opus-4-8", 0, out) }

	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:144
	e := EnrichmentFromEntries([]Entry{
		op("june", at(paris, 2026, 6, 29, 12), 700),
		op("july-3", at(paris, 2026, 7, 3, 12), 100),
		op("july-10", at(paris, 2026, 7, 10, 12), 40),
	}, now, time.Monday)
	var tokens int64
	var usd float64
	for _, d := range e.MonthDaily {
		if d.Day[:7] != "2026-07" {
			t.Errorf("last month leaked into the series: %s", d.Day)
		}
		tokens += d.Tokens
		usd += d.Cost.USD
	}
	if tokens != e.Month.Tokens || math.Abs(usd-e.Month.Cost.USD) > 1e-9 {
		t.Errorf("series sum %d / $%v != month %d / $%v", tokens, usd, e.Month.Tokens, e.Month.Cost.USD)
	}

	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:180
	s := DailySeries([]Entry{op("only", at(paris, 2026, 7, 3, 12), 500)}, StartOfMonth(now), now)
	var want []string
	for d := 1; d <= 10; d++ {
		want = append(want, fmt.Sprintf("2026-07-%02d", d))
	}
	if fmt.Sprint(dayKeys(s)) != fmt.Sprint(want) {
		t.Errorf("axis %v; want %v", dayKeys(s), want)
	}
	for _, d := range s {
		if (d.Day == "2026-07-03") != (d.Tokens > 0) {
			t.Errorf("%s has %d tokens", d.Day, d.Tokens)
		}
	}

	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:193
	if s := DailySeries(nil, StartOfMonth(now), now); len(s) != 10 || s[9].Day != DayKey(now) {
		t.Errorf("series must stop at today: %v", dayKeys(s))
	}

	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:237
	priced := EnrichmentFromEntries([]Entry{op("t", at(paris, 2026, 7, 5, 12), 5_000)}, now, time.Monday)
	usd = 0
	for _, d := range priced.MonthDaily {
		usd += d.Cost.USD
		if d.Tokens > 0 && d.Cost.Coverage.String() != "estimated" {
			t.Errorf("day coverage %s; want estimated", d.Cost.Coverage)
		}
	}
	if usd != priced.Month.Cost.USD || priced.Month.Cost.Coverage.String() != "estimated" {
		t.Errorf("series cost %v vs month %v (%s)", usd, priced.Month.Cost.USD, priced.Month.Cost.Coverage)
	}
}

func TestEnrichmentLastUsageIgnoresZeroTokenEntries(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	now := at(paris, 2026, 7, 10, 12)
	real := entryAt("real", at(paris, 2026, 7, 6, 12), "claude-opus-4-8", 0, 300)
	synthetic := entryAt("synthetic", at(paris, 2026, 7, 9, 12), "<synthetic>", 0, 0)
	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:166
	if got := EnrichmentFromEntries([]Entry{real, synthetic}, now, time.Monday).LastUsage; !got.Equal(real.Date) {
		t.Errorf("LastUsage = %v; want %v", got, real.Date)
	}
	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:172
	if got := EnrichmentFromEntries([]Entry{synthetic}, now, time.Monday).LastUsage; !got.IsZero() {
		t.Errorf("synthetic-only LastUsage = %v; want zero", got)
	}
}

// The axis length is the day of the month in every month and zone; stepping 86,400 s would
// duplicate or skip a day around DST. Santiago and Havana add a midnight that does not exist.
func TestMonthAxisAcrossDSTZones(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:210
	for _, name := range []string{"Asia/Seoul", "America/Los_Angeles", "Australia/Lord_Howe",
		"Pacific/Chatham", "Europe/Berlin", "America/Santiago", "America/Havana"} {
		loc := zone(t, name)
		type ymd struct {
			y int
			m time.Month
			d int
		}
		var cases []ymd
		for m := time.January; m <= time.December; m++ {
			cases = append(cases, ymd{2026, m, 27})
		}
		cases = append(cases, ymd{2028, 2, 29})
		for _, c := range cases {
			now := at(loc, c.y, c.m, c.d, 12)
			s := DailySeries(nil, StartOfMonth(now), now)
			seen := map[string]bool{}
			for _, d := range s {
				seen[d.Day] = true
			}
			first := fmt.Sprintf("%04d-%02d-01", c.y, c.m)
			if len(s) != c.d || s[0].Day != first || s[len(s)-1].Day != DayKey(now) || len(seen) != len(s) {
				t.Errorf("%s %v: axis %v", name, DayKey(now), dayKeys(s))
			}
		}
	}
}

func TestRecentSeriesAcrossDSTFallBack(t *testing.T) {
	la := zone(t, "America/Los_Angeles")
	// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:726 (pure part; the file scan is in package claude)
	now := time.Date(2026, 11, 2, 7, 59, 0, 0, time.UTC).In(la)
	transcript := time.Date(2026, 10, 12, 7, 30, 0, 0, time.UTC).In(la)
	firstDay := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	if !now.Add(-21 * 24 * time.Hour).After(transcript) {
		t.Fatal("precondition: a fixed 21 x 86,400 s window would miss the transcript")
	}
	if got := EnrichmentScanStart(now, time.Monday); !got.Equal(firstDay) {
		t.Errorf("scan start = %v; want %v", got.UTC(), firstDay)
	}
	e := EnrichmentFromEntries([]Entry{entryAt("oct12-early", transcript, "claude-opus-4-8", 10, 113)}, now, time.Monday)
	if len(e.RecentDaily) != 21 || e.RecentDaily[0].Day != "2026-10-12" || e.RecentDaily[0].Tokens != 123 ||
		e.RecentDaily[20].Day != "2026-11-01" {
		t.Errorf("recent series %v, first day %d tokens", dayKeys(e.RecentDaily), e.RecentDaily[0].Tokens)
	}
}

func TestActiveBlock(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	now := at(paris, 2026, 9, 15, 12)
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1054
	recent := entryAt("recent", now.Add(-30*time.Minute), "claude-opus-4-8", 600_000, 0)
	old := entryAt("old", now.Add(-10*time.Hour), "claude-opus-4-8", 999, 0)
	b := ActiveBlock([]Entry{recent, old}, now)
	if b == nil || b.Tokens != 600_000 || b.TokensPerMinute != 20_000 {
		t.Fatalf("block = %+v; want 600,000 tokens at 20,000/min", b)
	}
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1072
	if p := Period([]Entry{recent, old}, DayKey(now), DayKey(now)); p.Tokens < 600_000 {
		t.Errorf("today's period = %d", p.Tokens)
	}
	// Go-only: id, end, and the 1-minute floor of the rate (LocalUsageReader.swift:2124-2131).
	if b.ID != fmt.Sprintf("block-%d", recent.Date.Unix()) || !b.End.Equal(recent.Date.Add(5*time.Hour)) {
		t.Errorf("block id/end = %s %v", b.ID, b.End)
	}
	if b := ActiveBlock([]Entry{entryAt("now", now, "claude-opus-4-8", 50, 0)}, now); b.TokensPerMinute != 50 {
		t.Errorf("rate under a minute = %v; want 50 (one-minute floor)", b.TokensPerMinute)
	}
	// Go-only: the window edge is inclusive.
	if b := ActiveBlock([]Entry{entryAt("edge", now.Add(-BlockWindow), "x", 1, 0)}, now); b == nil {
		t.Error("an entry exactly 5 h old is in the block")
	}

	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1080
	synthetic := entryAt("synthetic", now.Add(-120*time.Second), "<synthetic>", 0, 0)
	if ActiveBlock([]Entry{synthetic}, now) != nil {
		t.Error("zero-token records must not create a block")
	}
	actual := entryAt("actual", now.Add(-60*time.Second), "claude-opus", 10, 5)
	if b := ActiveBlock([]Entry{synthetic, actual}, now); b == nil || b.Tokens != 15 ||
		b.ID != fmt.Sprintf("block-%d", actual.Date.Unix()) {
		t.Errorf("block = %+v; zero-token metadata must not move the start", b)
	}
}

func TestBurnTier(t *testing.T) {
	cases := []struct {
		tpm  float64
		want BurnTier
	}{
		// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:995
		{500, BurnIdle},
		// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:996
		{50_000, BurnNormal},
		// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:997
		{200_000, BurnFast},
		// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:998
		{500_000, BurnBlazing},
		// Go-only: boundaries (UsageStore.swift:880-883).
		{1_000, BurnIdle}, {1_001, BurnNormal}, {100_000, BurnFast}, {400_000, BurnBlazing},
		{math.NaN(), BurnIdle},
	}
	for _, c := range cases {
		if got := BurnTierFor(c.tpm); got != c.want {
			t.Errorf("BurnTierFor(%v) = %s; want %s", c.tpm, got, c.want)
		}
	}
}

func TestForecast(t *testing.T) {
	now := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:162
	d, ok := ForecastDepletion(23_000_000, 1_000_000, 23, now)
	if !ok || math.Abs(d.Sub(now).Minutes()-77) > 0.5 {
		t.Errorf("depletion = %v, %v; want about 77 minutes", d.Sub(now), ok)
	}
	declined := []struct {
		name      string
		tokens    int64
		tpm, util float64
	}{
		// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:168
		{"low utilization", 1_000_000, 1_000_000, 3},
		// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:170
		{"low burn", 23_000_000, 5_000, 23},
		// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:172
		{"a day or more away", 100_000_000, 10_000, 10},
		// Go-only: guards of UsageStore.swift:716.
		{"at 100 %", 1_000_000, 1_000_000, 100},
		{"no block tokens", 0, 1_000_000, 50},
	}
	for _, c := range declined {
		if _, ok := ForecastDepletion(c.tokens, c.tpm, c.util, now); ok {
			t.Errorf("%s: want no forecast", c.name)
		}
	}

	block := &Block{Totals: Totals{Tokens: 23_000_000}, TokensPerMinute: 1_000_000}
	reset := now.Add(2 * time.Hour)
	// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:915 (utilization 100 warns without a block)
	if f := FiveHourForecast(100, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), nil, now); f == nil || !f.BeforeReset || !f.DepletionAt.Equal(now) {
		t.Errorf("at 100 %%: %+v", f)
	}
	// Go-only: depletion in 77 min, before a reset in 2 h; after a reset in 1 h.
	if f := FiveHourForecast(23, reset, block, now); f == nil || !f.BeforeReset {
		t.Errorf("before reset: %+v", f)
	}
	if f := FiveHourForecast(23, now.Add(time.Hour), block, now); f == nil || f.BeforeReset {
		t.Errorf("after reset: %+v", f)
	}
	// Go-only: no block below 100 %, or an unstable estimate, gives no forecast.
	if FiveHourForecast(23, reset, nil, now) != nil || FiveHourForecast(3, reset, block, now) != nil {
		t.Error("want nil forecasts")
	}
}
