package companion

import (
	"math"
	"sort"
)

// Individual profiles (upstream PokemonProfile.swift): IVs rolled at hatch, gender, ability and
// moves resolved once details load, level derived from growth.

const (
	genderMale       = "male"
	genderFemale     = "female"
	genderGenderless = "genderless"
	shedinjaID       = 292
	enrichSeedSalt   = 0xA11B1E5D9EED
)

// statOrder is the PokéAPI stat order (PP:215).
var statOrder = []string{"hp", "attack", "defense", "special-attack", "special-defense", "speed"}

type ivSet struct {
	HP             int `json:"hp"`
	Attack         int `json:"attack"`
	Defense        int `json:"defense"`
	SpecialAttack  int `json:"specialAttack"`
	SpecialDefense int `json:"specialDefense"`
	Speed          int `json:"speed"`
}

// fields returns the six values in statOrder.
func (v *ivSet) fields() [6]*int {
	return [6]*int{&v.HP, &v.Attack, &v.Defense, &v.SpecialAttack, &v.SpecialDefense, &v.Speed}
}

type knownMove struct {
	Name           string `json:"name"`
	LearnedAtLevel int    `json:"learnedAtLevel"`
}

type profile struct {
	InstanceID      string      `json:"instanceID"`
	Seed            uint64      `json:"seed"`
	Gender          *string     `json:"gender,omitempty"`
	IVs             ivSet       `json:"ivs"`
	AbilitySlot     *int        `json:"abilitySlot,omitempty"`
	AbilityName     *string     `json:"abilityName,omitempty"`
	AbilityIsHidden bool        `json:"abilityIsHidden"`
	Level           int         `json:"level"`
	GrowthTokens    int         `json:"growthTokens"`
	Moves           []knownMove `json:"moves"`
}

// generateProfile rolls the IVs (hp, atk, def, spa, spd, spe; each % 32) from the seed (PP:105-126).
func generateProfile(seed uint64, growthTokens int, instanceID string) *profile {
	r := profileRNG(seed)
	p := &profile{InstanceID: instanceID, Seed: seed, Level: 5, GrowthTokens: max(0, growthTokens), Moves: []knownMove{}}
	for _, f := range p.IVs.fields() {
		*f = int(r.Next() % 32)
	}
	return p
}

func (p *profile) clone() *profile {
	c := *p
	c.Moves = append([]knownMove{}, p.Moves...)
	if p.Gender != nil {
		g := *p.Gender
		c.Gender = &g
	}
	if p.AbilitySlot != nil {
		s := *p.AbilitySlot
		c.AbilitySlot = &s
	}
	if p.AbilityName != nil {
		n := *p.AbilityName
		c.AbilityName = &n
	}
	return &c
}

// levelUpMoves lists level-up moves learnable at or below level, sorted by (level, name) (PP:23-37).
func levelUpMoves(d *Details, level int) []knownMove {
	best := map[string]int{}
	for _, m := range d.Moves {
		found := false
		lowest := 0
		for _, lm := range m.Methods {
			if lm.Method == "level-up" && lm.Level <= level && (!found || lm.Level < lowest) {
				lowest, found = lm.Level, true
			}
		}
		if !found {
			continue
		}
		if prev, ok := best[m.Name]; !ok || lowest < prev {
			best[m.Name] = lowest
		}
	}
	out := make([]knownMove, 0, len(best))
	for name, lvl := range best {
		out = append(out, knownMove{Name: name, LearnedAtLevel: lvl})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LearnedAtLevel != out[j].LearnedAtLevel {
			return out[i].LearnedAtLevel < out[j].LearnedAtLevel
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// enrich fills gender and ability once, from ProfileRNG(seed ^ 0xA11B1E5D9EED), and re-derives the
// four newest legal level-up moves. It never re-rolls a field already set (PP:142-176).
func (p *profile) enrich(d *Details) {
	r := profileRNG(p.Seed ^ enrichSeedSalt)
	if p.Gender == nil {
		g := genderGenderless
		if d.GenderRate >= 0 {
			g = genderMale
			if int(r.Next()%8) < d.GenderRate {
				g = genderFemale
			}
		}
		p.Gender = &g
	}
	var normal, hidden []Ability
	for _, a := range d.Abilities {
		if a.Hidden {
			hidden = append(hidden, a)
		} else {
			normal = append(normal, a)
		}
	}
	bySlot := func(s []Ability) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Slot < s[j].Slot })
	}
	bySlot(normal)
	bySlot(hidden)
	if p.AbilitySlot == nil {
		var pick *Ability
		switch {
		case len(hidden) > 0 && r.Next()%128 == 0:
			pick = &hidden[int(r.Next()%uint64(len(hidden)))]
		case len(normal) > 0:
			pick = &normal[int(r.Next()%uint64(len(normal)))]
		case len(d.Abilities) > 0:
			all := append([]Ability{}, d.Abilities...)
			bySlot(all)
			pick = &all[0]
		}
		if pick != nil {
			slot := pick.Slot
			p.AbilitySlot, p.AbilityIsHidden = &slot, pick.Hidden
		}
	}
	var chosen *Ability
	for _, match := range []func(Ability) bool{
		func(a Ability) bool {
			return p.AbilitySlot != nil && a.Slot == *p.AbilitySlot && a.Hidden == p.AbilityIsHidden
		},
		func(a Ability) bool { return p.AbilitySlot != nil && a.Slot == *p.AbilitySlot },
	} {
		for i := range d.Abilities {
			if chosen == nil && match(d.Abilities[i]) {
				chosen = &d.Abilities[i]
			}
		}
	}
	if chosen == nil && len(normal) > 0 {
		chosen = &normal[0]
	}
	if chosen == nil && len(hidden) > 0 {
		chosen = &hidden[0]
	}
	p.AbilityName = nil
	if chosen != nil {
		name, slot := chosen.Name, chosen.Slot
		p.AbilityName, p.AbilitySlot, p.AbilityIsHidden = &name, &slot, chosen.Hidden
	}
	moves := levelUpMoves(d, p.Level)
	p.Moves = append([]knownMove{}, moves[max(0, len(moves)-4):]...)
}

// advanceGrowth raises growth to candidate and derives the level; neither ever drops (PP:183-188).
func (p *profile) advanceGrowth(candidate int, rarity string) {
	p.GrowthTokens = min(maxTokenValue, max(p.GrowthTokens, max(0, candidate)))
	total := max(1, graduationTotal(rarity))
	progress := math.Min(1, float64(p.GrowthTokens)/float64(total))
	p.Level = min(100, max(p.Level, max(5, 5+int(math.Floor(progress*95)))))
}

// sanitize clamps a profile read from disk (PP:190-203).
func (p *profile) sanitize() {
	p.Level = min(100, max(5, p.Level))
	p.GrowthTokens = clampToken(p.GrowthTokens)
	for _, v := range p.IVs.fields() {
		*v = min(31, max(0, *v))
	}
	if len(p.Moves) > 4 {
		p.Moves = p.Moves[:4]
	}
	for i, m := range p.Moves {
		if r := []rune(m.Name); len(r) > 80 {
			p.Moves[i].Name = string(r[:80])
		}
		p.Moves[i].LearnedAtLevel = min(100, max(0, m.LearnedAtLevel))
	}
	if p.InstanceID == "" {
		p.InstanceID = newUUID()
	}
}

// computeStats returns the actual stats at the profile's level (PP:225-240). A missing base stat is 0.
func computeStats(d *Details, p *profile, nature *string) Stats {
	var out ivSet
	ivs, values := p.IVs.fields(), out.fields()
	for i, s := range statOrder {
		base := d.BaseStats[s]
		iv := *ivs[i]
		l := p.Level
		var v int
		if s == "hp" {
			v = ((2*base+iv)*l)/100 + l + 10
			if d.SpeciesID == shedinjaID {
				v = 1
			}
		} else {
			neutral := ((2*base+iv)*l)/100 + 5
			v = int(math.Floor(float64(neutral) * natureModifier(nature, s)))
		}
		*values[i] = v
	}
	return Stats(out)
}

// reconstructedGrowthTokens is the standard growth of the completed phases plus the current
// stage usage (CS:2266-2275).
func reconstructedGrowthTokens(rarity string, totalForms, completedStages, currentStageUsage int) int {
	forms := max(1, totalForms)
	completed := min(max(0, completedStages), forms)
	sum := 0
	for stage := 0; stage < completed; stage++ {
		sum += phaseThreshold(rarity, forms, stage, 1)
	}
	return min(maxTokenValue, sum+min(maxTokenValue, max(0, currentStageUsage)))
}
