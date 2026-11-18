package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/sprites"
	"poketokenbar/helper/internal/usage"
)

// ---------------------------------------------------------------- fake usage provider

type fakeUsage struct {
	mu      sync.Mutex
	id      string
	today   int64         // tokens today (all in cache read, one entry)
	history map[int]int64 // days ago → tokens
	err     error
	gate    chan struct{} // when set, each scan waits for one value
	scans   int
	flushes int
}

func newFakeUsage(today int64) *fakeUsage {
	return &fakeUsage{id: "claude_code", today: today, history: map[int]int64{}}
}

func (f *fakeUsage) ID() string          { return f.id }
func (f *fakeUsage) DisplayName() string { return "Claude Code" }
func (f *fakeUsage) ShortName() string   { return "Claude" }
func (f *fakeUsage) ReportsCost() bool   { return true }

func (f *fakeUsage) Scan(now time.Time, ws time.Weekday) (usage.Enrichment, error) {
	f.mu.Lock()
	f.scans++
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return usage.EnrichmentFromEntries(f.entries(now), now, ws), f.err
}

func (f *fakeUsage) entries(now time.Time) []usage.Entry {
	var out []usage.Entry
	add := func(at time.Time, tokens int64) {
		if tokens <= 0 {
			return
		}
		out = append(out, usage.Entry{ID: fmt.Sprintf("e%d", len(out)), Date: at, LocalDay: usage.DayKey(at),
			Model: "claude-sonnet-5", Input: tokens / 100, Output: tokens / 100, CacheWrite: tokens / 100,
			CacheRead: tokens - 3*(tokens/100)})
	}
	add(now, f.today)
	y, m, d := now.Date()
	for ago, tokens := range f.history {
		add(time.Date(y, m, d-ago, 12, 0, 0, 0, now.Location()), tokens)
	}
	return out
}

func (f *fakeUsage) Flush() error {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
	return nil
}

func (f *fakeUsage) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *fakeUsage) set(today int64) {
	f.mu.Lock()
	f.today = today
	f.mu.Unlock()
}

func (f *fakeUsage) flushCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushes
}

func (f *fakeUsage) scanCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scans
}

// ---------------------------------------------------------------- fake limits endpoint

type fakeLimits struct {
	srv      *httptest.Server
	mu       sync.Mutex
	fiveHour float64
	sevenDay float64
	status   int
	calls    int
	cred     string
}

func newFakeLimits(t *testing.T, fiveHour, sevenDay float64) *fakeLimits {
	f := &fakeLimits{fiveHour: fiveHour, sevenDay: sevenDay, status: 200}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if f.status != 200 {
			if f.status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "120")
			}
			w.WriteHeader(f.status)
			return
		}
		now := time.Now().UTC()
		fmt.Fprintf(w, `{"five_hour":{"utilization":%g,"resets_at":%q},"seven_day":{"utilization":%g,"resets_at":%q},"seven_day_opus":null,"limits":[],"extra_key":1}`,
			f.fiveHour, now.Add(2*time.Hour).Format(time.RFC3339), f.sevenDay, now.Add(72*time.Hour).Format(time.RFC3339))
	}))
	t.Cleanup(f.srv.Close)
	f.cred = filepath.Join(t.TempDir(), ".credentials.json")
	exp := time.Now().Add(24 * time.Hour).UnixMilli()
	if err := os.WriteFile(f.cred, []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"not-a-real-token","expiresAt":%d,"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}}`, exp)), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeLimits) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeLimits) setStatus(code int) {
	f.mu.Lock()
	f.status = code
	f.mu.Unlock()
}

func (f *fakeLimits) set(fiveHour, sevenDay float64) {
	f.mu.Lock()
	f.fiveHour, f.sevenDay = fiveHour, sevenDay
	f.mu.Unlock()
}

func (f *fakeLimits) spec() LimitsSpec {
	return LimitsSpec{ProviderID: "claude_code", Fetcher: &limits.Client{CredentialsPath: f.cred, BaseURL: f.srv.URL, Version: "test"}}
}

// ---------------------------------------------------------------- fake Pokémon data

type fakePoke struct {
	mu        sync.Mutex
	bases     []companion.BaseEntry
	lineCalls map[int]int
	offline   atomic.Bool
}

func newFakePoke(bases ...int) *fakePoke {
	f := &fakePoke{lineCalls: map[int]int{}}
	for _, b := range bases {
		f.bases = append(f.bases, companion.BaseEntry{ID: b, CaptureRate: 255})
	}
	return f
}

func monName(id int) string { return "Mon" + strconv.Itoa(id) }

func (f *fakePoke) BaseIndex(context.Context) ([]companion.BaseEntry, error) {
	if f.offline.Load() {
		return nil, fmt.Errorf("offline")
	}
	return f.bases, nil
}

// Line is a linear 3-stage line base → base+1 → base+2.
func (f *fakePoke) Line(_ context.Context, base int) (*companion.Line, error) {
	if f.offline.Load() {
		return nil, fmt.Errorf("offline")
	}
	f.mu.Lock()
	f.lineCalls[base]++
	f.mu.Unlock()
	l := &companion.Line{BaseID: base, CaptureRate: 255, Names: map[int]map[string]string{},
		Tree: companion.Node{ID: base, Children: []companion.Node{{ID: base + 1, Children: []companion.Node{{ID: base + 2}}}}}}
	for _, id := range []int{base, base + 1, base + 2} {
		l.Names[id] = map[string]string{"en": monName(id), "fr": monName(id) + "-fr"}
	}
	return l, nil
}

func (f *fakePoke) Species(_ context.Context, id int) (*companion.Species, error) {
	if f.offline.Load() {
		return nil, fmt.Errorf("offline")
	}
	return &companion.Species{ID: id, CaptureRate: 255, Names: map[string]string{"en": monName(id)}}, nil
}

func (f *fakePoke) Details(_ context.Context, id int) (*companion.Details, error) {
	if f.offline.Load() {
		return nil, fmt.Errorf("offline")
	}
	return &companion.Details{SpeciesID: id, Height: 11, Weight: 300, GenderRate: 4, Types: []string{"normal", "flying"},
		BaseStats: map[string]int{"hp": 63, "attack": 60, "defense": 55, "special-attack": 50, "special-defense": 50, "speed": 71},
		Abilities: []companion.Ability{{Name: "keen-eye", Slot: 1}, {Name: "big-pecks", Slot: 3, Hidden: true}},
		Moves: []companion.Move{{Name: "tackle", Methods: []companion.LearnMethod{{Method: "level-up", Level: 1}}},
			{Name: "gust", Methods: []companion.LearnMethod{{Method: "level-up", Level: 9}, {Method: "machine"}}},
			{Name: "fly", Methods: []companion.LearnMethod{{Method: "machine"}}}}}, nil
}

func (f *fakePoke) Names(_ context.Context, kind, name string) (map[string]string, error) {
	if f.offline.Load() {
		return nil, fmt.Errorf("offline")
	}
	return map[string]string{"en": strings.ToUpper(name[:1]) + name[1:]}, nil
}

func (f *fakePoke) lines() map[int]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]int{}
	for k, v := range f.lineCalls {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------- fake sprite server

type fakeSprites struct {
	srv      *httptest.Server
	backFail atomic.Bool
	hits     atomic.Int64
}

func testGIF() []byte {
	pal := color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}}
	g := &gif.GIF{}
	for i := 0; i < 2; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 4, 3), pal)
		img.SetColorIndex(i, 1, 1)
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	_ = gif.EncodeAll(&b, g)
	return b.Bytes()
}

func testPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{0, 0, 255, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func newFakeSprites(t *testing.T) *fakeSprites {
	f := &fakeSprites{}
	gifData, pngData := testGIF(), testPNG()
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.backFail.Load() && strings.Contains(r.URL.Path, "/back/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ".gif"):
			_, _ = w.Write(gifData)
		case strings.HasSuffix(r.URL.Path, ".png"):
			_, _ = w.Write(pngData)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSprites) store(cacheDir string) *sprites.Store {
	return sprites.New(sprites.Options{CacheDir: cacheDir, SpriteBase: f.srv.URL + "/pokemon", ItemBase: f.srv.URL + "/items"})
}

// ---------------------------------------------------------------- harness

type msg map[string]any

func (m msg) typ() string  { s, _ := m["type"].(string); return s }
func (m msg) kind() string { s, _ := m["kind"].(string); return s }
func (m msg) id() int64    { f, _ := m["id"].(float64); return int64(f) }

func (m msg) path(keys ...string) any {
	var cur any = map[string]any(m)
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

func (m msg) num(keys ...string) float64 { f, _ := m.path(keys...).(float64); return f }
func (m msg) str(keys ...string) string  { s, _ := m.path(keys...).(string); return s }
func (m msg) flag(keys ...string) bool   { b, _ := m.path(keys...).(bool); return b }

type harness struct {
	t      *testing.T
	cfg    Config
	usage  *fakeUsage
	poke   *fakePoke
	inW    *io.PipeWriter
	lines  chan []byte
	exited chan struct{}
	code   int
	seen   []msg
}

// newHarness configures a daemon on a fresh state dir with fast timers.
func newHarness(t *testing.T, dir string) *harness {
	if dir == "" {
		dir = t.TempDir()
	}
	h := &harness{t: t, usage: newFakeUsage(1_000_000), poke: newFakePoke(16)}
	h.cfg = Config{Version: "1.2.3", StateDir: dir, CacheDir: filepath.Join(dir, "cache"),
		Providers: []UsageSource{h.usage}, Data: h.poke, Rng: &companion.SplitMix64{State: 42},
		LockWait: 300 * time.Millisecond, SettingsWait: 5 * time.Second, EmptyRetry: time.Hour, Tick: time.Hour,
		StateInterval: time.Millisecond, SpeciesDeadline: 5 * time.Second, SpriteDeadline: 5 * time.Second,
		Logf: func(f string, a ...any) { t.Logf("helper: "+f, a...) }}
	return h
}

func (h *harness) start() {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h.inW = inW
	h.lines = make(chan []byte, 4096)
	h.exited = make(chan struct{})
	go func() {
		h.code = Serve(h.cfg, inR, outW, nil)
		outW.Close()
		close(h.exited)
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 1<<16), 4<<20)
		for sc.Scan() {
			h.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(h.lines)
	}()
	// A failed test still stops the helper before the test ends, so it never logs afterwards.
	h.t.Cleanup(func() {
		_ = inW.Close()
		go func() {
			for range h.lines {
			}
		}()
		select {
		case <-h.exited:
		case <-time.After(5 * time.Second):
		}
	})
}

// exitCode waits for Serve to return.
func (h *harness) exitCode() int {
	h.t.Helper()
	select {
	case <-h.exited:
		return h.code
	case <-time.After(5 * time.Second):
		h.t.Fatal("helper did not exit")
		return -1
	}
}

func (h *harness) send(line string) {
	h.t.Helper()
	if _, err := io.WriteString(h.inW, line+"\n"); err != nil {
		h.t.Fatalf("write stdin: %v", err)
	}
}

func (h *harness) sendSettings(id int, back bool) {
	h.send(fmt.Sprintf(`{"id":%d,"cmd":"setSettings","args":{"refreshInterval":0,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1.0,"weekStart":1,"language":"en","alwaysFacesBack":%t}}`, id, back))
}

// next returns the next output line, failing after timeout.
func (h *harness) next(timeout time.Duration) (msg, bool) {
	h.t.Helper()
	select {
	case b, ok := <-h.lines:
		if !ok {
			return nil, false
		}
		var m msg
		if err := json.Unmarshal(b, &m); err != nil {
			h.t.Fatalf("output is not JSON: %q", b)
		}
		if len(b) > 1<<20 {
			h.t.Fatalf("output line over 1 MiB")
		}
		h.seen = append(h.seen, m)
		return m, true
	case <-time.After(timeout):
		return nil, false
	}
}

// expect skips lines until pred matches.
func (h *harness) expect(what string, timeout time.Duration, pred func(msg) bool) msg {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m, ok := h.next(time.Until(deadline))
		if !ok {
			h.t.Fatalf("timed out waiting for %s; last lines: %s", what, h.tail(6))
		}
		if pred(m) {
			return m
		}
	}
}

// lastState is the newest state among the lines read so far.
func (h *harness) lastState() msg {
	for i := len(h.seen) - 1; i >= 0; i-- {
		if h.seen[i].typ() == "state" {
			return h.seen[i]
		}
	}
	return nil
}

func (h *harness) tail(n int) string {
	var parts []string
	for _, m := range h.seen[max(0, len(h.seen)-n):] {
		b, _ := json.Marshal(m)
		if len(b) > 300 {
			b = append(b[:300], "…"...)
		}
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n")
}

// quiet asserts that no line matching pred arrives within d.
func (h *harness) quiet(d time.Duration, what string, pred func(msg) bool) {
	h.t.Helper()
	deadline := time.Now().Add(d)
	for {
		m, ok := h.next(time.Until(deadline))
		if !ok {
			return
		}
		if pred(m) {
			h.t.Fatalf("unexpected %s: %v", what, m)
		}
	}
}

// stop closes stdin and returns the exit code and the time it took.
func (h *harness) stop() (int, time.Duration) {
	h.t.Helper()
	start := time.Now()
	_ = h.inW.Close()
	for range h.lines {
	}
	code := h.exitCode()
	return code, time.Since(start)
}

func isResult(id int64) func(msg) bool {
	return func(m msg) bool { return m.typ() == "result" && m.id() == id }
}

func isFreshState(m msg) bool {
	return m.typ() == "state" && !m.flag("meta", "fromCache") && !m.flag("meta", "refreshing")
}

func isEvent(kind string) func(msg) bool {
	return func(m msg) bool { return m.typ() == "event" && m.kind() == kind }
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
