package companion

import (
	"math"
	"time"
)

// Balance constants (upstream CompanionModel.swift PokemonBalance, PokemonOdds, RareCandy).
const (
	eggHatchThreshold        = 5_000_000
	repeatGrowthMultiplier   = 2
	sulkAfterIdle            = 3 * time.Hour
	maxTokenValue            = 1_000_000_000_000_000 // SaveTransfer.maxTokenValue
	shinyDenominator         = 64
	dittoDisguiseDenominator = 128
	dittoSpeciesID           = 132
	weeklyCandyGrant         = 5
	maxSpeciesID             = 649 // Gen V animated sprites bound every hatchable species
	defaultDifficulty        = 1.0
	minDifficulty            = 0.1
	maxDifficulty            = 2.0
)

// Rarity values (wire and save enum).
const (
	rarityCommon    = "common"
	rarityUncommon  = "uncommon"
	rarityRare      = "rare"
	rarityLegendary = "legendary"
)

var rarities = []string{rarityLegendary, rarityRare, rarityUncommon, rarityCommon}

func rarityRank(r string) int {
	switch r {
	case rarityUncommon:
		return 1
	case rarityRare:
		return 2
	case rarityLegendary:
		return 3
	}
	return 0
}

func validRarity(r string) bool {
	switch r {
	case rarityCommon, rarityUncommon, rarityRare, rarityLegendary:
		return true
	}
	return false
}

// rarityFrom classifies a line from its base species (CM:96-101).
func rarityFrom(captureRate int, legendary, mythical bool) string {
	switch {
	case legendary || mythical:
		return rarityLegendary
	case captureRate <= 45:
		return rarityRare
	case captureRate <= 120:
		return rarityUncommon
	}
	return rarityCommon
}

// graduationTotal is T, the standard growth of a whole line (CM:114-121).
func graduationTotal(r string) int {
	switch r {
	case rarityUncommon:
		return 1_875_000_000
	case rarityRare:
		return 3_000_000_000
	case rarityLegendary:
		return 6_000_000_000
	}
	return 750_000_000
}

// phaseThreshold is the standard cost of phase stageIndex (0-based) of a k-form line, divided by
// the repeat-growth multiplier. Both steps round half away from zero (CM:123-131).
func phaseThreshold(r string, k, stageIndex, multiplier int) int {
	kk := max(1, k)
	i := stageIndex + 1
	total := float64(graduationTotal(r))
	denom := float64(kk*(kk+1)) / 2
	standard := int(math.Round(total * float64(i) / denom))
	return max(1, int(math.Round(float64(standard)/float64(max(1, multiplier)))))
}

// clampDifficulty keeps a growth multiplier in 0.1–2.0; non-finite values become 1.0 (CM:142-145).
func clampDifficulty(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return defaultDifficulty
	}
	return math.Min(math.Max(v, minDifficulty), maxDifficulty)
}

// scaled applies a difficulty to a base threshold, rounding half away from zero (CM:150-152).
func scaled(base int, difficulty float64) int {
	return int(math.Round(float64(base) * clampDifficulty(difficulty)))
}

func clampToken(v int) int { return min(max(0, v), maxTokenValue) }

// natures in declaration order; the hatch roll indexes this list (CM:399-404).
var natures = []string{
	"hardy", "lonely", "brave", "adamant", "naughty",
	"bold", "docile", "relaxed", "impish", "lax",
	"timid", "hasty", "serious", "jolly", "naive",
	"modest", "mild", "quiet", "bashful", "rash",
	"calm", "gentle", "sassy", "careful", "quirky",
}

func validNature(n string) bool {
	for _, v := range natures {
		if v == n {
			return true
		}
	}
	return false
}

// natureModifier returns the main-series stat multiplier (PP:245-274).
func natureModifier(nature *string, stat string) float64 {
	if nature == nil {
		return 1
	}
	p, ok := naturePairs[*nature]
	switch {
	case !ok:
		return 1
	case stat == p[0]:
		return 1.1
	case stat == p[1]:
		return 0.9
	}
	return 1
}

// naturePairs maps a non-neutral nature to its (raised, lowered) stat.
var naturePairs = map[string][2]string{
	"lonely": {"attack", "defense"}, "brave": {"attack", "speed"},
	"adamant": {"attack", "special-attack"}, "naughty": {"attack", "special-defense"},
	"bold": {"defense", "attack"}, "relaxed": {"defense", "speed"},
	"impish": {"defense", "special-attack"}, "lax": {"defense", "special-defense"},
	"timid": {"speed", "attack"}, "hasty": {"speed", "defense"},
	"jolly": {"speed", "special-attack"}, "naive": {"speed", "special-defense"},
	"modest": {"special-attack", "attack"}, "mild": {"special-attack", "defense"},
	"quiet": {"special-attack", "speed"}, "rash": {"special-attack", "special-defense"},
	"calm": {"special-defense", "attack"}, "gentle": {"special-defense", "defense"},
	"sassy": {"special-defense", "speed"}, "careful": {"special-defense", "special-attack"},
}

// dittoDisguiseHit is the pure disguise roll: common lines with at least two forms, 1/128 (CS:1721-1723).
func dittoDisguiseHit(r string, totalForms int, roll uint64) bool {
	return r == rarityCommon && totalForms >= 2 && roll%dittoDisguiseDenominator == 0
}

// collectionWeight halves an already-collected weight, keeping every candidate selectable
// (CollectionWeight.swift).
func collectionWeight(weight int, collected bool) int {
	if collected {
		return max(1, weight/2)
	}
	return max(1, weight)
}
