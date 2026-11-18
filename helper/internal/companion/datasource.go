package companion

import "context"

// DataSource supplies Pokémon data. The daemon implements it over the PokéAPI client. Methods may
// block on the network and must honour ctx. Species calls Names from several goroutines at once, so
// an implementation must be safe for concurrent use.
type DataSource interface {
	// BaseIndex returns the hatch candidates: base species with id ≤ 649, Ditto excluded.
	BaseIndex(ctx context.Context) ([]BaseEntry, error)
	// Line returns the evolution line that starts at baseID, unpruned.
	Line(ctx context.Context, baseID int) (*Line, error)
	// Species returns one species; the REST fallback hatch and unowned dex pages use it.
	Species(ctx context.Context, id int) (*Species, error)
	// Details returns the default-form battle data of a species.
	Details(ctx context.Context, speciesID int) (*Details, error)
	// Names returns the localized names (language code → name) of a PokéAPI resource. kind is
	// "type", "ability", "move" or "pokemon-form"; name is its identifier or numeric id.
	Names(ctx context.Context, kind, name string) (map[string]string, error)
}

// BaseEntry is one hatch candidate.
type BaseEntry struct {
	ID          int
	CaptureRate int
}

// Node is one node of an evolution tree.
type Node struct {
	ID       int
	Children []Node
}

// Line is an evolution line: the tree, its base species' rarity inputs and every node's names.
type Line struct {
	BaseID      int
	Tree        Node
	CaptureRate int // of the base species
	IsLegendary bool
	IsMythical  bool
	Names       map[int]map[string]string // species id → language code → name
}

// Species is the part of pokemon-species the game uses.
type Species struct {
	ID          int
	CaptureRate int
	IsLegendary bool
	IsMythical  bool
	EvolvesFrom int // 0 for a base species
	Names       map[string]string
}

// Details is the default-form battle data of a species, moves limited to black-2-white-2.
type Details struct {
	SpeciesID  int
	Height     int // decimetres
	Weight     int // hectograms
	GenderRate int // female eighths; -1 is genderless
	Types      []string
	BaseStats  map[string]int // PokéAPI stat names: hp, attack, …, special-defense, speed
	Abilities  []Ability
	Moves      []Move
}

// Ability is one possible ability of a species.
type Ability struct {
	Name   string
	Slot   int
	Hidden bool
}

// Move is a learnable move with every learn method.
type Move struct {
	Name    string
	Methods []LearnMethod
}

// LearnMethod is a move-learn-method name and its level (0 unless "level-up").
type LearnMethod struct {
	Method string
	Level  int
}
