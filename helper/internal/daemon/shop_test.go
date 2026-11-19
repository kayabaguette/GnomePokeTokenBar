package daemon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
)

// shopWorld writes a save with an active Pokémon of the fake line 16 → 17 → 18 (common, k = 3)
// at the given stage and growth, a wallet and an inventory. Today's usage is 0, so no scan
// credits anything.
func shopWorld(t *testing.T, path string, stage, usedAtStage, used int, inventory string) *harness {
	h := newHarness(t, "")
	h.usage.set(0)
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), fmt.Sprintf(`{"installBaselineSet":true,
		"lastDate":"2026-01-01","claimedTodayTokensByProvider":{},"usedSinceInstall":%d,"inventory":%s,
		"active":{"baseID":16,"pathIDs":%s,"plannedPathIDs":[16,17,18],"stageIndex":%d,"usedAtStage":%d,
		"rarity":"common","totalForms":3,"nature":"adamant"}}`, used, inventory, path, stage, usedAtStage))
	return h
}

func isCandyReady(m msg) bool {
	return m.typ() == "state" && m.path("companion", "bag", "candyBlocked") == nil &&
		m.str("companion", "phase") == "active"
}

func errorCode(m msg) string { return m.str("error", "code") }

// indexOf finds the first line seen after from that matches pred; -1 when none.
func (h *harness) indexOf(from int, pred func(msg) bool) int {
	for i := from; i < len(h.seen); i++ {
		if pred(h.seen[i]) {
			return i
		}
	}
	return -1
}

// Protocol §4 and §7.4: the five Shop and Bag commands, their error codes, and the order state →
// events → result.
func TestShopAndBagCommands(t *testing.T) {
	h := shopWorld(t, "[16]", 0, 0, 10_000_000_000, `{"rareCandy":3,"mint":1}`)
	h.poke.bases = append(h.poke.bases, companion.BaseEntry{ID: 20, CaptureRate: 30})
	h.start()
	h.sendSettings(1, false)
	st := h.expect("candy usable", wait, func(m msg) bool { return isFreshState(m) && isCandyReady(m) })
	if st.num("companion", "bag", "candyMaxUse") != 3 || st.num("companion", "wallet") != 10_000_000_000 ||
		st.num("settingsApplied", "shopDifficulty") != 1 {
		t.Fatalf("initial companion: %v", st.path("companion", "bag"))
	}

	h.send(`{"id":2,"cmd":"planCandies","args":{"count":2}}`)
	r := h.expect("planCandies", wait, isResult(2))
	if r.num("data", "plan", "count") != 2 || r.num("data", "plan", "xp") != 200_000_000 || !r.flag("data", "plan", "evolves") ||
		r.flag("data", "plan", "graduates") || r.num("data", "plan", "carryoverXP") != 75_000_000 || r.num("data", "plan", "discardedXP") != 0 {
		t.Fatalf("plan = %v", r["data"])
	}
	h.send(`{"id":3,"cmd":"planCandies","args":{"count":0}}`)
	if r := h.expect("planCandies 0", wait, isResult(3)); !r.flag("ok") || r.path("data", "plan") != nil {
		t.Fatalf("plan(0) = %v", r)
	}

	from := len(h.seen)
	h.send(`{"id":4,"cmd":"useCandy","args":{"count":2}}`)
	r = h.expect("useCandy", wait, isResult(4))
	if r.num("data", "consumed") != 2 || r.num("data", "xp") != 200_000_000 || r.str("data", "result") != "evolved" {
		t.Fatalf("useCandy = %v", r)
	}
	stIdx := h.indexOf(from, func(m msg) bool {
		return m.typ() == "state" && m.num("companion", "active", "stageIndex") == 1 && m.num("companion", "inventory", "rareCandy") == 1
	})
	evIdx := h.indexOf(from, isEvent("evolve"))
	if stIdx < 0 || evIdx < stIdx || len(h.seen)-1 < evIdx {
		t.Fatalf("order state=%d event=%d result=%d\n%s", stIdx, evIdx, len(h.seen)-1, h.tail(6))
	}

	from = len(h.seen)
	h.send(`{"id":5,"cmd":"buyItem","args":{"item":"mint","count":2}}`)
	r = h.expect("buyItem", wait, isResult(5))
	if r.str("data", "item") != "mint" || r.num("data", "count") != 2 || r.num("data", "spent") != 200_000_000 ||
		r.num("data", "wallet") != 9_800_000_000 || r.num("data", "owned") != 3 {
		t.Fatalf("buyItem = %v", r)
	}
	if i := h.indexOf(from, func(m msg) bool { return m.typ() == "state" && m.num("companion", "inventory", "mint") == 3 }); i < 0 || i > len(h.seen)-2 {
		t.Fatalf("no state before the buyItem result:\n%s", h.tail(4))
	}

	from = len(h.seen)
	h.send(`{"id":6,"cmd":"useMint"}`)
	r = h.expect("useMint", wait, isResult(6))
	nature := r.str("data", "nature")
	if nature == "" || nature == "adamant" {
		t.Fatalf("useMint = %v", r)
	}
	if i := h.indexOf(from, func(m msg) bool { return m.typ() == "state" && m.str("companion", "active", "nature") == nature }); i < 0 {
		t.Fatalf("no state with the new nature before the result:\n%s", h.tail(4))
	}

	errs := []struct{ line, code string }{
		{`{"id":10,"cmd":"planCandies","args":{"count":"2"}}`, "badArgs"},
		{`{"id":11,"cmd":"planCandies"}`, "badArgs"},
		{`{"id":12,"cmd":"useCandy","args":{"count":0}}`, "badArgs"},
		{`{"id":13,"cmd":"useCandy","args":{"count":1.5}}`, "badArgs"},
		{`{"id":14,"cmd":"buyItem","args":{"item":"pokeBall"}}`, "badArgs"},
		{`{"id":15,"cmd":"buyItem","args":{"item":"mint","count":0}}`, "badArgs"},
		{`{"id":16,"cmd":"buyItem","args":{"item":"shinyCharm","count":2}}`, "badArgs"},
		{`{"id":17,"cmd":"buyItem","args":{"item":"rareCandy","count":100}}`, "insufficientFunds"},
		{`{"id":18,"cmd":"buyItem","args":{"item":"shinyCharm"}}`, ""},
		{`{"id":19,"cmd":"buyItem","args":{"item":"shinyCharm"}}`, "alreadyOwned"},
		{`{"id":20,"cmd":"buyItem","args":[1]}`, "badArgs"},
		{`{"id":27,"cmd":"buyItem","args":{"item":5}}`, "badArgs"},
		{`{"id":28,"cmd":"buyItem","args":{"item":"mint","count":"2"}}`, "badArgs"},
		{`{"id":21,"cmd":"buyEgg","args":{}}`, "badArgs"},
		{`{"id":22,"cmd":"buyEgg","args":{"tier":"legendary"}}`, "badArgs"},
		{`{"id":23,"cmd":"buyEgg","args":{"tier":"common"}}`, "badArgs"},
		{`{"id":24,"cmd":"useMint"}`, ""},
		{`{"id":25,"cmd":"useMint"}`, ""},
		{`{"id":26,"cmd":"useMint"}`, "noStock"},
	}
	for _, c := range errs {
		h.send(c.line)
	}
	for _, c := range errs {
		var id int64
		fmt.Sscanf(c.line, `{"id":%d`, &id)
		r := h.expect(c.line, wait, isResult(id))
		if got := errorCode(r); got != c.code || r.flag("ok") != (c.code == "") {
			t.Errorf("%s → %v, want %q", c.line, r, c.code)
		}
	}
	if st := h.lastState(); st.num("companion", "shop", "shinyOdds") != 48 || st.num("companion", "inventory", "shinyCharm") != 1 {
		t.Fatalf("shop after the Charm: %v", st.path("companion", "shop", "shinyOdds"))
	}

	from = len(h.seen)
	h.send(`{"id":30,"cmd":"buyEgg","args":{"tier":"rare"}}`)
	r = h.expect("buyEgg", wait, isResult(30))
	if r.str("data", "tier") != "rare" || r.num("data", "spent") != 4_000_000_000 || r.str("data", "releasedEntryId") == "" {
		t.Fatalf("buyEgg = %v", r)
	}
	if i := h.indexOf(from, func(m msg) bool {
		return m.typ() == "state" && m.str("companion", "phase") == "egg" && m.str("companion", "egg", "guarantee") == "rare"
	}); i < 0 || i > len(h.seen)-2 {
		t.Fatalf("no egg state before the buyEgg result:\n%s", h.tail(4))
	}
	for id, line := range map[int]string{
		31: `{"id":31,"cmd":"buyEgg","args":{"tier":null}}`, 32: `{"id":32,"cmd":"useCandy","args":{"count":1}}`,
		33: `{"id":33,"cmd":"useMint"}`,
	} {
		h.send(line)
		if r := h.expect(line, wait, isResult(int64(id))); errorCode(r) != "noActive" {
			t.Errorf("%s → %v", line, r)
		}
	}
	// The prefetch runs after the reply, inside the tier: only base 20 (capture rate 30) qualifies.
	save := filepath.Join(h.cfg.StateDir, "companion-state.json")
	deadline := time.Now().Add(wait)
	for {
		b, _ := os.ReadFile(save)
		if bytes.Contains(b, []byte(`"pendingHatchID": 20`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no prefetched species in the tier:\n%s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.stop()
}

// RareCandyTests.swift:541, Go half: a candy graduation updates the top bar at once. The state
// queued before the result shows the egg, with no refresh, and the graduate event comes between.
func TestUseCandyGraduationEmitsEggState(t *testing.T) {
	h := shopWorld(t, "[16,17,18]", 2, 325_000_000, 0, `{"rareCandy":1}`)
	h.start()
	h.sendSettings(1, false)
	h.expect("candy usable", wait, func(m msg) bool { return isFreshState(m) && isCandyReady(m) })
	from := len(h.seen)
	h.send(`{"id":2,"cmd":"useCandy","args":{"count":5}}`)
	r := h.expect("useCandy", wait, isResult(2))
	if r.num("data", "consumed") != 1 || r.str("data", "result") != "graduated" {
		t.Fatalf("useCandy = %v", r)
	}
	stIdx := h.indexOf(from, func(m msg) bool {
		return m.typ() == "state" && m.flag("companion", "menuSubject", "egg") && m.str("companion", "phase") == "egg"
	})
	evIdx := h.indexOf(from, isEvent("graduate"))
	if stIdx < 0 || evIdx < stIdx || evIdx > len(h.seen)-2 {
		t.Fatalf("order state=%d event=%d result=%d\n%s", stIdx, evIdx, len(h.seen)-1, h.tail(6))
	}
	h.stop()
}

// Decision §14.10: while the line cannot load, the Bag says lineLoading and useCandy is notReady;
// the Mint still works.
func TestCandyNotReadyWhileLineLoading(t *testing.T) {
	h := shopWorld(t, "[16]", 0, 0, 0, `{"rareCandy":2,"mint":1}`)
	h.poke.offline.Store(true)
	h.start()
	h.sendSettings(1, false)
	st := h.expect("fresh state", wait, isFreshState)
	if st.str("companion", "bag", "candyBlocked") != "lineLoading" || st.num("companion", "bag", "candyMaxUse") != 0 ||
		!st.flag("companion", "bag", "canUseMint") {
		t.Fatalf("bag = %v", st.path("companion", "bag"))
	}
	h.send(`{"id":2,"cmd":"useCandy","args":{"count":1}}`)
	if r := h.expect("useCandy", wait, isResult(2)); errorCode(r) != "notReady" {
		t.Fatalf("useCandy = %v", r)
	}
	h.send(`{"id":3,"cmd":"planCandies","args":{"count":1}}`)
	if r := h.expect("planCandies", wait, isResult(3)); !r.flag("ok") || r.path("data", "plan") != nil {
		t.Fatalf("planCandies = %v", r)
	}
	h.send(`{"id":4,"cmd":"useMint"}`)
	if r := h.expect("useMint", wait, isResult(4)); !r.flag("ok") {
		t.Fatalf("useMint = %v", r)
	}
	h.stop()
}

// Protocol §9.2: shopDifficulty is optional in setSettings, kept when absent, type-checked when
// present, persisted in settings-applied.json and never rescales growth.
func TestSetSettingsShopDifficulty(t *testing.T) {
	h := shopWorld(t, "[16]", 0, 50_000_000, 0, `{}`)
	h.start()
	settings := `{"id":%d,"cmd":"setSettings","args":{"refreshInterval":0,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1.0,"weekStart":1,"language":"en","alwaysFacesBack":false%s}}`
	h.send(fmt.Sprintf(settings, 1, `,"shopDifficulty":0.5`))
	r := h.expect("setSettings", wait, isResult(1))
	if r.num("data", "shopDifficulty") != 0.5 {
		t.Fatalf("reply = %v", r)
	}
	st := h.lastState()
	entries, _ := st.path("companion", "shop", "entries").([]any)
	if st.num("settingsApplied", "shopDifficulty") != 0.5 || len(entries) != 6 || entries[0].(map[string]any)["price"] != 50_000_000.0 ||
		st.num("companion", "active", "usedAtStage") != 50_000_000 {
		t.Fatalf("state after 0.5: %v %v", st.path("settingsApplied"), entries)
	}
	h.send(fmt.Sprintf(settings, 2, ""))
	if r := h.expect("setSettings without shop", wait, isResult(2)); r.num("data", "shopDifficulty") != 0.5 {
		t.Fatalf("absent field must keep 0.5: %v", r)
	}
	h.send(fmt.Sprintf(settings, 3, `,"shopDifficulty":20`))
	if r := h.expect("setSettings 20", wait, isResult(3)); r.num("data", "shopDifficulty") != 2 {
		t.Fatalf("clamp: %v", r)
	}
	for id, bad := range map[int]string{4: `,"shopDifficulty":"1"`, 5: `,"shopDifficulty":null`} {
		h.send(fmt.Sprintf(settings, id, bad))
		if r := h.expect("bad shopDifficulty", wait, isResult(int64(id))); errorCode(r) != "badArgs" {
			t.Fatalf("%s → %v", bad, r)
		}
	}
	h.stop()
	var saved Settings
	if ok, err := readJSON(filepath.Join(h.cfg.StateDir, settingsFile), &saved); !ok || err != nil || saved.ShopDifficulty != 2 {
		t.Fatalf("persisted %+v %v", saved, err)
	}
}

// DifficultyTests.swift:210 and DifficultySaveTests.swift:29/:44, Go form: the applied shop
// difficulty round-trips through settings-applied.json, a stored 20 loads as 2, and an M1 file
// without the key loads as 1.0. The store has it before the first cached state.
func TestShopDifficultyLoadsFromSettingsFile(t *testing.T) {
	for file, want := range map[string]float64{
		`{"refreshInterval":120,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":false,"shopDifficulty":1.7}`: 1.7,
		`{"refreshInterval":120,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":false,"shopDifficulty":20}`:  2,
		`{"refreshInterval":120,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":false}`:                      1,
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, settingsFile), file)
		cfg := Config{StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Data: newFakePoke(), Logf: t.Logf}.withDefaults()
		d, err := open(cfg, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		st := d.buildState(time.Now())
		mint := st.Companion.Shop.Entries[0]
		if d.store.ShopDifficulty() != want || st.SettingsApplied.ShopDifficulty != want || mint.Price != int(want*100_000_000+0.5) {
			t.Errorf("file %s: store %v applied %v mint %d, want %v", file, d.store.ShopDifficulty(), st.SettingsApplied.ShopDifficulty, mint.Price, want)
		}
	}
}
