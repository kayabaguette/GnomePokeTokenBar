package companion

import (
	"context"
	"sort"
	"sync"
	"time"
)

// View payloads of protocol §5.4 (state.companion) and §7 (Pokédex commands). JSON tags match the
// protocol; every documented field is present, null when missing.

// Stats is a stat block, also used for IVs.
type Stats struct {
	HP             int `json:"hp"`
	Attack         int `json:"attack"`
	Defense        int `json:"defense"`
	SpecialAttack  int `json:"specialAttack"`
	SpecialDefense int `json:"specialDefense"`
	Speed          int `json:"speed"`
}

// MoodInput carries the usage and limit signals the mood needs (companion.md §9).
type MoodInput struct {
	Now             time.Time
	HasUsageData    bool
	TodayTokens     int
	IdleFor         *time.Duration // time since the usage scan last saw tokens; nil when unknown (never sulks)
	LimitWarning    bool           // a limit at or above the critical threshold, or the 5 h forecast runs out first
	BurnPerMinute   float64        // summed tokens/min of every active 5 h block
	AlwaysFacesBack bool
}

// CompanionView is state.companion (protocol §5.4).
type CompanionView struct {
	Phase string `json:"phase"`
	Mood  string `json:"mood"`
	// SulkIdleSeconds is the idle time in whole hours, in seconds; 0 unless Mood is "sulk" (M2).
	SulkIdleSeconds int             `json:"sulkIdleSeconds"`
	Egg             *EggView        `json:"egg"`
	Active          *ActiveView     `json:"active"`
	GrowthPercent   int             `json:"growthPercent"`
	Representative  *Representative `json:"representative"`
	MenuSubject     MenuSubject     `json:"menuSubject"`
	Inventory       Inventory       `json:"inventory"`
	DexSummary      DexSummary      `json:"dexSummary"`
	LevelUpUntil    *string         `json:"levelUpUntil"`
	JustEvolvedTo   *string         `json:"justEvolvedTo"`
	JustGraduated   *string         `json:"justGraduated"`
}

// EggView is the egg card.
type EggView struct {
	Used         int  `json:"used"`
	Threshold    int  `json:"threshold"`
	Imminent     bool `json:"imminent"`
	HatchDelayed bool `json:"hatchDelayed"`
}

// TypeName is a PokéAPI type id and its localized name.
type TypeName struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ActiveView is the hatched companion card.
type ActiveView struct {
	SpeciesID    int        `json:"speciesId"`
	Form         *string    `json:"form"`
	DisplayName  string     `json:"displayName"`
	Rarity       string     `json:"rarity"`
	Nature       *string    `json:"nature"`
	IsShiny      bool       `json:"isShiny"`
	StageIndex   int        `json:"stageIndex"`
	TotalForms   int        `json:"totalForms"`
	IsFinalStage bool       `json:"isFinalStage"`
	UsedAtStage  int        `json:"usedAtStage"`
	Threshold    int        `json:"threshold"`
	Level        int        `json:"level"`
	GrowthBoost  bool       `json:"growthBoost"`
	Types        []TypeName `json:"types"`
	EvoLine      []EvoNode  `json:"evoLine"`
}

// EvoNode is one evolution-line item; SpeciesID and DisplayName are nil for the mystery node.
type EvoNode struct {
	SpeciesID   *int    `json:"speciesId"`
	Form        *string `json:"form"`
	DisplayName *string `json:"displayName"`
	Reached     bool    `json:"reached"`
	Current     bool    `json:"current"`
}

// Representative is the pinned Pokédex species; Shiny means the player owns it shiny.
type Representative struct {
	SpeciesID int     `json:"speciesId"`
	Form      *string `json:"form"`
	Shiny     bool    `json:"shiny"`
}

// SpriteRef is the sprite the daemon keeps ready for the menu subject; the daemon fills it.
type SpriteRef struct {
	Key          string  `json:"key"`
	ManifestPath *string `json:"manifestPath"`
	Status       string  `json:"status"`
}

// MenuSubject is what the top bar draws. Asset and StaticAsset are the form-aware sprite asset
// names (PF:153-167) for the daemon's sprite key; they are not sent.
type MenuSubject struct {
	Egg         bool      `json:"egg"`
	SpeciesID   *int      `json:"speciesId"`
	Form        *string   `json:"form"`
	Shiny       bool      `json:"shiny"`
	Back        bool      `json:"back"`
	Sprite      SpriteRef `json:"sprite"`
	Asset       string    `json:"-"`
	StaticAsset string    `json:"-"`
}

// Inventory lists the items shown in M1.
type Inventory struct {
	RareCandy int `json:"rareCandy"`
}

// DexSummary heads the Pokédex.
type DexSummary struct {
	Entries       int      `json:"entries"`
	OwnedSpecies  int      `json:"ownedSpecies"`
	ShinySpecies  int      `json:"shinySpecies"`
	NationalTotal int      `json:"nationalTotal"`
	Badges        []string `json:"badges"`
}

func ts(t time.Time) *string {
	v := t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	return &v
}

// mood picks the display state in upstream's order (CS:1987-2001).
func (s *Store) mood(in MoodInput, windowOpen bool) string {
	switch {
	case s.st.Active == nil:
		return "egg"
	case windowOpen:
		return "levelUp"
	case in.LimitWarning:
		return "tired"
	}
	// A rise in today's total seen by Update counts as usage at once (CS:1974-1985).
	if idle, ok := s.idleDuration(in); ok && idle >= sulkAfterIdle {
		return "sulk"
	}
	switch {
	case !in.HasUsageData || in.TodayTokens == 0:
		return "sleep"
	case in.BurnPerMinute <= 1_000:
		return "idle"
	case in.BurnPerMinute < 100_000:
		return "working"
	}
	return "focus"
}

// BuildState builds state.companion. It does no I/O: names and types come from what earlier
// fetches cached. The menu-subject sprite key, manifest path and status are left for the daemon.
func (s *Store) BuildState(in MoodInput) CompanionView {
	st := &s.st
	windowOpen := !s.window.until.IsZero() && in.Now.Before(s.window.until)
	v := CompanionView{
		Mood:       s.mood(in, windowOpen),
		Inventory:  Inventory{RareCandy: st.Inventory["rareCandy"]},
		DexSummary: s.dexSummary(),
	}
	v.SulkIdleSeconds = s.sulkIdleSeconds(in, v.Mood)
	if windowOpen {
		v.LevelUpUntil = ts(s.window.until)
		if r := s.window.evolvedTo; r != nil {
			v.JustEvolvedTo = strPtr(s.refName(*r))
		}
		if r := s.window.graduated; r != nil {
			v.JustGraduated = strPtr(s.refName(*r))
		}
	}
	used, total := st.EggUsage, s.eggThreshold()
	if a := st.Active; a != nil {
		v.Phase = "active"
		v.Active = s.activeView(a)
		used, total = a.UsedAtStage, v.Active.Threshold
	} else {
		v.Phase = "egg"
		v.Egg = &EggView{
			Used: st.EggUsage, Threshold: total, HatchDelayed: s.hatchDelayed,
			Imminent: float64(st.EggUsage)/float64(total) >= 0.9,
		}
	}
	if total > 0 {
		v.GrowthPercent = min(max(0, used), total) * 100 / total
	}
	if id := st.RepresentativeSpeciesID; id != nil {
		form := formForSpecies(*id, st.RepresentativeForm)
		v.Representative = &Representative{SpeciesID: *id, Form: form, Shiny: st.ownsShinySpecies(*id, form)}
	}
	v.MenuSubject = MenuSubject{Egg: true, Sprite: SpriteRef{Status: "loading"}}
	switch {
	case v.Representative != nil:
		v.MenuSubject.SpeciesID, v.MenuSubject.Form, v.MenuSubject.Shiny = intPtr(v.Representative.SpeciesID), v.Representative.Form, v.Representative.Shiny
	case v.Active != nil:
		v.MenuSubject.SpeciesID, v.MenuSubject.Form, v.MenuSubject.Shiny = intPtr(v.Active.SpeciesID), v.Active.Form, v.Active.IsShiny
	}
	if id := v.MenuSubject.SpeciesID; id != nil {
		v.MenuSubject.Egg = false
		v.MenuSubject.Asset = assetName(*id, v.MenuSubject.Form)
		v.MenuSubject.StaticAsset = staticAssetName(*id, v.MenuSubject.Form)
	}
	v.MenuSubject.Back = st.Active != nil && (in.AlwaysFacesBack || v.Mood == "sulk")
	return v
}

func (s *Store) activeView(a *monState) *ActiveView {
	id := a.currentID()
	form := formForSpecies(id, a.Form)
	v := &ActiveView{
		SpeciesID: id, Form: form, Rarity: a.Rarity, Nature: a.Nature, IsShiny: a.displaysShiny(),
		StageIndex: a.StageIndex, TotalForms: a.TotalForms, UsedAtStage: a.UsedAtStage,
		Threshold: s.stageThreshold(a), GrowthBoost: a.HasGrowthBoost,
		Types: []TypeName{}, EvoLine: []EvoNode{},
	}
	v.DisplayName = formDisplayName(s.speciesName(s.knownNames(id), id), id, form, s.formLabel(id, form))
	v.Level = a.Profile.Level
	if d := s.details[id]; d != nil {
		for _, t := range d.Types {
			v.Types = append(v.Types, TypeName{Key: t, Name: s.apiName("type", t)})
		}
	}
	if s.line == nil {
		return v
	}
	cur, ok := findNode(s.line.tree, id)
	v.IsFinalStage = !ok || len(cur.Children) == 0
	node := func(sid int, reached, current bool) EvoNode {
		f := formForSpecies(sid, a.Form)
		name := formDisplayName(s.speciesName(s.line.names[sid], sid), sid, f, s.formLabel(sid, f))
		return EvoNode{SpeciesID: intPtr(sid), Form: f, DisplayName: &name, Reached: reached, Current: current}
	}
	// Realized path, then the guaranteed linear prefix, then one mystery node before a branch (CS:297-316).
	for i, pid := range a.PathIDs {
		v.EvoLine = append(v.EvoLine, node(pid, true, i == a.StageIndex))
	}
	if ok {
		n := cur
		for len(n.Children) == 1 {
			n = n.Children[0]
			v.EvoLine = append(v.EvoLine, node(n.ID, false, false))
		}
		if len(n.Children) > 1 {
			v.EvoLine = append(v.EvoLine, EvoNode{})
		}
	}
	return v
}

// knownNames finds a species' names in the loaded line, else in any catch-log entry.
func (s *Store) knownNames(id int) map[string]string {
	if s.line != nil {
		if n, ok := s.line.names[id]; ok {
			return n
		}
	}
	for _, e := range s.st.Dex {
		if n, ok := e.Names[id]; ok && len(n) > 0 {
			return n
		}
	}
	for _, l := range s.lines {
		if n, ok := l.names[id]; ok {
			return n
		}
	}
	return nil
}

func (s *Store) dexSummary() DexSummary {
	owned := s.collectedDexSpecies(false)
	sum := DexSummary{Entries: len(s.st.Dex), NationalTotal: maxSpeciesID, Badges: []string{}}
	if s.st.Active != nil {
		sum.Entries++
	}
	for _, sp := range owned {
		sum.OwnedSpecies++
		if sp.isShiny {
			sum.ShinySpecies++
		}
	}
	for _, b := range s.earnedBadges() {
		sum.Badges = append(sum.Badges, b.key)
	}
	return sum
}

// ---------------------------------------------------------------- getDexPage (§7.1)

// DexQuery is the getDexPage args, already validated by the daemon. Page and PageSize ≤ 0 take
// their defaults (1 and 16).
type DexQuery struct {
	Page            int
	PageSize        int
	Query           string
	Rarity          *string
	Region          *string
	ShinyOnly       bool
	Sort            string // numberAsc (default), numberDesc, nameAsc, nameDesc, rarityDesc
	AnchorSpeciesID *int
}

// SpriteRequest is ready-to-send getSprite args. Asset and StaticAsset feed the daemon's sprite key.
type SpriteRequest struct {
	SpeciesID   int     `json:"speciesId"`
	Form        *string `json:"form"`
	Shiny       bool    `json:"shiny"`
	Back        bool    `json:"back"`
	Animated    bool    `json:"animated"`
	Asset       string  `json:"-"`
	StaticAsset string  `json:"-"`
}

// FormCount is a species' owned and total forms.
type FormCount struct {
	Owned int `json:"owned"`
	Total int `json:"total"`
}

// DexCell is one Pokédex grid cell. SpriteKey is left for the daemon to fill.
type DexCell struct {
	SpeciesID        int           `json:"speciesId"`
	DisplayName      string        `json:"displayName"`
	Rarity           string        `json:"rarity"`
	IsShiny          bool          `json:"isShiny"`
	HasNormal        bool          `json:"hasNormal"`
	IsRaising        bool          `json:"isRaising"`
	IsRepresentative bool          `json:"isRepresentative"`
	Forms            *FormCount    `json:"forms"`
	Sprite           SpriteRequest `json:"sprite"`
	SpriteKey        string        `json:"spriteKey"`
}

// RegionProgress is one region chip.
type RegionProgress struct {
	Key           string `json:"key"`
	Owned         int    `json:"owned"`
	ShinyOwned    int    `json:"shinyOwned"`
	Total         int    `json:"total"`
	Complete      bool   `json:"complete"`
	ShinyComplete bool   `json:"shinyComplete"`
}

// RarityCounts counts per rarity.
type RarityCounts struct {
	Legendary int `json:"legendary"`
	Rare      int `json:"rare"`
	Uncommon  int `json:"uncommon"`
	Common    int `json:"common"`
}

func (c *RarityCounts) add(r string) {
	switch r {
	case rarityLegendary:
		c.Legendary++
	case rarityRare:
		c.Rare++
	case rarityUncommon:
		c.Uncommon++
	default:
		c.Common++
	}
}

// DexPage is the getDexPage reply.
type DexPage struct {
	Page         int              `json:"page"`
	PageSize     int              `json:"pageSize"`
	PageCount    int              `json:"pageCount"`
	Total        int              `json:"total"`
	Cells        []DexCell        `json:"cells"`
	Regions      []RegionProgress `json:"regions"`
	RarityCounts RarityCounts     `json:"rarityCounts"`
}

// DexPage filters, sorts and pages the owned species (CS:646-694). Regions and rarity counts ignore
// the filters.
func (s *Store) DexPage(q DexQuery) DexPage {
	if q.PageSize <= 0 {
		q.PageSize = 16
	}
	all := s.collectedDexSpecies(false)
	out := DexPage{PageSize: q.PageSize, Cells: []DexCell{}}
	for _, p := range regionCompletions(all) {
		out.Regions = append(out.Regions, RegionProgress{
			Key: p.region.key, Owned: p.owned, ShinyOwned: p.shinyOwned, Total: p.region.total(),
			Complete: p.owned >= p.region.total(), ShinyComplete: p.shinyOwned >= p.region.total(),
		})
	}
	m := newMatcher(q.Query)
	var list []dexSpecies
	for _, sp := range all {
		out.RarityCounts.add(sp.rarity)
		r, _ := regionOf(sp.id)
		switch {
		case q.Rarity != nil && sp.rarity != *q.Rarity,
			q.Region != nil && r.key != *q.Region,
			q.ShinyOnly && !sp.isShiny,
			m != nil && !m.matches([]int{sp.id}, append([]string{s.speciesName(sp.names, sp.id)}, mapValues(sp.names)...)):
			continue
		}
		list = append(list, sp)
	}
	s.sortSpecies(list, q.Sort)
	out.Total = len(list)
	out.PageCount = max(1, (len(list)+q.PageSize-1)/q.PageSize)
	out.Page = min(max(1, q.Page), out.PageCount)
	if q.AnchorSpeciesID != nil {
		for i, sp := range list {
			if sp.id == *q.AnchorSpeciesID {
				out.Page = i/q.PageSize + 1
			}
		}
	}
	for _, sp := range list[min(len(list), (out.Page-1)*q.PageSize):min(len(list), out.Page*q.PageSize)] {
		out.Cells = append(out.Cells, s.cell(sp))
	}
	return out
}

func (s *Store) sortSpecies(list []dexSpecies, by string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch by {
		case "numberDesc":
			return a.id > b.id
		case "nameAsc", "nameDesc":
			c := nameCompare(s.speciesName(a.names, a.id), s.speciesName(b.names, b.id))
			if c == 0 {
				return a.id < b.id
			}
			return (c < 0) == (by == "nameAsc")
		case "rarityDesc":
			if rarityRank(a.rarity) != rarityRank(b.rarity) {
				return rarityRank(a.rarity) > rarityRank(b.rarity)
			}
		}
		return a.id < b.id
	})
}

// cell builds a grid cell: static default-form sprite, shiny only for a species without forms that
// was owned shiny (upstream DexSpeciesCell).
func (s *Store) cell(sp dexSpecies) DexCell {
	rep := s.st.RepresentativeSpeciesID
	c := DexCell{
		SpeciesID: sp.id, DisplayName: s.speciesName(sp.names, sp.id), Rarity: sp.rarity,
		IsShiny: sp.isShiny, HasNormal: sp.hasNormal, IsRaising: sp.isRaising,
		IsRepresentative: rep != nil && *rep == sp.id,
	}
	form := formForSpecies(sp.id, nil)
	if hasForms(sp.id) {
		c.Forms = &FormCount{Owned: len(s.st.collectedForms(sp.id)), Total: len(formsOf(sp.id))}
	}
	c.Sprite = SpriteRequest{
		SpeciesID: sp.id, Form: form, Shiny: c.Forms == nil && sp.isShiny,
		Asset: assetName(sp.id, form), StaticAsset: staticAssetName(sp.id, form),
	}
	return c
}

// ---------------------------------------------------------------- getSpecies (§7.2)

// SpeciesForm is one entry of the form picker.
type SpeciesForm struct {
	Form             string `json:"form"`
	DisplayName      string `json:"displayName"`
	HasNormal        bool   `json:"hasNormal"`
	IsShiny          bool   `json:"isShiny"`
	IsRepresentative bool   `json:"isRepresentative"`
}

// AbilityView is an ability with its localized name.
type AbilityView struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IsHidden    bool   `json:"isHidden"`
}

// KnownMoveView is a move an individual knows.
type KnownMoveView struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Level       int    `json:"level"`
}

// Individual is one owned individual of a species.
type Individual struct {
	ID          string          `json:"id"`
	Form        *string         `json:"form"`
	DisplayName string          `json:"displayName"`
	IsRaising   bool            `json:"isRaising"`
	Released    bool            `json:"released"`
	IsShiny     bool            `json:"isShiny"`
	CaughtAt    *string         `json:"caughtAt"`
	Nature      *string         `json:"nature"`
	Level       int             `json:"level"`
	Gender      *string         `json:"gender"`
	Ability     *AbilityView    `json:"ability"`
	IVs         Stats           `json:"ivs"`
	Stats       *Stats          `json:"stats"`
	Moves       []KnownMoveView `json:"moves"`
}

// MoveMethod is one learn method; Level is set only for level-up.
type MoveMethod struct {
	Method string `json:"method"`
	Level  *int   `json:"level"`
}

// MoveView is one move of the learnset.
type MoveView struct {
	Name        string       `json:"name"`
	DisplayName string       `json:"displayName"`
	Methods     []MoveMethod `json:"methods"`
}

// DetailsView is the default-form species data.
type DetailsView struct {
	Types     []TypeName    `json:"types"`
	HeightM   float64       `json:"heightM"`
	WeightKg  float64       `json:"weightKg"`
	BaseStats Stats         `json:"baseStats"`
	Abilities []AbilityView `json:"abilities"`
	Moves     []MoveView    `json:"moves"`
}

// SpeciesView is the getSpecies reply.
type SpeciesView struct {
	SpeciesID        int           `json:"speciesId"`
	DisplayName      string        `json:"displayName"`
	Rarity           *string       `json:"rarity"`
	Region           *string       `json:"region"`
	Owned            bool          `json:"owned"`
	HasNormal        bool          `json:"hasNormal"`
	IsShiny          bool          `json:"isShiny"`
	IsRaising        bool          `json:"isRaising"`
	IsRepresentative bool          `json:"isRepresentative"`
	Forms            []SpeciesForm `json:"forms"`
	Individuals      []Individual  `json:"individuals"`
	DetailsStatus    string        `json:"detailsStatus"`
	Details          *DetailsView  `json:"details"`
}

// Species builds the species detail page. It loads the species' details (enriching and saving
// matching profiles, as upstream's detail page does) and the localized names it shows.
func (s *Store) Species(ctx context.Context, id int) SpeciesView {
	st := &s.st
	v := SpeciesView{SpeciesID: id, Forms: []SpeciesForm{}, Individuals: []Individual{}, DetailsStatus: "unavailable"}
	if r, ok := regionOf(id); ok {
		v.Region = strPtr(r.key)
	}
	var names map[string]string
	for _, sp := range s.collectedDexSpecies(false) {
		if sp.id == id {
			v.Owned, v.HasNormal, v.IsShiny, v.IsRaising = true, sp.hasNormal, sp.isShiny, sp.isRaising
			v.Rarity, names = strPtr(sp.rarity), sp.names
		}
	}
	if names == nil {
		names = s.knownNames(id)
	}
	if names == nil {
		if sp, err := s.data.Species(ctx, id); err == nil {
			names = sp.Names
		}
	}
	if v.Rarity == nil {
		for _, l := range s.lines {
			if _, ok := findNode(l.tree, id); ok {
				v.Rarity = strPtr(l.rarity)
			}
		}
	}
	v.DisplayName = s.speciesName(names, id)
	rep := st.RepresentativeSpeciesID
	v.IsRepresentative = rep != nil && *rep == id

	d, err := s.loadDetails(ctx, id)
	if err == nil {
		v.DetailsStatus = "ready"
	}
	s.warmSpeciesNames(ctx, id, d)

	if hasForms(id) {
		owned := map[string]dexSpecies{}
		if v.Owned {
			for _, sp := range s.collectedDexSpecies(true) {
				if sp.id == id && sp.form != nil {
					owned[*sp.form] = sp
				}
			}
		}
		for _, f := range formsOf(id) {
			o := owned[f]
			v.Forms = append(v.Forms, SpeciesForm{
				Form: f, DisplayName: s.formLabel(id, strPtr(f)), HasNormal: o.hasNormal, IsShiny: o.isShiny,
				IsRepresentative: v.IsRepresentative && sameForm(st.RepresentativeForm, strPtr(f)),
			})
		}
	}
	for _, e := range s.sortedLog("recentFirst") {
		if e.FinalID != id {
			continue
		}
		v.Individuals = append(v.Individuals, s.individual(e, names, d))
	}
	if d != nil {
		v.Details = s.detailsView(d)
	}
	return v
}

func (s *Store) individual(e logEntry, names map[string]string, d *Details) Individual {
	id := e.FinalID
	form := formForSpecies(id, e.Form)
	p := e.Profile
	ind := Individual{
		ID: e.ID, Form: form, DisplayName: formDisplayName(s.speciesName(names, id), id, form, s.formLabel(id, form)),
		IsRaising: e.active, Released: e.ReleasedAt != nil, IsShiny: e.IsShiny, Nature: e.Nature,
		Level: p.Level, Gender: p.Gender, IVs: Stats(p.IVs), Moves: []KnownMoveView{},
	}
	if e.CaughtAt != nil && !e.active {
		ind.CaughtAt = ts(*e.CaughtAt)
	}
	if p.AbilityName != nil {
		ind.Ability = &AbilityView{Name: *p.AbilityName, DisplayName: s.apiName("ability", *p.AbilityName), IsHidden: p.AbilityIsHidden}
	}
	if d != nil {
		st := computeStats(d, p, e.Nature)
		ind.Stats = &st
	}
	for _, m := range p.Moves {
		ind.Moves = append(ind.Moves, KnownMoveView{Name: m.Name, DisplayName: s.apiName("move", m.Name), Level: m.LearnedAtLevel})
	}
	return ind
}

func (s *Store) detailsView(d *Details) *DetailsView {
	dv := &DetailsView{
		Types: []TypeName{}, HeightM: float64(d.Height) / 10, WeightKg: float64(d.Weight) / 10,
		Abilities: []AbilityView{}, Moves: []MoveView{},
	}
	for _, t := range d.Types {
		dv.Types = append(dv.Types, TypeName{Key: t, Name: s.apiName("type", t)})
	}
	var base ivSet
	for i, f := range base.fields() {
		*f = d.BaseStats[statOrder[i]]
	}
	dv.BaseStats = Stats(base)
	abilities := append([]Ability{}, d.Abilities...)
	sort.SliceStable(abilities, func(i, j int) bool { return abilities[i].Slot < abilities[j].Slot })
	for _, a := range abilities {
		dv.Abilities = append(dv.Abilities, AbilityView{Name: a.Name, DisplayName: s.apiName("ability", a.Name), IsHidden: a.Hidden})
	}
	for _, m := range d.Moves {
		mv := MoveView{Name: m.Name, DisplayName: s.apiName("move", m.Name), Methods: []MoveMethod{}}
		for _, lm := range m.Methods {
			mm := MoveMethod{Method: lm.Method}
			if lm.Method == "level-up" {
				mm.Level = intPtr(lm.Level)
			}
			mv.Methods = append(mv.Methods, mm)
		}
		dv.Moves = append(dv.Moves, mv)
	}
	return dv
}

// warmSpeciesNames fetches the names a species page shows, eight at a time.
func (s *Store) warmSpeciesNames(ctx context.Context, id int, d *Details) {
	type res struct{ kind, name string }
	var todo []res
	want := func(kind, name string) {
		if _, ok := s.names[nameKey(kind, name)]; !ok && name != "" {
			todo = append(todo, res{kind, name})
		}
	}
	for _, f := range formsOf(id) {
		want("pokemon-form", formNameResource(id, strPtr(f)))
	}
	if d != nil {
		for _, t := range d.Types {
			want("type", t)
		}
		for _, a := range d.Abilities {
			want("ability", a.Name)
		}
		for _, m := range d.Moves {
			want("move", m.Name)
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	got := map[string]map[string]string{}
	for _, r := range todo {
		wg.Add(1)
		go func(r res) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if m, err := s.data.Names(ctx, r.kind, r.name); err == nil && len(m) > 0 {
				mu.Lock()
				got[nameKey(r.kind, r.name)] = m
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	for k, m := range got {
		s.names[k] = m
	}
}

// ---------------------------------------------------------------- getCatchLog (§7.3)

// CatchLogQuery is the getCatchLog args, already validated by the daemon. Limit ≤ 0 means 20.
type CatchLogQuery struct {
	Offset    int
	Limit     int
	Query     string
	Rarity    *string
	ShinyOnly bool
	Sort      string // recentFirst (default), oldestFirst, numberAsc, numberDesc, nameAsc, nameDesc, rarityDesc
}

// ChainNode is one species an individual reached.
type ChainNode struct {
	SpeciesID   int     `json:"speciesId"`
	Form        *string `json:"form"`
	DisplayName string  `json:"displayName"`
}

// LogEntry is one catch-log row.
type LogEntry struct {
	ID          string      `json:"id"`
	IsRaising   bool        `json:"isRaising"`
	Released    bool        `json:"released"`
	Rarity      string      `json:"rarity"`
	IsShiny     bool        `json:"isShiny"`
	Nature      *string     `json:"nature"`
	Form        *string     `json:"form"`
	CaughtAt    *string     `json:"caughtAt"`
	SpeciesID   int         `json:"speciesId"`
	DisplayName string      `json:"displayName"`
	Chain       []ChainNode `json:"chain"`
}

// CatchLog is the getCatchLog reply.
type CatchLog struct {
	Offset       int          `json:"offset"`
	Limit        int          `json:"limit"`
	Total        int          `json:"total"`
	Entries      []LogEntry   `json:"entries"`
	RarityCounts RarityCounts `json:"rarityCounts"`
}

// CatchLog filters, sorts and pages the individuals, the active one included (CS:697-794).
// Rarity counts ignore the filters.
func (s *Store) CatchLog(q CatchLogQuery) CatchLog {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	out := CatchLog{Offset: max(0, q.Offset), Limit: q.Limit, Entries: []LogEntry{}}
	m := newMatcher(q.Query)
	var list []logEntry
	for _, e := range s.sortedLog(q.Sort) {
		out.RarityCounts.add(e.Rarity)
		if q.Rarity != nil && e.Rarity != *q.Rarity || q.ShinyOnly && !e.IsShiny {
			continue
		}
		if m != nil {
			var names []string
			for _, n := range e.Names {
				names = append(names, mapValues(n)...)
			}
			if !m.matches(append([]int{e.BaseID, e.FinalID}, e.ChainOrder...), names) {
				continue
			}
		}
		list = append(list, e)
	}
	out.Total = len(list)
	for _, e := range list[min(len(list), out.Offset):min(len(list), out.Offset+q.Limit)] {
		form := formForSpecies(e.FinalID, e.Form)
		le := LogEntry{
			ID: e.ID, IsRaising: e.active, Released: e.ReleasedAt != nil, Rarity: e.Rarity, IsShiny: e.IsShiny,
			Nature: e.Nature, Form: form, SpeciesID: e.FinalID, Chain: []ChainNode{},
			DisplayName: formDisplayName(s.entryName(e.dexEntry), e.FinalID, form, s.formLabel(e.FinalID, form)),
		}
		if e.CaughtAt != nil && !e.active {
			le.CaughtAt = ts(*e.CaughtAt)
		}
		chain := e.ChainOrder
		if e.active {
			chain = e.ChainOrder[:min(len(e.ChainOrder), s.st.Active.StageIndex+1)]
		}
		for _, sid := range chain {
			le.Chain = append(le.Chain, ChainNode{
				SpeciesID: sid, Form: formForSpecies(sid, e.Form), DisplayName: s.speciesName(e.Names[sid], sid),
			})
		}
		out.Entries = append(out.Entries, le)
	}
	return out
}

// sortedLog sorts catch-log entries. recentFirst pins the active entry first and oldestFirst last;
// ties fall back to newest caughtAt, then id (CS:717-794).
func (s *Store) sortedLog(by string) []logEntry {
	list := s.logEntries()
	newerFirst := func(a, b logEntry) bool {
		ta, tb := caughtOrZero(a.CaughtAt), caughtOrZero(b.CaughtAt)
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		return a.ID < b.ID
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch by {
		case "oldestFirst":
			if a.active != b.active {
				return b.active
			}
			ta, tb := caughtOrZero(a.CaughtAt), caughtOrZero(b.CaughtAt)
			if !ta.Equal(tb) {
				return ta.Before(tb)
			}
			return a.ID < b.ID
		case "numberAsc", "numberDesc":
			if a.FinalID != b.FinalID {
				return (a.FinalID < b.FinalID) == (by == "numberAsc")
			}
		case "nameAsc", "nameDesc":
			if c := nameCompare(s.entryName(a.dexEntry), s.entryName(b.dexEntry)); c != 0 {
				return (c < 0) == (by == "nameAsc")
			}
		case "rarityDesc":
			if rarityRank(a.Rarity) != rarityRank(b.Rarity) {
				return rarityRank(a.Rarity) > rarityRank(b.Rarity)
			}
		default: // recentFirst
			if a.active != b.active {
				return a.active
			}
		}
		return newerFirst(a, b)
	})
	return list
}
