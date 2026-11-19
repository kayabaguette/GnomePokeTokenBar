package daemon

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Protocol §5.2 Heatmap and §7.5 getRecap end to end: the grid and the recap read the same history,
// the recap echoes its args, and the graduates are the dex entries caught in the period.
func TestGetRecapAndHeatmapAgree(t *testing.T) {
	dir := t.TempDir()
	caught := time.Now().UTC().Format(time.RFC3339)
	writeFile(t, filepath.Join(dir, "companion-state.json"), fmt.Sprintf(`{"installBaselineSet":true,"lastDate":"2026-01-01",
		"claimedTodayTokensByProvider":{},"dex":[
		{"id":"g-new","baseID":16,"finalID":16,"chainOrder":[16],"rarity":"common","caughtAt":%q,"isShiny":true,"names":{"16":{"en":"Mon16"}}},
		{"id":"g-old","baseID":16,"finalID":16,"chainOrder":[16],"rarity":"common","caughtAt":"2020-01-01T00:00:00Z","names":{"16":{"en":"Mon16"}}}]}`, caught))
	h := newHarness(t, dir)
	for ago, tokens := range map[int]int64{1: 700_000, 2: 300_000, 3: 900_000, 9: 50_000} {
		h.usage.history[ago] = tokens
	}
	h.start()
	h.sendSettings(1, false)
	h.expect("setSettings", wait, isResult(1))
	st := h.expect("a scanned state", wait, func(m msg) bool {
		return m.typ() == "state" && m.num("usage", "combined", "week", "tokens") > 0 && !m.flag("meta", "refreshing")
	})

	hm, _ := st.path("usage", "combined", "heatmap").(map[string]any)
	cells, _ := hm["tokens"].([]any)
	if len(cells) != 112 {
		t.Fatalf("heatmap = %v", hm)
	}
	start, _ := time.Parse("2006-01-02", hm["start"].(string))
	if start.Weekday() != time.Monday {
		t.Errorf("heatmap start %v is not a Monday (weekStart 1)", hm["start"])
	}
	today := st.str("usage", "calendar", "today")
	at := func(day string) int64 {
		d, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatal(err)
		}
		i := int(d.Sub(start).Hours() / 24)
		if i < 0 || i >= 112 {
			return -1
		}
		return int64(cells[i].(float64))
	}
	if got := at(today); got != int64(st.num("usage", "combined", "today", "tokens")) {
		t.Errorf("today's cell %d, today's total %v", got, st.num("usage", "combined", "today", "tokens"))
	}

	h.send(`{"id":2,"cmd":"getRecap","args":{"scope":"week","offset":0}}`)
	r := h.expect("getRecap", wait, isResult(2))
	if !r.flag("ok") || r.str("data", "scope") != "week" || r.num("data", "offset") != 0 || !r.flag("data", "isInProgress") {
		t.Fatalf("recap = %v", r)
	}
	buckets, _ := r.path("data", "buckets").([]any)
	if len(buckets) != 7 || r.str("data", "start") != buckets[0].(map[string]any)["key"] {
		t.Fatalf("buckets = %v", buckets)
	}
	var total int64
	for _, b := range buckets {
		b := b.(map[string]any)
		total += int64(b["tokens"].(float64))
		if got := at(b["key"].(string)); got != int64(b["tokens"].(float64)) {
			t.Errorf("%s: recap %v, heatmap %d", b["key"], b["tokens"], got)
		}
	}
	if int64(r.num("data", "total")) != total {
		t.Errorf("total %v ≠ Σ buckets %d", r.num("data", "total"), total)
	}
	if r.num("data", "graduated") != 1 {
		t.Fatalf("graduated = %v", r.path("data", "graduated"))
	}
	g := r.path("data", "graduates").([]any)[0].(map[string]any)
	if g["entryId"] != "g-new" || g["displayName"] != "Mon16" || g["isShiny"] != true || g["speciesId"] != 16.0 || g["form"] != nil {
		t.Errorf("graduate = %v", g)
	}

	// offset and scope are echoed; year buckets are months; the old dex entry is not in this year.
	h.send(`{"id":3,"cmd":"getRecap","args":{"scope":"year","offset":-1}}`)
	y := h.expect("getRecap year", wait, isResult(3))
	if y.num("data", "offset") != -1 || y.str("data", "scope") != "year" || len(y.path("data", "buckets").([]any)) != 12 ||
		y.flag("data", "isInProgress") || y.num("data", "graduated") != 0 {
		t.Errorf("year recap = %v", y["data"])
	}
	if g := y.path("data", "graduates"); g == nil || len(g.([]any)) != 0 {
		t.Errorf("an empty strip must be [] not null: %v", g)
	}
	h.stop()
}

// Protocol §7.5: scope and offset are validated like every other argument.
func TestGetRecapRejectsBadArgs(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh state", wait, isFreshState)
	bad := []string{
		`{"id":10,"cmd":"getRecap"}`,
		`{"id":11,"cmd":"getRecap","args":{"offset":0}}`,
		`{"id":12,"cmd":"getRecap","args":{"scope":"week"}}`,
		`{"id":13,"cmd":"getRecap","args":{"scope":"day","offset":0}}`,
		`{"id":14,"cmd":"getRecap","args":{"scope":"Week","offset":0}}`,
		`{"id":15,"cmd":"getRecap","args":{"scope":5,"offset":0}}`,
		`{"id":16,"cmd":"getRecap","args":{"scope":"week","offset":1}}`,
		`{"id":17,"cmd":"getRecap","args":{"scope":"week","offset":-1001}}`,
		`{"id":18,"cmd":"getRecap","args":{"scope":"week","offset":-1.5}}`,
		`{"id":19,"cmd":"getRecap","args":{"scope":"week","offset":"0"}}`,
		`{"id":20,"cmd":"getRecap","args":{"scope":"week","offset":null}}`,
		`{"id":21,"cmd":"getRecap","args":[1]}`,
	}
	for _, line := range bad {
		h.send(line)
	}
	for i := range bad {
		r := h.expect(bad[i], wait, isResult(int64(10+i)))
		if e, _ := r["error"].(map[string]any); r.flag("ok") || e == nil || e["code"] != "badArgs" {
			t.Errorf("%s → %v, want badArgs", bad[i], r)
		}
	}
	// The limits of the range are accepted, and a far offset is a valid, empty period.
	h.send(`{"id":30,"cmd":"getRecap","args":{"scope":"month","offset":-1000}}`)
	h.send(`{"id":31,"cmd":"getRecap","args":{"scope":"year","offset":-1000,"extra":true}}`)
	for _, id := range []int64{30, 31} {
		r := h.expect("far offset", wait, isResult(id))
		if !r.flag("ok") || r.flag("data", "canGoBack") || r.num("data", "total") != 0 || r.path("data", "bestDay") != nil ||
			r.path("data", "previousTotal") != nil {
			t.Errorf("id %d: %v", id, r)
		}
	}
	h.stop()
}

// Protocol §2.3: the cached initial state recomputes the heatmap from the loaded ledger, with no
// scan, the same way it recomputes the streak.
func TestHeatmapFromTheLoadedLedger(t *testing.T) {
	d, now := budgetDaemon(t)
	d.ledger.TokensByDay["2026-10-05"] = 123
	d.ledger.CoveredSince = "2026-09-20"
	st := d.buildState(now)
	hm := st.Usage.Combined.Heatmap
	// Derived: 2026-12-31 is a Thursday; the Monday-first week starts 2026-12-28; 15 weeks earlier is 2026-09-14.
	if hm.Start != "2026-09-14" || hm.First != "2026-09-20" {
		t.Errorf("start %s, first %s (recordedSince = min(ledger 2026-09-20, month series 2026-12-01))", hm.Start, hm.First)
	}
	i := int(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Sub(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if hm.Tokens[i] != 123 {
		t.Errorf("ledger day = %d", hm.Tokens[i])
	}
	if last := hm.Tokens[len(hm.Tokens)-1]; last != 0 { // 2027-01-03, a future day
		t.Errorf("a future cell holds %d", last)
	}
	// A day the live series also knows keeps the larger of the two.
	if got := hm.Tokens[int(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC).Sub(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)).Hours()/24)]; got != 187_291_640 {
		t.Errorf("today's cell = %d", got)
	}
}
