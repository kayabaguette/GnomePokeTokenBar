// Package companion is the companion game of PokeTokenBar: the save, growth economics,
// hatch/evolve/graduate, the Pokédex, Rare Candy grants and the view payloads of protocol §5.4/§7.
// It ports upstream CompanionStore.swift and its model files (baseline 38e3e55).
package companion

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Options configures Open.
type Options struct {
	Dir  string     // state directory; the save is Dir/companion-state.json
	Data DataSource // Pokémon data; required
	Rng  Rng        // nil: crypto/rand
	Now  func() time.Time
	// DittoDisguiseEnabled turns on the 1/128 Ditto disguise roll at hatch. The daemon sets it; the
	// default stays off so tests keep upstream's draw counts (upstream gates it the same way).
	DittoDisguiseEnabled bool
	AppVersion           string // written into snapshot envelopes
	Logf                 func(format string, args ...any)
}

// Store is the companion game state and its rules.
//
// It is synchronous and single-owner: the daemon calls every method from one goroutine, so there
// is no locking. Upstream's `activeGeneration` guard, which stops an async completion from landing
// on a replaced subject, is unnecessary here: a fetch can only return into the call that started it.
// DataSource calls may block; methods that fetch take a ctx.
type Store struct {
	dir          string
	data         DataSource
	rng          Rng
	now          func() time.Time
	dittoEnabled bool
	appVersion   string
	logFn        func(string, ...any)

	st   state
	lang string // locale tag for name resolution (protocol §10)

	shopDifficulty float64 // applied shop price multiplier; not in the save (bag-shop.md §1.3)

	lines   map[int]*evoLine // memory line cache, keyed by base id
	line    *evoLine         // the active Pokémon's line; nil until loaded
	details map[int]*Details
	names   map[string]map[string]string // "kind/name" → language → name; fetched names only

	prefetchedLineID int
	hatchDelayed     bool
	warmPending      bool
	window           levelUpWindow

	lastSeenDate      string
	lastSeenTotal     int
	haveLastSeen      bool
	lastObservedUsage time.Time

	newestSnapshot time.Time
	saveErr        error
	pending        []Event
}

// evoLine is a loaded evolution line, pruned to the animated-sprite range.
type evoLine struct {
	baseID int
	tree   Node
	rarity string
	names  map[int]map[string]string
}

// nameRef is a species shown in a celebration, resolved at build time in the current language.
type nameRef struct {
	speciesID int
	form      *string
	names     map[string]string
}

// levelUpWindow is the 4 s hatch/evolve, 6 s graduate or 5 s Ditto-reveal celebration
// (CS:1053, 1142, 1817, 1869).
type levelUpWindow struct {
	until     time.Time
	evolvedTo *nameRef
	graduated *nameRef
}

// openWindow starts a celebration window. Names of an expired window are dropped first, as
// upstream clears them once the window ends (CS:968-970); within a live window they carry over.
func (s *Store) openWindow(d time.Duration) {
	now := s.now()
	if !now.Before(s.window.until) {
		s.window = levelUpWindow{}
	}
	s.window.until = now.Add(d)
}

// UsageInput is one refresh's view of today's usage (CS:867).
type UsageInput struct {
	// TodayByProvider holds today's total tokens (input + output + cache write + cache read) per
	// provider, only for providers whose snapshot is dated TodayDate.
	TodayByProvider map[string]int
	TodayDate       string // local yyyy-MM-dd
	HasUsageData    bool   // at least one provider snapshot exists
}

// Open loads (or creates) the save in o.Dir. A non-object save is moved to .corrupt and the newest
// valid snapshot is restored.
func Open(o Options) (*Store, error) {
	if o.Data == nil {
		return nil, errors.New("companion: Options.Data is required")
	}
	s := &Store{
		dir: o.Dir, data: o.Data, rng: o.Rng, now: o.Now, dittoEnabled: o.DittoDisguiseEnabled,
		appVersion: o.AppVersion, logFn: o.Logf, shopDifficulty: defaultDifficulty,
		lines: map[int]*evoLine{}, details: map[int]*Details{}, names: map[string]map[string]string{},
	}
	if s.rng == nil {
		s.rng = cryptoRng{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	if err := s.loadState(); err != nil {
		return nil, err
	}
	s.lang = languageCode(s.st.Language)
	if snaps := s.listSnapshots(); len(snaps) > 0 {
		s.newestSnapshot = snaps[0].date
	}
	s.ensureProfiles()
	s.seedDexCompletions()
	return s, nil
}

func (s *Store) logf(format string, args ...any) {
	if s.logFn != nil {
		s.logFn(format, args...)
	}
}

func (s *Store) emit(e Event) { s.pending = append(s.pending, e) }

func (s *Store) drain() []Event {
	out := s.pending
	s.pending = nil
	return out
}

// SaveError returns the error of the last save attempt, nil once a save succeeds.
func (s *Store) SaveError() error { return s.saveErr }

// GrowthDifficulty returns the last applied growth multiplier (0.1–2.0).
func (s *Store) GrowthDifficulty() float64 { return s.st.GrowthDifficulty }

// SetLanguage sets the locale tag used for names (protocol §10). The save keeps upstream's enum.
func (s *Store) SetLanguage(tag string) {
	tag = languageCode(tag)
	s.lang = tag
	if app := appLanguage(tag); app != s.st.Language {
		s.st.Language = app
		s.save()
	}
}

// SetGrowthDifficulty clamps and applies a growth multiplier, rescaling the current egg or stage so
// its earned fraction is kept. It never completes a stage (CS:126-152).
func (s *Store) SetGrowthDifficulty(v float64) {
	c := clampDifficulty(v)
	if c == s.st.GrowthDifficulty {
		return
	}
	old := s.st.GrowthDifficulty
	rescaled := func(credits, base int) int {
		oldThr := max(1, int(math.Round(float64(base)*old)))
		newThr := max(1, int(math.Round(float64(base)*c)))
		value := float64(credits) / float64(oldThr) * float64(newThr)
		r := int(math.Min(float64(maxTokenValue), math.Max(0, math.Floor(value))))
		if credits < oldThr {
			return min(newThr-1, r)
		}
		return r
	}
	if a := s.st.Active; a != nil {
		a.UsedAtStage = rescaled(a.UsedAtStage, a.standardThreshold())
	} else {
		s.st.EggUsage = rescaled(s.st.EggUsage, eggHatchThreshold)
	}
	s.st.GrowthDifficulty = c
	s.save()
}

// SetRepresentative pins a Pokédex species (and form; nil means the default) for the top bar, or
// follows the companion again when speciesID is nil. It returns false, keeping the previous choice,
// for a species or form the player does not own (CS:230-236).
func (s *Store) SetRepresentative(speciesID *int, form *string) bool {
	if speciesID != nil && !s.st.ownsSpecies(*speciesID, form) {
		return false
	}
	id := 0
	if speciesID != nil {
		v := *speciesID
		speciesID, id = &v, v
	}
	s.st.RepresentativeSpeciesID = speciesID
	s.st.RepresentativeForm = formForSpecies(id, form)
	s.save()
	return true
}

func (s *Store) eggThreshold() int { return scaled(eggHatchThreshold, s.st.GrowthDifficulty) }

func (s *Store) stageThreshold(a *monState) int {
	return scaled(a.standardThreshold(), s.st.GrowthDifficulty)
}

// ---------------------------------------------------------------- ledger

// Update credits today's usage through the per-provider ledger, then hatches, prefetches the next
// species or loads the active line as needed (CS:867-997). It returns the events to emit.
func (s *Store) Update(ctx context.Context, in UsageInput) []Event {
	now := s.now()
	today := 0
	for _, v := range in.TodayByProvider {
		today += v
	}
	hasCurrent := in.HasUsageData && len(in.TodayByProvider) > 0
	if hasCurrent {
		if s.haveLastSeen && s.lastSeenDate == in.TodayDate && today > s.lastSeenTotal {
			s.lastObservedUsage = now
		}
		s.lastSeenDate, s.lastSeenTotal, s.haveLastSeen = in.TodayDate, today, true
	}
	st := &s.st
	switch {
	case !st.InstallBaselineSet && !hasCurrent:
		// An imported active Pokémon still shows and its line keeps loading (CS:880-887).
		s.loadCurrentLine(ctx)
		s.revealIfNeeded(ctx)
		return s.drain()
	case !st.InstallBaselineSet:
		st.InstallBaselineSet = true
		st.Claimed = copyLedger(in.TodayByProvider)
		st.LastDate = in.TodayDate
		s.save()
	case !hasCurrent:
		// A refresh without today's data never moves the ledger or the date (CS:894-897).
	case st.Claimed == nil:
		st.Claimed = copyLedger(in.TodayByProvider)
		st.LastDate = in.TodayDate
	case in.TodayDate != st.LastDate:
		// Day snapshots are not comparable: credit the new day's whole total and open every known
		// provider at 0, so one that reports late is still credited in full (CS:905-931).
		st.LastDate = in.TodayDate
		ledger := map[string]int{}
		for p := range st.Claimed {
			ledger[p] = 0
		}
		for p, v := range in.TodayByProvider {
			ledger[p] = v
		}
		st.Claimed = ledger
		s.credit(today)
	default:
		delta := 0
		for p, current := range in.TodayByProvider {
			previous, known := st.Claimed[p]
			switch {
			case !known:
				st.Claimed[p] = current // a new provider is seeded, never credited retroactively
			case current < previous:
				st.Claimed[p] = current // rebase this provider only
				s.logf("companion: usage regression provider=%s previous=%d current=%d; rebased", p, previous, current)
			default:
				delta += current - previous
				st.Claimed[p] = current
			}
		}
		s.credit(delta)
	}
	if st.Active == nil {
		if st.EggUsage >= s.eggThreshold() {
			s.hatchIfNeeded(ctx)
		} else {
			s.prefetchEgg(ctx)
		}
	}
	if st.Active != nil && s.line == nil {
		s.loadCurrentLine(ctx)
	}
	s.revealIfNeeded(ctx)
	if s.warmPending {
		s.warmActive(ctx)
	}
	s.save()
	s.autoSnapshotIfNeeded(now)
	return s.drain()
}

func copyLedger(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) credit(delta int) {
	if delta <= 0 {
		return
	}
	s.st.UsedSinceInstall += delta
	if s.st.Active == nil {
		s.st.EggUsage += delta
	} else {
		s.applyUsage(delta)
	}
}

// ---------------------------------------------------------------- growth

// applyUsage adds credits to the current stage and evolves or graduates while a threshold is met,
// carrying the excess over (at most 50 steps). Without a loaded line the credits are banked and
// evaluated when the line loads (CS:1002-1059).
func (s *Store) applyUsage(delta int) {
	s.st.Active.UsedAtStage += delta
	s.reconcileActiveProfileGrowth()
	if s.line == nil {
		s.save()
		return
	}
	for guard := 0; guard < 50 && s.st.Active != nil; guard++ {
		a := s.st.Active
		thr := s.stageThreshold(a)
		if a.UsedAtStage < thr {
			break
		}
		node, ok := findNode(s.line.tree, a.currentID())
		if !ok {
			break
		}
		if a.DittoDisguise != nil && !a.DittoRevealed {
			break // the reveal replaces this evolution; Update runs it (revealIfNeeded, CS:1016-1019)
		}
		if len(node.Children) == 0 {
			s.graduate()
			break
		}
		next, found := Node{}, false
		if i := a.StageIndex + 1; i < len(a.PlannedPathIDs) {
			for _, ch := range node.Children {
				if ch.ID == a.PlannedPathIDs[i] {
					next, found = ch, true
					break
				}
			}
		}
		if !found {
			next = s.pickChild(node, a.BaseID)
			repaired := repairedPlan(a.PathIDs, a.StageIndex, append([]int{node.ID}, s.makePlan(next, a.BaseID)...))
			a.PlannedPathIDs, a.TotalForms = repaired, len(repaired)
			s.logf("companion: repaired invalid planned path for base %d", a.BaseID)
		}
		from := s.lineRef(a.currentID(), a.Form)
		a.PathIDs = append(append([]int{}, a.PathIDs[:a.StageIndex+1]...), next.ID)
		a.StageIndex++
		a.UsedAtStage -= thr
		if d := s.details[next.ID]; d != nil {
			a.Profile.enrich(d)
		}
		to := s.lineRef(next.ID, a.Form)
		s.openWindow(4 * time.Second)
		s.window.evolvedTo = &to
		s.warmPending = true
		s.emit(EvolveEvent{
			Type: "event", Kind: "evolve", FromSpeciesID: from.speciesID, FromDisplayName: s.refName(from),
			SpeciesID: next.ID, Form: to.form, DisplayName: s.refName(to), IsShiny: a.displaysShiny(),
		})
	}
	s.reconcileActiveProfileGrowth()
	s.save()
}

// graduate archives the active Pokémon in the dex at level 100; the excess is discarded (CS:1123-1154).
func (s *Store) graduate() {
	a := s.st.Active
	a.Profile.advanceGrowth(graduationTotal(a.Rarity), a.Rarity)
	if d := s.details[a.currentID()]; d != nil {
		a.Profile.enrich(d)
	}
	finalID := a.currentID()
	s.st.CollectedFinals[strconv.Itoa(a.BaseID)+":"+strconv.Itoa(finalID)] = true
	names := map[int]map[string]string{}
	allNamed := true
	for _, id := range a.PathIDs {
		if n, ok := s.line.names[id]; ok {
			names[id] = n
		}
		allNamed = allNamed && len(s.line.names[id]) > 0
	}
	now := s.now()
	entry := dexEntry{
		ID: a.Profile.InstanceID, BaseID: a.BaseID, FinalID: finalID, ChainOrder: append([]int{}, a.PathIDs...),
		Rarity: a.Rarity, CaughtAt: &now, IsShiny: a.IsShiny, Nature: a.Nature, Form: formForLine(a.BaseID, a.Form),
		Profile: a.Profile, Prayers: a.Prayers, Names: names,
	}
	if allNamed {
		entry.NamesVersion = intPtr(currentNamesVersion)
	}
	s.st.Dex = append(s.st.Dex, entry)
	ref := s.lineRef(finalID, a.Form)
	s.openWindow(6 * time.Second)
	s.window.graduated = &ref
	s.emit(GraduateEvent{
		Type: "event", Kind: "graduate", SpeciesID: finalID, Form: ref.form, DisplayName: s.refName(ref),
		Rarity: a.Rarity, IsShiny: a.IsShiny, EntryID: entry.ID,
	})
	s.st.Active = nil
	s.st.reconcileRepresentative()
	s.line = nil
	s.st.EggUsage = 0
	s.st.EggPrayers = nil
	s.hatchDelayed = false
}

// reconcileActiveProfileGrowth keeps the profile's standard-unit growth in step with the stage:
// completed phases at full value plus the current phase's earned fraction (CS:2250-2264).
func (s *Store) reconcileActiveProfileGrowth() {
	a := s.st.Active
	if a == nil {
		return
	}
	completed := reconstructedGrowthTokens(a.Rarity, a.TotalForms, a.StageIndex, 0)
	standard := phaseThreshold(a.Rarity, a.TotalForms, a.StageIndex, 1)
	fraction := math.Min(1, math.Max(0, float64(a.UsedAtStage)/float64(max(1, s.stageThreshold(a)))))
	candidate := min(graduationTotal(a.Rarity), completed+int(math.Floor(float64(standard)*fraction)))
	a.Profile.advanceGrowth(candidate, a.Rarity)
	if d := s.details[a.currentID()]; d != nil {
		a.Profile.enrich(d)
	}
}

// ---------------------------------------------------------------- evolution plan

func findNode(n Node, id int) (Node, bool) {
	if n.ID == id {
		return n, true
	}
	for _, ch := range n.Children {
		if f, ok := findNode(ch, id); ok {
			return f, true
		}
	}
	return Node{}, false
}

func finalIDs(n Node) []int {
	if len(n.Children) == 0 {
		return []int{n.ID}
	}
	var out []int
	for _, ch := range n.Children {
		out = append(out, finalIDs(ch)...)
	}
	return out
}

func depth(n Node) int {
	d := 0
	for _, ch := range n.Children {
		d = max(d, depth(ch))
	}
	return d + 1
}

// prune keeps only species 1…649 (Gen V animated sprites); a pruned node drops its subtree (CM:349-353).
func prune(n Node) (Node, bool) {
	if n.ID < 1 || n.ID > maxSpeciesID {
		return Node{}, false
	}
	out := Node{ID: n.ID}
	for _, ch := range n.Children {
		if p, ok := prune(ch); ok {
			out.Children = append(out.Children, p)
		}
	}
	return out, true
}

// pickChild prefers children that can still reach an uncollected final; one draw per call, even
// with a single child (CS:1061-1067).
func (s *Store) pickChild(n Node, baseID int) Node {
	var fresh []Node
	for _, ch := range n.Children {
		for _, f := range finalIDs(ch) {
			if !s.st.CollectedFinals[strconv.Itoa(baseID)+":"+strconv.Itoa(f)] {
				fresh = append(fresh, ch)
				break
			}
		}
	}
	pool := fresh
	if len(pool) == 0 {
		pool = n.Children
	}
	return pool[int(s.rng.Next()%uint64(len(pool)))]
}

// makePlan walks root to leaf, one draw per non-leaf node (CS:1069-1078).
func (s *Store) makePlan(root Node, baseID int) []int {
	plan := []int{root.ID}
	for n := root; len(n.Children) > 0; {
		n = s.pickChild(n, baseID)
		plan = append(plan, n.ID)
	}
	return plan
}

// repairedPlan keeps the realized prefix and appends a fallback route that starts at its end (CS:1080-1086).
func repairedPlan(realized []int, stageIndex int, fallback []int) []int {
	if len(realized) == 0 {
		return fallback
	}
	prefix := append([]int{}, realized[:min(stageIndex, len(realized)-1)+1]...)
	if len(fallback) == 0 || fallback[0] != prefix[len(prefix)-1] {
		return prefix
	}
	return append(prefix, fallback[1:]...)
}

func longestValidPath(ids []int, root Node) ([]int, Node) {
	path, n := []int{root.ID}, root
	if len(ids) == 0 || ids[0] != root.ID {
		return path, n
	}
	for _, id := range ids[1:] {
		next, ok := Node{}, false
		for _, ch := range n.Children {
			if ch.ID == id {
				next, ok = ch, true
				break
			}
		}
		if !ok {
			break
		}
		path, n = append(path, id), next
	}
	return path, n
}

// normalizeEvolution fits a saved path and plan to the loaded tree. A complete valid plan is
// reused, so a restart spends no RNG (CS:1102-1121).
func (s *Store) normalizeEvolution(m *monState, root Node) {
	realized, last := longestValidPath(m.PathIDs, root)
	candidate, candLast := longestValidPath(m.PlannedPathIDs, root)
	reuse := reflect.DeepEqual(candidate, m.PlannedPathIDs) && len(candidate) >= len(realized) &&
		reflect.DeepEqual(candidate[:len(realized)], realized) && len(candLast.Children) == 0
	plan := candidate
	if !reuse {
		plan = append(append([]int{}, realized...), s.makePlan(last, m.BaseID)[1:]...)
	}
	m.PathIDs, m.PlannedPathIDs = realized, plan
	m.StageIndex, m.TotalForms = len(realized)-1, len(plan)
}

// ---------------------------------------------------------------- lines

func (s *Store) fetchLine(ctx context.Context, baseID int) (*evoLine, error) {
	if l := s.lines[baseID]; l != nil {
		return l, nil
	}
	raw, err := s.data.Line(ctx, baseID)
	if err != nil {
		return nil, err
	}
	tree, ok := prune(raw.Tree)
	if !ok {
		tree = Node{ID: baseID}
	}
	names := raw.Names
	if names == nil {
		names = map[int]map[string]string{}
	}
	l := &evoLine{baseID: baseID, tree: tree, rarity: rarityFrom(raw.CaptureRate, raw.IsLegendary, raw.IsMythical), names: names}
	s.lines[baseID] = l
	return l, nil
}

// loadCurrentLine loads the active line, normalizes the saved path, then evaluates banked usage (CS:1880-1904).
func (s *Store) loadCurrentLine(ctx context.Context) {
	a := s.st.Active
	if a == nil || s.line != nil {
		return
	}
	l, err := s.fetchLine(ctx, a.BaseID)
	if err != nil {
		return
	}
	s.normalizeEvolution(a, l.tree)
	s.st.reconcileRepresentative()
	s.line = l
	s.save() // persist the migration choice before usage is evaluated
	s.applyUsage(0)
	s.warmActive(ctx)
}

// ---------------------------------------------------------------- hatch

// hatchIfNeeded hatches a ready egg: the prefetched species, or a fresh weighted roll (CS:1560-1590).
func (s *Store) hatchIfNeeded(ctx context.Context) {
	if s.st.Active != nil || s.st.EggUsage < s.eggThreshold() {
		return
	}
	var base int
	if p := s.st.PendingHatchID; p != nil {
		base = *p
	} else {
		id, ok := s.chooseBase(ctx)
		if !ok {
			s.hatchDelayed = true
			return
		}
		base = id
	}
	var form *string
	if p := s.st.PendingHatchID; p != nil && *p == base {
		form = s.st.PendingForm
	}
	s.hatchCore(ctx, base, form)
}

// hatch hatches baseID directly (upstream hatch(baseID:)).
func (s *Store) hatch(ctx context.Context, baseID int) { s.hatchCore(ctx, baseID, nil) }

// hatchCore rolls the individual. Draw order after the species: shiny, nature, [Ditto disguise],
// one draw per non-leaf plan node, profile seed, then the form when none was prepared (CS:1739-1824).
func (s *Store) hatchCore(ctx context.Context, baseID int, pendingForm *string) {
	l, err := s.fetchLine(ctx, baseID)
	if err != nil {
		if s.st.Active == nil && s.st.EggUsage >= s.eggThreshold() {
			s.hatchDelayed = true
		}
		s.logf("companion: hatch line fetch failed for base %d; egg kept: %v", baseID, err)
		return
	}
	st := &s.st
	// The last guard of a bought guarantee: only the line knows the real rarity, and the index has
	// no legendary flag. A species below the tier is dropped with no draw; the egg, its usage and
	// its tier are kept, and the next tick rolls again (CS:1759-1767).
	if st.EggTier != nil && rarityRank(l.rarity) < rarityRank(*st.EggTier) {
		s.logf("companion: hatch rolled %s below the guaranteed %s; re-rolling next tick", l.rarity, *st.EggTier)
		st.PendingHatchID, st.PendingForm = nil, nil
		s.prefetchedLineID = 0
		if st.Active == nil && st.EggUsage >= s.eggThreshold() {
			s.hatchDelayed = true
		}
		s.save()
		return
	}
	st.PendingHatchID, st.PendingForm = nil, nil
	s.prefetchedLineID = 0
	s.line = l
	s.hatchDelayed = false
	overflow := max(0, st.EggUsage-s.eggThreshold())
	st.EggUsage = 0
	st.EggTier = nil // the hatch consumes a premium guarantee (CS:1777)
	prayers := st.EggPrayers
	st.EggPrayers = nil
	// The Shiny Charm changes only the modulus of the same single draw (CS:1781-1783).
	charm := s.ownsShinyCharm()
	odds := shinyOdds(charm)
	isShiny := rollsShiny(s.rng.Next(), charm)
	nature := natures[int(s.rng.Next()%uint64(len(natures)))]
	var disguise *int
	if s.dittoEnabled && dittoDisguiseHit(l.rarity, depth(l.tree), s.rng.Next()) {
		disguise = intPtr(l.baseID)
	}
	plan := s.makePlan(l.tree, l.baseID)
	prof := generateProfile(s.rng.Next(), 0, newUUID())
	if d := s.details[l.baseID]; d != nil {
		prof.enrich(d)
	}
	form := pendingForm
	if form == nil {
		form = s.rollLineForm(l.baseID)
	}
	st.Active = &monState{
		BaseID: l.baseID, PathIDs: []int{l.baseID}, PlannedPathIDs: plan, Rarity: l.rarity, TotalForms: len(plan),
		IsShiny: isShiny, Nature: &nature, Profile: prof, HasGrowthBoost: st.hasCollectedFinal(l.baseID),
		DittoDisguise: disguise, Form: formForLine(l.baseID, form), Prayers: prayers,
	}
	ref := s.lineRef(l.baseID, st.Active.Form)
	s.emit(HatchEvent{
		Type: "event", Kind: "hatch", SpeciesID: l.baseID, Form: ref.form, DisplayName: s.refName(ref),
		Rarity: l.rarity, IsShiny: isShiny && disguise == nil, ShinyOdds: odds, Prayers: prayerCount(prayers),
	})
	s.openWindow(4 * time.Second)
	s.window.evolvedTo = nil
	if overflow > 0 {
		s.applyUsage(overflow)
	}
	s.save()
	s.warmActive(ctx)
}

// rollLineForm draws only for lines with forms, so other species keep their sequence (CS:1681-1685).
func (s *Store) rollLineForm(baseID int) *string {
	forms := lineForms(baseID)
	if len(forms) == 0 {
		return nil
	}
	f := rollForm(s.rng.Next(), forms, s.st.collectedLineForms(baseID))
	return &f
}

// chooseBase rolls a species weighted by capture rate (halved once collected) in exactly one draw;
// without an index it falls back to REST. A premium egg first narrows the index to its tier; an
// empty narrowed pool keeps the egg without drawing (CS:1912-1937).
func (s *Store) chooseBase(ctx context.Context) (int, bool) {
	full, err := s.data.BaseIndex(ctx)
	if err != nil || len(full) == 0 {
		return s.chooseBaseViaREST(ctx)
	}
	idx := full
	if t := s.st.EggTier; t != nil {
		idx = nil
		for _, e := range full {
			if tierIncludes(*t, e.CaptureRate) {
				idx = append(idx, e)
			}
		}
		if len(idx) == 0 {
			s.logf("companion: no hatch candidate for the guaranteed %s; egg kept", *t)
			return 0, false
		}
	}
	weights := make([]int, len(idx))
	total := 0
	for i, e := range idx {
		weights[i] = collectionWeight(e.CaptureRate, s.st.hasCollectedFinal(e.ID))
		total += weights[i]
	}
	r := int(s.rng.Next() % uint64(total))
	for i := 0; i < len(idx)-1; i++ {
		if r -= weights[i]; r < 0 {
			return idx[i].ID, true
		}
	}
	return idx[len(idx)-1].ID, true // any remaining roll belongs to the last entry
}

// chooseBaseViaREST tries up to 16 uniform ids in 1…649 and keeps the first base species that meets
// the egg's tier; a tier miss still consumes the attempt and its draw. A network error gives up
// until the next tick (CS:1942-1963).
func (s *Store) chooseBaseViaREST(ctx context.Context) (int, bool) {
	for attempt := 0; attempt < 16; attempt++ {
		id := int(s.rng.Next()%maxSpeciesID) + 1
		if id == dittoSpeciesID {
			continue
		}
		sp, err := s.data.Species(ctx, id)
		if err != nil {
			return 0, false
		}
		if sp.EvolvesFrom != 0 {
			continue
		}
		if t := s.st.EggTier; t != nil && !tierIncludes(*t, sp.CaptureRate) {
			continue
		}
		return id, true
	}
	return 0, false
}

// prefetchEgg pre-rolls and persists the next species and its form, then warms its line, so the
// hatch needs no network (CS:1628-1678).
func (s *Store) prefetchEgg(ctx context.Context) {
	if s.st.PendingHatchID == nil {
		id, ok := s.chooseBase(ctx)
		if !ok {
			return
		}
		s.st.PendingHatchID = &id
		s.st.PendingForm = s.rollLineForm(id)
		s.save()
	}
	id := *s.st.PendingHatchID
	if s.prefetchedLineID == id {
		s.hatchDelayed = false
		return
	}
	if _, err := s.fetchLine(ctx, id); err != nil {
		return
	}
	// Only a complete prefetch counts: with a form name missing, the hatch event would carry the
	// raw form identifier, and nothing would fetch the name again before the hatch.
	if !s.warmFormNames(ctx, id, s.st.PendingForm) {
		return
	}
	s.prefetchedLineID = id
	s.hatchDelayed = false
}

// ---------------------------------------------------------------- details and names

// warmActive loads the current species' details (enriching its profile) and the names its card shows.
func (s *Store) warmActive(ctx context.Context) {
	s.warmPending = false
	a := s.st.Active
	if a == nil {
		return
	}
	typesOK := false
	d, err := s.loadDetails(ctx, a.currentID())
	if err == nil {
		typesOK = s.warmNames(ctx, "type", d.Types)
	}
	formsOK := s.warmFormNames(ctx, a.BaseID, a.Form)
	// A failed fetch is retried by the next Update. Otherwise the card would lack its types and
	// localized names until the next evolution; the daemon's memory-only data view always misses
	// on the first attempt and fetches in the background.
	s.warmPending = err != nil || !typesOK || !formsOK
}

// loadDetails returns cached or fetched details and enriches every matching profile once (CS:2151-2197).
func (s *Store) loadDetails(ctx context.Context, speciesID int) (*Details, error) {
	d := s.details[speciesID]
	if d == nil {
		var err error
		if d, err = s.data.Details(ctx, speciesID); err != nil {
			return nil, err
		}
		s.details[speciesID] = d
	}
	changed := false
	enrich := func(p *profile) {
		before := p.clone()
		p.enrich(d)
		changed = changed || !reflect.DeepEqual(before, p)
	}
	if a := s.st.Active; a != nil && a.currentID() == speciesID {
		enrich(a.Profile)
	}
	for i := range s.st.Dex {
		if s.st.Dex[i].FinalID == speciesID {
			enrich(s.st.Dex[i].Profile)
		}
	}
	if changed {
		s.save()
	}
	return d, nil
}

func nameKey(kind, name string) string { return kind + "/" + name }

// warmNames fetches missing names; failures are not cached, so a fallback is never kept. It
// reports whether every fetch succeeded (an empty answer counts as success).
func (s *Store) warmNames(ctx context.Context, kind string, names []string) bool {
	ok := true
	for _, n := range names {
		if _, have := s.names[nameKey(kind, n)]; have {
			continue
		}
		m, err := s.data.Names(ctx, kind, n)
		if err != nil {
			ok = false
			continue
		}
		if len(m) > 0 {
			s.names[nameKey(kind, n)] = m
		}
	}
	return ok
}

func (s *Store) warmFormNames(ctx context.Context, baseID int, form *string) bool {
	var res []string
	for _, id := range formSpecies(baseID) {
		if r := formNameResource(id, form); r != "" {
			res = append(res, r)
		}
	}
	return s.warmNames(ctx, "pokemon-form", res)
}

// apiName resolves a type, ability or move name, else returns the API identifier (protocol §10).
func (s *Store) apiName(kind, name string) string {
	if v, ok := resolveName(s.names[nameKey(kind, name)], s.lang); ok {
		return v
	}
	return name
}

// formLabel is a form's localized label: Unown's letter, "Normal Form" for the unnamed usual looks,
// the pokemon-form name, else the title-cased identifier; "" for species without forms (PF:229-237).
func (s *Store) formLabel(speciesID int, form *string) string {
	r := formForSpecies(speciesID, form)
	switch {
	case r == nil:
		return ""
	case speciesID == unownSpeciesID:
		return formSymbol(*r)
	case unnamedDefaultSpecies[speciesID] && isDefaultForm(r, speciesID):
		return normalFormNames[appLanguage(s.lang)]
	}
	if v, ok := resolveName(s.names[nameKey("pokemon-form", formNameResource(speciesID, r))], s.lang); ok {
		return v
	}
	return identifierLabel(*r)
}

func (s *Store) speciesName(names map[string]string, id int) string {
	if v, ok := resolveName(names, s.lang); ok {
		return v
	}
	return "#" + strconv.Itoa(id)
}

// lineRef captures a species of the active line with its resolved form and names.
func (s *Store) lineRef(id int, form *string) nameRef {
	var names map[string]string
	if s.line != nil {
		names = s.line.names[id]
	}
	return nameRef{speciesID: id, form: formForSpecies(id, form), names: names}
}

func (s *Store) refName(r nameRef) string {
	return formDisplayName(s.speciesName(r.names, r.speciesID), r.speciesID, r.form, s.formLabel(r.speciesID, r.form))
}

// ---------------------------------------------------------------- load-time repair

// ensureProfiles regenerates missing profiles from deterministic FNV-1a seeds (no game RNG) and
// reconciles the active profile's growth, as upstream does on every launch (CS:2201-2246). The
// one-off pre-profile backup file is not ported.
func (s *Store) ensureProfiles() {
	changed := false
	if a := s.st.Active; a != nil && a.Profile == nil {
		ids := make([]string, len(a.PathIDs))
		for i, id := range a.PathIDs {
			ids[i] = strconv.Itoa(id)
		}
		key := "active:" + strconv.Itoa(a.BaseID) + ":" + strings.Join(ids, ",") + ":" + s.st.LastDate
		a.Profile = generateProfile(fnv1a64(key), 0, newUUID())
		changed = true
	}
	for i := range s.st.Dex {
		e := &s.st.Dex[i]
		if e.Profile != nil {
			continue
		}
		growth := graduationTotal(e.Rarity)
		if e.ReleasedAt != nil {
			n := max(1, len(e.ChainOrder))
			growth = reconstructedGrowthTokens(e.Rarity, n, n-1, 0)
		}
		p := generateProfile(fnv1a64("dex:"+e.ID+":"+strconv.Itoa(e.FinalID)), growth, e.ID)
		p.advanceGrowth(p.GrowthTokens, e.Rarity)
		e.Profile = p
		changed = true
	}
	var before profile
	if s.st.Active != nil {
		before = *s.st.Active.Profile.clone()
	}
	s.reconcileActiveProfileGrowth()
	if changed || (s.st.Active != nil && !reflect.DeepEqual(&before, s.st.Active.Profile)) {
		s.save()
	}
}
