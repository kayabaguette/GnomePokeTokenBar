package companion

import (
	"sort"
	"time"
)

// Graduate is one Pokémon that graduated in a recap period (protocol §7.5).
type Graduate struct {
	// EntryID is the catch-log Entry.id, so the extension can fetch the sprite with getSprite.
	EntryID   string  `json:"entryId"`
	SpeciesID int     `json:"speciesId"`
	Form      *string `json:"form"`
	IsShiny   bool    `json:"isShiny"`
	// DisplayName is localized; only Unown gets a " [LETTER]" suffix (RV:31-35).
	DisplayName string `json:"displayName"`
}

// Graduates returns how many dex entries graduated in [from, to) and the newest limit of them
// (UsageRecap.swift:250-254). A graduate is an entry that is not released and has a catch date in
// the period; the Pokémon still being raised is not in the dex yet.
func (s *Store) Graduates(from, to time.Time, limit int) (count int, items []Graduate) {
	var in []dexEntry
	for _, e := range s.st.Dex {
		if e.ReleasedAt == nil && e.CaughtAt != nil && !e.CaughtAt.Before(from) && e.CaughtAt.Before(to) {
			in = append(in, e)
		}
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].CaughtAt.After(*in[j].CaughtAt) })
	items = []Graduate{}
	for _, e := range in[:min(len(in), max(0, limit))] {
		form := formForSpecies(e.FinalID, e.Form)
		items = append(items, Graduate{EntryID: e.ID, SpeciesID: e.FinalID, Form: form, IsShiny: e.IsShiny,
			DisplayName: formDisplayName(s.entryName(e), e.FinalID, form, "")})
	}
	return len(in), items
}
