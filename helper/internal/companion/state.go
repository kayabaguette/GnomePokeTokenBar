package companion

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"
)

// Save model (upstream CompanionModel.swift CompanionState, MonState, DexEntry). Key names match
// upstream so a later macOS-export import stays cheap; dates are RFC 3339 strings instead of Apple
// reference-date doubles. Deferred features (prayers, Ditto) keep their fields so they round-trip
// unchanged.

const (
	schemaVersion       = 1
	currentNamesVersion = 1
)

type prayerRecord struct {
	Count int `json:"count"`
}

type monState struct {
	BaseID         int           `json:"baseID"`
	PathIDs        []int         `json:"pathIDs"`
	PlannedPathIDs []int         `json:"plannedPathIDs"`
	StageIndex     int           `json:"stageIndex"`
	UsedAtStage    int           `json:"usedAtStage"`
	Rarity         string        `json:"rarity"`
	TotalForms     int           `json:"totalForms"`
	IsShiny        bool          `json:"isShiny"`
	Nature         *string       `json:"nature,omitempty"`
	Form           *string       `json:"form,omitempty"`
	Profile        *profile      `json:"profile,omitempty"`
	HasGrowthBoost bool          `json:"hasGrowthBoost"`
	DittoDisguise  *int          `json:"dittoDisguise,omitempty"`
	DittoRevealed  bool          `json:"dittoRevealed"`
	Prayers        *prayerRecord `json:"prayers,omitempty"`
}

// currentID is pathIDs[min(stageIndex, count−1)], or baseID for an empty path (CM:486).
func (m *monState) currentID() int {
	if len(m.PathIDs) == 0 {
		return m.BaseID
	}
	return m.PathIDs[max(0, min(m.StageIndex, len(m.PathIDs)-1))]
}

// displaysShiny hides a disguised Ditto's shiny until the reveal (CM:484).
func (m *monState) displaysShiny() bool {
	return m.IsShiny && (m.DittoDisguise == nil || m.DittoRevealed)
}

// reached is the realized path up to the current stage.
func (m *monState) reached() []int {
	return m.PathIDs[:max(0, min(m.StageIndex+1, len(m.PathIDs)))]
}

func (m *monState) standardThreshold() int {
	mult := 1
	if m.HasGrowthBoost {
		mult = repeatGrowthMultiplier
	}
	return phaseThreshold(m.Rarity, m.TotalForms, m.StageIndex, mult)
}

type dexEntry struct {
	ID           string                    `json:"id"`
	BaseID       int                       `json:"baseID"`
	FinalID      int                       `json:"finalID"`
	ChainOrder   []int                     `json:"chainOrder"`
	Rarity       string                    `json:"rarity"`
	CaughtAt     *time.Time                `json:"caughtAt,omitempty"`
	IsShiny      bool                      `json:"isShiny"`
	Nature       *string                   `json:"nature,omitempty"`
	Form         *string                   `json:"form,omitempty"`
	Profile      *profile                  `json:"profile,omitempty"`
	Prayers      *prayerRecord             `json:"prayers,omitempty"`
	Names        map[int]map[string]string `json:"names,omitempty"`
	NamesVersion *int                      `json:"namesVersion,omitempty"`
	ReleasedAt   *time.Time                `json:"releasedAt,omitempty"`
}

type state struct {
	SchemaVersion           int               `json:"schemaVersion"`
	GrowthDifficulty        float64           `json:"growthDifficulty"` // last applied; upstream kept it in UserDefaults
	InstallBaselineSet      bool              `json:"installBaselineSet"`
	UsedSinceInstall        int               `json:"usedSinceInstall"`
	SpentTokens             int               `json:"spentTokens"`
	EggUsage                int               `json:"eggUsage"`
	EggTier                 *string           `json:"eggTier,omitempty"`
	PendingHatchID          *int              `json:"pendingHatchID,omitempty"`
	PendingForm             *string           `json:"pendingForm,omitempty"`
	EggPrayers              *prayerRecord     `json:"eggPrayers,omitempty"`
	LastDate                string            `json:"lastDate"`
	Active                  *monState         `json:"active,omitempty"`
	RepresentativeSpeciesID *int              `json:"representativeSpeciesID,omitempty"`
	RepresentativeForm      *string           `json:"representativeForm,omitempty"`
	Dex                     []dexEntry        `json:"dex"`
	Language                string            `json:"language"`
	Inventory               map[string]int    `json:"inventory"`
	CandyGrantTier          map[string]int    `json:"candyGrantTier"`
	CandyWindowEpoch        map[string]string `json:"candyWindowEpoch"`
	CandyFeatureSeeded      bool              `json:"candyFeatureSeeded"`
	DexCompletionsSeeded    bool              `json:"dexCompletionsSeeded"`

	// Claimed is the per-provider "already credited today" ledger. nil (key absent) means not yet
	// seeded, which is distinct from an empty map.
	Claimed                map[string]int  `json:"-"`
	CollectedFinals        map[string]bool `json:"-"` // "base:final", graduations only
	NotifiedDexCompletions map[string]bool `json:"-"` // badge keys already announced
}

func freshState() state {
	return state{
		SchemaVersion:          schemaVersion,
		GrowthDifficulty:       defaultDifficulty,
		Dex:                    []dexEntry{},
		Language:               "en",
		Inventory:              map[string]int{},
		CandyGrantTier:         map[string]int{},
		CandyWindowEpoch:       map[string]string{},
		CollectedFinals:        map[string]bool{},
		NotifiedDexCompletions: map[string]bool{},
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// encodeState serializes the save; sets are written sorted.
func encodeState(s *state) ([]byte, error) {
	type plain state
	out := struct {
		*plain
		Claimed         *map[string]int `json:"claimedTodayTokensByProvider,omitempty"`
		CollectedFinals []string        `json:"collectedFinals"`
		Notified        []string        `json:"notifiedDexCompletions"`
	}{
		plain:           (*plain)(s),
		CollectedFinals: sortedKeys(s.CollectedFinals),
		Notified:        sortedKeys(s.NotifiedDexCompletions),
	}
	if s.Claimed != nil {
		out.Claimed = &s.Claimed
	}
	return json.MarshalIndent(out, "", "  ")
}

// ---------------------------------------------------------------- lenient decode

type jsonObject map[string]json.RawMessage

var errMissing = errors.New("missing value")

func parseObject(data []byte) (jsonObject, bool) {
	var o jsonObject
	if err := json.Unmarshal(data, &o); err != nil || o == nil {
		return nil, false
	}
	return o, true
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// required decodes a mandatory key (upstream `decode`): absent, null or mistyped is an error.
func required[T any](o jsonObject, key string) (T, error) {
	var v T
	raw, ok := o[key]
	if !ok || isNull(raw) {
		return v, errMissing
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

// optional decodes an optional key (upstream `decodeIfPresent`): absent or null is nil, mistyped
// is an error.
func optional[T any](o jsonObject, key string) (*T, error) {
	raw, ok := o[key]
	if !ok || isNull(raw) {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// lenient decodes a key, falling back to def on any failure (CM:640-649).
func lenient[T any](o jsonObject, key string, def T) T {
	v, err := required[T](o, key)
	if err != nil {
		return def
	}
	return v
}

// lenientOptional decodes an optional key, nil on any failure.
func lenientOptional[T any](o jsonObject, key string) *T {
	v, _ := optional[T](o, key)
	return v
}

func toSet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// decodeState reads a save leniently: each field falls back to its default on a type mismatch and
// dex entries decode one by one. ok is false only when data is not a JSON object.
func decodeState(data []byte) (state, bool) {
	o, ok := parseObject(data)
	if !ok {
		return state{}, false
	}
	s := freshState()
	s.SchemaVersion = lenient(o, "schemaVersion", schemaVersion)
	s.GrowthDifficulty = clampDifficulty(lenient(o, "growthDifficulty", defaultDifficulty))
	s.InstallBaselineSet = lenient(o, "installBaselineSet", false)
	s.UsedSinceInstall = lenient(o, "usedSinceInstall", 0)
	s.SpentTokens = lenient(o, "spentTokens", 0)
	s.EggUsage = lenient(o, "eggUsage", 0)
	if t := lenientOptional[string](o, "eggTier"); t != nil && validRarity(*t) {
		s.EggTier = t
	}
	s.PendingHatchID = lenientOptional[int](o, "pendingHatchID")
	pendingBase := 0
	if s.PendingHatchID != nil {
		pendingBase = *s.PendingHatchID
	}
	s.PendingForm = formForLine(pendingBase, lenientOptional[string](o, "pendingForm"))
	s.EggPrayers = lenientOptional[prayerRecord](o, "eggPrayers")
	if _, present := o["claimedTodayTokensByProvider"]; present {
		s.Claimed = lenient(o, "claimedTodayTokensByProvider", map[string]int{})
	}
	s.LastDate = lenient(o, "lastDate", "")
	if raw, ok := o["active"]; ok && !isNull(raw) {
		if m, err := decodeMon(raw); err == nil {
			s.Active = m
		}
	}
	s.RepresentativeSpeciesID = lenientOptional[int](o, "representativeSpeciesID")
	repID := 0
	if s.RepresentativeSpeciesID != nil {
		repID = *s.RepresentativeSpeciesID
	}
	s.RepresentativeForm = formForSpecies(repID, lenientOptional[string](o, "representativeForm"))
	for _, raw := range lenient(o, "dex", []json.RawMessage{}) {
		if e, err := decodeDexEntry(raw); err == nil {
			s.Dex = append(s.Dex, *e)
		}
	}
	s.CollectedFinals = toSet(lenient(o, "collectedFinals", []string{}))
	s.Language = lenient(o, "language", "en")
	s.Inventory = lenient(o, "inventory", map[string]int{})
	s.CandyGrantTier = lenient(o, "candyGrantTier", map[string]int{})
	s.CandyWindowEpoch = lenient(o, "candyWindowEpoch", map[string]string{})
	s.CandyFeatureSeeded = lenient(o, "candyFeatureSeeded", false)
	s.NotifiedDexCompletions = toSet(lenient(o, "notifiedDexCompletions", []string{}))
	s.DexCompletionsSeeded = lenient(o, "dexCompletionsSeeded", false)
	return s, true
}

// decodeMon fails like upstream MonState.init(from:): a missing required field, an empty path, an
// unknown rarity or nature all drop the active Pokémon (the state falls back to an egg). A corrupt
// profile or prayer record only decodes as nil (CM:522-549).
func decodeMon(raw json.RawMessage) (*monState, error) {
	o, ok := parseObject(raw)
	if !ok {
		return nil, errMissing
	}
	var m monState
	var err error
	if m.BaseID, err = required[int](o, "baseID"); err != nil {
		return nil, err
	}
	if m.PathIDs, err = required[[]int](o, "pathIDs"); err != nil || len(m.PathIDs) == 0 {
		return nil, errMissing
	}
	m.PlannedPathIDs = m.PathIDs
	if plan, err := optional[[]int](o, "plannedPathIDs"); err != nil {
		return nil, err
	} else if plan != nil && len(*plan) > 0 {
		m.PlannedPathIDs = *plan
	}
	stage, err := required[int](o, "stageIndex")
	if err != nil {
		return nil, err
	}
	m.StageIndex = min(max(0, stage), len(m.PathIDs)-1)
	if m.UsedAtStage, err = required[int](o, "usedAtStage"); err != nil {
		return nil, err
	}
	if m.Rarity, err = required[string](o, "rarity"); err != nil || !validRarity(m.Rarity) {
		return nil, errMissing
	}
	if m.TotalForms, err = required[int](o, "totalForms"); err != nil {
		return nil, err
	}
	if m.IsShiny, err = optionalBool(o, "isShiny"); err != nil {
		return nil, err
	}
	if m.Nature, err = optionalNature(o); err != nil {
		return nil, err
	}
	m.Profile = decodeProfileField(o)
	if m.HasGrowthBoost, err = optionalBool(o, "hasGrowthBoost"); err != nil {
		return nil, err
	}
	if m.DittoDisguise, err = optional[int](o, "dittoDisguise"); err != nil {
		return nil, err
	}
	if m.DittoRevealed, err = optionalBool(o, "dittoRevealed"); err != nil {
		return nil, err
	}
	m.Prayers = lenientOptional[prayerRecord](o, "prayers")
	m.Form = formForLine(m.BaseID, lenientOptional[string](o, "form"))
	return &m, nil
}

// decodeDexEntry fails like upstream DexEntry.init(from:), dropping only this entry (CM:610-630).
func decodeDexEntry(raw json.RawMessage) (*dexEntry, error) {
	o, ok := parseObject(raw)
	if !ok {
		return nil, errMissing
	}
	var e dexEntry
	var err error
	if id, err := optional[string](o, "id"); err != nil {
		return nil, err
	} else if id != nil {
		e.ID = *id
	} else {
		e.ID = newUUID()
	}
	if e.BaseID, err = required[int](o, "baseID"); err != nil {
		return nil, err
	}
	if e.FinalID, err = required[int](o, "finalID"); err != nil {
		return nil, err
	}
	if e.ChainOrder, err = required[[]int](o, "chainOrder"); err != nil {
		return nil, err
	}
	if e.Rarity, err = required[string](o, "rarity"); err != nil || !validRarity(e.Rarity) {
		return nil, errMissing
	}
	if e.CaughtAt, err = optional[time.Time](o, "caughtAt"); err != nil {
		return nil, err
	}
	if e.IsShiny, err = optionalBool(o, "isShiny"); err != nil {
		return nil, err
	}
	if e.Nature, err = optionalNature(o); err != nil {
		return nil, err
	}
	e.Profile = decodeProfileField(o)
	e.Prayers = lenientOptional[prayerRecord](o, "prayers")
	if names := lenientOptional[map[int]map[string]string](o, "names"); names != nil {
		e.Names = *names
	}
	e.NamesVersion = lenientOptional[int](o, "namesVersion")
	if e.ReleasedAt, err = optional[time.Time](o, "releasedAt"); err != nil {
		return nil, err
	}
	e.Form = formForLine(e.BaseID, lenientOptional[string](o, "form"))
	return &e, nil
}

func optionalBool(o jsonObject, key string) (bool, error) {
	v, err := optional[bool](o, key)
	if err != nil || v == nil {
		return false, err
	}
	return *v, nil
}

func optionalNature(o jsonObject) (*string, error) {
	n, err := optional[string](o, "nature")
	if err != nil {
		return nil, err
	}
	if n != nil && !validNature(*n) {
		return nil, errMissing
	}
	return n, nil
}

// decodeProfileField decodes a profile, nil when any field is missing or mistyped (CM:543).
func decodeProfileField(o jsonObject) *profile {
	po, ok := parseObject(o["profile"])
	if !ok {
		return nil
	}
	ivo, ok := parseObject(po["ivs"])
	if !ok {
		return nil
	}
	var p profile
	errs := []error{}
	collect := func(err error) { errs = append(errs, err) }
	var err error
	p.InstanceID, err = required[string](po, "instanceID")
	collect(err)
	p.Seed, err = required[uint64](po, "seed")
	collect(err)
	p.Gender, err = optional[string](po, "gender")
	collect(err)
	for i, key := range []string{"hp", "attack", "defense", "specialAttack", "specialDefense", "speed"} {
		*p.IVs.fields()[i], err = required[int](ivo, key)
		collect(err)
	}
	p.AbilitySlot, err = optional[int](po, "abilitySlot")
	collect(err)
	p.AbilityName, err = optional[string](po, "abilityName")
	collect(err)
	p.AbilityIsHidden, err = required[bool](po, "abilityIsHidden")
	collect(err)
	p.Level, err = required[int](po, "level")
	collect(err)
	p.GrowthTokens, err = required[int](po, "growthTokens")
	collect(err)
	p.Moves, err = required[[]knownMove](po, "moves")
	collect(err)
	if p.Gender != nil && *p.Gender != genderMale && *p.Gender != genderFemale && *p.Gender != genderGenderless {
		return nil
	}
	if errors.Join(errs...) != nil {
		return nil
	}
	return &p
}

// ---------------------------------------------------------------- sanitize

func dedupe(ids []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// sanitize clamps values that reach arithmetic, at the trust boundary (ST:143-193).
func (s *state) sanitize() {
	s.UsedSinceInstall = clampToken(s.UsedSinceInstall)
	s.SpentTokens = clampToken(s.SpentTokens)
	s.EggUsage = clampToken(s.EggUsage)
	for k, v := range s.Claimed {
		s.Claimed[k] = clampToken(v)
	}
	if s.Active != nil {
		s.EggTier, s.PendingHatchID, s.EggPrayers = nil, nil, nil
	}
	if s.EggPrayers != nil {
		s.EggPrayers.Count = clampToken(s.EggPrayers.Count)
	}
	pendingBase := 0
	if s.PendingHatchID != nil {
		pendingBase = *s.PendingHatchID
	}
	s.PendingForm = formForLine(pendingBase, s.PendingForm)
	if s.EggTier != nil && *s.EggTier == rarityLegendary { // no capture-rate ceiling: unsatisfiable
		s.EggTier = nil
	}
	if a := s.Active; a != nil {
		a.UsedAtStage = clampToken(a.UsedAtStage)
		a.TotalForms = min(max(1, a.TotalForms), 12)
		a.StageIndex = min(max(0, a.StageIndex), max(0, len(a.PathIDs)-1))
		current := a.currentID()
		a.PathIDs = dedupe(a.PathIDs)
		a.PlannedPathIDs = dedupe(a.PlannedPathIDs)
		for i, id := range a.PathIDs {
			if id == current {
				a.StageIndex = i
				break
			}
		}
		a.StageIndex = min(a.StageIndex, max(0, len(a.PathIDs)-1))
		if a.Profile != nil {
			a.Profile.sanitize()
		}
	}
	for i := range s.Dex {
		s.Dex[i].ChainOrder = dedupe(s.Dex[i].ChainOrder)
		if s.Dex[i].Profile != nil {
			s.Dex[i].Profile.sanitize()
		}
	}
	s.reconcileRepresentative()
}

// ---------------------------------------------------------------- ownership

func containsInt(ids []int, id int) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// ownsSpecies reports whether a graduated or released entry, or the active Pokémon's reached
// stages, include the species in the resolved form (CM:752-761).
func (s *state) ownsSpecies(speciesID int, form *string) bool {
	f := formForSpecies(speciesID, form)
	for _, e := range s.Dex {
		if containsInt(e.ChainOrder, speciesID) && sameForm(formForSpecies(speciesID, e.Form), f) {
			return true
		}
	}
	a := s.Active
	return a != nil && containsInt(a.reached(), speciesID) && sameForm(formForSpecies(speciesID, a.Form), f)
}

// ownsShinySpecies is ownsSpecies restricted to shiny individuals; the active one uses
// displaysShiny (CM:794-804).
func (s *state) ownsShinySpecies(speciesID int, form *string) bool {
	f := formForSpecies(speciesID, form)
	for _, e := range s.Dex {
		if e.IsShiny && containsInt(e.ChainOrder, speciesID) && sameForm(formForSpecies(speciesID, e.Form), f) {
			return true
		}
	}
	a := s.Active
	if a == nil || !containsInt(a.reached(), speciesID) || !sameForm(formForSpecies(speciesID, a.Form), f) {
		return false
	}
	return a.displaysShiny()
}

func (s *state) hasCollectedFinal(baseID int) bool {
	prefix := strconv.Itoa(baseID) + ":"
	for k := range s.CollectedFinals {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// collectedForms is the set of owned forms of a species, active and released included (CM:769-779).
func (s *state) collectedForms(speciesID int) map[string]bool {
	out := map[string]bool{}
	if !hasForms(speciesID) {
		return out
	}
	for _, e := range s.Dex {
		if containsInt(e.ChainOrder, speciesID) {
			if f := formForSpecies(speciesID, e.Form); f != nil {
				out[*f] = true
			}
		}
	}
	if a := s.Active; a != nil && containsInt(a.reached(), speciesID) {
		if f := formForSpecies(speciesID, a.Form); f != nil {
			out[*f] = true
		}
	}
	return out
}

// collectedLineForms: a form counts as collected once every species of the line that shows it is
// owned in it (CM:783-790).
func (s *state) collectedLineForms(baseID int) map[string]bool {
	species := formSpecies(baseID)
	owned := make([]map[string]bool, len(species))
	for i, id := range species {
		owned[i] = s.collectedForms(id)
	}
	out := map[string]bool{}
	for _, form := range lineForms(baseID) {
		all := true
		for i, id := range species {
			shows := false
			for _, f := range formsOf(id) {
				if f == form {
					shows = true
				}
			}
			if shows && !owned[i][form] {
				all = false
			}
		}
		if all {
			out[form] = true
		}
	}
	return out
}

// reconcileRepresentative drops a pinned species the player no longer owns (CM:811-821).
func (s *state) reconcileRepresentative() {
	if s.RepresentativeSpeciesID == nil {
		s.RepresentativeForm = nil
		return
	}
	id := *s.RepresentativeSpeciesID
	s.RepresentativeForm = formForSpecies(id, s.RepresentativeForm)
	if !s.ownsSpecies(id, s.RepresentativeForm) {
		s.RepresentativeSpeciesID, s.RepresentativeForm = nil, nil
	}
}

func intPtr(v int) *int              { return &v }
func strPtr(v string) *string        { return &v }
func timePtr(v time.Time) *time.Time { return &v }
