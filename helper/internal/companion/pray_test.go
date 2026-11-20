package companion

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Prayers (docs/spec/prayers-ditto.md §1, §3.1). The Swift oracle is EggPrayerTests.swift and
// PetEventLineTests.swift.

// hatchReadyEgg mirrors EggPrayerTests.swift:25-36: an egg at the hatch threshold; pending means the
// species (base 1) is already rolled.
func hatchReadyEgg(t *testing.T, seed uint64, pending bool) *Store {
	t.Helper()
	extra := ""
	if pending {
		extra = `,"pendingHatchID":1`
	}
	json := fmt.Sprintf(`{"installBaselineSet":true,"usedSinceInstall":10000000,"spentTokens":0,"lastDate":"d","active":null,"dex":[],"collectedFinals":[],"eggUsage":%d%s}`,
		eggHatchThreshold, extra)
	l := makeLine(1, node(1, node(2, node(3))), rarityCommon)
	l.Names = map[int]map[string]string{1: {"en": "P1"}, 2: {"en": "P2"}, 3: {"en": "P3"}}
	return openAt(t, writeSave(t, json), stub(l), seed)
}

// activeStoreWithWallet mirrors EggPrayerTests.swift:38-47 (offline provider).
func activeStoreWithWallet(t *testing.T) *Store {
	t.Helper()
	m := `{"baseID":10,"pathIDs":[10],"stageIndex":0,"usedAtStage":200000000,"rarity":"common","totalForms":3,"isShiny":false}`
	return openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, m, "")), noData(), 7)
}

// rollSignature is the hatched individual minus the fields prayers may differ in (EggPrayerTests.swift:49-57).
func rollSignature(t *testing.T, s *Store) string {
	t.Helper()
	m := *s.st.Active
	m.Prayers = nil
	m.Profile = m.Profile.clone()
	m.Profile.InstanceID = ""
	b, err := json.Marshal(m) // struct fields marshal in a fixed order
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrayCounts(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:61-65
	s := hatchReadyEgg(t, 7, true)
	for want := 1; want <= 3; want++ {
		n, err := s.Pray()
		if err != nil {
			t.Fatal(err)
		}
		eq(t, n, want, "reply count")
	}
	if p := s.st.EggPrayers; p == nil || p.Count != 3 {
		t.Fatalf("eggPrayers = %+v", p)
	}
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Egg.Prayers, 3, "egg.prayers")
	// Derived (protocol §7.6): the helper saves on every prayer.
	raw, err := os.ReadFile(filepath.Join(s.dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := decodeState(raw); st.EggPrayers == nil || st.EggPrayers.Count != 3 {
		t.Fatalf("saved eggPrayers = %+v", st.EggPrayers)
	}
}

func TestPrayIsRefusedWhileRaisingAPokemon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:67-71 (upstream: a silent no-op; Go: notEgg).
	s := activeStoreWithWallet(t)
	n, err := s.Pray()
	eq(t, errCode(err), "notEgg", "error code")
	eq(t, n, 0, "count")
	if s.st.EggPrayers != nil {
		t.Fatal("prayers counted while a Pokémon is active")
	}
}

func TestPrayersDoNotChangeTheHatchResult(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:75-86
	for _, pending := range []bool{true, false} {
		plain := hatchReadyEgg(t, 7, pending)
		prayed := hatchReadyEgg(t, 7, pending)
		for i := 0; i < 50; i++ {
			if _, err := prayed.Pray(); err != nil {
				t.Fatal(err)
			}
		}
		plain.hatchIfNeeded(bg)
		prayed.hatchIfNeeded(bg)
		if plain.st.Active == nil || prayed.st.Active == nil {
			t.Fatalf("pending=%v: no hatch", pending)
		}
		eq(t, rollSignature(t, prayed), rollSignature(t, plain), fmt.Sprintf("pending=%v: roll", pending))
	}
}

func TestHatchCopiesPrayersAndClearsEggScope(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:90-96
	s := hatchReadyEgg(t, 7, true)
	_, _ = s.Pray()
	_, _ = s.Pray()
	s.hatchIfNeeded(bg)
	if p := s.st.Active.Prayers; p == nil || p.Count != 2 {
		t.Fatalf("active.prayers = %+v", p)
	}
	if s.st.EggPrayers != nil {
		t.Fatal("eggPrayers kept")
	}
	// Derived (protocol §6): the hatch event carries the count.
	evs := s.drain()
	if len(evs) != 1 || evs[0].(HatchEvent).Prayers != 2 {
		t.Fatalf("events = %+v", evs)
	}
	// And an unprayed hatch sends 0.
	plain := hatchReadyEgg(t, 7, true)
	plain.hatchIfNeeded(bg)
	eq(t, plain.drain()[0].(HatchEvent).Prayers, 0, "no prayers")
}

func TestGraduationKeepsPrayersOnTheDexEntryAndStartsAPlainEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:98-108
	s := hatchReadyEgg(t, 7, true)
	_, _ = s.Pray()
	s.hatchIfNeeded(bg)
	for stage := 0; stage < 3; stage++ {
		s.applyUsage(phase(rarityCommon, 3, stage))
	}
	if s.st.Active != nil {
		t.Fatal("not graduated")
	}
	if p := s.st.Dex[0].Prayers; p == nil || p.Count != 1 {
		t.Fatalf("dex[0].prayers = %+v", p)
	}
	if s.st.EggPrayers != nil {
		t.Fatal("the next egg must start without prayers")
	}
	// Derived (protocol §7.2): the graduated individual carries the count.
	eq(t, s.Species(bg, 3).Individuals[0].Prayers, 1, "Individual.prayers")
}

func TestSyntheticActiveEntryCarriesThePrayers(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:118-129 (the synthetic-entry half; the
	// release half is TestReleasedEntryKeepsThePrayersOfTheIndividual).
	m := `{"baseID":10,"pathIDs":[10],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"prayers":{"count":4}}`
	s := openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, m, "")), noData(), 7)
	entries := s.logEntries()
	if p := entries[len(entries)-1].Prayers; !entries[len(entries)-1].active || p == nil || p.Count != 4 {
		t.Fatalf("active entry prayers = %+v", p)
	}
	ind := s.Species(bg, 10).Individuals
	if len(ind) != 1 || !ind[0].IsRaising || ind[0].Prayers != 4 {
		t.Fatalf("individuals = %+v", ind)
	}
	// An individual hatched without prayers sends 0.
	plain := openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, mon10(false), "")), noData(), 7)
	eq(t, plain.Species(bg, 10).Individuals[0].Prayers, 0, "no prayers")
}

func TestSanitizeDropsEggPrayersBesideAnActivePokemon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:131-142 (the Int.max clamp is
	// TestExtremeValuesAreClampedOnLoad).
	st := freshState()
	st.Active = &monState{BaseID: 4, PathIDs: []int{4}, PlannedPathIDs: []int{4}, Rarity: rarityCommon, TotalForms: 1}
	st.EggPrayers = &prayerRecord{Count: 9}
	st.sanitize()
	if st.EggPrayers != nil {
		t.Fatal("egg prayers kept beside an active Pokémon")
	}
	egg := freshState()
	egg.EggPrayers = &prayerRecord{Count: math.MaxInt}
	egg.sanitize()
	eq(t, egg.EggPrayers.Count, maxTokenValue, "clamped")
}

func TestPrayerFieldsDecode(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:146-187
	mon := `{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":1,"prayers":%s}`
	entry := `{"id":"e","baseID":1,"finalID":1,"chainOrder":[1],"rarity":"common","prayers":%s}`
	// Round trip of a MonState's prayers (the state and dex fields: TestFullSaveRoundTripKeepsDeferredFields).
	st := freshState()
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1}, Rarity: rarityCommon, TotalForms: 1, Prayers: &prayerRecord{Count: 2}}
	data, err := encodeState(&st)
	if err != nil {
		t.Fatal(err)
	}
	if back, _ := decodeState(data); back.Active.Prayers == nil || back.Active.Prayers.Count != 2 {
		t.Fatalf("active prayers = %+v", back.Active.Prayers)
	}
	// Dev builds stored extra keys; those saves still decode.
	record := `{"count":7,"wasFutile":true,"rolledSpeciesID":1,"eggWasPurchased":true}`
	legacy, _ := decodeState([]byte(`{"eggUsage":5,"eggWasPurchased":true,"eggPrayers":` + record + `}`))
	if legacy.EggPrayers == nil || legacy.EggPrayers.Count != 7 {
		t.Fatalf("legacy eggPrayers = %+v", legacy.EggPrayers)
	}
	withMon, _ := decodeState([]byte(`{"active":` + fmt.Sprintf(mon, record) + `,"dex":[` + fmt.Sprintf(entry, record) + `]}`))
	if p := withMon.Active.Prayers; p == nil || p.Count != 7 {
		t.Fatalf("legacy mon prayers = %+v", p)
	}
	if p := withMon.Dex[0].Prayers; p == nil || p.Count != 7 {
		t.Fatalf("legacy entry prayers = %+v", p)
	}
	// Old saves have none, and a damaged field does not take the save down.
	old, _ := decodeState([]byte(`{"eggUsage":5}`))
	if old.EggPrayers != nil {
		t.Fatal("old save prayers")
	}
	broken, ok := decodeState([]byte(`{"eggPrayers":"oops"}`))
	if !ok || broken.EggPrayers != nil {
		t.Fatal("a damaged eggPrayers must decode as nil")
	}
}

// shinyDisguisedDittoSeed: with the roll order shiny, nature, Ditto disguise, this seed hatches a
// shiny, disguised Ditto (PetEventLineTests.swift:112-114).
const shinyDisguisedDittoSeed = 2466

// dittoEventData mirrors PetEventLineTests.swift DittoEventProvider: Bulb → Ivy → Venu, common, and
// Ditto's rare leaf line for base 132.
func dittoEventData() *fakeData {
	ditto := &Line{BaseID: 132, Tree: node(132), CaptureRate: 45, Names: map[int]map[string]string{132: {"en": "Ditto"}}}
	bulb := &Line{BaseID: 1, Tree: node(1, node(2, node(3))), CaptureRate: 255,
		Names: map[int]map[string]string{1: {"en": "Bulb"}, 2: {"en": "Ivy"}, 3: {"en": "Venu"}}}
	return &fakeData{
		lineFn: func(b int) (*Line, error) {
			if b == dittoSpeciesID {
				return ditto, nil
			}
			return bulb, nil
		},
		index: []BaseEntry{{ID: 1, CaptureRate: 255}},
	}
}

func TestDisguisedDittoHatchEventShowsTheDisguiseAndHidesShiny(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PetEventLineTests.swift:125-140. The text is the extension's;
	// the helper's part is the hatch event it is built from.
	plain := newStore(t, dittoEventData(), shinyDisguisedDittoSeed, withDitto())
	plain.SetLanguage("en")
	plain.hatch(bg, 1)
	if plain.st.Active.DittoDisguise == nil || !plain.st.Active.IsShiny {
		t.Fatalf("seed %d must hatch a shiny disguise: %+v", shinyDisguisedDittoSeed, plain.st.Active)
	}
	want := HatchEvent{Type: "event", Kind: "hatch", SpeciesID: 1, DisplayName: "Bulb", Rarity: rarityCommon, ShinyOdds: 64}
	eq(t, plain.drain()[0].(HatchEvent), want, "plain hatch")

	prayed := newStore(t, dittoEventData(), shinyDisguisedDittoSeed, withDitto())
	prayed.SetLanguage("en")
	_, _ = prayed.Pray()
	_, _ = prayed.Pray()
	prayed.hatch(bg, 1)
	want.Prayers = 2
	eq(t, prayed.drain()[0].(HatchEvent), want, "prayed hatch: no Ditto, no shiny")
}
