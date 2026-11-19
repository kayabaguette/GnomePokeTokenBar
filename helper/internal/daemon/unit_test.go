package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/pokeapi"
)

// Protocol §9.1/§9.2 normalization.
func TestSettingsNormalization(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 29: 0, 31: 60, 90: 60, 91: 120, 200: 120, 211: 300, 3600: 900} {
		if got := (Settings{RefreshInterval: in, WeekStart: 1}).normalized().RefreshInterval; got != want {
			t.Errorf("interval %d → %d, want %d", in, got, want)
		}
	}
	for _, c := range []struct{ warn, crit, wantWarn, wantCrit int }{
		{80, 95, 80, 95}, {95, 90, 85, 90}, {40, 120, 50, 100}, {95, 80, 75, 80}, {60, 60, 60, 80}, {90, 92, 87, 92},
	} {
		s := Settings{WarnThreshold: c.warn, CritThreshold: c.crit, WeekStart: 1}.normalized()
		if s.WarnThreshold != c.wantWarn || s.CritThreshold != c.wantCrit {
			t.Errorf("thresholds %d/%d → %d/%d", c.warn, c.crit, s.WarnThreshold, s.CritThreshold)
		}
	}
	for in, want := range map[float64]float64{0: 0.1, 0.05: 0.1, 1.5: 1.5, 7: 2} {
		if got := (Settings{GrowthDifficulty: in, WeekStart: 1}).normalized().GrowthDifficulty; got != want {
			t.Errorf("difficulty %g → %g", in, got)
		}
	}
	for in, want := range map[string]string{"fr_FR.UTF-8": "fr-fr", "C": "en", "POSIX": "en", "": "en", "pt_BR@latin": "pt-br", " JA ": "ja"} {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("language %q → %q, want %q", in, got, want)
		}
	}
	if (Settings{WeekStart: 7}).weekday() != time.Sunday || (Settings{WeekStart: 1}).weekday() != time.Monday ||
		(Settings{WeekStart: 6}).weekday() != time.Saturday {
		t.Error("ISO weekday mapping")
	}
	s, err := parseSettings(json.RawMessage(`{"refreshInterval":90,"warnThreshold":90,"critThreshold":90,"growthDifficulty":0.01,"weekStart":7,"language":"de_DE","alwaysFacesBack":true,"future":1}`), 0.7)
	if err != nil || s.RefreshInterval != 60 || s.WarnThreshold != 85 || s.GrowthDifficulty != 0.1 || s.Language != "de-de" || !s.AlwaysFacesBack || s.ShopDifficulty != 0.7 {
		t.Fatalf("parseSettings = %+v, %v", s, err)
	}
	for _, bad := range []string{`{}`, `[]`, `{"refreshInterval":60.5,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":false}`,
		`{"refreshInterval":60,"warnThreshold":80,"critThreshold":95,"growthDifficulty":"1","weekStart":1,"language":"en","alwaysFacesBack":false}`,
		`{"refreshInterval":60,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":0,"language":"en","alwaysFacesBack":false}`,
		`{"refreshInterval":60,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":null,"alwaysFacesBack":false}`,
		`{"refreshInterval":60,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":1}`} {
		if _, err := parseSettings(json.RawMessage(bad), 1); err == nil || !isBadArgs(err) {
			t.Errorf("%s accepted (%v)", bad, err)
		}
	}
}

// Protocol §11.1: only a positive integer id gets a reply.
func TestParseRequest(t *testing.T) {
	for line, want := range map[string]bool{
		`{"id":1,"cmd":"ping"}`: true, `{"id":9007199254740991}`: true, `{"id":9007199254740992}`: false,
		`{"id":0}`: false, `{"id":-1}`: false, `{"id":1.0}`: false, `{"id":1e3}`: false, `{"id":"1"}`: false,
		`{"id":null}`: false, `{"cmd":"ping"}`: false, `"str"`: false, `null`: false, `{`: false,
	} {
		if _, ok, _ := parseRequest([]byte(line)); ok != want {
			t.Errorf("%s: ok=%t, want %t", line, ok, want)
		}
	}
	r, _, _ := parseRequest([]byte(`{"id":5,"cmd":7,"args":{"a":1}}`))
	if r.ID != 5 || r.Cmd != "" || string(r.Args) != `{"a":1}` {
		t.Fatalf("request = %+v", r)
	}
}

// Protocol §2.4: states coalesce (latest wins); a result first flushes the pending state.
func TestOutputCoalescingAndOrdering(t *testing.T) {
	var buf syncBuffer
	o := newOutput(&buf, time.Hour, t.Logf)
	o.setState([]byte(`{"type":"state","n":1}`))
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"n":1`) })
	o.setState([]byte(`{"type":"state","n":2}`))
	o.setState([]byte(`{"type":"state","n":3}`))
	o.send(resultMsg{Type: "result", ID: 9, OK: true, Data: emptyObject})
	o.setState([]byte(`{"type":"state","n":4}`))
	o.close(time.Now().Add(time.Second))
	want := `{"type":"state","n":1}` + "\n" + `{"type":"state","n":3}` + "\n" + `{"type":"result","id":9,"ok":true,"data":{}}` + "\n" +
		`{"type":"state","n":4}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestOutputSurvivesBrokenStdout(t *testing.T) {
	o := newOutput(failWriter{}, time.Millisecond, t.Logf)
	o.send(helloMsg{Type: "hello"})
	o.setState([]byte(`{"type":"state"}`))
	o.send(resultMsg{Type: "result", ID: 1, OK: true, Data: emptyObject})
	o.close(time.Now().Add(time.Second))
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestDirs(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	s, c, err := Dirs(env(map[string]string{"PTB_STATE_DIR": "/x/st", "XDG_STATE_HOME": "/ignored"}))
	if err != nil || s != "/x/st" || c != "/x/st/cache" {
		t.Errorf("PTB_STATE_DIR: %s %s %v", s, c, err)
	}
	s, c, _ = Dirs(env(map[string]string{"HOME": "/h", "XDG_STATE_HOME": "/xs", "XDG_CACHE_HOME": "relative"}))
	if s != "/xs/poketokenbar" || c != "/h/.cache/poketokenbar" {
		t.Errorf("XDG: %s %s", s, c)
	}
	if _, _, err := Dirs(env(map[string]string{})); err == nil {
		t.Error("no HOME accepted")
	}
}

// The memory view records misses, suppresses keys that just failed, and serves warmed data.
func TestDataCacheViewAndWarm(t *testing.T) {
	poke := newFakePoke(16)
	now := time.Now()
	c := newDataCache(poke, func() time.Time { return now })
	v := c.view()
	ctx := context.Background()
	if _, err := v.Line(ctx, 16); err != errNotLoaded {
		t.Fatal("line should miss")
	}
	_, _ = v.Names(ctx, "type", "normal")
	_, _ = v.BaseIndex(ctx)
	_, _ = v.Species(ctx, 99)
	wants := c.takeWants()
	if len(wants) != 4 || len(c.takeWants()) != 0 {
		t.Fatalf("wants = %v", wants)
	}
	got, failed := c.warm(ctx, wants)
	if got != 3 || failed != 0 { // the species want is dropped once the base index loads
		t.Fatalf("warm got=%d failed=%d", got, failed)
	}
	if l, err := v.Line(ctx, 16); err != nil || l.BaseID != 16 {
		t.Fatalf("line after warm: %v", err)
	}
	if m, err := v.Names(ctx, "type", "normal"); err != nil || m["en"] != "Normal" {
		t.Fatalf("names after warm: %v %v", m, err)
	}
	poke.offline.Store(true)
	_, _ = v.Details(ctx, 16)
	if got, failed := c.warm(ctx, c.takeWants()); got != 0 || failed != 1 {
		t.Fatalf("offline warm got=%d failed=%d", got, failed)
	}
	_, _ = v.Details(ctx, 16)
	if w := c.takeWants(); len(w) != 0 {
		t.Fatalf("a key that just failed is wanted again: %v", w)
	}
	c.clearFailures()
	_, _ = v.Details(ctx, 16)
	if w := c.takeWants(); len(w) != 1 {
		t.Fatalf("networkUp must re-arm failed keys: %v", w)
	}
}

func TestReplayRng(t *testing.T) {
	src := &countingRng{}
	r := &replayRng{src: src}
	a, b := r.Next(), r.Next()
	r.requeue()
	if r.Next() != a || r.Next() != b || r.Next() != 3 {
		t.Fatal("requeued draws must come back first, in order")
	}
	r.commit()
	if r.Next() != 4 {
		t.Fatal("commit must not replay")
	}
}

type countingRng struct{ n uint64 }

func (c *countingRng) Next() uint64 { c.n++; return c.n }

// The adapter converts PokéAPI data and keeps every black-2-white-2 learn method (protocol §7.2).
func TestPokeSourceAdapter(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pokemon-species/16", "/pokemon-species/17":
			id := strings.TrimPrefix(r.URL.Path, "/pokemon-species/")
			evolves := `null`
			if id == "17" {
				evolves = `{"name":"pidgey","url":"https://pokeapi.co/api/v2/pokemon-species/16/"}`
			}
			fmt.Fprintf(w, `{"capture_rate":255,"is_legendary":false,"is_mythical":false,"gender_rate":4,
				"names":[{"name":"Mon%s","language":{"name":"en"}},{"name":"Mon%s-fr","language":{"name":"fr"}}],
				"evolution_chain":{"url":"https://pokeapi.co/api/v2/evolution-chain/7/"},"evolves_from_species":%s}`, id, id, evolves)
		case "/pokemon/16":
			fmt.Fprint(w, `{"name":"pidgey","height":3,"weight":18,"types":[{"slot":2,"type":{"name":"flying"}},{"slot":1,"type":{"name":"normal"}}],
				"abilities":[{"is_hidden":true,"slot":3,"ability":{"name":"big-pecks"}},{"is_hidden":false,"slot":1,"ability":{"name":"keen-eye"}}],
				"stats":[{"base_stat":40,"stat":{"name":"hp"}}],
				"moves":[{"move":{"name":"gust"},"version_group_details":[{"level_learned_at":9,"move_learn_method":{"name":"level-up"},"version_group":{"name":"black-2-white-2"}},
				{"level_learned_at":0,"move_learn_method":{"name":"machine"},"version_group":{"name":"black-2-white-2"}}]},
				{"move":{"name":"fly"},"version_group_details":[{"level_learned_at":0,"move_learn_method":{"name":"machine"},"version_group":{"name":"black-2-white-2"}}]},
				{"move":{"name":"old"},"version_group_details":[{"level_learned_at":5,"move_learn_method":{"name":"level-up"},"version_group":{"name":"red-blue"}}]}]}`)
		case "/graphql":
			fmt.Fprint(w, `{"data":{"pokemonspecies":[{"id":16,"capture_rate":255},{"id":19,"capture_rate":255}]}}`)
		case "/type/normal/":
			fmt.Fprint(w, `{"names":[{"name":"Normal","language":{"name":"en"}},{"name":"ノーマル","language":{"name":"ja-Hrkt"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	// The chain URL must be https://pokeapi.co (SSRF guard), so route that host to the test server.
	tr := &http.Transport{}
	hc := &http.Client{Transport: rewriteHost{to: srv.URL, base: tr}}
	c := pokeapi.New(pokeapi.Options{CacheDir: t.TempDir(), RESTBase: srv.URL, GraphQLURL: srv.URL + "/graphql", HTTPClient: hc})
	defer c.Close()
	chainSrv := http.NewServeMux()
	chainSrv.HandleFunc("/api/v2/evolution-chain/7/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"chain":{"species":{"name":"pidgey","url":"https://pokeapi.co/api/v2/pokemon-species/16/"},
			"evolves_to":[{"species":{"name":"pidgeotto","url":"https://pokeapi.co/api/v2/pokemon-species/17/"},"evolves_to":[]}]}}`)
	})
	srv.Config.Handler = combine(chainSrv, srv.Config.Handler)
	p := PokeSource{C: c}
	ctx := context.Background()
	l, err := p.Line(ctx, 16)
	if err != nil || l.BaseID != 16 || l.CaptureRate != 255 || len(l.Tree.Children) != 1 || l.Tree.Children[0].ID != 17 ||
		l.Names[17]["fr"] != "Mon17-fr" {
		t.Fatalf("line = %+v, %v", l, err)
	}
	d, err := p.Details(ctx, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Moves) != 2 || d.Types[0] != "normal" || d.GenderRate != 4 || d.Abilities[0].Name != "keen-eye" {
		t.Fatalf("details = %+v", d)
	}
	methods := map[string]int{}
	for _, m := range d.Moves {
		for _, lm := range m.Methods {
			methods[lm.Method]++
		}
	}
	if methods["machine"] != 2 || methods["level-up"] != 1 {
		t.Fatalf("learn methods = %v", methods)
	}
	idx, err := p.BaseIndex(ctx)
	if err != nil || len(idx) != 2 || idx[1].ID != 19 || idx[0].CaptureRate != 255 {
		t.Fatalf("base index = %v, %v", idx, err)
	}
	names, err := p.Names(ctx, "type", "normal")
	if err != nil || names["ja-hrkt"] != "ノーマル" {
		t.Fatalf("names = %v, %v", names, err)
	}
	sp, err := p.Species(ctx, 17)
	if err != nil || sp.EvolvesFrom != 16 || sp.Names["en"] != "Mon17" {
		t.Fatalf("species = %+v, %v", sp, err)
	}
}

type rewriteHost struct {
	to   string
	base http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "pokeapi.co" {
		u := *req.URL
		target, _ := http.NewRequest(req.Method, r.to+u.Path, nil)
		target = target.WithContext(req.Context())
		return r.base.RoundTrip(target)
	}
	return r.base.RoundTrip(req)
}

func combine(a *http.ServeMux, b http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := a.Handler(r); pattern != "" {
			a.ServeHTTP(w, r)
			return
		}
		b.ServeHTTP(w, r)
	})
}

// A running 429 backoff survives a restart; persisted limits rebuild the same status.
func TestLimitsPersistRestore(t *testing.T) {
	now := time.Now()
	st, _ := limits.ParseStatus([]byte(`{"five_hour":{"utilization":12,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":null}`))
	st.SubscriptionType = "pro"
	var l providerLimits
	l.applyResult(st, nil, now)
	l.applyResult(nil, &limits.Error{Code: limits.CodeRateLimited, HTTPStatus: 429, RetryAfter: 10 * time.Minute}, now)
	if l.status == nil || l.authState() != "error" || !l.backoff.Active(now) {
		t.Fatal("a failure must keep the status and start the backoff")
	}
	b, _ := json.Marshal(l.persist())
	var p persistedLimits
	_ = json.Unmarshal(b, &p)
	var r providerLimits
	r.restore(p, now.Add(time.Minute))
	if r.status == nil || r.status.Plan() != "Pro" || *r.status.FiveHour.Utilization != 12 || r.errCode != limits.CodeRateLimited ||
		!r.backoff.Active(now.Add(5*time.Minute)) || r.backoff.Active(now.Add(11*time.Minute)) {
		t.Fatalf("restored = %+v", r)
	}
	var e providerLimits
	e.applyResult(nil, &limits.Error{Code: limits.CodeTokenExpired}, now)
	if e.authState() != "expired" || e.status != nil {
		t.Fatal("expired token")
	}
	e.applyResult(nil, fmt.Errorf("plain"), now)
	if e.errCode != limits.CodeNetwork {
		t.Fatal("a non-limits error maps to network")
	}
}

// authExpired is emitted on the transition only, and not again after a restart.
func TestAuthExpiredTransition(t *testing.T) {
	fl := newFakeLimits(t, 10, 10)
	h := newHarness(t, "")
	h.cfg.Limits = []LimitsSpec{fl.spec()}
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh", wait, isFreshState)
	fl.mu.Lock()
	fl.status = 401
	fl.mu.Unlock()
	h.send(`{"id":2,"cmd":"refresh","args":{"manual":true}}`)
	ev := h.expect("authExpired", wait, isEvent("authExpired"))
	if ev.str("errorCode") != "unauthorized" || ev.str("providerId") != "claude_code" {
		t.Fatalf("event = %v", ev)
	}
	st := h.lastState()
	if st.str("limits", "providers", "claude_code", "authState") != "expired" || st.num("limits", "providers", "claude_code", "httpStatus") != 401 ||
		!st.flag("limits", "providers", "claude_code", "available") {
		t.Fatalf("limits = %v", st.path("limits", "providers", "claude_code"))
	}
	h.expect("reply", wait, isResult(2))
	h.send(`{"id":3,"cmd":"refresh","args":{"manual":true}}`)
	h.quiet(300*time.Millisecond, "repeated authExpired", isEvent("authExpired"))
	h.stop()
}

// The helper process lowers every thread to nice 10.
func TestLowerPriority(t *testing.T) {
	if os.Getenv("PTB_NICE_CHILD") == "1" {
		if err := LowerPriority(); err != nil {
			fmt.Println("error", err)
		}
		ents, _ := os.ReadDir("/proc/self/task")
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join("/proc/self/task", e.Name(), "stat"))
			f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+2:]))
			fmt.Println("nice", f[16])
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLowerPriority$")
	cmd.Env = append(os.Environ(), "PTB_NICE_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	lines := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "nice ") {
			lines++
			if l != "nice 10" {
				t.Fatalf("a thread is not at nice 10:\n%s", out)
			}
		}
	}
	if lines < 2 {
		t.Fatalf("expected several threads:\n%s", out)
	}
}
