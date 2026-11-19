package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
)

// savedEggPrayers reads eggPrayers.count from the save on disk; -1 when the key is absent.
func savedEggPrayers(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "companion-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		EggPrayers *struct{ Count int } `json:"eggPrayers"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.EggPrayers == nil {
		return -1
	}
	return s.EggPrayers.Count
}

// Protocol §4 and §7.6: pray is saved, the state showing egg.prayers precedes the result, and an
// active Pokémon refuses it with notEgg.
func TestPrayCommand(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	h.sendSettings(1, false)
	h.expect("egg state", wait, func(m msg) bool { return isFreshState(m) && m.str("companion", "phase") == "egg" })
	for i, want := range []int{1, 2} {
		id := int64(10 + i)
		from := len(h.seen)
		h.send(fmt.Sprintf(`{"id":%d,"cmd":"pray"}`, id))
		r := h.expect("pray", wait, isResult(id))
		if !r.flag("ok") || r.num("data", "count") != float64(want) {
			t.Fatalf("pray = %v", r)
		}
		st := h.indexOf(from, func(m msg) bool { return m.typ() == "state" && m.num("companion", "egg", "prayers") == float64(want) })
		if st < 0 || st > len(h.seen)-2 {
			t.Fatalf("no state with egg.prayers %d before the result:\n%s", want, h.tail(4))
		}
		if got := savedEggPrayers(t, h.cfg.StateDir); got != want {
			t.Fatalf("saved eggPrayers = %d, want %d", got, want)
		}
	}
	h.send(`{"id":20,"cmd":"pray","args":[1]}`)
	if r := h.expect("pray badArgs", wait, isResult(20)); errorCode(r) != "badArgs" {
		t.Fatalf("pray with array args = %v", r)
	}

	a := shopWorld(t, "[16]", 0, 0, 0, `{}`)
	a.start()
	a.sendSettings(1, false)
	a.expect("active state", wait, func(m msg) bool { return isFreshState(m) && m.str("companion", "phase") == "active" })
	a.send(`{"id":2,"cmd":"pray"}`)
	if r := a.expect("pray while active", wait, isResult(2)); r.flag("ok") || errorCode(r) != "notEgg" {
		t.Fatalf("pray while active = %v", r)
	}
	if got := savedEggPrayers(t, a.cfg.StateDir); got != -1 {
		t.Fatalf("saved eggPrayers = %d after notEgg", got)
	}
}

// disguiseWorld is shopWorld with a disguised Ditto showing as base 16 (common, k = 3, reveal
// threshold 125M) and five Rare Candies.
func disguiseWorld(t *testing.T, usedAtStage int) *harness {
	h := newHarness(t, "")
	h.usage.set(0)
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), fmt.Sprintf(`{"installBaselineSet":true,
		"lastDate":"2026-01-01","claimedTodayTokensByProvider":{},"inventory":{"rareCandy":5},"candyFeatureSeeded":true,
		"active":{"baseID":16,"pathIDs":[16],"plannedPathIDs":[16,17,18],"stageIndex":0,"usedAtStage":%d,
		"rarity":"common","totalForms":3,"isShiny":true,"dittoDisguise":16}}`, usedAtStage))
	return h
}

func isDittoState(m msg) bool {
	return m.typ() == "state" && m.num("companion", "active", "speciesId") == 132
}

// Protocol §7.4 and decision 42, end to end: the candy that reaches the threshold replies
// progressed; the reveal's state and dittoReveal event follow the result. While line 132 cannot be
// loaded the bag shows revealPending, until networkUp gets the line.
func TestDittoRevealAfterCandy(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(fmt.Sprintf("line132Offline=%v", offline), func(t *testing.T) {
			h := disguiseWorld(t, 100_000_000)
			h.poke.dittoOffline.Store(offline)
			h.start()
			h.sendSettings(1, false)
			st := h.expect("candy usable", wait, func(m msg) bool { return isFreshState(m) && isCandyReady(m) })
			if st.num("companion", "active", "speciesId") != 16 || st.flag("companion", "active", "isShiny") {
				t.Fatalf("disguise state: %v", st.path("companion", "active"))
			}
			sent := len(h.seen)
			h.send(`{"id":2,"cmd":"useCandy","args":{"count":5}}`)
			r := h.expect("useCandy", wait, isResult(2))
			if r.num("data", "consumed") != 1 || r.str("data", "result") != "progressed" {
				t.Fatalf("useCandy = %v", r)
			}
			res := len(h.seen) - 1
			if offline {
				// The state before the result already shows it (the batch stops at the threshold).
				if h.indexOf(sent, func(m msg) bool {
					return m.typ() == "state" && m.str("companion", "bag", "candyBlocked") == "revealPending"
				}) < 0 {
					t.Fatalf("no revealPending state:\n%s", h.tail(4))
				}
				h.send(`{"id":3,"cmd":"useCandy","args":{"count":1}}`)
				if r := h.expect("useCandy pending", wait, isResult(3)); errorCode(r) != "notReady" {
					t.Fatalf("useCandy while pending = %v", r)
				}
				h.quiet(300*time.Millisecond, "no reveal offline", isEvent("dittoReveal"))
				h.poke.dittoOffline.Store(false)
				h.send(`{"id":4,"cmd":"networkUp"}`)
			}
			ev := h.expect("dittoReveal", wait, isEvent("dittoReveal"))
			if ev.num("disguiseSpeciesId") != 16 || ev.str("disguiseDisplayName") != "Mon16" || ev.num("speciesId") != 132 ||
				ev.str("displayName") != "Ditto" || ev.str("rarity") != "rare" || !ev.flag("isShiny") {
				t.Fatalf("dittoReveal = %v", ev)
			}
			evIdx := len(h.seen) - 1
			stIdx := h.indexOf(res, isDittoState)
			if stIdx < 0 || stIdx > evIdx {
				t.Fatalf("order result=%d state=%d event=%d\n%s", res, stIdx, evIdx, h.tail(8))
			}
			s := h.seen[stIdx]
			if s.num("companion", "active", "usedAtStage") != 75_000_000 || !s.flag("companion", "active", "isShiny") ||
				s.str("companion", "active", "rarity") != "rare" || s.path("companion", "levelUpUntil") == nil {
				t.Fatalf("revealed state: %v", s.path("companion"))
			}
		})
	}
}

// Decision 41: the helper rolls the Ditto disguise. With a seed whose fourth draw (species, shiny,
// nature, disguise) is a multiple of 128, a ready egg hatches a disguise; it shows as itself, not shiny.
func TestDaemonRollsTheDittoDisguise(t *testing.T) {
	var seed uint64
	for ; seed < 100_000; seed++ {
		r := companion.SplitMix64{State: seed}
		v := [4]uint64{r.Next(), r.Next(), r.Next(), r.Next()}
		if v[3]%128 == 0 && v[1]%64 != 0 {
			break
		}
	}
	h := newHarness(t, "")
	h.cfg.Rng = &companion.SplitMix64{State: seed}
	h.usage.set(0)
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), `{"installBaselineSet":true,
		"lastDate":"2026-01-01","claimedTodayTokensByProvider":{},"eggUsage":5000000}`)
	h.start()
	h.sendSettings(1, false)
	ev := h.expect("hatch", wait, isEvent("hatch"))
	if ev.num("speciesId") != 16 || ev.flag("isShiny") || ev.str("rarity") != "common" {
		t.Fatalf("hatch = %v", ev)
	}
	raw, err := os.ReadFile(filepath.Join(h.cfg.StateDir, "companion-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct{ Active struct{ DittoDisguise *int } }
	if err := json.Unmarshal(raw, &s); err != nil || s.Active.DittoDisguise == nil || *s.Active.DittoDisguise != 16 {
		t.Fatalf("seed %d: no disguise in the save: %s", seed, raw)
	}
}
