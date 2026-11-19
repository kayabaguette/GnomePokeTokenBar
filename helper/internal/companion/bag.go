package companion

// Bag: Rare Candy planning and use, Mint use (upstream CompanionStore.swift:1155-1271;
// docs/spec/bag-shop.md §2).

// Candy use results (protocol §7.4 CandyResult).
const (
	CandyEvolved    = "evolved"
	CandyGraduated  = "graduated"
	CandyProgressed = "progressed"
)

// Why candy use is unavailable (protocol §5.4 CandyBlocked), in evaluation order.
const (
	candyBlockedEgg           = "egg"
	candyBlockedNoStock       = "noStock"
	candyBlockedLineLoading   = "lineLoading"
	candyBlockedRevealPending = "revealPending"
)

// revealPending: a disguised, unrevealed Ditto has reached its threshold. The accepted batch waits
// there until the next Update reveals it, which needs evolution line 132 in memory (CS:1179-1182).
func (s *Store) revealPending(a *monState) bool {
	return a.DittoDisguise != nil && !a.DittoRevealed && a.UsedAtStage >= s.stageThreshold(a)
}

// candyBlocked is the first reason candy cannot be used, or "" when it can; it gates the stage
// costs, so candyMaxUse is 0 exactly when a reason is returned.
func (s *Store) candyBlocked() string {
	a := s.st.Active
	switch {
	case a == nil:
		return candyBlockedEgg
	case s.itemCount(itemRareCandy) <= 0:
		return candyBlockedNoStock
	case s.line == nil:
		return candyBlockedLineLoading
	case s.revealPending(a):
		return candyBlockedRevealPending
	}
	return ""
}

// candyStageCosts are the effective costs of the apparent planned path from the current stage on;
// nil without an active Pokémon, without the loaded line, or while a Ditto reveal is pending
// (CS:1177-1187).
func (s *Store) candyStageCosts() []int {
	a := s.st.Active
	if a == nil || s.line == nil || s.revealPending(a) {
		return nil
	}
	var costs []int
	m := *a
	for stage := a.StageIndex; stage < a.TotalForms; stage++ {
		m.StageIndex = stage
		costs = append(costs, s.stageThreshold(&m))
	}
	return costs
}

func ceilCandies(xp int) int { return xp/rareCandyXP + min(1, xp%rareCandyXP) }

// MaxCandyUse is min(stock, candies needed to graduate); 0 disables candy (CS:1189-1193).
func (s *Store) MaxCandyUse() int {
	if s.candyBlocked() != "" {
		return 0
	}
	total := 0
	for _, c := range s.candyStageCosts() {
		total += c
	}
	return min(s.itemCount(itemRareCandy), ceilCandies(max(0, total-s.st.Active.UsedAtStage)))
}

// CandyPlan previews a candy batch (protocol §7.4).
type CandyPlan struct {
	Count       int  `json:"count"`
	XP          int  `json:"xp"`
	Evolves     bool `json:"evolves"`
	Graduates   bool `json:"graduates"`
	CarryoverXP int  `json:"carryoverXP"`
	DiscardedXP int  `json:"discardedXP"`
}

// PlanCandies clamps the count to [0, MaxCandyUse] and walks the stage costs; nil when the clamped
// count is 0. evolves reports only stages passed before the last one (CS:1195-1221).
func (s *Store) PlanCandies(requested int) *CandyPlan {
	c := min(max(0, requested), s.MaxCandyUse())
	if c == 0 {
		return nil
	}
	costs := s.candyStageCosts()
	rem := s.st.Active.UsedAtStage + c*rareCandyXP
	evolves := false
	for i, cost := range costs {
		if rem < cost {
			break
		}
		rem -= cost
		if i == len(costs)-1 {
			return &CandyPlan{Count: c, XP: c * rareCandyXP, Evolves: evolves, Graduates: true, DiscardedXP: rem}
		}
		evolves = true
	}
	p := &CandyPlan{Count: c, XP: c * rareCandyXP, Evolves: evolves}
	if evolves {
		p.CarryoverXP = rem
	}
	return p
}

// CandyUse is the useCandy reply (protocol §7.4).
type CandyUse struct {
	Consumed int    `json:"consumed"`
	XP       int    `json:"xp"`
	Result   string `json:"result"`
}

// UseCandy spends up to count candies (clamped to MaxCandyUse) and applies their XP through the
// normal growth loop, which never touches usedSinceInstall. A disguised Ditto spends only what its
// reveal needs. It returns the evolve and graduate events. Errors in the protocol §7.4 order:
// badArgs, noActive, noStock, notReady (CS:1229-1249).
func (s *Store) UseCandy(count int) (CandyUse, []Event, error) {
	if count < 1 {
		return CandyUse{}, nil, gameErr("badArgs", "count must be at least 1")
	}
	switch s.candyBlocked() {
	case candyBlockedEgg:
		return CandyUse{}, nil, gameErr("noActive", "an egg is incubating")
	case candyBlockedNoStock:
		return CandyUse{}, nil, gameErr("noStock", "no Rare Candy in the bag")
	case candyBlockedLineLoading, candyBlockedRevealPending:
		return CandyUse{}, nil, gameErr("notReady", "the evolution line is not ready")
	}
	plan := s.PlanCandies(count)
	consumed := plan.Count
	a := s.st.Active
	if a.DittoDisguise != nil && !a.DittoRevealed {
		consumed = min(consumed, ceilCandies(max(0, s.stageThreshold(a)-a.UsedAtStage)))
	}
	s.st.Inventory[itemRareCandy] -= consumed
	before := a.StageIndex
	s.applyUsage(consumed * rareCandyXP) // saves inventory and growth together
	use := CandyUse{Consumed: consumed, XP: consumed * rareCandyXP, Result: CandyProgressed}
	switch {
	case s.st.Active == nil:
		use.Result = CandyGraduated
	case s.st.Active.StageIndex > before:
		use.Result = CandyEvolved
	}
	return use, s.drain(), nil
}

// canUseMint: an active Pokémon and a Mint; the line is not needed (CS:1255).
func (s *Store) canUseMint() bool { return s.st.Active != nil && s.itemCount(itemMint) > 0 }

// UseMint replaces the nature with a different one in one draw from the game RNG and returns it.
// Stats are derived from the nature at view time, so nothing else changes. Errors: noActive,
// noStock (CS:1259-1271).
func (s *Store) UseMint() (string, error) {
	switch {
	case s.st.Active == nil:
		return "", gameErr("noActive", "an egg is incubating")
	case s.itemCount(itemMint) <= 0:
		return "", gameErr("noStock", "no Mint in the bag")
	}
	a := s.st.Active
	pool := make([]string, 0, len(natures))
	for _, n := range natures {
		if a.Nature == nil || n != *a.Nature {
			pool = append(pool, n)
		}
	}
	nature := pool[int(s.rng.Next()%uint64(len(pool)))]
	a.Nature = &nature
	s.st.Inventory[itemMint]--
	s.save()
	return nature, nil
}

// BagView is state.companion.bag.
type BagView struct {
	CandyMaxUse  int     `json:"candyMaxUse"`
	CanUseMint   bool    `json:"canUseMint"`
	CandyBlocked *string `json:"candyBlocked"`
}

func (s *Store) bagView() BagView {
	v := BagView{CandyMaxUse: s.MaxCandyUse(), CanUseMint: s.canUseMint()}
	if r := s.candyBlocked(); r != "" {
		v.CandyBlocked = strPtr(r)
	}
	return v
}
