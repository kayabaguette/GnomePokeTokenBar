package companion

// Event is a game transition the daemon writes as one `event` line (protocol §6). Each value
// marshals to the complete line, "type":"event" included, and is returned only after the state
// change it reports has been applied.
type Event interface{ EventKind() string }

// HatchEvent: an egg hatched.
type HatchEvent struct {
	Type        string  `json:"type"`
	Kind        string  `json:"kind"`
	SpeciesID   int     `json:"speciesId"`
	Form        *string `json:"form"`
	DisplayName string  `json:"displayName"`
	Rarity      string  `json:"rarity"`
	IsShiny     bool    `json:"isShiny"`
	ShinyOdds   int     `json:"shinyOdds"`
	Prayers     int     `json:"prayers"` // the egg's prayer count at hatch; 0 for none (M3)
}

// EvolveEvent: the active Pokémon evolved.
type EvolveEvent struct {
	Type            string  `json:"type"`
	Kind            string  `json:"kind"`
	FromSpeciesID   int     `json:"fromSpeciesId"`
	FromDisplayName string  `json:"fromDisplayName"`
	SpeciesID       int     `json:"speciesId"`
	Form            *string `json:"form"`
	DisplayName     string  `json:"displayName"`
	IsShiny         bool    `json:"isShiny"`
}

// GraduateEvent: the active Pokémon graduated into the catch log.
type GraduateEvent struct {
	Type        string  `json:"type"`
	Kind        string  `json:"kind"`
	SpeciesID   int     `json:"speciesId"`
	Form        *string `json:"form"`
	DisplayName string  `json:"displayName"`
	Rarity      string  `json:"rarity"`
	IsShiny     bool    `json:"isShiny"`
	EntryID     string  `json:"entryId"`
}

// CandyGrantedEvent: Rare Candies were granted for a limit window reaching 100 %.
type CandyGrantedEvent struct {
	Type       string `json:"type"`
	Kind       string `json:"kind"`
	Item       string `json:"item"`
	Count      int    `json:"count"`
	ProviderID string `json:"providerId"`
	WindowKey  string `json:"windowKey"`
	WindowKind string `json:"windowKind"`
}

// BadgeEvent: a Pokédex completion badge was earned (once per key, ever).
type BadgeEvent struct {
	Type   string  `json:"type"`
	Kind   string  `json:"kind"`
	Key    string  `json:"key"`
	Badge  string  `json:"badge"`
	Region *string `json:"region"`
}

// DittoRevealEvent: a disguised Ditto revealed itself at its first evolution threshold (M3).
// It carries no shinyOdds: charm ownership at hatch time is not saved.
type DittoRevealEvent struct {
	Type                string `json:"type"`
	Kind                string `json:"kind"`
	DisguiseSpeciesID   int    `json:"disguiseSpeciesId"`
	DisguiseDisplayName string `json:"disguiseDisplayName"` // plain species name, no form label; #id fallback
	SpeciesID           int    `json:"speciesId"`
	DisplayName         string `json:"displayName"`
	Rarity              string `json:"rarity"`
	IsShiny             bool   `json:"isShiny"` // the raw roll, now displayed
}

// EventKind implements Event.
func (HatchEvent) EventKind() string { return "hatch" }

// EventKind implements Event.
func (EvolveEvent) EventKind() string { return "evolve" }

// EventKind implements Event.
func (GraduateEvent) EventKind() string { return "graduate" }

// EventKind implements Event.
func (CandyGrantedEvent) EventKind() string { return "candyGranted" }

// EventKind implements Event.
func (BadgeEvent) EventKind() string { return "badge" }

// EventKind implements Event.
func (DittoRevealEvent) EventKind() string { return "dittoReveal" }
