package companion

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Ditto disguise reveal (docs/spec/prayers-ditto.md §2, §3.3). The Swift oracle is DittoTests.swift.
// Not ported: DittoTests.swift:278-318 (DelayedDittoProvider). It tests upstream's async race guard;
// the Go Store is synchronous, so a buyEgg cannot land between the line 132 fetch and the swap.

func dittoLine() *Line { return makeLine(dittoSpeciesID, node(dittoSpeciesID), rarityRare) }

// dittoData mirrors DittoTestProvider (DittoTests.swift:19-35): base 132 is Ditto's rare leaf line,
// every other base the disguise line; details are Bulbasaur's or Ditto's.
func dittoData(disguise *Line) *fakeData {
	ditto := dittoLine()
	return &fakeData{
		lineFn: func(b int) (*Line, error) {
			if b == dittoSpeciesID {
				return ditto, nil
			}
			return disguise, nil
		},
		index: []BaseEntry{{ID: disguise.BaseID, CaptureRate: 255}},
		detailsFn: func(id int) (*Details, error) {
			if id == dittoSpeciesID {
				return &Details{SpeciesID: id, Height: 3, Weight: 40, GenderRate: -1, Types: []string{"normal"},
					BaseStats: map[string]int{"hp": 48}, Abilities: []Ability{{"limber", 1, false}},
					Moves: []Move{{"transform", []LearnMethod{{"level-up", 1}}}}}, nil
			}
			return &Details{SpeciesID: id, Height: 7, Weight: 69, GenderRate: 1, Types: []string{"grass"},
				BaseStats: map[string]int{"hp": 45}, Abilities: []Ability{{"overgrow", 1, false}},
				Moves: []Move{{"tackle", []LearnMethod{{"level-up", 1}}}}}, nil
		},
	}
}

type disguiseSeed struct {
	used                      int
	shiny, revealed, boosted  bool
	finals                    []string
	difficulty                float64 // 0 means 1.0
	installBaselineNotYetSeen bool
}

// seedDisguise mirrors DittoTests.swift:104-117: a disguised Ditto showing as base 1 of the common
// three-form line 1 → 2 → 3 (reveal threshold 125M at difficulty 1.0), with the line not loaded.
func seedDisguise(t *testing.T, o disguiseSeed, data DataSource) *Store {
	t.Helper()
	finals, _ := json.Marshal(append([]string{}, o.finals...))
	diff := o.difficulty
	if diff == 0 {
		diff = 1
	}
	js := fmt.Sprintf(`{"installBaselineSet":%t,"usedSinceInstall":1000000000,"spentTokens":0,"lastDate":"d1","growthDifficulty":%v,`+
		`"active":{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":%d,"rarity":"common","totalForms":3,"isShiny":%t,`+
		`"hasGrowthBoost":%t,"dittoDisguise":1,"dittoRevealed":%t},"dex":[],"collectedFinals":%s}`,
		!o.installBaselineNotYetSeen, diff, o.used, o.shiny, o.boosted, o.revealed, finals)
	if data == nil {
		data = dittoData(linear3())
	}
	return openAt(t, writeSave(t, js), data, 7)
}

// drainReveal is DittoTests.swift:119-123: one update with no new usage.
func drainReveal(s *Store) []Event { return use(s, 0) }

func revealEvents(evs []Event) []DittoRevealEvent {
	var out []DittoRevealEvent
	for _, e := range evs {
		if r, ok := e.(DittoRevealEvent); ok {
			out = append(out, r)
		}
	}
	return out
}

func TestRevealAtFirstEvolution(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:130-149
	s := seedDisguise(t, disguiseSeed{}, nil)
	s.applyUsage(300_000_000) // banked while the line is not loaded
	eq(t, s.st.Active.UsedAtStage, 300_000_000, "banked")
	eq(t, s.st.Active.currentID(), 1, "still the disguise")
	eq(t, s.st.Active.DittoRevealed, false, "not revealed yet")
	evs := drainReveal(s)
	a := s.st.Active
	eq(t, a.DittoRevealed, true, "revealed at the first evolution threshold")
	eq(t, a.BaseID, dittoSpeciesID, "base")
	eq(t, a.currentID(), dittoSpeciesID, "current species, never 2")
	eq(t, a.Rarity, rarityRare, "rarity")
	eq(t, a.TotalForms, 1, "totalForms")
	eq(t, a.StageIndex, 0, "stage")
	if !ints(a.PathIDs, []int{132}) || !ints(a.PlannedPathIDs, []int{132}) {
		t.Fatalf("paths %v %v", a.PathIDs, a.PlannedPathIDs)
	}
	eq(t, a.UsedAtStage, 300_000_000-125_000_000, "the excess carries over")
	if a.DittoDisguise == nil || *a.DittoDisguise != 1 {
		t.Fatal("the disguise marker is kept")
	}
	r := revealEvents(evs)
	if len(r) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	eq(t, r[0].IsShiny, false, "reveal event shiny")
	// Derived (protocol §6): the event's other fields.
	eq(t, r[0], DittoRevealEvent{Type: "event", Kind: "dittoReveal", DisguiseSpeciesID: 1, DisguiseDisplayName: "P1",
		SpeciesID: 132, DisplayName: "P132", Rarity: rarityRare}, "event")
	for _, e := range evs {
		if e.EventKind() == "evolve" {
			t.Fatal("a disguise never evolves")
		}
	}
}

func TestRevealRebasesDisguiseProfileAndLoadsDittoDetails(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:151-171
	s := seedDisguise(t, disguiseSeed{}, nil)
	if _, err := s.loadDetails(bg, 1); err != nil {
		t.Fatal(err)
	}
	p := s.st.Active.Profile
	if p.Gender == nil || *p.Gender == genderGenderless || deref(p.AbilityName) != "overgrow" || len(p.Moves) != 1 || p.Moves[0].Name != "tackle" {
		t.Fatalf("disguise profile %+v", p)
	}
	s.applyUsage(300_000_000)
	drainReveal(s)
	p = s.st.Active.Profile
	eq(t, deref(p.Gender), genderGenderless, "gender")
	eq(t, deref(p.AbilityName), "limber", "ability")
	if len(p.Moves) != 1 || p.Moves[0].Name != "transform" {
		t.Fatalf("moves %+v", p.Moves)
	}
	eq(t, p.GrowthTokens, 500_000_000, "growth")
	eq(t, p.Level, 20, "revealing Ditto preserves earned phase progress")
}

func TestBoostedEasyDittoRetainsLevelAndIdentityAcrossReveal(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:173-200
	s := seedDisguise(t, disguiseSeed{boosted: true}, nil)
	s.SetGrowthDifficulty(0.1)
	if _, err := s.loadDetails(bg, 1); err != nil {
		t.Fatal(err)
	}
	initial := s.st.Active.Profile.clone()
	s.applyUsage(7_250_000) // 6.25M reveal threshold plus 1M raw carryover
	earnedLevel := s.st.Active.Profile.Level
	drainReveal(s)
	a := s.st.Active
	eq(t, a.UsedAtStage, 1_000_000, "carry")
	eq(t, a.HasGrowthBoost, false, "a new Ditto drops the disguise's boost but keeps its level")
	eq(t, a.Profile.InstanceID, initial.InstanceID, "instanceID")
	eq(t, a.Profile.IVs, initial.IVs, "IVs")
	eq(t, a.Profile.Level, earnedLevel, "level")
	eq(t, deref(a.Profile.Gender), genderGenderless, "gender")
	if len(a.Profile.Moves) != 1 || a.Profile.Moves[0].Name != "transform" {
		t.Fatalf("moves %+v", a.Profile.Moves)
	}
	s.applyUsage(s.stageThreshold(a) - a.UsedAtStage) // tokensToNext
	if s.st.Active != nil || len(s.st.Dex) != 1 {
		t.Fatal("not graduated")
	}
	eq(t, s.st.Dex[0].Profile.Level, 100, "graduation level")
}

func TestBoostedDisguiseRevealsAtTheHalvedThresholdAndANewDittoDropsTheBoost(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:202-213
	s := seedDisguise(t, disguiseSeed{boosted: true}, nil)
	s.applyUsage(62_500_000)
	drainReveal(s)
	a := s.st.Active
	eq(t, a.DittoRevealed, true, "revealed")
	eq(t, a.HasGrowthBoost, false, "boost")
	eq(t, a.UsedAtStage, 0, "carry")
	eq(t, a.standardThreshold(), 3_000_000_000, "phaseThreshold")
}

func TestRevealingAnAlreadyGraduatedDittoGrantsTheRepeatBoost(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:215-227
	s := seedDisguise(t, disguiseSeed{finals: []string{"132:132"}}, nil)
	s.applyUsage(125_000_000)
	drainReveal(s)
	a := s.st.Active
	eq(t, a.DittoRevealed, true, "revealed")
	eq(t, a.HasGrowthBoost, true, "boost")
	eq(t, a.standardThreshold(), 1_500_000_000, "phaseThreshold")
}

func TestRevealClearsRepresentativePinnedToDisguiseAndFollowsDitto(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:229-247
	s := seedDisguise(t, disguiseSeed{shiny: true}, nil)
	if !s.SetRepresentative(intPtr(1), nil) {
		t.Fatal("the reached disguise species can be pinned before the reveal")
	}
	subject := func() (int, bool) {
		m := s.BuildState(MoodInput{Now: fixedNow}).MenuSubject
		if m.SpeciesID == nil {
			return 0, m.Shiny
		}
		return *m.SpeciesID, m.Shiny
	}
	id, sh := subject()
	if id != 1 || sh {
		t.Fatalf("subject (%d, %v): the pinned sprite hides the shiny while disguised", id, sh)
	}
	s.applyUsage(300_000_000)
	drainReveal(s)
	eq(t, s.st.Active.DittoRevealed, true, "revealed")
	if s.st.RepresentativeSpeciesID != nil {
		t.Fatal("the pin on a species no longer owned is cleared")
	}
	id, sh = subject()
	if id != dittoSpeciesID || !sh {
		t.Fatalf("subject (%d, %v): follows the revealed shiny Ditto", id, sh)
	}
}

func TestPrunedLeafDisguiseRevealsBeforeGraduation(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:249-276
	thr := phase(rarityCommon, 1, 0)
	eq(t, thr, 750_000_000, "single-form threshold")
	pruned := makeLine(206, node(206, node(982)), rarityCommon)
	pruned.CaptureRate = 190
	ditto := dittoLine()
	data := &fakeData{
		lineFn: func(b int) (*Line, error) {
			if b == dittoSpeciesID {
				return ditto, nil
			}
			return pruned, nil
		},
		index: []BaseEntry{{ID: 206, CaptureRate: 190}},
	}
	js := fmt.Sprintf(`{"installBaselineSet":true,"usedSinceInstall":1000000000,"lastDate":"d1","active":{"baseID":206,"pathIDs":[206],`+
		`"plannedPathIDs":[206,982],"stageIndex":0,"usedAtStage":%d,"rarity":"common","totalForms":2,"isShiny":true,"nature":"timid",`+
		`"dittoDisguise":206,"dittoRevealed":false},"dex":[],"collectedFinals":[]}`, thr)
	s := openAt(t, writeSave(t, js), data, 7)
	drainReveal(s)
	if f, _ := s.fetchLine(bg, 206); len(f.tree.Children) != 0 {
		t.Fatal("#982 is pruned, so #206 is a leaf")
	}
	a := s.st.Active
	eq(t, a.BaseID, dittoSpeciesID, "base")
	if !ints(a.PathIDs, []int{132}) || !ints(a.PlannedPathIDs, []int{132}) {
		t.Fatalf("paths %v %v", a.PathIDs, a.PlannedPathIDs)
	}
	eq(t, a.UsedAtStage, 0, "only the excess over the normalized single-form threshold carries")
	eq(t, *a.DittoDisguise, 206, "disguise")
	eq(t, a.DittoRevealed, true, "revealed")
	eq(t, a.IsShiny, true, "shiny")
	eq(t, deref(a.Nature), "timid", "nature")
	for _, e := range s.st.Dex {
		if e.FinalID == 206 {
			t.Fatal("the disguise must not graduate")
		}
	}
	if s.st.CollectedFinals["206:206"] {
		t.Fatal("206:206 collected")
	}
}

func TestShinyUnmaskedAfterReveal(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:320-329
	s := seedDisguise(t, disguiseSeed{shiny: true}, nil)
	s.applyUsage(300_000_000)
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.IsShiny, false, "hidden before the reveal")
	r := revealEvents(drainReveal(s))
	eq(t, s.st.Active.DittoRevealed, true, "revealed")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.IsShiny, true, "shown after the reveal")
	if len(r) != 1 || !r[0].IsShiny {
		t.Fatalf("reveal events %+v", r)
	}
}

func TestNoRevealBelowThreshold(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:331-337
	s := seedDisguise(t, disguiseSeed{}, nil)
	s.applyUsage(100_000_000)
	evs := drainReveal(s)
	eq(t, s.st.Active.DittoRevealed, false, "below the threshold the disguise stays")
	eq(t, s.st.Active.currentID(), 1, "still the disguise")
	eq(t, len(revealEvents(evs)), 0, "no reveal event")
}

func TestCandyPickerMatchesApparentPokemonButOnlySpendsEnoughToReveal(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:339-374
	s := seedDisguise(t, disguiseSeed{used: 100_000_000}, nil)
	drainReveal(s)
	normal := newStore(t, dittoData(linear3()), 7)
	normal.hatch(bg, 1)
	normal.applyUsage(100_000_000)
	giveCandies(s, 5)
	giveCandies(normal, 5)
	eq(t, s.MaxCandyUse(), normal.MaxCandyUse(), "max")
	for n := 1; n <= 5; n++ {
		eq(t, mustPlan(t, s, n), mustPlan(t, normal, n), fmt.Sprintf("plan(%d)", n))
	}
	u, _, err := s.UseCandy(5)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, s.itemCount(itemRareCandy), 4, "stock")
	eq(t, u.XP, rareCandyXP, "xp")
	eq(t, u.Result, CandyProgressed, "the candy that reaches the reveal threshold progresses")
	eq(t, candy(t, s, 5), "notReady", "no spend while the reveal is pending")
	drainReveal(s)
	eq(t, s.st.Active.currentID(), dittoSpeciesID, "revealed")
	eq(t, s.st.Active.UsedAtStage, 75_000_000, "carry")
	eq(t, s.MaxCandyUse(), 4, "max after the reveal")
	eq(t, candy(t, s, 2), CandyProgressed, "use after the reveal")
	eq(t, s.st.Active.UsedAtStage, 275_000_000, "used")
	eq(t, s.itemCount(itemRareCandy), 2, "stock")
}

func TestApparentGraduationBatchPreservesUnusedCandiesAtDittoReveal(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:376-403
	for _, difficulty := range []float64{0.1, 1.0, 2.0} {
		for _, boosted := range []bool{false, true} {
			label := fmt.Sprintf("difficulty %v boosted %v", difficulty, boosted)
			s := seedDisguise(t, disguiseSeed{boosted: boosted, difficulty: difficulty}, nil)
			drainReveal(s)
			giveCandies(s, 20)
			thr := s.stageThreshold(s.st.Active)
			eq(t, mustPlan(t, s, 20).Graduates, true, label+": the preview uses the apparent path")
			consumed := (thr + rareCandyXP - 1) / rareCandyXP
			u, _, err := s.UseCandy(20)
			if err != nil {
				t.Fatal(err)
			}
			drainReveal(s)
			eq(t, s.st.Active.currentID(), dittoSpeciesID, label+": revealed")
			eq(t, s.st.Active.UsedAtStage, consumed*rareCandyXP-thr, label+": carry")
			eq(t, u.XP, consumed*rareCandyXP, label+": xp")
			eq(t, s.itemCount(itemRareCandy), 20-consumed, label+": stock")
			eq(t, len(s.st.Dex), 0, label+": dex")
		}
	}
}

func TestCandyBatchStopsExactlyAtRevealWithoutOverflow(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:405-417
	s := seedDisguise(t, disguiseSeed{used: 25_000_000}, nil)
	drainReveal(s)
	giveCandies(s, 5)
	if _, _, err := s.UseCandy(5); err != nil {
		t.Fatal(err)
	}
	drainReveal(s)
	eq(t, s.st.Active.currentID(), dittoSpeciesID, "revealed")
	eq(t, s.st.Active.UsedAtStage, 0, "used")
	eq(t, s.itemCount(itemRareCandy), 4, "stock")
}

func TestBackwardCompatDecodeNoDittoFields(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:419-431
	js := `{"installBaselineSet":true,"usedSinceInstall":0,"spentTokens":0,"lastDate":"d1","active":{"baseID":1,"pathIDs":[1],` +
		`"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"isShiny":true},"dex":[],"collectedFinals":[]}`
	s := openAt(t, writeSave(t, js), dittoData(linear3()), 7)
	if s.st.Active.DittoDisguise != nil || s.st.Active.DittoRevealed {
		t.Fatalf("%+v", s.st.Active)
	}
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.IsShiny, true, "not a disguise: the shiny shows")
}

func TestSpeciesIdentityRebaseKeepsIVsButClearsDisguiseMetadata(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:193-210
	p := generateProfile(7, 0, "ditto")
	p.enrich(slowpoke())
	ivs := p.IVs
	p.advanceGrowth(p.GrowthTokens+125_000_000, rarityCommon) // applyGrowth
	p.rebaseForSpeciesIdentity(rarityCommon, rarityRare)
	eq(t, p.InstanceID, "ditto", "instanceID")
	eq(t, p.IVs, ivs, "IVs")
	if p.Gender != nil || p.AbilitySlot != nil || p.AbilityName != nil || p.AbilityIsHidden || len(p.Moves) != 0 {
		t.Fatalf("species metadata kept: %+v", p)
	}
	eq(t, p.GrowthTokens, 500_000_000, "growth")
	eq(t, p.Level, 20, "level")
}

func TestRevealedDittoDropsTheDisguiseForm(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:274-296
	forms := formData()
	ditto := dittoLine()
	data := &fakeData{indexErr: errOffline, lineFn: func(b int) (*Line, error) {
		if b == dittoSpeciesID {
			return ditto, nil
		}
		return forms.lineFn(b)
	}}
	js := `{"installBaselineSet":true,"usedSinceInstall":1000000000,"lastDate":"d1","growthDifficulty":1,"dex":[],"collectedFinals":[],` +
		`"active":{"baseID":412,"pathIDs":[412],"stageIndex":0,"usedAtStage":900000000,"rarity":"common","totalForms":2,` +
		`"form":"sandy","dittoDisguise":412,"dittoRevealed":false}}`
	s := openAt(t, writeSave(t, js), data, 7)
	eq(t, deref(s.st.Active.Form), "sandy", "form before")
	evs := drainReveal(s)
	eq(t, s.st.Active.BaseID, dittoSpeciesID, "base")
	if s.st.Active.Form != nil || s.BuildState(MoodInput{Now: fixedNow}).Active.Form != nil {
		t.Fatal("the disguise form is dropped")
	}
	// Derived (protocol §6): the disguise name carries no form label.
	if r := revealEvents(evs); len(r) != 1 || r[0].DisguiseDisplayName != "Burmy" {
		t.Fatalf("reveal events %+v", r)
	}
}

func TestRevealEventReplacesTheHatchLineAndNeverShowsAnEvolution(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PetEventLineTests.swift:142-153. The callout text is the
	// extension's; the helper's part is the event it is built from.
	s := newStore(t, dittoEventData(), shinyDisguisedDittoSeed, withDitto())
	s.SetLanguage("en")
	s.hatch(bg, 1)
	s.drain()
	s.applyUsage(130_000_000) // past the first evolution threshold
	evs := drainReveal(s)
	eq(t, s.st.Active.DittoRevealed, true, "revealed")
	if len(evs) != 1 {
		t.Fatalf("events %+v", evs)
	}
	eq(t, evs[0].(DittoRevealEvent), DittoRevealEvent{Type: "event", Kind: "dittoReveal", DisguiseSpeciesID: 1,
		DisguiseDisplayName: "Bulb", SpeciesID: 132, DisplayName: "Ditto", Rarity: rarityRare, IsShiny: true}, "event")
}

// ---------------------------------------------------------------- Go-only (derived from the spec)

// Protocol §6: an immediate graduation from the carried XP adds a graduate after the reveal, and
// the graduation keeps the raw shiny (CS:1872-1873, CS:1123-1154).
func TestRevealThenGraduateInOneUpdate(t *testing.T) {
	s := seedDisguise(t, disguiseSeed{shiny: true, finals: []string{"132:132"}}, nil)
	s.applyUsage(125_000_000 + 1_500_000_000)
	evs := drainReveal(s)
	if len(evs) != 2 || evs[0].EventKind() != "dittoReveal" || evs[1].EventKind() != "graduate" {
		t.Fatalf("events %+v", evs)
	}
	g := evs[1].(GraduateEvent)
	eq(t, g.SpeciesID, dittoSpeciesID, "graduate species")
	eq(t, g.IsShiny, true, "graduate shiny")
	if s.st.Active != nil || len(s.st.Dex) != 1 || s.st.Dex[0].FinalID != dittoSpeciesID || s.st.Dex[0].BaseID != dittoSpeciesID {
		t.Fatalf("dex %+v", s.st.Dex)
	}
}

// Protocol §5.4: the reveal opens a 5 s levelUp window.
func TestRevealOpensAFiveSecondWindow(t *testing.T) {
	s := seedDisguise(t, disguiseSeed{used: 125_000_000}, nil)
	drainReveal(s)
	v := s.BuildState(MoodInput{Now: fixedNow})
	eq(t, deref(v.LevelUpUntil), *ts(fixedNow.Add(5 * time.Second)), "levelUpUntil")
	eq(t, v.Mood, "levelUp", "mood")
	eq(t, s.BuildState(MoodInput{Now: fixedNow.Add(5 * time.Second)}).LevelUpUntil == nil, true, "closed after 5 s")
}

// Decision 41: line 132 is loaded while a disguise is active, before the threshold; not for a
// normal Pokémon or a revealed Ditto.
func TestLine132IsPreWarmedOnlyWhileDisguised(t *testing.T) {
	calls := func(o disguiseSeed, disguised bool) int {
		f := dittoData(linear3())
		n := 0
		inner := f.lineFn
		f.lineFn = func(b int) (*Line, error) {
			if b == dittoSpeciesID {
				n++
			}
			return inner(b)
		}
		var s *Store
		if disguised {
			s = seedDisguise(t, o, f)
		} else {
			s = openAt(t, writeSave(t, `{"installBaselineSet":true,"lastDate":"d1","active":{"baseID":1,"pathIDs":[1],"stageIndex":0,`+
				`"usedAtStage":0,"rarity":"common","totalForms":3}}`), f, 7)
		}
		drainReveal(s)
		if disguised && !o.revealed && s.lines[dittoSpeciesID] == nil {
			t.Fatal("line 132 not in memory")
		}
		return n
	}
	eq(t, calls(disguiseSeed{}, true), 1, "unrevealed disguise below its threshold")
	eq(t, calls(disguiseSeed{revealed: true}, true), 0, "revealed")
	eq(t, calls(disguiseSeed{}, false), 0, "normal Pokémon")
}

// Protocol §5.4 and decision 42: while line 132 cannot be loaded, the reveal waits, the bag shows
// revealPending and candy is refused; the first update that gets the line reveals.
func TestRevealWaitsForLine132(t *testing.T) {
	f := dittoData(linear3())
	inner := f.lineFn
	offline := true
	f.lineFn = func(b int) (*Line, error) {
		if b == dittoSpeciesID && offline {
			return nil, errOffline
		}
		return inner(b)
	}
	s := seedDisguise(t, disguiseSeed{used: 125_000_000}, f)
	giveCandies(s, 1)
	evs := drainReveal(s)
	eq(t, s.st.Active.DittoRevealed, false, "no reveal offline")
	eq(t, len(revealEvents(evs)), 0, "no event")
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).Bag.CandyBlocked), candyBlockedRevealPending, "bag")
	eq(t, candy(t, s, 1), "notReady", "candy")
	offline = false
	eq(t, len(revealEvents(drainReveal(s))), 1, "revealed once the line loads")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Bag.CandyBlocked == nil, true, "bag unblocked")
}

// Upstream CS:880-887 keeps loading an imported Pokémon's line before any usage arrives; its
// applyUsage(0) can kick the reveal there too, so the no-baseline early return reveals as well.
func TestRevealBeforeTheInstallBaseline(t *testing.T) {
	s := seedDisguise(t, disguiseSeed{used: 125_000_000, installBaselineNotYetSeen: true}, nil)
	evs := s.Update(bg, UsageInput{})
	eq(t, s.st.InstallBaselineSet, false, "no baseline yet")
	eq(t, s.st.Active.DittoRevealed, true, "revealed")
	eq(t, len(revealEvents(evs)), 1, "event")
}

// CS:1874-1876: the reveal loads Ditto's details itself. With every name already loaded nothing
// else would retry them (warmPending is false), so the revealed card and profile would stay empty.
func TestRevealLoadsDittoDetailsWhenNothingElseIsPending(t *testing.T) {
	f := dittoData(linear3())
	f.namesFn = func(_, name string) (map[string]string, error) { return map[string]string{"en": name}, nil }
	s := seedDisguise(t, disguiseSeed{used: 125_000_000}, f)
	drainReveal(s)
	eq(t, s.st.Active.DittoRevealed, true, "revealed")
	eq(t, deref(s.st.Active.Profile.Gender), genderGenderless, "Ditto's details enriched the profile")
	eq(t, len(s.BuildState(MoodInput{Now: fixedNow}).Active.Types), 1, "Ditto's types on the card")
}
