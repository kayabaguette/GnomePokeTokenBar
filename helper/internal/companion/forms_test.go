package companion

import (
	"reflect"
	"strings"
	"testing"
)

func sp(v string) *string { return &v }

func formLine(base int, tree Node, rarity string, names map[int]map[string]string) *Line {
	l := makeLine(base, tree, rarity)
	l.Names = names
	return l
}

// formData mirrors FormTestProvider: Burmy, Shaymin, Shellos and Unown lines from memory, no index.
func formData() *fakeData {
	lines := map[int]*Line{
		412: formLine(412, node(412, node(413), node(414)), rarityCommon,
			map[int]map[string]string{412: {"en": "Burmy", "fr": "Cheniti"}, 413: {"en": "Wormadam"}, 414: {"en": "Mothim"}}),
		492: formLine(492, node(492), rarityLegendary, map[int]map[string]string{492: {"en": "Shaymin", "fr": "Shaymin"}}),
		422: formLine(422, node(422, node(423)), rarityCommon, map[int]map[string]string{422: {"en": "Shellos"}, 423: {"en": "Gastrodon"}}),
		201: formLine(201, node(201), rarityCommon, map[int]map[string]string{201: {"ko": "안농", "en": "Unown", "ja": "アンノーン"}}),
		25:  formLine(25, node(25), rarityCommon, map[int]map[string]string{25: {"en": "Pikachu"}}),
	}
	return &fakeData{
		indexErr: errOffline,
		lineFn: func(b int) (*Line, error) {
			if l := lines[b]; l != nil {
				return l, nil
			}
			return nil, errOffline
		},
	}
}

func TestFormCatalog(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:77
	eq(t, len(formCatalog), 23, "catalog size")
	for id, entries := range formCatalog {
		if id < 1 || id > maxSpeciesID || len(entries) < 2 || entries[0].formID != id {
			t.Errorf("#%d bad catalog entry", id)
		}
		seen := map[string]bool{}
		for i, e := range entries {
			if seen[e.form] || (i > 0 && e.formID <= 10000) {
				t.Errorf("#%d form %s", id, e.form)
			}
			seen[e.form] = true
		}
	}
	eq(t, len(formsOf(493)), 18, "Arceus")
	if !reflect.DeepEqual(formsOf(492), []string{"land", "sky"}) || hasForms(414) || hasForms(172) || hasForms(592) {
		t.Fatal("catalog membership")
	}
}

func TestLineFormsAndResolution(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:93
	if !reflect.DeepEqual(formSpecies(412), []int{412, 413}) || !reflect.DeepEqual(lineForms(412), []string{"plant", "sandy", "trash"}) ||
		!reflect.DeepEqual(formSpecies(420), []int{421}) || !reflect.DeepEqual(lineForms(420), []string{"overcast", "sunshine"}) ||
		!reflect.DeepEqual(lineForms(554), []string{"standard", "zen"}) || len(lineForms(1)) != 0 {
		t.Fatal("line forms")
	}
	eq(t, deref(formForLine(420, nil)), "overcast", "Cherubi line default")
	eq(t, deref(formForSpecies(420, sp("sunshine"))), "<nil>", "Cherubi has one look")
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:104
	eq(t, deref(formForSpecies(412, sp("sandy"))), "sandy", "Burmy")
	eq(t, deref(formForSpecies(413, sp("sandy"))), "sandy", "Wormadam")
	eq(t, deref(formForSpecies(414, sp("sandy"))), "<nil>", "Mothim")
	eq(t, deref(formForSpecies(492, nil)), "land", "Shaymin default")
	eq(t, deref(formForSpecies(492, sp("future"))), "land", "unknown form")
	eq(t, deref(formForSpecies(25, sp("sky"))), "<nil>", "Pikachu")
}

func TestAssetNames(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:115 and :131 (asset part; URLs and keys are the sprite package's)
	cases := []struct {
		id           int
		form         *string
		asset, stAss string
	}{
		{492, sp("sky"), "492-sky", "10006"},
		{412, sp("sandy"), "412-sandy", "412-sandy"},
		{492, sp("land"), "492", "492"},
		{414, sp("sandy"), "414", "414"},
		{413, sp("sandy"), "413-sandy", "10004"},
		{25, nil, "25", "25"},
	}
	for _, c := range cases {
		eq(t, assetName(c.id, c.form), c.asset, "asset")
		eq(t, staticAssetName(c.id, c.form), c.stAss, "static asset")
		a, s := SpriteAssets(c.id, c.form)
		eq(t, a+"/"+s, c.asset+"/"+c.stAss, "exported SpriteAssets")
	}
}

func TestStoredFormNormalizedAgainstLine(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:179
	st, _ := decodeState([]byte(`{"active":{"baseID":412,"pathIDs":[412,413],"stageIndex":1,"usedAtStage":0,"rarity":"common","totalForms":2,"form":"sky"},
		"dex":[{"baseID":1,"finalID":1,"chainOrder":[1],"rarity":"common","form":"sandy"}]}`))
	eq(t, deref(st.Active.Form), "plant", "other line's form falls back to the default")
	eq(t, deref(st.Dex[0].Form), "<nil>", "no forms stored for a line without forms")
}

func TestEvolutionKeepsCloakAndMothimKeepsItsLook(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:192
	st := freshState()
	st.Active = &monState{BaseID: 412, PathIDs: []int{412, 413}, PlannedPathIDs: []int{412, 413}, StageIndex: 1, Rarity: rarityCommon, TotalForms: 2, Form: sp("sandy")}
	st.Dex = []dexEntry{entry(412, 414, []int{412, 414}, rarityCommon, withForm("sandy"))}
	eq(t, st.ownsSpecies(413, sp("sandy")), true, "sandy Wormadam")
	eq(t, st.ownsSpecies(413, sp("plant")), false, "plant Wormadam")
	eq(t, st.ownsSpecies(414, nil), true, "Mothim")
	if !reflect.DeepEqual(st.collectedForms(412), map[string]bool{"sandy": true}) ||
		!reflect.DeepEqual(st.collectedForms(413), map[string]bool{"sandy": true}) || len(st.collectedForms(414)) != 0 {
		t.Fatal("collected forms")
	}
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:205
	st.Active = nil
	if st.collectedLineForms(412)["sandy"] {
		t.Fatal("Sandy Wormadam is still missing")
	}
	st.Dex = append(st.Dex, entry(412, 413, []int{412, 413}, rarityCommon, withForm("sandy")))
	if !reflect.DeepEqual(st.collectedLineForms(412), map[string]bool{"sandy": true}) || len(st.collectedLineForms(201)) != 0 {
		t.Fatal("collected line forms")
	}
}

func TestSpeciesFormsAndRepresentative(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:214
	st := freshState()
	st.Dex = []dexEntry{
		entry(412, 413, []int{412, 413}, rarityCommon, withForm("sandy")),
		entry(412, 413, []int{412, 413}, rarityCommon, withForm("plant"), shiny),
		entry(412, 414, []int{412, 414}, rarityCommon, withForm("trash")),
	}
	s := openAt(t, writeState(t, st), formData(), 7)
	ids := []int{}
	for _, c := range s.DexPage(DexQuery{}).Cells {
		ids = append(ids, c.SpeciesID)
	}
	if !ints(ids, []int{412, 413, 414}) {
		t.Fatalf("cells %v", ids)
	}
	v := s.Species(bg, 413)
	if len(v.Forms) != 3 || !v.Forms[0].IsShiny || v.Forms[0].HasNormal || !v.Forms[1].HasNormal || v.Forms[2].HasNormal || v.Forms[2].IsShiny {
		t.Fatalf("Wormadam forms %+v", v.Forms)
	}
	if len(s.Species(bg, 414).Forms) != 0 {
		t.Fatal("Mothim has no forms")
	}
	if !s.SetRepresentative(intPtr(413), sp("sandy")) {
		t.Fatal("sandy Wormadam is owned")
	}
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).MenuSubject.Form), "sandy", "subject form")
	if s.SetRepresentative(intPtr(413), sp("trash")) {
		t.Fatal("trash Wormadam is not owned")
	}
	eq(t, deref(s.st.RepresentativeForm), "sandy", "previous choice kept")
}

func TestHatchUsesPreparedFormAndRollsLineForms(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:233
	st := freshState()
	st.PendingHatchID, st.PendingForm, st.EggUsage = intPtr(492), sp("sky"), eggHatchThreshold
	s := openAt(t, writeState(t, st), formData(), 7)
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.BaseID, 492, "base")
	eq(t, deref(s.st.Active.Form), "sky", "form")
	if s.st.PendingForm != nil {
		t.Fatal("pending form not cleared")
	}
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:262
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 24; seed++ {
		s := newStore(t, formData(), seed)
		s.hatch(bg, 412)
		seen[deref(s.st.Active.Form)] = true
	}
	if len(seen) != 3 || !seen["plant"] || !seen["sandy"] || !seen["trash"] {
		t.Fatalf("seen %v", seen)
	}
}

func TestEvolutionNamesTheForm(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:246
	st := freshState()
	st.PendingHatchID, st.PendingForm, st.EggUsage = intPtr(422), sp("east"), eggHatchThreshold
	s := openAt(t, writeState(t, st), formData(), 7)
	s.hatchIfNeeded(bg)
	s.drain()
	s.applyUsage(phase(rarityCommon, 2, 0))
	eq(t, s.st.Active.currentID(), 423, "Gastrodon")
	ev := s.drain()[0].(EvolveEvent)
	eq(t, ev.DisplayName, "Gastrodon [East]", "identifier label until names load")
	eq(t, deref(ev.Form), "east", "event form")
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).JustEvolvedTo), "Gastrodon [East]", "not the West default")
}

func TestFormLabelsAndDisplayNames(t *testing.T) {
	s := newStore(t, formData(), 7)
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:322
	s.lang = "fr"
	for _, id := range []int{351, 479, 646, 649} {
		eq(t, s.formLabel(id, nil), "Forme Normale", "unnamed usual look")
	}
	s.lang = "de"
	eq(t, s.formLabel(479, sp("normal")), "Normalform", "German")
	s.lang = "fr"
	eq(t, s.formLabel(479, sp("heat")), "Heat", "identifier until PokéAPI answers")
	eq(t, formDisplayName("Motisma", 479, nil, s.formLabel(479, nil)), "Motisma", "default form adds no label")
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:334
	eq(t, s.formLabel(492, sp("sky")), "Sky", "identifier")
	s.names[nameKey("pokemon-form", "10064")] = map[string]string{"fr": "Forme Céleste", "en": "Sky Forme"}
	eq(t, s.formLabel(492, sp("sky")), "Forme Céleste", "loaded name")
	eq(t, s.formLabel(201, sp("question")), "?", "Unown symbol")
	eq(t, s.formLabel(25, sp("sky")), "", "no forms")
	eq(t, formDisplayName("Shaymin", 492, sp("sky"), "Forme Céleste"), "Shaymin [Forme Céleste]", "non-default")
	eq(t, formDisplayName("Shaymin", 492, sp("land"), "Forme Terrestre"), "Shaymin", "default")
	eq(t, formDisplayName("Zarbi", 201, sp("a"), ""), "Zarbi [A]", "Unown always")
	eq(t, formDisplayName("Pikachu", 25, sp("sky"), "Sky"), "Pikachu", "no forms")
	// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:301 (resource ids)
	eq(t, formNameResource(492, sp("sky")), "10064", "Sky Shaymin")
	eq(t, formNameResource(492, nil), "492", "Land Shaymin")
	eq(t, formNameResource(201, sp("b")), "", "Unown")
	eq(t, formNameResource(25, nil), "", "Pikachu")
	var rotom []string
	for _, f := range formsOf(479) {
		if r := formNameResource(479, sp(f)); r != "" {
			rotom = append(rotom, r)
		}
	}
	eq(t, strings.Join(rotom, ","), "10058,10059,10060,10061,10062", "Rotom's usual look has no name")
}

// ---------------------------------------------------------------- Unown (UnownTests.swift)

func unownEntry(form string, isShiny bool) dexEntry {
	e := entry(201, 201, []int{201}, rarityCommon, withForm(form),
		withNames(map[int]map[string]string{201: {"ko": "안농", "en": "Unown"}}))
	e.IsShiny = isShiny
	e.Nature = sp("brave")
	return e
}

func unownMon(form string, isShiny bool) *monState {
	m := &monState{BaseID: 201, PathIDs: []int{201}, PlannedPathIDs: []int{201}, UsedAtStage: 123, Rarity: rarityCommon,
		TotalForms: 1, IsShiny: isShiny, Nature: sp("brave"), Form: sp(form)}
	return m
}

func TestUnownRollWeights(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:113
	var got []string
	for i := uint64(0); i < 56; i++ {
		got = append(got, formSymbol(rollForm(i, unownLetters, nil)))
	}
	var want []string
	for _, l := range unownLetters {
		want = append(want, formSymbol(l), formSymbol(l))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rolls %v", got)
	}
	eq(t, rollForm(56, unownLetters, nil), "a", "wraps")
	eq(t, formSymbol("exclamation")+formSymbol("question"), "!?", "symbols")
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:122
	for _, collected := range []map[string]bool{{}, {"a": true, "b": true, "exclamation": true}, toSet(unownLetters)} {
		total := 56 - len(collected)
		count := map[string]int{}
		for i := 0; i < total; i++ {
			count[rollForm(uint64(i), unownLetters, collected)]++
		}
		for _, l := range unownLetters {
			want := 2
			if collected[l] {
				want = 1
			}
			eq(t, count[l], want, l)
		}
		eq(t, rollForm(uint64(total), unownLetters, collected), "a", "wrap")
	}
}

func TestUnownCollectionIncludesActiveAndReleased(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:142
	st := freshState()
	legacy := unownEntry("a", false)
	released := unownEntry("exclamation", true)
	released.ReleasedAt = timePtr(fixedNow)
	st.Dex = []dexEntry{legacy, unownEntry("b", false), unownEntry("b", true), released, entry(25, 25, []int{25}, rarityCommon)}
	st.Active = unownMon("question", true)
	if !reflect.DeepEqual(st.collectedForms(201), toSet([]string{"a", "b", "exclamation", "question"})) {
		t.Fatal("collected forms")
	}
	s := openAt(t, writeState(t, st), formData(), 7)
	var ids []int
	for _, c := range s.collectedDexSpecies(false) {
		ids = append(ids, c.id)
	}
	if !ints(ids, []int{25, 201}) {
		t.Fatalf("species %v", ids)
	}
	var forms []string
	var shinyFlags, raising []bool
	for _, c := range s.collectedDexSpecies(true) {
		if c.id == 201 {
			forms, shinyFlags, raising = append(forms, *c.form), append(shinyFlags, c.isShiny), append(raising, c.isRaising)
		}
	}
	if !reflect.DeepEqual(forms, []string{"a", "b", "exclamation", "question"}) ||
		!reflect.DeepEqual(shinyFlags, []bool{false, true, true, true}) || !reflect.DeepEqual(raising, []bool{false, false, false, true}) {
		t.Fatalf("forms %v shiny %v raising %v", forms, shinyFlags, raising)
	}
}

func TestUnownRepresentativeNeedsOwnedLetter(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:158
	st := freshState()
	st.Dex = []dexEntry{unownEntry("question", true)}
	s := openAt(t, writeState(t, st), formData(), 7)
	if s.SetRepresentative(intPtr(201), sp("a")) || !s.SetRepresentative(intPtr(201), sp("question")) {
		t.Fatal("representative ownership")
	}
	if !s.DexPage(DexQuery{}).Cells[0].IsRepresentative {
		t.Fatal("cell star")
	}
}

func TestUnownFormDrawOrder(t *testing.T) {
	ownedAllButQuestion := func() state {
		st := freshState()
		for _, l := range unownLetters[:27] {
			st.Dex = append(st.Dex, unownEntry(l, false))
		}
		return st
	}
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:169 — the form is the 4th draw of hatch(baseID:).
	var seed uint64
	for seed = 0; seed < 1000; seed++ {
		if r := draws(seed, 4)[3]; r%29 == 27 && r%56/2 != 27 {
			break
		}
	}
	s := openAt(t, writeState(t, ownedAllButQuestion()), formData(), seed)
	s.hatch(bg, 201)
	eq(t, deref(s.st.Active.Form), "question", "missing form fills both final slots")
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:183 — prefetch draws species, then form.
	for seed = 0; seed < 1000; seed++ {
		if r := draws(seed, 2)[1]; r%29 == 27 && r%56/2 != 27 {
			break
		}
	}
	st := ownedAllButQuestion()
	st.InstallBaselineSet, st.LastDate = true, "d1"
	f := formData()
	f.indexErr, f.index = nil, []BaseEntry{{201, 255}}
	s = openAt(t, writeState(t, st), f, seed)
	use(s, 1_000)
	eq(t, *s.st.PendingHatchID, 201, "pending species")
	eq(t, deref(s.st.PendingForm), "question", "pending form")
}

func TestFormOwnershipDoesNotChangeSpeciesOrShinyRolls(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:203 — Pikachu 255 + collected Unown 112 → % 367.
	selected := map[int]bool{}
	for _, seed := range []uint64{1, 7, 17, 42, 128, 999} {
		for _, owned := range [][]string{nil, {"b"}, unownLetters} {
			st := freshState()
			st.EggUsage = eggHatchThreshold
			st.CollectedFinals = toSet([]string{"201:201"})
			for _, l := range owned {
				st.Dex = append(st.Dex, unownEntry(l, false))
			}
			f := formData()
			f.indexErr, f.index = nil, []BaseEntry{{25, 255}, {201, 225}}
			s := openAt(t, writeState(t, st), f, seed)
			d := draws(seed, 4)
			wantSpecies := 201
			if d[0]%367 < 255 {
				wantSpecies = 25
			}
			s.hatchIfNeeded(bg)
			a := s.st.Active
			eq(t, a.currentID(), wantSpecies, "species")
			eq(t, a.displaysShiny(), d[1]%64 == 0, "shiny")
			eq(t, *a.Nature, natures[d[2]%25], "nature")
			eq(t, a.Profile.Seed, d[3], "profile seed")
			selected[wantSpecies] = true
		}
	}
	if !selected[25] || !selected[201] {
		t.Fatalf("selected %v", selected)
	}
}

func TestInvalidFormFieldsDefaultWithoutLosingProgress(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:230
	for _, invalid := range []string{`null`, `"future-form"`, `42`, ``} {
		field := ""
		if invalid != "" {
			field = `,"form":` + invalid
		}
		rep := ""
		if invalid != "" {
			rep = `,"representativeForm":` + invalid
		}
		raw := `{"active":{"baseID":201,"pathIDs":[201],"stageIndex":0,"usedAtStage":123,"rarity":"common","totalForms":1,"isShiny":true` + field + `},
			"dex":[{"id":"q","baseID":201,"finalID":201,"chainOrder":[201],"rarity":"common","isShiny":true` + field + `}],
			"representativeSpeciesID":201` + rep + `,"usedSinceInstall":123456789,"inventory":{"rareCandy":3}}`
		st, ok := decodeState([]byte(raw))
		if !ok {
			t.Fatal("decode")
		}
		st.sanitize()
		if deref(st.Active.Form) != "a" || st.Active.UsedAtStage != 123 || !st.Active.IsShiny || len(st.Dex) != 1 ||
			st.Dex[0].ID != "q" || deref(st.Dex[0].Form) != "a" || deref(st.RepresentativeForm) != "a" ||
			st.UsedSinceInstall != 123456789 || st.Inventory["rareCandy"] != 3 {
			t.Errorf("invalid %q: %+v", invalid, st)
		}
	}
}

func TestUnownFormsShareOneCellAndRoundTrip(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:276
	st := freshState()
	st.Language = "ko"
	for _, l := range unownLetters {
		st.Dex = append(st.Dex, unownEntry(l, true))
	}
	s := openAt(t, writeState(t, st), formData(), 7)
	cells := s.DexPage(DexQuery{}).Cells
	if len(cells) != 1 || cells[0].DisplayName != "안농" || cells[0].Forms == nil || cells[0].Forms.Owned != 28 || cells[0].Forms.Total != 28 {
		t.Fatalf("cells %+v", cells)
	}
	eq(t, deref(cells[0].Sprite.Form), "a", "cell sprite uses the default form")
	eq(t, cells[0].Sprite.Shiny, false, "forms species keep the normal colour")
	v := s.Species(bg, 201)
	for i, f := range v.Forms {
		eq(t, f.DisplayName, formSymbol(unownLetters[i]), "label")
	}
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:300
	eq(t, deref(formForLine(25, sp("question"))), "<nil>", "form only applies to lines with forms")
	eq(t, deref(formForLine(201, nil)), "a", "Unown default A")
}

func TestUnownDexScopesShinyAndRaising(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:379
	st := freshState()
	st.Dex = []dexEntry{unownEntry("a", false), unownEntry("b", false), unownEntry("b", true)}
	st.Active = unownMon("c", false)
	s := openAt(t, writeState(t, st), formData(), 7)
	cell := s.collectedDexSpecies(false)
	if len(cell) != 1 || !cell[0].isShiny || !cell[0].isRaising {
		t.Fatalf("cell %+v", cell)
	}
	eq(t, s.st.ownsSpecies(201, sp("c")), true, "owns C")
	eq(t, s.st.ownsSpecies(201, sp("z")), false, "not Z")
	eq(t, s.st.ownsShinySpecies(201, sp("a")), false, "A normal")
	eq(t, s.st.ownsShinySpecies(201, sp("b")), true, "B shiny")
	eq(t, s.st.ownsShinySpecies(201, sp("c")), false, "C normal")
}

func TestUnownRepresentativeUsesExactLetter(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:399
	st := freshState()
	st.Dex = []dexEntry{unownEntry("a", false), unownEntry("b", true)}
	dir := writeState(t, st)
	s := openAt(t, dir, formData(), 7)
	s.SetRepresentative(intPtr(201), sp("a"))
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Representative.Shiny, false, "shiny B must not colour A")
	s.SetRepresentative(intPtr(201), sp("b"))
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Representative.Shiny, true, "B shiny")
	if s.SetRepresentative(intPtr(201), sp("z")) || deref(s.st.RepresentativeForm) != "b" {
		t.Fatal("unowned letter must keep the previous choice")
	}
	re := openAt(t, dir, formData(), 7)
	eq(t, deref(re.st.RepresentativeForm), "b", "persisted")
	re.SetRepresentative(nil, nil)
	if re.st.RepresentativeForm != nil || re.BuildState(MoodInput{Now: fixedNow}).Representative != nil {
		t.Fatal("cleared")
	}
}

func TestUnownIndividualsScopedToLetter(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:458 (getSpecies lists every letter's individuals)
	st := freshState()
	st.Dex = []dexEntry{unownEntry("a", false), unownEntry("b", false), unownEntry("b", true)}
	st.Active = unownMon("b", false)
	s := openAt(t, writeState(t, st), formData(), 7)
	v := s.Species(bg, 201)
	eq(t, len(v.Individuals), 4, "individuals")
	eq(t, v.Individuals[0].IsRaising, true, "active first")
	eq(t, deref(v.Individuals[0].Form), "b", "active letter")
	var raising []bool
	for _, f := range v.Forms[:2] {
		raising = append(raising, f.HasNormal)
	}
	if !reflect.DeepEqual(raising, []bool{true, true}) {
		t.Fatal("A and B owned")
	}
}

func TestPendingLetterSurvivesFailedLineLoad(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:587
	st := freshState()
	st.PendingHatchID, st.PendingForm, st.EggUsage = intPtr(201), sp("question"), eggHatchThreshold+123
	dir := writeState(t, st)
	f := formData()
	fail := true
	ok := f.lineFn
	f.lineFn = func(b int) (*Line, error) {
		if fail {
			fail = false
			return nil, errOffline
		}
		return ok(b)
	}
	s := openAt(t, dir, f, 17)
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || *s.st.PendingHatchID != 201 || deref(s.st.PendingForm) != "question" || s.st.EggUsage != eggHatchThreshold+123 {
		t.Fatal("pending egg lost on failure")
	}
	s.SetLanguage("ko")
	re := openAt(t, dir, formData(), 17)
	eq(t, deref(re.st.PendingForm), "question", "persisted pending letter")
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.currentID(), 201, "hatched")
	eq(t, deref(s.st.Active.Form), "question", "letter")
	eq(t, s.st.Active.UsedAtStage, 123, "overflow")
	if s.st.PendingHatchID != nil || s.st.PendingForm != nil {
		t.Fatal("pending not cleared")
	}
}

func TestLegacyPendingUnownKeepsDefaultA(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:576
	s := openAt(t, writeSave(t, `{"pendingHatchID":201,"eggUsage":5000000}`), formData(), 17)
	eq(t, deref(s.st.PendingForm), "a", "decoded default")
	s.hatchIfNeeded(bg)
	eq(t, deref(s.st.Active.Form), "a", "hatched A")
}

func TestSanitizeClearsPendingLetterWithoutMatchingEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:614
	st := freshState()
	st.Active = unownMon("b", false)
	st.PendingHatchID, st.PendingForm = intPtr(201), sp("question")
	st.sanitize()
	if deref(st.Active.Form) != "b" || st.PendingHatchID != nil || st.PendingForm != nil {
		t.Fatal("active egg fields")
	}
	st.Active, st.PendingHatchID, st.PendingForm = nil, intPtr(25), sp("question")
	st.sanitize()
	if st.PendingForm != nil {
		t.Fatal("Pikachu has no forms")
	}
	st.PendingHatchID = nil
	st.sanitize()
	if st.PendingForm != nil {
		t.Fatal("no pending species")
	}
}

func TestNonFormHatchDrawsNoExtraForm(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:360 (the Mint follow-up is deferred)
	rng := &countingRng{SplitMix64: SplitMix64{State: 17}}
	s := newStore(t, formData(), 0, withRng(rng))
	d := draws(17, 3)
	s.hatch(bg, 25)
	if s.st.Active.Form != nil || s.st.Active.IsShiny != (d[0]%64 == 0) || *s.st.Active.Nature != natures[d[1]%25] || s.st.Active.Profile.Seed != d[2] {
		t.Fatal("draws")
	}
	eq(t, rng.calls, 3, "no form draw for a line without forms")
}

func TestHatchGraduationReloadPreserveLetterAndShiny(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:314
	for _, wantShiny := range []bool{false, true} {
		var seed uint64
		for seed = 0; seed < 20000; seed++ {
			d := draws(seed, 4)
			if (d[0]%64 == 0) == wantShiny && d[3]%56/2 != 0 {
				break
			}
		}
		d := draws(seed, 4)
		form := unownLetters[d[3]%56/2]
		dir := t.TempDir()
		s := openAt(t, dir, formData(), seed)
		s.hatch(bg, 201)
		eq(t, deref(s.st.Active.Form), form, "letter")
		eq(t, s.st.Active.displaysShiny(), wantShiny, "shiny")
		eq(t, *s.st.Active.Nature, natures[d[1]%25], "nature")
		prof := *s.st.Active.Profile
		eq(t, prof.Seed, d[2], "profile seed")
		s.SetRepresentative(intPtr(201), &form)
		eq(t, deref(openAt(t, dir, formData(), 7).st.Active.Form), form, "reload")
		s.applyUsage(graduationTotal(rarityCommon))
		e := s.st.Dex[0]
		if deref(e.Form) != form || e.IsShiny != wantShiny || e.Profile.InstanceID != prof.InstanceID || e.Profile.IVs != prof.IVs || e.Profile.Level != 100 {
			t.Fatalf("graduated %+v", e)
		}
		re := openAt(t, dir, formData(), 7)
		eq(t, deref(re.st.Dex[0].Form), form, "reloaded form")
		eq(t, deref(re.st.RepresentativeForm), form, "representative form")
		eq(t, re.BuildState(MoodInput{Now: fixedNow}).Representative.Shiny, wantShiny, "representative shiny")
	}
}
