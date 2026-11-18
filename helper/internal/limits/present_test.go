package limits

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1_800_000_000, 0).UTC()

func fp(v float64) *float64 { return &v }

func TestPaceTierBoundaries(t *testing.T) {
	pace := fp(0.5)
	// Swift: Tests/PokeTokenBarTests/PaceTierTests.swift:13 (pace 0.5, crit 95)
	cases := []struct {
		u    float64
		want Tier
	}{
		{0, WayUnder}, {24, WayUnder}, // delta -50, -26
		{25, Under}, {39, Under}, // -25, -11
		{40, OnPace}, {54, OnPace}, // -10, +4
		{55, SlightlyOver}, {64, SlightlyOver}, // +5, +14
		{65, Over}, {79, Over}, // +15, +29
		{80, WayOver}, // +30
		// Swift: PaceTierTests.swift:33 (the tier follows the rounded delta)
		{54.6, SlightlyOver}, {54.4, OnPace},
		// Half away from zero on both signs: +14.5 -> 15, -10.5 -> -11, -25.5 -> -26.
		{64.5, Over}, {39.5, Under}, {24.5, WayUnder},
	}
	for _, c := range cases {
		got, ok := PaceTier(c.u, pace, 95)
		if !ok || got != c.want {
			t.Errorf("u=%v: got %v ok=%v want %v", c.u, got, ok, c.want)
		}
	}
}

func TestRoundedDelta(t *testing.T) {
	cases := []struct {
		u, pace float64
		want    int
	}{
		// Swift: PaceTierTests.swift:35
		{54.6, 0.5, 5}, {38.5, 0.5, -12},
		// Negative halves round away from zero (JS Math.round(-2.5) would give -2).
		{47.5, 0.5, -3}, {52.5, 0.5, 3}, {50, 0.5, 0},
	}
	for _, c := range cases {
		if got := RoundedDelta(c.u, c.pace); got != c.want {
			t.Errorf("RoundedDelta(%v,%v)=%d want %d", c.u, c.pace, got, c.want)
		}
	}
}

func TestPaceTierCritAndHold(t *testing.T) {
	// Swift: PaceTierTests.swift:41 (crit overrides pace)
	if got, _ := PaceTier(95, fp(0.99), 95); got != WayOver {
		t.Errorf("95 @ 0.99: %v", got)
	}
	if got, _ := PaceTier(94, fp(0.99), 95); got != OnPace {
		t.Errorf("94 @ 0.99: %v", got)
	}
	// Swift: PaceTierTests.swift:51 (no pace or pace < 0.1 -> no tier; crit is held too)
	for _, p := range []*float64{nil, fp(0.02), fp(minimumPace - 0.001)} {
		if _, ok := PaceTier(99, p, 95); ok {
			t.Errorf("pace %v: want no tier", p)
		}
	}
	if got, ok := PaceTier(10, fp(minimumPace), 95); !ok || got != OnPace {
		t.Errorf("pace == 0.1: %v %v", got, ok)
	}
}

func TestGaugeTierFallback(t *testing.T) {
	cases := []struct {
		u    float64
		pace *float64
		want Tier
	}{
		{50, nil, OnPace}, {80, nil, Over}, {94.9, nil, Over}, {95, nil, WayOver},
		{99, fp(0.02), WayOver},        // held early in the window: absolute rule
		{50, fp(0.5), OnPace},          // pace tier wins
		{85, fp(0.9), OnPace},          // pace says fine even though >= warn
		{30, fp(0.5), Under},           // pace tier wins over absolute "onPace"
		{79, fp(0.2), WayOver},         // delta +59
		{96, fp(0.99), WayOver},        // crit
		{80, fp(0.0999), Over},         // just under the hold -> absolute
		{80, fp(minimumPace), WayOver}, // at the hold boundary the pace tier applies (+70)
	}
	for _, c := range cases {
		if got := GaugeTier(c.u, c.pace, 80, 95); got != c.want {
			t.Errorf("u=%v pace=%v: got %v want %v", c.u, c.pace, got, c.want)
		}
	}
}

func TestPaceFraction(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/LimitPaceTests.swift:56
	cases := []struct {
		name  string
		reset time.Duration // from now
		span  time.Duration
		want  float64
		ok    bool
	}{
		{"just started", FiveHour, FiveHour, 0, true},
		{"half", FiveHour / 2, FiveHour, 0.5, true},
		{"three quarters", FiveHour / 4, FiveHour, 0.75, true},
		{"ending", 0, FiveHour, 1, true},
		// Swift: LimitPaceTests.swift:70
		{"reset in the past", -time.Second, FiveHour, 0, false},
		{"reset beyond the span", FiveHour + time.Second, FiveHour, 0, false},
		{"zero span", FiveHour / 2, 0, 0, false},
		{"negative span", FiveHour / 2, -FiveHour, 0, false},
	}
	for _, c := range cases {
		got, ok := PaceFraction(testNow.Add(c.reset), c.span, testNow)
		if ok != c.ok || (ok && (got-c.want > 1e-4 || c.want-got > 1e-4)) {
			t.Errorf("%s: got %v ok=%v", c.name, got, ok)
		}
	}
}

func TestDisplayPercentAndStale(t *testing.T) {
	if DisplayPercent(66, false) != 66 || DisplayPercent(66, true) != 34 || DisplayPercent(120, true) != 0 {
		t.Fatal("display percent")
	}
	cases := []struct {
		name      string
		updatedAt time.Time
		want      bool
	}{
		{"never fetched", time.Time{}, false},
		{"fresh", testNow.Add(-time.Minute), false},
		{"exactly 15 min", testNow.Add(-StaleAfter), false},
		{"just over", testNow.Add(-StaleAfter - time.Second), true},
	}
	for _, c := range cases {
		if got := IsStale(c.updatedAt, testNow); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestIsLimitWarning(t *testing.T) {
	w := func(us ...float64) (out []WindowInfo) {
		for _, u := range us {
			out = append(out, WindowInfo{Utilization: u})
		}
		return
	}
	cases := []struct {
		name     string
		windows  []WindowInfo
		crit     float64
		forecast bool
		want     bool
	}{
		{"none", nil, 95, false, false},
		{"below crit", w(10, 94), 95, false, false},
		{"weekly at crit while session is calm", w(10, 95), 95, false, true},
		// Swift: UsageStoreTests.swift:915 (u = 100, crit = 101 -> warning from the forecast alone)
		{"forecast only", w(100), 101, true, true},
		{"forecast false and below crit", w(100), 101, false, false},
	}
	for _, c := range cases {
		if got := IsLimitWarning(c.windows, c.crit, c.forecast); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestTierJSON(t *testing.T) {
	b, err := json.Marshal(struct{ T Tier }{SlightlyOver})
	if err != nil || string(b) != `{"T":"slightlyOver"}` {
		t.Fatalf("%s %v", b, err)
	}
}

func iso(d time.Duration) string { return testNow.Add(d).Format("2006-01-02T15:04:05.000000-07:00") }

func mustStatus(t *testing.T, body string) *Status {
	t.Helper()
	s, err := ParseStatus([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func keys(ws []WindowInfo) string {
	var k []string
	for _, w := range ws {
		k = append(k, w.Key)
	}
	return strings.Join(k, ",")
}

func TestWindowsFullStatus(t *testing.T) {
	s := mustStatus(t, `{
 "five_hour":{"utilization":40,"resets_at":"`+iso(150*time.Minute)+`"},
 "seven_day":{"utilization":66,"resets_at":"`+iso(84*time.Hour)+`"},
 "seven_day_opus":null,
 "seven_day_sonnet":{"utilization":3,"resets_at":null},
 "limits":[
  {"kind":"session","percent":40,"resets_at":"`+iso(150*time.Minute)+`"},
  {"kind":"weekly_all","percent":66,"resets_at":"`+iso(84*time.Hour)+`"},
  {"kind":"weekly_scoped","percent":10,"resets_at":"`+iso(84*time.Hour)+`","scope":{"model":{"display_name":"Opus"}}},
  {"kind":"weekly_scoped","percent":null,"scope":{"model":{"display_name":"Skipped"}}},
  {"kind":"mystery","percent":5,"resets_at":"`+iso(time.Hour)+`"},
  {"percent":7}
 ]}`)
	ws := s.Windows(testNow, 80, 95)
	want := "claude.fiveHour,claude.sevenDay,claude.sevenDaySonnet," +
		"claude.scoped.weekly_scoped.Opus.2,claude.scoped.mystery.any.4,claude.scoped.unknown.any.5"
	if keys(ws) != want {
		t.Fatalf("keys:\n got %s\nwant %s", keys(ws), want)
	}
	five := ws[0]
	if five.Kind != KindFiveHour || five.Pace == nil || *five.Pace < 0.499 || *five.Pace > 0.501 {
		t.Fatalf("fiveHour pace: %+v", five)
	}
	if *five.PaceDelta != -10 || *five.PaceTier != OnPace || five.GaugeTier != OnPace {
		t.Fatalf("fiveHour tiers: delta=%d tier=%v gauge=%v", *five.PaceDelta, *five.PaceTier, five.GaugeTier)
	}
	sonnet := ws[2]
	if sonnet.Pace != nil || sonnet.PaceDelta != nil || sonnet.PaceTier != nil || sonnet.ResetsAt != nil ||
		sonnet.NotStarted || sonnet.GaugeTier != OnPace {
		t.Fatalf("sonnet without reset: %+v", sonnet)
	}
	opus := ws[3]
	if opus.Kind != KindScoped || *opus.ScopeKind != "weekly_scoped" || *opus.Label != "Opus" || opus.Pace == nil {
		t.Fatalf("scoped opus: %+v", opus)
	}
	if mys := ws[4]; mys.Pace != nil || *mys.ScopeKind != "mystery" || mys.Label != nil {
		t.Fatalf("unknown scoped kind must have no pace: %+v", mys)
	}
	if bare := ws[5]; bare.ScopeKind != nil {
		t.Fatalf("missing kind: %+v", bare)
	}
	if s.HeadlineKey() != "claude.fiveHour" {
		t.Fatal("headline")
	}
	// JSON shape: whole-second UTC timestamp and protocol names.
	b, _ := json.Marshal(five)
	for _, frag := range []string{`"key":"claude.fiveHour"`, `"kind":"fiveHour"`, `"scopeKind":null`,
		`"resetsAt":"2027-01-15T10:30:00Z"`, `"paceDelta":-10`, `"paceTier":"onPace"`, `"gaugeTier":"onPace"`} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("json missing %s in %s", frag, b)
		}
	}
}

// Ultra sends five_hour: null; the session entry of limits[] is then a duplicate of nothing and is
// still excluded because seven_day is present. Swift: Sources/PokeTokenBar/Core/Models.swift:329.
func TestWindowsUltraAndLegacyFallback(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:1880 (headline: session, then weekly, else nil)
	ultra := mustStatus(t, `{"five_hour":null,"seven_day":{"utilization":66,"resets_at":"`+iso(84*time.Hour)+`"},
	 "limits":[{"kind":"session","percent":1},{"kind":"weekly_all","percent":66},{"kind":"weekly_scoped","percent":9,"scope":{"model":{"display_name":"Fable"}}}]}`)
	if got := keys(ultra.Windows(testNow, 80, 95)); got != "claude.sevenDay,claude.scoped.weekly_scoped.Fable.2" {
		t.Fatalf("ultra: %s", got)
	}
	if ultra.HeadlineKey() != "claude.sevenDay" {
		t.Fatal("headline should fall back to weekly")
	}
	// Swift: UsageStoreTests.swift:1849 (weekly 66 %, reset in 3.5 d -> pace 0.5)
	if p := ultra.Windows(testNow, 80, 95)[0].Pace; p == nil || *p < 0.499 || *p > 0.501 {
		t.Fatalf("weekly pace: %v", p)
	}
	// Both legacy fields null: limits[] is the only source and is shown whole.
	newOnly := mustStatus(t, `{"five_hour":null,"seven_day":null,
	 "limits":[{"kind":"session","percent":12},{"kind":"weekly_all","percent":3}]}`)
	if got := keys(newOnly.Windows(testNow, 80, 95)); got != "claude.scoped.session.any.0,claude.scoped.weekly_all.any.1" {
		t.Fatalf("limits-only: %s", got)
	}
	if sess := newOnly.Windows(testNow, 80, 95)[0]; sess.Kind != KindScoped || sess.NotStarted {
		t.Fatalf("12 %% without a reset is not 'not started': %+v", sess)
	}
	empty := mustStatus(t, `{"five_hour":null,"seven_day":null}`)
	if empty.HeadlineKey() != "" || len(empty.Windows(testNow, 80, 95)) != 0 {
		t.Fatal("empty status")
	}
	// A legacy window with a null utilization is omitted and does not count as headline.
	nullU := mustStatus(t, `{"five_hour":{"utilization":null,"resets_at":null},"seven_day":{"utilization":5,"resets_at":null}}`)
	if keys(nullU.Windows(testNow, 80, 95)) != "claude.sevenDay" || nullU.HeadlineKey() != "claude.sevenDay" {
		t.Fatal("null utilization must be omitted")
	}
}

func TestWindowsNotStarted(t *testing.T) {
	// Swift fixture: Tests/PokeTokenBarTests/SessionKeyLimitsTests.swift:21 (idle org)
	idle := mustStatus(t, `{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":0,"resets_at":null},
	 "seven_day_opus":null,"seven_day_sonnet":null,
	 "limits":[{"kind":"session","group":"session","percent":0,"severity":"normal","resets_at":null}]}`)
	ws := idle.Windows(testNow, 80, 95)
	if len(ws) != 2 || !ws[0].NotStarted || !ws[1].NotStarted {
		t.Fatalf("%+v", ws)
	}
	// 0 % with a reset is a running window, not "not started".
	running := mustStatus(t, `{"five_hour":{"utilization":0,"resets_at":"`+iso(time.Hour)+`"}}`)
	if running.Windows(testNow, 80, 95)[0].NotStarted {
		t.Fatal("running window flagged not started")
	}
	// An unparseable reset counts as no reset.
	bad := mustStatus(t, `{"five_hour":{"utilization":0,"resets_at":"soon"}}`)
	if w := bad.Windows(testNow, 80, 95)[0]; !w.NotStarted || w.ResetsAt != nil {
		t.Fatalf("%+v", w)
	}
}

func TestWindowsStaleResetHasNoPace(t *testing.T) {
	// An idle 5 h window can keep an old resets_at: no marker rather than a clamped lie.
	s := mustStatus(t, `{"five_hour":{"utilization":20,"resets_at":"`+iso(-time.Hour)+`"}}`)
	w := s.Windows(testNow, 80, 95)[0]
	if w.Pace != nil || w.PaceDelta != nil || w.PaceTier != nil || w.ResetsAt == nil {
		t.Fatalf("%+v", w)
	}
}

func TestWindowsGaugeTierEarlyWindow(t *testing.T) {
	// Pace 0.02 (< 0.1): no paceTier, paceDelta present, gauge falls back to the absolute rule.
	s := mustStatus(t, `{"five_hour":{"utilization":96,"resets_at":"`+iso(FiveHour-6*time.Minute)+`"}}`)
	w := s.Windows(testNow, 80, 95)[0]
	if w.PaceTier != nil || w.PaceDelta == nil || w.GaugeTier != WayOver {
		t.Fatalf("%+v", w)
	}
}

func TestPlan(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:306 (planDisplay, no profile name)
	cases := []struct{ sub, tier, want string }{
		{"max", "default_claude_max_20x", "Ultra"},
		{"max", "default_claude_max_5x", "Max"},
		{"max", "default_claude_max", "Max"},
		{"max", "", "Max"},
		{"max", "default_claude_max_10x", "Max 10x"},
		{"pro", "default_claude_pro", "Pro"},
		{"pro", "default_claude_pro_5x", "Pro 5x"},
		{"free", "", "Free"},
		{"", "default_claude_max_20x", ""},
		{"max", "x_x_ax", "Max"}, // "x" and "ax" are not <digits>x
	}
	for _, c := range cases {
		s := &Status{SubscriptionType: c.sub, RateLimitTier: c.tier}
		if got := s.Plan(); got != c.want {
			t.Errorf("%q/%q: got %q want %q", c.sub, c.tier, got, c.want)
		}
	}
}

func TestParseStatusTolerance(t *testing.T) {
	// Spike B: the live response had ~34 top-level keys, most unknown.
	s := mustStatus(t, `{"five_hour":null,"seven_day":{"utilization":16,"resets_at":"2026-08-21T17:20:00.186770+00:00"},
	 "seven_day_oauth_apps":null,"iguana_necktie":{"a":[1,2]},"extra_usage":{"is_enabled":false},
	 "limits":[{"kind":"weekly_scoped","group":"weekly","percent":4.5,"severity":"normal","is_active":true,
	   "resets_at":"2026-08-25T00:00:00Z","future_field":1,"scope":{"model":{"display_name":"Sonnet","id":"x"},"other":1}}]}`)
	if s.FiveHour != nil || *s.SevenDay.Utilization != 16 {
		t.Fatalf("%+v", s)
	}
	e := s.Limits[0]
	if e.Kind != "weekly_scoped" || e.Group != "weekly" || *e.Percent != 4.5 || e.Severity != "normal" ||
		!*e.IsActive || e.ModelName() != "Sonnet" || e.Span() != SevenDay {
		t.Fatalf("%+v", e)
	}
	if _, ok := s.SevenDay.ResetDate(); !ok {
		t.Fatal("microsecond reset must parse")
	}
	// Offsets, milliseconds, no fraction.
	for _, ts := range []string{"2026-08-21T17:20:00.303Z", "2026-08-21T19:20:00+02:00", "2026-08-21T17:20:00Z"} {
		if _, ok := (&Window{ResetsAt: &ts}).ResetDate(); !ok {
			t.Errorf("%s did not parse", ts)
		}
	}
	if _, err := ParseStatus([]byte(`[]`)); CodeOf(err) != CodeDecode {
		t.Fatal("array body must be a decode error")
	}
	if _, err := ParseStatus([]byte(`{"five_hour":{"utilization":"x"}}`)); CodeOf(err) != CodeDecode {
		t.Fatal("wrong type must be a decode error")
	}
}
