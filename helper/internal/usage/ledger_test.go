package usage

import (
	"encoding/json"
	"maps"
	"testing"
	"time"
)

func day(key string, tokens int64) DailyUsage {
	return DailyUsage{Day: key, Totals: Totals{Tokens: tokens}}
}

// ledgerSince records since (empty) plus the active days, like UsageRecapTests' helper.
func ledgerSince(since string, days ...DailyUsage) Ledger {
	var l Ledger
	l.Merge(append([]DailyUsage{day(since, 0)}, days...))
	return l
}

func TestLedger(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:55
	var l Ledger
	l.Merge([]DailyUsage{day("2026-09-10", 500), day("2026-09-11", 300)})
	l.Merge([]DailyUsage{day("2026-09-10", 120), day("2026-09-11", 900)})
	if l.Tokens("2026-09-10") != 500 || l.Tokens("2026-09-11") != 900 {
		t.Errorf("a day never goes down: %v", l.TokensByDay)
	}

	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:65
	var v Ledger
	v.Merge([]DailyUsage{day("2026-09-01", 0), day("2026-09-02", 40)})
	if !maps.Equal(v.TokensByDay, map[string]int64{"2026-09-02": 40}) || v.CoveredSince != "2026-09-01" {
		t.Errorf("empty days add coverage but no row: %+v", v)
	}
	v.Merge([]DailyUsage{day("2026-09-05", 10)})
	if v.CoveredSince != "2026-09-01" {
		t.Errorf("a later series must not shrink the coverage: %q", v.CoveredSince)
	}

	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:80
	p := ledgerSince("2025-06-01", day("2025-06-10", 100), day("2026-02-01", 200))
	p.Prune("2026-01-01")
	if !maps.Equal(p.TokensByDay, map[string]int64{"2026-02-01": 200}) || p.CoveredSince != "2026-01-01" {
		t.Errorf("prune: %+v", p)
	}

	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:88
	var junk Ledger
	if err := json.Unmarshal([]byte(`{"tokensByDay":{"2026-09-10":400,"nope":10,"0000-00-00":10,"2026-09-11":0},"coveredSince":"0000-00-00"}`), &junk); err != nil {
		t.Fatal(err)
	}
	junk.Sanitize()
	if !maps.Equal(junk.TokensByDay, map[string]int64{"2026-09-10": 400}) || junk.CoveredSince != "" {
		t.Errorf("junk kept: %+v", junk)
	}

	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:100
	saved := ledgerSince("2026-09-01", day("2026-09-10", 700))
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Ledger
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	loaded.Sanitize()
	if !maps.Equal(loaded.TokensByDay, saved.TokensByDay) || loaded.CoveredSince != saved.CoveredSince {
		t.Errorf("round trip: %+v != %+v", loaded, saved)
	}
}

func TestLedgerRecord(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:150 (the first refresh fills the ledger)
	var first Ledger
	if !first.Record([]DailyUsage{day("2026-11-17", 900), day("2026-11-18", 300)}, now) {
		t.Error("the first record must report a change")
	}
	if first.Tokens("2026-11-18") != 300 || first.Tokens("2026-11-17") != 900 || first.CoveredSince != "2026-11-17" {
		t.Errorf("first refresh: %+v", first)
	}
	// Go-only: the same series again changes nothing, so nothing needs saving.
	if first.Record([]DailyUsage{day("2026-11-17", 900), day("2026-11-18", 300)}, now) {
		t.Error("an identical record must not report a change")
	}

	// Swift: Tests/PokeTokenBarTests/UsageRecapTests.swift:174 (keep last year, drop the year before)
	var kept Ledger
	kept.Merge([]DailyUsage{day("2024-12-31", 70), day("2025-01-01", 80)})
	kept.Record([]DailyUsage{day("2026-11-18", 5)}, now)
	if kept.Tokens("2025-01-01") != 80 || kept.Tokens("2024-12-31") != 0 || kept.CoveredSince != "2025-01-01" {
		t.Errorf("retention: %+v", kept)
	}
}

func TestStreak(t *testing.T) {
	utc := time.UTC
	la := zone(t, "America/Los_Angeles")
	d := func(key string) DailyUsage { return day(key, 1_000) }
	cases := []struct {
		name   string
		ledger Ledger
		now    time.Time
		want   Streak
	}{
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:45
		{"run ending today", ledgerSince("2026-09-01", d("2026-09-16"), d("2026-09-17"), d("2026-09-18")),
			at(utc, 2026, 9, 18, 12), Streak{3, true}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:53
		{"unused today keeps the run", ledgerSince("2026-09-01", d("2026-09-16"), d("2026-09-17")),
			at(utc, 2026, 9, 18, 8), Streak{2, false}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:60
		{"an empty day ends the run", ledgerSince("2026-09-01", d("2026-09-14"), d("2026-09-15"), d("2026-09-17"), d("2026-09-18")),
			at(utc, 2026, 9, 18, 12), Streak{2, true}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:63
		{"lapsed run", ledgerSince("2026-09-01", d("2026-09-15"), d("2026-09-16")),
			at(utc, 2026, 9, 18, 12), Streak{0, false}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:71
		{"across the 1st", ledgerSince("2026-09-01", d("2026-09-28"), d("2026-09-29"), d("2026-09-30"), d("2026-10-01")),
			at(utc, 2026, 10, 1, 12), Streak{4, true}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:80 (8 March is 23 h long and empty)
		{"spring forward", ledgerSince("2026-03-01", d("2026-03-06"), d("2026-03-07"), d("2026-03-09")),
			at(la, 2026, 3, 9, 12), Streak{1, true}},
		// Swift: Tests/PokeTokenBarTests/UsageStreakTests.swift:84 (1 November is 25 h long)
		{"fall back", ledgerSince("2026-10-25", d("2026-10-31"), d("2026-11-01"), d("2026-11-02")),
			at(la, 2026, 11, 2, 12), Streak{3, true}},
	}
	for _, c := range cases {
		if got := c.ledger.Streak(c.now); got != c.want {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}
