package companion

import (
	"testing"
	"time"
)

func caughtOn(t time.Time) func(*dexEntry) { return func(e *dexEntry) { e.CaughtAt = &t } }
func releasedOn(t time.Time) func(*dexEntry) {
	return func(e *dexEntry) { e.ReleasedAt = &t }
}
func withID(id string) func(*dexEntry) { return func(e *dexEntry) { e.ID = id } }

func ids(g []Graduate) []string {
	out := []string{}
	for _, x := range g {
		out = append(out, x.EntryID)
	}
	return out
}

func utc(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.UTC) }

func graduateWorld(t *testing.T) *Store {
	s := newStore(t, stub(linear3()), 7)
	names := withNames(map[int]map[string]string{3: {"en": "Venusaur"}})
	mk := func(id string, opts ...func(*dexEntry)) dexEntry {
		return entry(1, 3, []int{1, 2, 3}, rarityCommon, append([]func(*dexEntry){withID(id), names}, opts...)...)
	}
	// UsageRecapTests.swift:300-309 testGraduationsInThePeriodOnly (UTC, week of 2026-09-14).
	s.st.Dex = []dexEntry{
		mk("this-week", caughtOn(utc(9, 17, 12))),
		mk("monday", caughtOn(utc(9, 14, 0)), shiny),
		mk("let-go", caughtOn(utc(9, 16, 12)), releasedOn(utc(9, 17, 0))),
		mk("last-week", caughtOn(utc(9, 13, 23))),
	}
	return s
}

// UsageRecapTests.swift:300-309 testGraduationsInThePeriodOnly.
func TestGraduatesInThePeriodOnly(t *testing.T) {
	s := graduateWorld(t)
	count, week := s.Graduates(utc(9, 14, 0), utc(9, 21, 0), 4)
	if count != 2 || len(week) != 2 || week[0].EntryID != "this-week" || week[1].EntryID != "monday" {
		t.Errorf("week graduates = %v (%d): newest first, released out, Sunday 13 is last week", ids(week), count)
	}
	if !week[1].IsShiny || week[0].IsShiny || week[0].SpeciesID != 3 || week[0].DisplayName != "Venusaur" {
		t.Errorf("fields: %+v", week)
	}
	if count, _ := s.Graduates(utc(9, 1, 0), utc(10, 1, 0), 4); count != 3 {
		t.Errorf("month graduated = %d, want 3", count)
	}
	// Derived: the period is half-open, so the instant that ends it belongs to the next one.
	if count, _ := s.Graduates(utc(9, 14, 0), utc(9, 17, 12), 4); count != 1 {
		t.Errorf("[from, to) with a catch at `to` counted %d, want 1", count)
	}
}

// Derived from protocol §7.5: at most `limit` are listed, newest first, while the count stays whole;
// an empty period lists [] and not null.
func TestGraduatesLimitKeepsTheTotal(t *testing.T) {
	s := newStore(t, stub(linear3()), 7)
	for i := 0; i < 6; i++ {
		s.st.Dex = append(s.st.Dex, entry(1, 3, []int{1, 2, 3}, rarityCommon, withID(string(rune('a'+i))), caughtOn(utc(9, 14+i%3, i))))
	}
	count, got := s.Graduates(utc(9, 14, 0), utc(9, 21, 0), 4)
	if count != 6 || len(got) != 4 {
		t.Fatalf("count %d, listed %d", count, len(got))
	}
	// Caught: a=14th 00h, b=15th 01h, c=16th 02h, d=14th 03h, e=15th 04h, f=16th 05h.
	if want := []string{"f", "c", "e", "b"}; ids(got)[0] != want[0] || ids(got)[1] != want[1] || ids(got)[2] != want[2] || ids(got)[3] != want[3] {
		t.Errorf("order = %v, want %v", ids(got), want)
	}
	count, none := s.Graduates(utc(10, 1, 0), utc(11, 1, 0), 4)
	if count != 0 || none == nil || len(none) != 0 {
		t.Errorf("empty period: %d, %#v", count, none)
	}
	// An entry without a catch date (a corrupt save) never graduates in a period.
	s.st.Dex = append(s.st.Dex, dexEntry{ID: "nodate", FinalID: 3})
	if count, _ := s.Graduates(utc(9, 14, 0), utc(9, 21, 0), 4); count != 6 {
		t.Errorf("an undated entry counted: %d", count)
	}
}

// UsageRecapTests.swift:415-437 testTheGraduateStripCarriesTheUnownLetterAndOnlyTheSpriteOfOtherForms.
func TestGraduateNamesCarryTheUnownLetterOnly(t *testing.T) {
	s := newStore(t, formData(), 7)
	s.st.Dex = []dexEntry{
		entry(201, 201, []int{201}, rarityCommon, withID("unown-q"), withForm("q"), caughtOn(utc(9, 17, 0)),
			withNames(map[int]map[string]string{201: {"en": "Unown"}})),
		entry(412, 413, []int{412, 413}, rarityCommon, withID("wormadam-sandy"), withForm("sandy"), caughtOn(utc(9, 16, 0)),
			withNames(map[int]map[string]string{413: {"en": "Wormadam"}})),
	}
	_, got := s.Graduates(utc(9, 14, 0), utc(9, 21, 0), 4)
	if len(got) != 2 || got[0].EntryID != "unown-q" || got[1].EntryID != "wormadam-sandy" {
		t.Fatalf("graduates = %+v", got)
	}
	if deref(got[0].Form) != "q" || got[0].DisplayName != "Unown [Q]" {
		t.Errorf("Unown = %+v, the strip would otherwise draw the default A sprite", got[0])
	}
	if deref(got[1].Form) != "sandy" || got[1].DisplayName != "Wormadam" {
		t.Errorf("Wormadam = %+v: a form name does not fit a quarter-row chip", got[1])
	}
	// Derived: an entry whose names are missing falls back to "#<finalID>".
	s.st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon, caughtOn(utc(9, 17, 0)))}
	if _, g := s.Graduates(utc(9, 14, 0), utc(9, 21, 0), 4); len(g) != 1 || g[0].DisplayName != "#3" {
		t.Errorf("unnamed = %+v", g)
	}
}

// Derived from protocol §5.4: sulkIdleSeconds is whole hours in seconds, floored, 0 unless sulk.
func TestSulkIdleSecondsIsFlooredToWholeHours(t *testing.T) {
	idle := func(d time.Duration) *time.Duration { return &d }
	s := newStore(t, stub(linear3()), 7)
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).SulkIdleSeconds, 0, "egg")
	s.hatch(bg, 1)
	later := fixedNow.Add(10 * time.Second)
	for _, c := range []struct {
		idle time.Duration
		want int
	}{
		{3 * time.Hour, 10800},
		{3*time.Hour + 59*time.Minute + 59*time.Second, 10800},
		{4 * time.Hour, 14400},
		{30*time.Hour + time.Minute, 108000},
		{2*time.Hour + 59*time.Minute, 0}, // not sulking
	} {
		v := s.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, IdleFor: idle(c.idle)})
		eq(t, v.SulkIdleSeconds, c.want, "idle "+c.idle.String())
	}
	// 0 whenever the mood is not sulk, whatever the idle time says.
	tired := s.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 5, IdleFor: idle(5 * time.Hour), LimitWarning: true})
	eq(t, tired.Mood, "tired", "limit warning outranks sulk")
	eq(t, tired.SulkIdleSeconds, 0, "tired")
	eq(t, s.BuildState(MoodInput{Now: later, HasUsageData: false, IdleFor: idle(5 * time.Hour)}).SulkIdleSeconds, 0, "no usage data")
	eq(t, s.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 5}).SulkIdleSeconds, 0, "idle unknown")
	// A rise in today's total seen by Update shortens the idle time (CS:1974-1985), here to under 3 h.
	u := newStore(t, stub(noEvo()), 7)
	u.hatch(bg, 20)
	use(u, 100)
	use(u, 200)
	eq(t, u.BuildState(MoodInput{Now: later, HasUsageData: true, TodayTokens: 200, IdleFor: idle(5 * time.Hour)}).SulkIdleSeconds, 0, "observed rise")
}
