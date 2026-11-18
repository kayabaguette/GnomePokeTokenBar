package companion

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCorruptDexEntryDroppedWhileRestSurvives(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:277
	dir := writeSave(t, `{"dex":[{"baseID":1,"finalID":3,"chainOrder":[1,2,3],"rarity":"common"},{"baseID":99,"rarity":"rare"},{"baseID":7,"finalID":9,"chainOrder":[7,8,9],"rarity":"uncommon"}],"inventory":{"rareCandy":2}}`)
	s := openAt(t, dir, stub(linear3()), 7)
	if len(s.st.Dex) != 2 || s.st.Dex[0].BaseID != 1 || s.st.Dex[1].BaseID != 7 {
		t.Fatalf("dex %+v", s.st.Dex)
	}
	eq(t, s.st.Inventory["rareCandy"], 2, "inventory kept")
}

func TestCorruptStateFileBackedUpBeforeReset(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:295
	garbage := "this is not valid json {{{ 손상"
	dir := writeSave(t, garbage)
	s := openAt(t, dir, stub(linear3()), 7)
	if len(s.st.Dex) != 0 || s.st.Active != nil || s.st.UsedSinceInstall != 0 {
		t.Fatal("not fresh")
	}
	got, err := os.ReadFile(filepath.Join(dir, stateFileName+".corrupt"))
	if err != nil || string(got) != garbage {
		t.Fatalf(".corrupt: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); !os.IsNotExist(err) {
		t.Fatal("fresh state must not be written without a snapshot")
	}
	// A non-object top level (array, null) is corrupt too.
	for _, raw := range []string{`[1,2]`, `null`, `"x"`} {
		if _, ok := decodeState([]byte(raw)); ok {
			t.Errorf("%s decoded", raw)
		}
	}
}

func TestCorruptActiveFallsBackToEggWhileRestSurvives(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:316 and :1696
	for _, active := range []string{`{"baseID":1}`, `{"baseID":1,"pathIDs":[],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3}`,
		`{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"mythic","totalForms":3}`,
		`{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"nature":"grumpy"}`} {
		dir := writeSave(t, `{"active":`+active+`,"dex":[{"baseID":1,"finalID":3,"chainOrder":[1,2,3],"rarity":"common"}],"inventory":{"rareCandy":3},"usedSinceInstall":5000,"installBaselineSet":true}`)
		s := openAt(t, dir, stub(linear3()), 7)
		if s.st.Active != nil || len(s.st.Dex) != 1 || s.st.Inventory["rareCandy"] != 3 || s.st.UsedSinceInstall != 5000 || !s.st.InstallBaselineSet {
			t.Errorf("active %s: %+v", active, s.st)
		}
		if _, err := os.Stat(filepath.Join(dir, stateFileName+".corrupt")); err == nil {
			t.Error("partial restore must not move the file aside")
		}
	}
}

func TestBackwardCompatibleDecode(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1673 and :1110
	st, ok := decodeState([]byte(`{"installBaselineSet":true,"usedSinceInstall":100,"eggUsage":0,"claimedTodayTokens":100,"lastDate":"d1",
		"active":{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":5,"rarity":"common","totalForms":3},
		"dex":[{"id":"x","baseID":4,"finalID":6,"chainOrder":[4,5,6],"rarity":"rare"}],"collectedFinals":["4:6"],"language":"ko"}`))
	if !ok || !ints(st.Active.PlannedPathIDs, []int{1}) || st.Active.IsShiny || st.Active.Nature != nil || st.Claimed != nil ||
		st.Dex[0].IsShiny || st.Dex[0].Nature != nil || st.UsedSinceInstall != 100 || st.EggUsage != 0 {
		t.Fatalf("%+v", st)
	}
	data, _ := encodeState(&st)
	round, _ := decodeState(data)
	if round.Active.IsShiny || !round.CollectedFinals["4:6"] || round.Language != "ko" {
		t.Fatal("round trip")
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1708 — currentID falls back to baseID.
	eq(t, (&monState{BaseID: 42}).currentID(), 42, "empty path")
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:238
	st, _ = decodeState([]byte(`{"installBaselineSet":true,"usedSinceInstall":5,"lastDate":"d","dex":[],"language":"ko"}`))
	if len(st.Inventory) != 0 || len(st.CandyGrantTier) != 0 || st.CandyFeatureSeeded || st.Inventory == nil {
		t.Fatal("defaults")
	}
}

func TestFullSaveRoundTripKeepsDeferredFields(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:246 plus the deferred-feature fields (design decision).
	at := time.Date(2026, 11, 18, 13, 20, 0, 123456789, time.UTC)
	st := freshState()
	st.GrowthDifficulty, st.SpentTokens, st.EggTier = 0.5, 7, sp("rare")
	st.EggPrayers = &prayerRecord{Count: 3}
	st.Claimed = map[string]int{"claude_code": 12}
	st.Inventory = map[string]int{"rareCandy": 3, "mint": 1, "shinyCharm": 1}
	st.CandyGrantTier, st.CandyWindowEpoch, st.CandyFeatureSeeded = map[string]int{"claude.fiveHour": 1}, map[string]string{"claude.fiveHour": "E"}, true
	st.NotifiedDexCompletions, st.DexCompletionsSeeded = toSet([]string{"region.kanto"}), true
	st.Dex = []dexEntry{{ID: "e", BaseID: 1, FinalID: 1, ChainOrder: []int{1}, Rarity: rarityCommon, CaughtAt: &at, ReleasedAt: &at,
		Prayers: &prayerRecord{Count: 2}, Names: map[int]map[string]string{1: {"en": "P1"}}, NamesVersion: intPtr(1),
		Profile: generateProfile(math.MaxUint64, 5, "e")}}
	data, err := encodeState(&st)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := decodeState(data)
	if !ok || !reflect.DeepEqual(back, st) {
		t.Fatalf("round trip differs:\n%+v\n%+v", back, st)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(data, &keys)
	for _, k := range []string{"schemaVersion", "growthDifficulty", "eggTier", "eggPrayers", "spentTokens", "claimedTodayTokensByProvider",
		"inventory", "candyGrantTier", "candyWindowEpoch", "collectedFinals", "notifiedDexCompletions", "dexCompletionsSeeded"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	if !strings.Contains(string(data), `"caughtAt": "2026-11-18T13:20:00.123456789Z"`) {
		t.Error("dates must be RFC 3339 with nanoseconds")
	}
	if !strings.Contains(string(data), `"seed": 18446744073709551615`) {
		t.Error("uint64 seed must survive exactly")
	}
}

func TestDuplicateSpeciesInSavedChainsAreRemovedOnLoad(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:118
	st := freshState()
	st.Dex = []dexEntry{entry(1, 2, []int{1, 1, 2}, rarityCommon)}
	st.Active = &monState{BaseID: 4, PathIDs: []int{4, 4, 5}, PlannedPathIDs: []int{4, 4, 5, 5, 6}, StageIndex: 2, Rarity: rarityCommon, TotalForms: 3}
	s := openAt(t, writeState(t, st), &fakeData{}, 7)
	if !ints(s.st.Dex[0].ChainOrder, []int{1, 2}) || !ints(s.st.Active.PathIDs, []int{4, 5}) ||
		!ints(s.st.Active.PlannedPathIDs, []int{4, 5, 6}) || s.st.Active.currentID() != 5 || s.st.Active.StageIndex != 1 {
		t.Fatalf("%+v %+v", s.st.Dex[0], s.st.Active)
	}
	log := s.CatchLog(CatchLogQuery{Sort: "numberAsc"})
	eq(t, log.Entries[0].Chain[0].DisplayName, "#1", "offline names fall back to the number")
}

func TestExtremeValuesAreClampedOnLoad(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:489 and :528
	dir := writeSave(t, `{"installBaselineSet":true,"usedSinceInstall":9223372036854775807,"spentTokens":-9223372036854775808,
		"eggUsage":9223372036854775807,"claimedTodayTokensByProvider":{"test":-42},"lastDate":"2026-08-03","eggPrayers":{"count":9223372036854775807}}`)
	s := openAt(t, dir, stub(linear3()), 7)
	if s.st.UsedSinceInstall != maxTokenValue || s.st.SpentTokens != 0 || s.st.EggUsage != maxTokenValue || s.st.Claimed["test"] != 0 ||
		s.st.EggPrayers.Count != maxTokenValue {
		t.Fatalf("%+v", s.st)
	}
	update(s, map[string]int{"test": 1000}, "2026-08-03", true)
	if s.st.UsedSinceInstall > maxTokenValue+1000 {
		t.Fatal("overflow")
	}
	st := freshState()
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1}, StageIndex: math.MaxInt, UsedAtStage: math.MaxInt,
		Rarity: rarityCommon, TotalForms: math.MaxInt}
	st.EggTier, st.PendingHatchID = sp("rare"), intPtr(4)
	st.sanitize()
	if st.Active.UsedAtStage != maxTokenValue || st.Active.TotalForms != 12 || st.Active.StageIndex != 0 || st.EggTier != nil || st.PendingHatchID != nil {
		t.Fatalf("%+v", st.Active)
	}
	// An unsatisfiable (legendary) egg tier is dropped (ST:167).
	egg := freshState()
	egg.EggTier = sp(rarityLegendary)
	egg.sanitize()
	if egg.EggTier != nil {
		t.Fatal("legendary tier kept")
	}
}

func TestUnavailableRepresentativeDroppedAtLoad(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:607
	s := openAt(t, writeSave(t, `{"representativeSpeciesID":999}`), stub(linear3()), 7)
	if s.st.RepresentativeSpeciesID != nil || s.SetRepresentative(intPtr(999), nil) {
		t.Fatal("unowned representative")
	}
}

// ---------------------------------------------------------------- snapshots (SaveSnapshotTests.swift)

func sampleState(tokens, dexCount int) state {
	st := freshState()
	st.InstallBaselineSet, st.UsedSinceInstall, st.SpentTokens = true, tokens, 1000
	for i := 1; i <= dexCount; i++ {
		st.Dex = append(st.Dex, entry(i, i, []int{i}, rarityCommon))
	}
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1}, UsedAtStage: 500, Rarity: rarityCommon, TotalForms: 1}
	return st
}

func TestSnapshotPruningKeepsTen(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:158
	s := openAt(t, writeState(t, sampleState(20_000, 2)), &fakeData{}, 7)
	for i := 1; i <= 15; i++ {
		if err := s.createSnapshot(fixedNow.Add(time.Duration(i) * time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	snaps := s.listSnapshots()
	eq(t, len(snaps), 10, "kept")
	eq(t, snaps[0].date.Equal(fixedNow.Add(15*time.Hour)), true, "newest")
	eq(t, snaps[9].date.Equal(fixedNow.Add(6*time.Hour)), true, "oldest kept")
	if !strings.HasPrefix(snaps[0].id, snapshotPrefix+fixedNow.Add(15*time.Hour).Local().Format(snapshotStampLayout)) {
		t.Fatalf("name %s", snaps[0].id)
	}
}

func TestAutoSnapshotInterval(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:180
	now := fixedNow
	s := openAt(t, writeState(t, sampleState(5000, 1)), &fakeData{}, 7, withNow(func() time.Time { return now }))
	s.autoSnapshotIfNeeded(now)
	eq(t, len(s.listSnapshots()), 1, "first")
	s.autoSnapshotIfNeeded(now.Add(time.Hour))
	eq(t, len(s.listSnapshots()), 1, "1 h later")
	s.autoSnapshotIfNeeded(now.Add(13 * time.Hour))
	eq(t, len(s.listSnapshots()), 2, "13 h later")
	// A trivial state is never snapshotted (CS:2114).
	empty := newStore(t, &fakeData{}, 7)
	empty.autoSnapshotIfNeeded(now)
	eq(t, len(empty.listSnapshots()), 0, "trivial state")
	// Update takes the snapshot at the end of a tick.
	u := openAt(t, writeState(t, sampleState(5000, 1)), &fakeData{}, 7)
	update(u, map[string]int{"t": 0}, "d", true)
	eq(t, len(u.listSnapshots()), 1, "tick snapshot")
}

func TestCorruptionRecoveryFromLatestSnapshot(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:201
	dir := writeState(t, sampleState(77_777, 5))
	s := openAt(t, dir, &fakeData{}, 7)
	if err := s.createSnapshot(fixedNow); err != nil {
		t.Fatal(err)
	}
	garbage := "{ corrupted state json missing brackets..."
	_ = os.WriteFile(filepath.Join(dir, stateFileName), []byte(garbage), 0o600)
	r := openAt(t, dir, &fakeData{}, 7)
	if r.st.UsedSinceInstall != 77_777 || len(r.st.Dex) != 5 || r.st.Active.BaseID != 1 {
		t.Fatalf("recovered %+v", r.st)
	}
	got, _ := os.ReadFile(filepath.Join(dir, stateFileName+".corrupt"))
	eq(t, string(got), garbage, ".corrupt copy")
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatal("recovered state must be saved")
	}
}

func TestCorruptSnapshotFileSkipped(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:309
	s := newStore(t, &fakeData{}, 7)
	_ = os.MkdirAll(s.snapshotDir(), 0o700)
	_ = os.WriteFile(filepath.Join(s.snapshotDir(), "companion-snapshot-2026-09-18-120000.json"), []byte("corrupted invalid json"), 0o600)
	eq(t, len(s.listSnapshots()), 0, "skipped")
}

func TestSnapshotNameClashGetsSuffix(t *testing.T) {
	// SS:142-151: a second snapshot in the same second gets -1.
	s := openAt(t, writeState(t, sampleState(1, 1)), &fakeData{}, 7)
	_ = s.createSnapshot(fixedNow)
	_ = s.createSnapshot(fixedNow)
	snaps := s.listSnapshots()
	if len(snaps) != 2 || !strings.HasSuffix(snaps[1].id, "-1.json") { // equal dates sort by id, descending
		t.Fatalf("%+v", snaps)
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	s := newStore(t, stub(linear3()), 7)
	use(s, 0)
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
	if s.SaveError() != nil {
		t.Fatal(s.SaveError())
	}
}
