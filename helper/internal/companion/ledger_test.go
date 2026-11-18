package companion

import (
	"reflect"
	"testing"
)

func TestInstallBaselineExcludesPreInstallUsage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:815
	s := newStore(t, stub(linear3()), 7)
	update(s, map[string]int{"test": 0}, "d1", false)
	eq(t, s.st.InstallBaselineSet, false, "no data, no baseline")
	update(s, map[string]int{"test": 48_000_000}, "d1", true)
	eq(t, s.st.InstallBaselineSet, true, "baseline")
	eq(t, s.st.UsedSinceInstall, 0, "baseline credits nothing")
	update(s, map[string]int{"test": 148_000_000}, "d1", true)
	eq(t, s.st.UsedSinceInstall, 100_000_000, "rise after baseline")
}

func TestUsageIncreaseAfterValidDropContinuesEggProgress(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:831
	s := newStore(t, stub(linear3()), 7)
	use(s, 0)
	use(s, 200)
	eq(t, s.st.EggUsage, 200, "first credit")
	use(s, 40)
	eq(t, s.st.EggUsage, 200, "drop is not credited")
	eq(t, s.st.Claimed["test"], 40, "drop rebases")
	use(s, 75)
	eq(t, s.st.EggUsage, 235, "rise after rebase")
	eq(t, s.st.UsedSinceInstall, 235, "lifetime")
}

func TestEmptyUsageSnapshotDoesNotRebaseDailyLedger(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:850
	s := newStore(t, stub(linear3()), 7)
	use(s, 0)
	use(s, 200)
	update(s, map[string]int{"test": 0}, "d1", false)
	eq(t, s.st.EggUsage, 200, "empty snapshot")
	eq(t, s.st.Claimed["test"], 200, "ledger kept")
	use(s, 250)
	eq(t, s.st.EggUsage, 250, "egg")
	eq(t, s.st.UsedSinceInstall, 250, "lifetime")
}

func TestProviderLedgerTable(t *testing.T) {
	type step struct {
		m        map[string]int
		date     string
		has      bool
		used     int
		ledger   map[string]int // nil: not checked
		lastDate string         // "": not checked
	}
	cases := []struct {
		name  string
		swift string
		steps []step
	}{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:866
		{"partial provider loss then recovery", "CompanionTests.swift:866", []step{
			{map[string]int{"claude_code": 0, "codex": 0}, "d1", true, 0, nil, ""},
			{map[string]int{"claude_code": 1000, "codex": 500}, "d1", true, 1500, nil, ""},
			{map[string]int{"claude_code": 1000}, "d1", true, 1500, map[string]int{"claude_code": 1000, "codex": 500}, ""},
			{map[string]int{"claude_code": 1000, "codex": 500}, "d1", true, 1500, nil, ""},
			{map[string]int{"claude_code": 1000, "codex": 700}, "d1", true, 1700, nil, ""},
		}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:887
		{"carrier without today data", "CompanionTests.swift:887", []step{
			{map[string]int{"codex": 0}, "d1", true, 0, nil, ""},
			{map[string]int{"codex": 2000}, "d1", true, 2000, nil, ""},
			{map[string]int{}, "d1", false, 2000, map[string]int{"codex": 2000}, ""},
			{map[string]int{"codex": 2200}, "d1", true, 2200, nil, ""},
		}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:903
		{"date rollover credits the new day", "CompanionTests.swift:903", []step{
			{map[string]int{"codex": 0}, "d1", true, 0, nil, ""},
			{map[string]int{"codex": 200}, "d1", true, 200, nil, ""},
			{map[string]int{"codex": 100}, "d2", true, 300, map[string]int{"codex": 100}, ""},
			{map[string]int{"codex": 150}, "d2", true, 350, nil, ""},
		}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:920
		{"late provider after rollover", "CompanionTests.swift:920", []step{
			{map[string]int{"claude_code": 0, "codex": 0}, "d1", true, 0, nil, ""},
			{map[string]int{"claude_code": 1000, "codex": 500}, "d1", true, 1500, nil, ""},
			{map[string]int{"claude_code": 100}, "d2", true, 1600, map[string]int{"claude_code": 100, "codex": 0}, ""},
			{map[string]int{"claude_code": 100, "codex": 700}, "d2", true, 2300, nil, ""},
			{map[string]int{"claude_code": 100, "codex": 900}, "d2", true, 2500, nil, ""},
		}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:943
		{"stale empty map does not consume the date", "CompanionTests.swift:943", []step{
			{map[string]int{"codex": 0}, "d1", true, 0, nil, ""},
			{map[string]int{"codex": 200}, "d1", true, 200, nil, ""},
			{map[string]int{}, "d2", true, 200, map[string]int{"codex": 200}, "d1"},
			{map[string]int{"codex": 100}, "d2", true, 300, nil, ""},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t, stub(linear3()), 7)
			for i, st := range c.steps {
				update(s, st.m, st.date, st.has)
				if s.st.UsedSinceInstall != st.used {
					t.Fatalf("%s step %d: used %d, want %d", c.swift, i, s.st.UsedSinceInstall, st.used)
				}
				if st.ledger != nil && !reflect.DeepEqual(s.st.Claimed, st.ledger) {
					t.Fatalf("%s step %d: ledger %v, want %v", c.swift, i, s.st.Claimed, st.ledger)
				}
				if st.lastDate != "" {
					eq(t, s.st.LastDate, st.lastDate, "lastDate")
				}
			}
		})
	}
}

func TestLegacyAggregateLedgerSeedsWithoutRetroactiveCredit(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:959
	dir := writeSave(t, `{"installBaselineSet":true,"usedSinceInstall":10000,"claimedTodayTokens":9000,"lastDate":"d1"}`)
	s := openAt(t, dir, stub(linear3()), 7)
	if s.st.Claimed != nil {
		t.Fatal("legacy ledger must decode as nil")
	}
	update(s, map[string]int{"codex": 500}, "d1", true)
	eq(t, s.st.UsedSinceInstall, 10_000, "seed only")
	if !reflect.DeepEqual(s.st.Claimed, map[string]int{"codex": 500}) {
		t.Fatalf("ledger %v", s.st.Claimed)
	}
	update(s, map[string]int{"codex": 700}, "d1", true)
	eq(t, s.st.UsedSinceInstall, 10_200, "rise")
}

func TestLedgerNilVersusEmptyRoundTrips(t *testing.T) {
	// companion.md §1.2: nil ledger (unseeded) must stay distinct from an empty one across a save.
	st := freshState()
	data, _ := encodeState(&st)
	back, _ := decodeState(data)
	if back.Claimed != nil {
		t.Fatal("nil ledger came back non-nil")
	}
	st.Claimed = map[string]int{}
	data, _ = encodeState(&st)
	back, _ = decodeState(data)
	if back.Claimed == nil {
		t.Fatal("empty ledger came back nil")
	}
	// CM:723-731: a present key, even null, means "seeded" (Swift lenient default [:]).
	back, _ = decodeState([]byte(`{"claimedTodayTokensByProvider":null}`))
	if back.Claimed == nil {
		t.Fatal("present null must decode as an empty, seeded ledger")
	}
}
