package companion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Branch coverage for paths the Swift suite reaches only implicitly. Each cites the Swift source.

func TestSameDayNewProviderIsSeeded(t *testing.T) {
	// CS:936-941: a provider first seen mid-day is seeded, never credited retroactively.
	s := newStore(t, stub(linear3()), 7)
	update(s, map[string]int{"claude_code": 0}, "d1", true)
	update(s, map[string]int{"claude_code": 10, "codex": 5000}, "d1", true)
	eq(t, s.st.UsedSinceInstall, 10, "only claude_code credited")
	update(s, map[string]int{"claude_code": 10, "codex": 5100}, "d1", true)
	eq(t, s.st.UsedSinceInstall, 110, "codex credited from its seed")
}

func TestEvolveRepairsAPlanThatLeftTheTree(t *testing.T) {
	// CS:1025-1035 (evolve-time repair) and CS:1013 (current species missing from the tree).
	// Unreachable through Open/Update, which normalize the plan on line load; kept for parity.
	s := newStore(t, stub(wurmpleLine()), 7)
	s.hatch(bg, 265)
	s.st.Active.PlannedPathIDs = []int{265, 999}
	s.applyUsage(phase(rarityCommon, 3, 0))
	a := s.st.Active
	if a.StageIndex != 1 || len(a.PlannedPathIDs) != 3 || a.PlannedPathIDs[1] != a.currentID() {
		t.Fatalf("repaired %+v", a)
	}
	s.st.Active.PathIDs = []int{265, 999}
	s.applyUsage(1_000_000_000)
	eq(t, s.st.Active.StageIndex, 1, "a species outside the tree stops growth")
	if got := repairedPlan([]int{1, 2}, 1, []int{5, 6}); !ints(got, []int{1, 2}) {
		t.Fatalf("mismatched fallback keeps the prefix: %v", got)
	}
	if got := repairedPlan(nil, 0, []int{5, 6}); !ints(got, []int{5, 6}) {
		t.Fatalf("empty path takes the fallback: %v", got)
	}
}

func TestFetchLinePrunesAndToleratesMissingNames(t *testing.T) {
	// CM:388: a root outside 1…649 falls back to a single node; API:111-115 names may be absent.
	f := &fakeData{lineFn: func(b int) (*Line, error) { return &Line{BaseID: b, Tree: node(700, node(701)), CaptureRate: 45}, nil }}
	s := newStore(t, f, 7)
	l, err := s.fetchLine(bg, 5)
	if err != nil || l.tree.ID != 5 || len(l.tree.Children) != 0 || l.names == nil || l.rarity != rarityRare {
		t.Fatalf("line %+v %v", l, err)
	}
	if p, ok := prune(node(133, node(134), node(700))); !ok || len(p.Children) != 1 {
		t.Fatal("Eevee keeps 134 and loses 700")
	}
}

func TestRESTFallbackSkipsDittoAndGivesUp(t *testing.T) {
	// CS:1942-1963 and API:239: Ditto is never fetched; 16 non-base tries give up.
	var seed uint64
	for seed = 0; seed < 100000 && draws(seed, 1)[0]%maxSpeciesID+1 != dittoSpeciesID; seed++ {
	}
	calls := 0
	f := &fakeData{indexErr: errOffline, speciesFn: func(id int) (*Species, error) {
		calls++
		if id == dittoSpeciesID {
			t.Error("Ditto fetched")
		}
		return &Species{ID: id, EvolvesFrom: 1}, nil
	}}
	st := freshState()
	st.EggUsage = eggHatchThreshold
	s := openAt(t, writeState(t, st), f, seed)
	s.hatchIfNeeded(bg)
	eq(t, calls, 15, "16 tries, the Ditto one without a request")
	eq(t, s.hatchDelayed, true, "delayed")
	// A network error stops at once.
	f.speciesFn = func(int) (*Species, error) { return nil, errOffline }
	_, ok := s.chooseBaseViaREST(bg)
	eq(t, ok, false, "network error")
}

func TestPrefetchFailuresKeepTheEgg(t *testing.T) {
	// CS:1635-1661: a failed species roll or line fetch leaves the egg and retries next tick.
	st := freshState()
	st.InstallBaselineSet, st.LastDate = true, "d1"
	f := &fakeData{indexErr: errOffline, speciesFn: func(int) (*Species, error) { return nil, errOffline }}
	s := openAt(t, writeState(t, st), f, 7)
	use(s, 10)
	if s.st.PendingHatchID != nil {
		t.Fatal("pending without a roll")
	}
	f.indexErr, f.index = nil, []BaseEntry{{77, 255}}
	use(s, 20)
	eq(t, *s.st.PendingHatchID, 77, "rolled")
	eq(t, s.prefetchedLineID, 0, "line fetch failed")
	f.lineFn = func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }
	use(s, 30)
	eq(t, s.prefetchedLineID, 77, "retried")
	use(s, 40)
	eq(t, f.lineCalls, 2, "warm line is not refetched")
}

func TestOpenOptionsAndFailures(t *testing.T) {
	if _, err := Open(Options{Dir: t.TempDir()}); err == nil {
		t.Fatal("nil Data accepted")
	}
	var logged []string
	s, err := Open(Options{Dir: writeSave(t, "garbage"), Data: &fakeData{}, Logf: func(f string, a ...any) { logged = append(logged, f) }})
	if err != nil || s.rng == nil || s.now == nil {
		t.Fatal("defaults")
	}
	_ = s.rng.Next() // crypto source
	if len(logged) == 0 {
		t.Fatal("the corrupt move was not logged")
	}
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := Open(Options{Dir: file, Data: &fakeData{}}); err == nil {
		t.Fatal("a file as state dir")
	}
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, stateFileName), 0o700)
	if _, err := Open(Options{Dir: dir, Data: &fakeData{}}); err == nil {
		t.Fatal("an unreadable save must fail Open, not start fresh")
	}
}

func TestDifficultyEdges(t *testing.T) {
	// CS:128: the same value is a no-op; CS:144: a complete egg keeps its ratio (it may exceed the threshold).
	s := diffStore(t, 1.0)
	s.st.EggUsage = 6_000_000
	s.SetGrowthDifficulty(1.0)
	eq(t, s.st.EggUsage, 6_000_000, "no-op")
	s.SetGrowthDifficulty(2.0)
	eq(t, s.st.EggUsage, 12_000_000, "ready egg keeps its ratio")
}

func TestUpdateWarmsDetailsAfterEvolving(t *testing.T) {
	// CS:1040-1044: an evolution loads the new species' details.
	f := stub(linear3())
	f.detailsFn = func(id int) (*Details, error) { return &Details{SpeciesID: id, GenderRate: -1}, nil }
	s := newStore(t, f, 7)
	use(s, 0)
	s.hatch(bg, 1)
	f.detailIDs = nil
	use(s, 125_000_000)
	eq(t, s.st.Active.currentID(), 2, "evolved")
	if !ints(f.detailIDs, []int{2}) {
		t.Fatalf("details %v", f.detailIDs)
	}
	if v := s.BuildState(MoodInput{Now: fixedNow}); len(v.Active.Types) != 0 {
		t.Fatal("no types without names")
	}
}

func TestMistypedFieldsFollowUpstreamDecodeRules(t *testing.T) {
	// CM:522-549 and CM:610-630: a mistyped required or decodeIfPresent field drops the record; a
	// corrupt profile only drops the profile (CM:543, CM:621).
	mon := map[string]string{"baseID": `1`, "pathIDs": `[1]`, "plannedPathIDs": `[1]`, "stageIndex": `0`, "usedAtStage": `0`,
		"rarity": `"common"`, "totalForms": `1`, "isShiny": `false`, "nature": `"bold"`, "hasGrowthBoost": `false`,
		"dittoDisguise": `1`, "dittoRevealed": `false`}
	build := func(m map[string]string, key, val string) string {
		parts := []string{`"` + key + `":` + val}
		for k, v := range m {
			if k != key {
				parts = append(parts, `"`+k+`":`+v)
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	if _, err := decodeMon([]byte(build(mon, "isShiny", `true`))); err != nil {
		t.Fatalf("valid active rejected: %v", err)
	}
	for key := range mon {
		if _, err := decodeMon([]byte(build(mon, key, `{}`))); err == nil {
			t.Errorf("active.%s mistyped accepted", key)
		}
	}
	if _, err := decodeMon([]byte(build(mon, "nature", `"x"`))); err == nil {
		t.Error("unknown nature accepted")
	}
	if _, err := decodeMon([]byte(`[]`)); err == nil {
		t.Error("non-object active")
	}
	dex := map[string]string{"id": `"e"`, "baseID": `1`, "finalID": `1`, "chainOrder": `[1]`, "rarity": `"common"`,
		"caughtAt": `"2026-01-01T00:00:00Z"`, "isShiny": `false`, "nature": `"bold"`, "releasedAt": `"2026-01-01T00:00:00Z"`}
	for key := range dex {
		if _, err := decodeDexEntry([]byte(build(dex, key, `{}`))); err == nil {
			t.Errorf("dex.%s mistyped accepted", key)
		}
	}
	if _, err := decodeDexEntry([]byte(`1`)); err == nil {
		t.Error("non-object entry")
	}
	if e, err := decodeDexEntry([]byte(build(dex, "names", `{"1":{"en":"P1"}}`))); err != nil || e.Names[1]["en"] != "P1" {
		t.Error("names")
	}
	good := generateProfile(1, 0, "p")
	good.Gender, good.AbilitySlot, good.AbilityName = sp("male"), intPtr(1), sp("x")
	raw := strings.TrimSuffix(jsonOf(good), "}")
	if decodeProfileField(jsonObject{"profile": []byte(raw + "}")}) == nil {
		t.Fatal("valid profile rejected")
	}
	for _, field := range []string{`"instanceID":1`, `"seed":"x"`, `"gender":"robot"`, `"ivs":{"hp":1}`, `"ivs":3`,
		`"abilitySlot":"x"`, `"abilityName":1`, `"abilityIsHidden":1`, `"level":"x"`, `"growthTokens":"x"`, `"moves":1`} {
		if decodeProfileField(jsonObject{"profile": []byte(raw + "," + field + "}")}) != nil {
			t.Errorf("profile with %s accepted", field)
		}
	}
}

func TestPersistenceFailures(t *testing.T) {
	// A failed save is reported, not fatal (protocol §11.3 saveWrite).
	s := newStore(t, stub(linear3()), 7)
	_ = os.RemoveAll(s.dir)
	use(s, 0)
	if s.SaveError() == nil {
		t.Fatal("save error not reported")
	}
	_ = os.MkdirAll(s.dir, 0o700)
	use(s, 1)
	if s.SaveError() != nil {
		t.Fatal("recovered save still reports an error")
	}
	// A rename onto a non-empty directory fails and removes the temp file.
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	_ = os.MkdirAll(filepath.Join(target, "x"), 0o700)
	if writeFileAtomic(target, []byte("x")) == nil {
		t.Fatal("rename onto a directory")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("temp file left behind")
	}
	// The .corrupt slot is blocked: the original stays and the state starts fresh.
	bad := writeSave(t, "garbage")
	_ = os.MkdirAll(filepath.Join(bad, stateFileName+".corrupt", "x"), 0o700)
	c := openAt(t, bad, &fakeData{}, 7)
	if c.st.UsedSinceInstall != 0 {
		t.Fatal("not fresh")
	}
	// A blocked snapshot directory is logged and skipped.
	var logged []string
	b := openAt(t, writeState(t, sampleState(5, 1)), &fakeData{}, 7)
	b.logFn = func(f string, _ ...any) { logged = append(logged, f) }
	_ = os.MkdirAll(filepath.Join(b.dir, ".snapshots"), 0o700)
	_ = os.WriteFile(b.snapshotDir(), nil, 0o600)
	b.autoSnapshotIfNeeded(fixedNow)
	if len(logged) == 0 {
		t.Fatal("snapshot failure not logged")
	}
}

func TestSnapshotListingSkipsForeignFiles(t *testing.T) {
	s := openAt(t, writeState(t, sampleState(5, 1)), &fakeData{}, 7)
	_ = s.createSnapshot(fixedNow)
	d := s.snapshotDir()
	_ = os.WriteFile(filepath.Join(d, "notes.txt"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(d, snapshotPrefix+"a.json"), []byte(`{"format":"poketokenbar.save","schema":2,"exportedAt":"yesterday","state":{}}`), 0o600)
	_ = os.WriteFile(filepath.Join(d, snapshotPrefix+"b.json"), []byte(`{"format":"poketokenbar.save","schema":2,"exportedAt":"2026-01-01T00:00:00Z","state":[]}`), 0o600)
	_ = os.WriteFile(filepath.Join(d, snapshotPrefix+"c.json"), []byte(`{"format":"poketokenbar.save","schema":3,"exportedAt":"2026-01-01T00:00:00Z","state":{}}`), 0o600)
	_ = os.Mkdir(filepath.Join(d, snapshotPrefix+"d.json"), 0o700)
	eq(t, len(s.listSnapshots()), 1, "only the valid envelope")
}

func TestNamesEdges(t *testing.T) {
	_, ok := resolveName(map[string]string{"de": "Bisasam"}, "fr")
	eq(t, ok, false, "no candidate language")
	v, _ := resolveName(map[string]string{"ja-Hrkt": " フシギダネ ", "ja": "不思議種"}, "ja")
	eq(t, v, "フシギダネ", "bare ja prefers ja-hrkt; mixed-case codes and spaces normalized")
	v, _ = resolveName(map[string]string{"pt-BR": "B", "en": "E"}, "pt_PT")
	eq(t, v, "B", "pt falls back to pt-br")
	eq(t, fold("Flabébé"), "flabebe", "decomposed accents")
	eq(t, fold("Straße Æ Œ"), "strasse ae oe", "ligatures")
	eq(t, appLanguage("it-IT"), "en", "unsupported save language")
	eq(t, identifierLabel("blue-striped"), "Blue Striped", "identifier")
}

func TestLogAndDexSortEdges(t *testing.T) {
	st := freshState()
	st.Language = "en"
	old, nocatch, chainNamed := entry(5, 5, []int{5}, rarityUncommon), entry(6, 6, []int{6}, rarityRare), entry(7, 8, []int{7, 8}, rarityCommon)
	old.ID, old.CaughtAt = "b", timePtr(fixedNow.Add(-time.Hour))
	nocatch.ID, nocatch.CaughtAt = "a", nil
	chainNamed.ID = "c"
	chainNamed.Names = map[int]map[string]string{7: {"en": "Zeta"}} // the final stage has no name
	st.Dex = []dexEntry{old, nocatch, chainNamed}
	st.Active = &monState{BaseID: 9, PathIDs: []int{9}, PlannedPathIDs: []int{9}, Rarity: rarityCommon, TotalForms: 1}
	f := &fakeData{lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
	s := openAt(t, writeState(t, st), f, 7)
	ids := func(q CatchLogQuery) string {
		var out []string
		for _, e := range s.CatchLog(q).Entries {
			out = append(out, e.ID)
		}
		return strings.Join(out, ",")
	}
	eq(t, ids(CatchLogQuery{}), "active-9-9,c,b,a", "recent first; nil caughtAt last")
	eq(t, ids(CatchLogQuery{Sort: "oldestFirst"}), "a,b,c,active-9-9", "oldest first; active last")
	eq(t, ids(CatchLogQuery{Sort: "numberDesc"}), "active-9-9,c,a,b", "number desc")
	eq(t, ids(CatchLogQuery{Sort: "nameAsc"}), "b,a,active-9-9,c", "#5 < #6 < #9 < Zeta")
	eq(t, ids(CatchLogQuery{Sort: "nameDesc"}), "c,active-9-9,a,b", "name desc")
	eq(t, ids(CatchLogQuery{Sort: "rarityDesc"}), "a,b,c,active-9-9", "rarity desc, then newest")
	eq(t, ids(CatchLogQuery{Rarity: sp("rare")}), "a", "rarity filter")
	eq(t, ids(CatchLogQuery{ShinyOnly: true}), "", "shiny filter")
	eq(t, ids(CatchLogQuery{Query: "zet"}), "c", "chain names match")
	eq(t, ids(CatchLogQuery{Offset: 3, Limit: 5}), "a", "paging")
	eq(t, s.CatchLog(CatchLogQuery{}).Entries[1].DisplayName, "Zeta", "name falls back to an earlier stage")
	eq(t, s.CatchLog(CatchLogQuery{}).RarityCounts.Uncommon, 1, "uncommon count")
	// The line cache supplies names and rarity of an unowned species seen in a loaded line.
	use(s, 0)
	s.lines[400] = &evoLine{baseID: 400, tree: node(400, node(401)), rarity: rarityRare,
		names: map[int]map[string]string{401: {"en": "Bibarel"}}}
	v := s.Species(bg, 401)
	eq(t, v.DisplayName, "Bibarel", "cached line name")
	eq(t, deref(v.Rarity), "rare", "cached line rarity")
	eq(t, v.Owned, false, "unowned")
	f.speciesFn = func(id int) (*Species, error) {
		return &Species{ID: id, Names: map[string]string{"en": "Kricketot"}}, nil
	}
	eq(t, s.Species(bg, 402).DisplayName, "Kricketot", "REST species name")
	// Name sort ties fall back to the number; abilities list by slot.
	st2 := freshState()
	st2.Dex = []dexEntry{entry(2, 2, []int{2}, rarityCommon, withNames(map[int]map[string]string{2: {"en": "Same"}})),
		entry(1, 1, []int{1}, rarityCommon, withNames(map[int]map[string]string{1: {"en": "Same"}}))}
	f2 := &fakeData{detailsFn: func(id int) (*Details, error) {
		return &Details{SpeciesID: id, Abilities: []Ability{{"b", 2, false}, {"a", 1, false}}}, nil
	}}
	s2 := openAt(t, writeState(t, st2), f2, 7)
	if c := s2.DexPage(DexQuery{Sort: "nameAsc"}).Cells; c[0].SpeciesID != 1 {
		t.Fatal("tie by number")
	}
	if a := s2.Species(bg, 1).Details.Abilities; a[0].Name != "a" {
		t.Fatalf("abilities %+v", a)
	}
}

func TestCandyEpochBookkeepingBelow100(t *testing.T) {
	// CS:1471-1476: a reading below 100 % stores its epoch, and an epoch change alone is persisted.
	tier, epoch := map[string]int{}, map[string]string{}
	EvaluateCandyGrants([]CandyWindow{w("k", CandySession, 40, "E1")}, tier, epoch)
	eq(t, epoch["k"], "E1", "stored")
	dir := t.TempDir()
	s := openAt(t, dir, &fakeData{}, 7)
	s.GrantCandies(nil, true)
	s.GrantCandies([]CandyWindow{w("k", CandySession, 40, "E1")}, true)
	s.GrantCandies([]CandyWindow{w("k", CandySession, 40, "E2")}, true)
	eq(t, openAt(t, dir, &fakeData{}, 7).st.CandyWindowEpoch["k"], "E2", "epoch change persisted")
}

func TestNameCacheAndOldestFirstTies(t *testing.T) {
	calls := 0
	f := &fakeData{namesFn: func(kind, name string) (map[string]string, error) {
		calls++
		return map[string]string{"en": "Normal"}, nil
	}}
	s := newStore(t, f, 7)
	s.warmNames(bg, "type", []string{"normal"})
	s.warmNames(bg, "type", []string{"normal"})
	eq(t, calls, 1, "fetched names are cached")
	eq(t, s.apiName("type", "normal"), "Normal", "resolved")
	// CS:729-735: equal caughtAt falls back to the id.
	st := freshState()
	a, b := entry(1, 1, []int{1}, rarityCommon), entry(2, 2, []int{2}, rarityCommon)
	a.ID, b.ID = "y", "x"
	st.Dex = []dexEntry{a, b}
	o := openAt(t, writeState(t, st), &fakeData{}, 7)
	eq(t, o.CatchLog(CatchLogQuery{Sort: "oldestFirst"}).Entries[0].ID, "x", "tie by id")
}

func TestSnapshotWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := openAt(t, writeState(t, sampleState(5, 1)), &fakeData{}, 7)
	_ = os.MkdirAll(s.snapshotDir(), 0o700)
	_ = os.Chmod(s.snapshotDir(), 0o500)
	defer os.Chmod(s.snapshotDir(), 0o700)
	if s.createSnapshot(fixedNow) == nil {
		t.Fatal("write into a read-only snapshot directory")
	}
}
