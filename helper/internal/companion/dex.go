package companion

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pokédex aggregation, regions and badges (upstream CompanionStore.swift:427-794, PokedexRegion.swift).

type region struct {
	key    string
	lo, hi int
}

// regions in National Dex order (PokedexRegion.swift:14-34).
var regions = []region{
	{"kanto", 1, 151}, {"johto", 152, 251}, {"hoenn", 252, 386}, {"sinnoh", 387, 493}, {"unova", 494, 649},
}

func (r region) total() int { return r.hi - r.lo + 1 }

func regionOf(id int) (region, bool) {
	for _, r := range regions {
		if id >= r.lo && id <= r.hi {
			return r, true
		}
	}
	return region{}, false
}

// dexSpecies is one Pokédex cell: a species (or, grouped, a species in one form).
type dexSpecies struct {
	id        int
	form      *string // set only when grouping forms
	order     int     // the form's catalog index, for sorting
	rarity    string
	names     map[string]string
	isShiny   bool // ever owned shiny
	hasNormal bool // ever owned in normal colours
	isRaising bool
}

type dexKey struct {
	id   int
	form string
}

// collectedDexSpecies folds every owned species: graduated and released entries plus the active
// Pokémon's reached stages, never its plan. groupForms splits species by resolved form (CS:558-596).
func (s *Store) collectedDexSpecies(groupForms bool) []dexSpecies {
	acc := map[dexKey]*dexSpecies{}
	add := func(id int, form *string, rarity string, names map[string]string, hasNames, shiny bool) {
		k := dexKey{id: id}
		var f *string
		if groupForms {
			if f = formForSpecies(id, form); f != nil {
				k.form = *f
			}
		}
		a := acc[k]
		if a == nil {
			a = &dexSpecies{id: id, form: f, rarity: rarity}
			for i, x := range formsOf(id) {
				if f != nil && x == *f {
					a.order = i
				}
			}
			acc[k] = a
		}
		if hasNames {
			a.names = names
		}
		if shiny {
			a.isShiny = true
		} else {
			a.hasNormal = true
		}
	}
	for _, e := range s.st.Dex {
		for _, id := range e.ChainOrder {
			n, ok := e.Names[id]
			add(id, e.Form, e.Rarity, n, ok, e.IsShiny)
		}
	}
	a := s.st.Active
	if a != nil {
		for _, id := range a.reached() {
			var n map[string]string
			ok := false
			if s.line != nil {
				n, ok = s.line.names[id]
			}
			add(id, a.Form, a.Rarity, n, ok, a.displaysShiny())
		}
	}
	out := make([]dexSpecies, 0, len(acc))
	for _, v := range acc {
		if a != nil && v.id == a.currentID() {
			v.isRaising = !groupForms || sameForm(v.form, formForSpecies(v.id, a.Form))
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}
		return out[i].order < out[j].order
	})
	return out
}

// ---------------------------------------------------------------- badges

type badge struct {
	key, kind, region string // region "" for unown/national
}

type regionProgress struct {
	region            region
	owned, shinyOwned int
}

func regionCompletions(owned []dexSpecies) []regionProgress {
	shiny := map[int]bool{}
	for _, sp := range owned {
		shiny[sp.id] = shiny[sp.id] || sp.isShiny
	}
	out := make([]regionProgress, len(regions))
	for i, r := range regions {
		out[i].region = r
		for id, sh := range shiny {
			if id >= r.lo && id <= r.hi {
				out[i].owned++
				if sh {
					out[i].shinyOwned++
				}
			}
		}
	}
	return out
}

// earnedBadges lists earned badges in stable order: per region (region, then regionShiny), Unown,
// national, nationalShiny (PokedexRegion.swift:101-112). Keys are persisted and never renamed.
func (s *Store) earnedBadges() []badge {
	owned := s.collectedDexSpecies(false)
	var out []badge
	national, nationalShiny := 0, 0
	for _, p := range regionCompletions(owned) {
		if p.owned >= p.region.total() {
			out = append(out, badge{"region." + p.region.key, "region", p.region.key})
		}
		if p.shinyOwned >= p.region.total() {
			out = append(out, badge{"regionShiny." + p.region.key, "regionShiny", p.region.key})
		}
		national += p.owned
		nationalShiny += p.shinyOwned
	}
	if len(s.st.collectedForms(unownSpeciesID)) >= len(unownLetters) {
		out = append(out, badge{"unown", "unown", ""})
	}
	if national >= maxSpeciesID {
		out = append(out, badge{"national", "national", ""})
	}
	if nationalShiny >= maxSpeciesID {
		out = append(out, badge{"nationalShiny", "nationalShiny", ""})
	}
	return out
}

// checkDexCompletions announces every badge earned since the last check, once (CS:518-527).
func (s *Store) checkDexCompletions() {
	if !s.st.DexCompletionsSeeded {
		return
	}
	for _, b := range s.earnedBadges() {
		if s.st.NotifiedDexCompletions[b.key] {
			continue
		}
		s.st.NotifiedDexCompletions[b.key] = true
		ev := BadgeEvent{Type: "event", Kind: "badge", Key: b.key, Badge: b.kind}
		if b.region != "" {
			ev.Region = strPtr(b.region)
		}
		s.emit(ev)
	}
}

// seedDexCompletions records badges already earned on the first run without announcing them; an
// empty seed is not written on its own (CS:531-541).
func (s *Store) seedDexCompletions() {
	if s.st.DexCompletionsSeeded {
		return
	}
	s.st.DexCompletionsSeeded = true
	earned := s.earnedBadges()
	if len(earned) == 0 {
		return
	}
	for _, b := range earned {
		s.st.NotifiedDexCompletions[b.key] = true
	}
	s.save()
}

// ---------------------------------------------------------------- catch log entries

// activeEntryID is the synthetic catch-log id of the companion being raised (CS:329).
func activeEntryID(a *monState) string {
	return "active-" + strconv.Itoa(a.BaseID) + "-" + strconv.Itoa(a.currentID())
}

// logEntry is a catch-log row: a dex entry, or the synthetic active one.
type logEntry struct {
	dexEntry
	active bool
}

// logEntries returns the dex plus the active Pokémon (reached path, displayed shiny), active last.
func (s *Store) logEntries() []logEntry {
	out := make([]logEntry, 0, len(s.st.Dex)+1)
	for _, e := range s.st.Dex {
		out = append(out, logEntry{dexEntry: e})
	}
	if a := s.st.Active; a != nil {
		var names map[int]map[string]string
		if s.line != nil {
			names = map[int]map[string]string{}
			for _, id := range a.PathIDs {
				if n, ok := s.line.names[id]; ok {
					names[id] = n
				}
			}
		}
		out = append(out, logEntry{active: true, dexEntry: dexEntry{
			ID: activeEntryID(a), BaseID: a.BaseID, FinalID: a.currentID(), ChainOrder: a.PathIDs,
			Rarity: a.Rarity, IsShiny: a.displaysShiny(), Nature: a.Nature, Form: a.Form,
			Profile: a.Profile, Prayers: a.Prayers, Names: names,
		}})
	}
	return out
}

// entryName is an entry's name in the current language: its final species, else the newest named
// stage, else "#final" (CS:599-610).
func (s *Store) entryName(e dexEntry) string {
	if v, ok := resolveName(e.Names[e.FinalID], s.lang); ok {
		return v
	}
	for i := len(e.ChainOrder) - 1; i >= 0; i-- {
		if v, ok := resolveName(e.Names[e.ChainOrder[i]], s.lang); ok {
			return v
		}
	}
	return "#" + strconv.Itoa(e.FinalID)
}

func caughtOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ---------------------------------------------------------------- search

// matcher is the search rule shared by the Pokédex and the catch log: a dex number (25 or #25), a
// partial number ("25" also finds #125), or part of a stored name in any language, ignoring case and
// Latin diacritics (CS:615-643).
type matcher struct {
	query, folded string
	number        int
	hasNumber     bool
}

func newMatcher(raw string) *matcher {
	q := strings.TrimSpace(raw)
	if q == "" {
		return nil
	}
	m := &matcher{query: q, folded: fold(q)}
	if n, err := strconv.Atoi(strings.TrimPrefix(q, "#")); err == nil {
		m.number, m.hasNumber = n, true
	}
	return m
}

func (m *matcher) matches(ids []int, names []string) bool {
	for _, id := range ids {
		if m.hasNumber && id == m.number {
			return true
		}
		if strings.Contains("#"+strconv.Itoa(id), m.query) {
			return true
		}
	}
	for _, n := range names {
		if strings.Contains(fold(n), m.folded) {
			return true
		}
	}
	return false
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// nameCompare orders names case- and accent-insensitively, a stand-in for Foundation's localizedCompare.
func nameCompare(a, b string) int {
	return strings.Compare(fold(a), fold(b))
}
