package companion

// Prayers over the egg (upstream CompanionStore.swift:1404-1412; docs/spec/prayers-ditto.md §1).

// Pray counts one prayer over the egg, saves, and returns the new count. It is a placebo: it never
// reads the RNG and changes no roll. While a Pokémon is active it fails with notEgg and changes
// nothing; upstream is a silent no-op, but the reply needs a code (protocol §7.6).
func (s *Store) Pray() (int, error) {
	if s.st.Active != nil {
		return 0, gameErr("notEgg", "a Pokémon is active; only an egg can be prayed over")
	}
	if s.st.EggPrayers == nil {
		s.st.EggPrayers = &prayerRecord{}
	}
	s.st.EggPrayers.Count++
	s.save() // on every prayer, as upstream (CS:1411)
	return s.st.EggPrayers.Count, nil
}

// prayerCount is a prayer record's count on the wire: 0 for none (protocol §5.4, §6, §7.2).
func prayerCount(p *prayerRecord) int {
	if p == nil {
		return 0
	}
	return p.Count
}
