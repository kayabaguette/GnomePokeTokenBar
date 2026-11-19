package companion

import (
	"context"
	"time"
)

// Ditto disguise reveal (upstream CompanionStore.swift:985-990, 1012-1019, 1826-1878;
// docs/spec/prayers-ditto.md §2).

// revealIfNeeded reveals a disguised Ditto once its usage reaches the disguise's first threshold.
//
// Update calls it after the active line is loaded. It stands in for both upstream triggers: the
// applyUsage kick (CS:1016-1019; applyUsage only stops there and stays ctx-free) and the backup
// check of every tick (CS:985-990). The daemon re-runs Update after a candy, a warm and networkUp,
// so a reveal lands in the pass that follows the usage.
//
// While a disguise is active it also loads line 132 ahead of the threshold, so the reveal needs no
// extra pass when the line is in memory (decision 41). A miss is warmed by the daemon and retried by
// the next Update; meanwhile the bag shows revealPending.
//
// There is no generation guard and no in-progress flag: the Store is synchronous, so nothing can
// replace the active Pokémon between the fetch and the swap.
func (s *Store) revealIfNeeded(ctx context.Context) {
	a := s.st.Active
	if a == nil || a.DittoDisguise == nil || a.DittoRevealed || s.line == nil {
		return
	}
	thr := s.stageThreshold(a)
	due := a.UsedAtStage >= thr
	dl, err := s.fetchLine(ctx, dittoSpeciesID)
	if err != nil {
		if due {
			s.logf("companion: ditto reveal: line fetch failed; retry next update: %v", err)
		}
		return
	}
	if !due {
		return
	}
	// Read from the disguise's line before the swap: the plain species name, no form label (CS:1842).
	disguiseName := s.speciesName(s.line.names[a.BaseID], a.BaseID)
	carry := max(0, a.UsedAtStage-thr)
	previousRarity := a.Rarity
	plan := s.makePlan(dl.tree, dl.baseID) // a leaf: no draw
	a.BaseID, a.PathIDs, a.PlannedPathIDs = dl.baseID, []int{dl.baseID}, plan
	a.StageIndex, a.Rarity, a.TotalForms = 0, dl.rarity, len(plan)
	a.UsedAtStage, a.DittoRevealed, a.Form = carry, true, nil
	// The hatch boost came from the disguise's line; from now on only a repeat Ditto earns it.
	a.HasGrowthBoost = s.st.hasCollectedFinal(dl.baseID)
	// Cached Ditto details enrich the rebased profile in applyUsage(0) below (as upstream's own
	// enrich at CS:1856 does); otherwise warmActive loads them.
	a.Profile.rebaseForSpeciesIdentity(previousRarity, dl.rarity)
	s.st.reconcileRepresentative() // a pin that only the disguise species justified is cleared
	s.line = dl
	s.logf("companion: ditto reveal: disguise=%d -> ditto rarity=%s shiny=%v", *a.DittoDisguise, dl.rarity, a.IsShiny)
	s.emit(DittoRevealEvent{
		Type: "event", Kind: "dittoReveal", DisguiseSpeciesID: *a.DittoDisguise, DisguiseDisplayName: disguiseName,
		SpeciesID: dl.baseID, DisplayName: s.speciesName(dl.names[dl.baseID], dl.baseID), Rarity: dl.rarity,
		IsShiny: a.IsShiny,
	})
	s.openWindow(5 * time.Second)
	s.save()
	s.applyUsage(0) // the carry can graduate Ditto at once; its graduate event follows the reveal
	s.warmActive(ctx)
}
