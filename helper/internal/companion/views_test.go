package companion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// shape returns every key path of a JSON value; arrays contribute their first element.
func shape(v any, prefix string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			out[prefix+"."+k] = true
			shape(val, prefix+"."+k, out)
		}
	case []any:
		if len(x) > 0 {
			shape(x[0], prefix+"[]", out)
		}
	}
}

func shapeOf(t *testing.T, v any) map[string]bool {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	_ = json.Unmarshal(data, &generic)
	out := map[string]bool{}
	shape(generic, "", out)
	return out
}

func fixture(t *testing.T, name string, path ...string) any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "extension", "test", "fixtures", name))
	if err != nil {
		t.Skipf("fixture %s: %v", name, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	for _, p := range path {
		v = v.(map[string]any)[p]
	}
	return v
}

// sameShape reports fixture key paths our payload lacks and keys it adds. Paths below a null in
// either value cannot be compared and are ignored.
func sameShape(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	var missing, extra []string
	for k := range want {
		if !got[k] && !underNull(k, got) {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] && !underNull(k, want) {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Errorf("%s: missing %v, extra %v", label, missing, extra)
	}
}

func underNull(path string, set map[string]bool) bool {
	for i := strings.LastIndex(path, "."); i > 0; i = strings.LastIndex(path[:i], ".") {
		parent := strings.TrimSuffix(path[:i], "[]")
		if set[parent] {
			return !hasChild(parent, set)
		}
	}
	return false
}

func hasChild(parent string, set map[string]bool) bool {
	for k := range set {
		if strings.HasPrefix(k, parent+".") || strings.HasPrefix(k, parent+"[]") {
			return true
		}
	}
	return false
}

// m2Pending lists the companion fixture paths (docs/protocol.md, Milestone 2) that the Go views do
// not emit yet. withoutPending drops them, and everything below them, from the fixture shape, so the
// comparison stays green until each phase lands. The phase that implements a field deletes its line
// here; from then on the shape check covers it. Groups are separated by blank lines so parallel
// phases do not touch the same lines.
var m2Pending = []string{}

// The Milestone 3 contract (prayers, docs/protocol.md) is pending the same way, per payload, because
// the paths are relative to each payload. Phase 17 deletes these lines.
var (
	m3PendingEgg     = []string{".egg.prayers"}
	m3PendingSpecies = []string{".individuals[].prayers"}
	m3PendingHatch   = []string{".prayers"}
)

func withoutPending(want map[string]bool, more ...string) map[string]bool {
	out := map[string]bool{}
next:
	for k := range want {
		for _, p := range append(append([]string{}, m2Pending...), more...) {
			if k == p || strings.HasPrefix(k, p+".") || strings.HasPrefix(k, p+"[]") {
				continue next
			}
		}
		out[k] = true
	}
	return out
}

// pidgeottoWorld approximates the fixture world: Pidgeotto at stage 2/3 and a graduated Butterfree.
func pidgeottoWorld(t *testing.T) *Store {
	st := freshState()
	st.InstallBaselineSet, st.LastDate = true, "d1"
	st.Dex = []dexEntry{entry(10, 12, []int{10, 11, 12}, rarityCommon, shiny,
		withNames(map[int]map[string]string{10: {"en": "Caterpie"}, 11: {"en": "Metapod"}, 12: {"en": "Butterfree"}}))}
	st.Dex[0].Nature = sp("bold")
	st.Active = &monState{BaseID: 16, PathIDs: []int{16, 17}, PlannedPathIDs: []int{16, 17, 18}, StageIndex: 1, UsedAtStage: 162_400_000,
		Rarity: rarityCommon, TotalForms: 3, Nature: sp("adamant")}
	st.RepresentativeSpeciesID = intPtr(12)
	f := &fakeData{
		lineFn: func(b int) (*Line, error) {
			l := makeLine(16, node(16, node(17, node(18))), rarityCommon)
			l.Names = map[int]map[string]string{16: {"en": "Pidgey"}, 17: {"en": "Pidgeotto"}, 18: {"en": "Pidgeot"}}
			return l, nil
		},
		detailsFn: func(id int) (*Details, error) {
			return &Details{SpeciesID: id, Height: 11, Weight: 300, GenderRate: 4, Types: []string{"normal", "flying"},
				BaseStats: map[string]int{"hp": 63, "attack": 60}, Abilities: []Ability{{"keen-eye", 1, false}},
				Moves: []Move{{"tackle", []LearnMethod{{"level-up", 1}, {"machine", 0}}}}}, nil
		},
		namesFn: func(kind, name string) (map[string]string, error) {
			return map[string]string{"en": strings.ToUpper(name[:1]) + name[1:]}, nil
		},
	}
	s := openAt(t, writeState(t, st), f, 7)
	use(s, 0)
	return s
}

func TestPayloadShapesMatchFixtures(t *testing.T) {
	s := pidgeottoWorld(t)
	v := s.BuildState(MoodInput{Now: fixedNow, HasUsageData: true, TodayTokens: 1, BurnPerMinute: 500_000})
	eq(t, v.Mood, "focus", "fixture mood")
	eq(t, v.GrowthPercent, 64, "162.4M / 250M")
	eq(t, v.Active.DisplayName, "Pidgeotto", "name")
	eq(t, v.Active.Types[1].Name, "Flying", "type name")
	eq(t, v.MenuSubject.Asset, "12", "asset")
	sameShape(t, "state.companion (active)", shapeOf(t, v), withoutPending(shapeOf(t, fixture(t, "state-active.json", "companion"))))
	egg := newStore(t, stub(linear3()), 7)
	sameShape(t, "state.companion (egg)", shapeOf(t, egg.BuildState(MoodInput{Now: fixedNow})), withoutPending(shapeOf(t, fixture(t, "state-egg.json", "companion")), m3PendingEgg...))
	sameShape(t, "getDexPage", shapeOf(t, s.DexPage(DexQuery{})), shapeOf(t, fixture(t, "dex-page.json", "data")))
	sameShape(t, "getCatchLog", shapeOf(t, s.CatchLog(CatchLogQuery{})), shapeOf(t, fixture(t, "catch-log.json", "data")))
	sp := s.Species(bg, 17)
	sameShape(t, "getSpecies", shapeOf(t, sp), withoutPending(shapeOf(t, fixture(t, "species.json", "data")), m3PendingSpecies...))
	eq(t, sp.Details.HeightM, 1.1, "height in metres")
	eq(t, *sp.Details.Moves[0].Methods[0].Level, 1, "level-up level")
	if sp.Details.Moves[0].Methods[1].Level != nil {
		t.Error("machine level must be null")
	}
	// Event shapes against events.ndjson.
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "extension", "test", "fixtures", "events.ndjson"))
	if err != nil {
		t.Skip(err)
	}
	want := map[string]map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		_ = json.Unmarshal([]byte(line), &m)
		want[m["kind"].(string)] = shapeOf(t, m)
	}
	want["hatch"] = withoutPending(want["hatch"], m3PendingHatch...)
	for _, e := range []Event{
		HatchEvent{Type: "event", Kind: "hatch"}, EvolveEvent{Type: "event", Kind: "evolve"},
		GraduateEvent{Type: "event", Kind: "graduate"}, CandyGrantedEvent{Type: "event", Kind: "candyGranted"},
		BadgeEvent{Type: "event", Kind: "badge", Region: sp.Region},
	} {
		sameShape(t, e.EventKind(), shapeOf(t, e), want[e.EventKind()])
	}
}

func TestMood(t *testing.T) {
	idle := func(d time.Duration) *time.Duration { return &d }
	s := newStore(t, stub(linear3()), 7)
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Mood, "egg", "egg")
	s.hatch(bg, 1)
	later := fixedNow.Add(10 * time.Second)
	cases := []struct {
		in   MoodInput
		want string
	}{
		{MoodInput{Now: fixedNow}, "levelUp"},
		{MoodInput{Now: later, LimitWarning: true, HasUsageData: true, TodayTokens: 5}, "tired"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, IdleFor: idle(3 * time.Hour)}, "sulk"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, IdleFor: idle(3*time.Hour - time.Second)}, "idle"},
		{MoodInput{Now: later, HasUsageData: false, IdleFor: idle(5 * time.Hour)}, "sleep"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 0}, "sleep"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, BurnPerMinute: 1000}, "idle"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, BurnPerMinute: 1001}, "working"},
		{MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, BurnPerMinute: 100_000}, "focus"},
	}
	for i, c := range cases {
		eq(t, s.BuildState(c.in).Mood, c.want, "case "+string(rune('0'+i)))
	}
	sulk := s.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, IdleFor: idle(4 * time.Hour)})
	eq(t, sulk.MenuSubject.Back, true, "sulk faces back")
	eq(t, s.BuildState(MoodInput{Now: later, AlwaysFacesBack: true}).MenuSubject.Back, true, "always faces back")
	egg := newStore(t, stub(linear3()), 7)
	eq(t, egg.BuildState(MoodInput{Now: later, AlwaysFacesBack: true}).MenuSubject.Back, false, "never for an egg")
	// A rise in today's total seen by Update caps the reported idle time (CS:1974-1985).
	u := newStore(t, stub(noEvo()), 7)
	u.hatch(bg, 20)
	use(u, 100)
	use(u, 200)
	eq(t, u.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 200, IdleFor: idle(5 * time.Hour)}).Mood, "idle", "observed rise")
}

func TestEggView(t *testing.T) {
	s := newStore(t, stub(linear3()), 7)
	s.st.EggUsage = 4_500_000
	v := s.BuildState(MoodInput{Now: fixedNow})
	if v.Phase != "egg" || v.Egg.Used != 4_500_000 || v.Egg.Threshold != 5_000_000 || !v.Egg.Imminent || v.GrowthPercent != 90 || !v.MenuSubject.Egg || v.MenuSubject.SpeciesID != nil {
		t.Fatalf("%+v %+v", v, v.Egg)
	}
	s.st.EggUsage = 7_000_000
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).GrowthPercent, 100, "clamped")
}
