package claude

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"poketokenbar/helper/internal/usage"
)

// cacheLine mirrors LocalUsageCacheTests.swift:67-71: input 10, cache write 5, cache read 100.
func cacheLine(id string, output int64, ts string) string {
	return fmt.Sprintf(`{"type":"assistant","requestId":"r-%s","timestamp":"%s","message":{"id":"m-%s","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":%d,"cache_creation_input_tokens":5,"cache_read_input_tokens":100}}}`,
		id, ts, id, output)
}

const tsJuly = "2026-07-02T01:00:00.000Z"

var epoch = time.Unix(0, 0)

type fixture struct {
	t     *testing.T
	root  string
	cache string
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	base := t.TempDir()
	f := &fixture{t: t, root: filepath.Join(base, "projects"), cache: filepath.Join(base, "cache"), now: time.Now()}
	if err := os.MkdirAll(f.root, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// write replaces the file's content in place (same inode) and sets its mtime when non-zero.
func (f *fixture) write(name, content string, mtime time.Time) string {
	f.t.Helper()
	p := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			f.t.Fatal(err)
		}
	}
	return p
}

func (f *fixture) provider(loc *time.Location) *Provider {
	if loc == nil {
		loc = time.UTC
	}
	return New(Options{Roots: []string{f.root}, CacheDir: f.cache, Loc: loc, Now: func() time.Time { return f.now }})
}

func outputs(entries []usage.Entry) []int64 {
	var out []int64
	for _, e := range entries {
		out = append(out, e.Output)
	}
	slices.Sort(out)
	return out
}

func (f *fixture) snapshotText() string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.cache, CacheFileName))
	if err != nil {
		f.t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		f.t.Fatalf("cache is not zlib: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(plain)
}

func wholeSecond(t time.Time) time.Time { return t.Truncate(time.Second) }

func TestScanParsesDedupesAndPrices(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:92
	f := newFixture(t)
	ts := "2026-06-30T10:00:00.000Z"
	f.write("proj/sub/s.jsonl", strings.Join([]string{
		claudeLine("A", "R1", "claude-opus-4-8", ts, 100, 5, 0, 1000),
		claudeLine("A", "R1", "claude-opus-4-8", ts, 100, 200, 0, 1000),
		claudeLine("B", "R2", "claude-sonnet-4-6", ts, 50, 10, 0, 0),
	}, "\n"), time.Time{})
	entries, _ := f.provider(nil).Scan(epoch)
	if len(entries) != 2 || entries[0].ID != "A|R1" || entries[0].Output != 200 || entries[0].CacheRead != 1000 {
		t.Errorf("keep-max: %+v", entries)
	}

	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:109
	g := newFixture(t)
	g.write("p/s.jsonl", claudeLine("A", "R1", "claude-opus-4-8", ts, 1_000_000, 0, 0, 0), time.Time{})
	entries, _ = g.provider(nil).Scan(epoch)
	d := usage.Daily(entries, "2026-06-30")
	if d == nil || d.Tokens != 1_000_000 || math.Abs(d.Cost.USD-5) > 1e-6 {
		t.Errorf("daily: %+v", d)
	}
	if usage.Daily(entries, "2000-01-01") != nil {
		t.Error("another day must be nil")
	}
}

func TestMultipleRootsShareGlobalDedup(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:257
	cli, desktop := newFixture(t), newFixture(t)
	ts := "2026-06-30T10:00:00.000Z"
	cli.write("p/s.jsonl", claudeLine("A", "R1", "claude-opus-4-8", ts, 100, 10, 0, 0), time.Time{})
	desktop.write("p/s.jsonl", claudeLine("A", "R1", "claude-opus-4-8", ts, 100, 10, 0, 0)+"\n"+
		claudeLine("B", "R2", "claude-opus-4-8", ts, 7, 3, 0, 0), time.Time{})
	both, _ := New(Options{Roots: []string{cli.root, desktop.root}}).Scan(epoch)
	var total int64
	for _, e := range both {
		total += e.Total()
	}
	if len(both) != 2 || total != 120 {
		t.Errorf("two roots: %d entries, %d tokens; want 2, 120", len(both), total)
	}
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:272 (control: one root only)
	if one, _ := New(Options{Roots: []string{cli.root}}).Scan(epoch); len(one) != 1 {
		t.Errorf("one root: %d entries", len(one))
	}
	// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:251 (same, through the cache)
	cached, _ := New(Options{Roots: []string{cli.root, desktop.root}, CacheDir: t.TempDir()}).Scan(epoch)
	if len(cached) != 2 {
		t.Errorf("cached two roots: %d entries", len(cached))
	}
}

func TestEntriesCarryTheirSession(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/AdditionalClaudeAccountsTests.swift:1546
	f := newFixture(t)
	line := `{"type":"assistant","timestamp":"2026-09-16T10:00:00Z","requestId":"r1","message":{"id":"m1","model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":5}}}`
	f.write("-Users-example-app/sess-1.jsonl", line, time.Time{})
	f.write("-Users-example-app/sess-1/subagents/agent-1.jsonl",
		cacheLine("sub", 1, "2026-09-16T10:01:00Z"), time.Time{})
	entries, _ := f.provider(nil).Scan(epoch)
	if len(entries) != 2 || fmt.Sprint(entries[0].Sessions) != "[sess-1]" || fmt.Sprint(entries[1].Sessions) != "[sess-1]" {
		t.Errorf("sessions: %+v", entries)
	}
}

func TestFileSelection(t *testing.T) {
	// Go-only: .jsonl only, hidden names skipped, mtime filter (LocalUsageCache.swift:393-404).
	f := newFixture(t)
	old := time.Now().Add(-10 * 24 * time.Hour)
	f.write("p/a.jsonl", cacheLine("a", 1, tsJuly), time.Time{})
	f.write("p/a.meta.json", cacheLine("json", 2, tsJuly), time.Time{})
	f.write("p/.hidden.jsonl", cacheLine("hidden", 3, tsJuly), time.Time{})
	f.write(".hiddendir/b.jsonl", cacheLine("hiddendir", 4, tsJuly), time.Time{})
	f.write("p/old.jsonl", cacheLine("old", 5, tsJuly), old)
	entries, _ := f.provider(nil).Scan(time.Now().Add(-24 * time.Hour))
	if got := outputs(entries); !slices.Equal(got, []int64{1}) {
		t.Errorf("selected outputs %v; want [1]", got)
	}
}

func TestCacheReuseAndInvalidation(t *testing.T) {
	t0 := wholeSecond(time.Now()).Add(-time.Hour)

	t.Run("unchanged file is not reparsed", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:235
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly), t0)
		p := f.provider(nil)
		if got, _ := p.Scan(epoch); !slices.Equal(outputs(got), []int64{111}) {
			t.Fatalf("first scan %v", outputs(got))
		}
		f.write("a.jsonl", cacheLine("1", 222, tsJuly), t0) // same length, mtime and inode
		got, st := p.Scan(epoch)
		if !slices.Equal(outputs(got), []int64{111}) || st.Hits != 1 {
			t.Errorf("second scan %v %+v; want the cached 111", outputs(got), st)
		}
	})

	t.Run("changed mtime is reparsed", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:275
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly), t0)
		p := f.provider(nil)
		p.Scan(epoch)
		f.write("a.jsonl", cacheLine("1", 999, tsJuly), t0.Add(10*time.Second))
		if got, st := p.Scan(epoch); !slices.Equal(outputs(got), []int64{999}) || st.Parsed != 1 {
			t.Errorf("got %v %+v", outputs(got), st)
		}
	})

	t.Run("appended file parses only the tail", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:298
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly)+"\n", t0)
		p := f.provider(nil)
		p.Scan(epoch)
		f.write("a.jsonl", cacheLine("1", 222, tsJuly)+"\n"+cacheLine("2", 333, tsJuly)+"\n", t0.Add(10*time.Second))
		got, st := p.Scan(epoch)
		if !slices.Equal(outputs(got), []int64{111, 333}) || st.Appended != 1 {
			t.Errorf("got %v %+v; the prefix must come from the cache", outputs(got), st)
		}
	})

	t.Run("truncated file is reparsed", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:314
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly)+"\n"+cacheLine("2", 222, tsJuly)+"\n", t0)
		p := f.provider(nil)
		p.Scan(epoch)
		f.write("a.jsonl", cacheLine("3", 333, tsJuly)+"\n", t0.Add(10*time.Second))
		if got, _ := p.Scan(epoch); !slices.Equal(outputs(got), []int64{333}) {
			t.Errorf("got %v", outputs(got))
		}
	})

	t.Run("half-written line is retried", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:329
		f := newFixture(t)
		full := cacheLine("2", 222, tsJuly)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly)+"\n"+full[:len(full)/2], t0)
		p := f.provider(nil)
		if got, _ := p.Scan(epoch); !slices.Equal(outputs(got), []int64{111}) {
			t.Fatalf("first %v", outputs(got))
		}
		f.write("a.jsonl", cacheLine("1", 111, tsJuly)+"\n"+full+"\n", t0.Add(10*time.Second))
		if got, st := p.Scan(epoch); !slices.Equal(outputs(got), []int64{111, 222}) || st.Appended != 1 {
			t.Errorf("second %v %+v", outputs(got), st)
		}
	})

	t.Run("replaced file with a new inode is reparsed", func(t *testing.T) {
		// Go-only: a larger file renamed over the old one is not an append. Upstream has no
		// inode check and would misread it as one (spec §5 "Rotation").
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly)+"\n", t0)
		p := f.provider(nil)
		p.Scan(epoch)
		next := f.write("next.tmp", cacheLine("9", 999, tsJuly)+"\n"+cacheLine("8", 888, tsJuly)+"\n", t0.Add(10*time.Second))
		if err := os.Rename(next, filepath.Join(f.root, "a.jsonl")); err != nil {
			t.Fatal(err)
		}
		got, st := p.Scan(epoch)
		if !slices.Equal(outputs(got), []int64{888, 999}) || st.Parsed != 1 {
			t.Errorf("got %v %+v; want only the new file's entries", outputs(got), st)
		}
	})

	t.Run("same-size same-mtime replacement is reparsed", func(t *testing.T) {
		// Go-only: the inode is part of the cache-hit signature, so a file swapped by rename
		// with the same length and mtime is not served from the cache.
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 111, tsJuly), t0)
		p := f.provider(nil)
		p.Scan(epoch)
		next := f.write("next.tmp", cacheLine("1", 222, tsJuly), t0)
		if err := os.Rename(next, filepath.Join(f.root, "a.jsonl")); err != nil {
			t.Fatal(err)
		}
		if got, st := p.Scan(epoch); !slices.Equal(outputs(got), []int64{222}) || st.Parsed != 1 {
			t.Errorf("got %v %+v; want the new file's 222", outputs(got), st)
		}
	})

	t.Run("read failure keeps the old entries and retries", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:1026 (same rule for Claude, :425-429)
		if os.Geteuid() == 0 {
			t.Skip("root ignores file permissions")
		}
		f := newFixture(t)
		path := f.write("a.jsonl", cacheLine("1", 10, tsJuly), t0)
		p := f.provider(nil)
		p.Scan(epoch)
		f.write("a.jsonl", cacheLine("1", 20, tsJuly), t0.Add(10*time.Second))
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		got, st := p.Scan(epoch)
		if !slices.Equal(outputs(got), []int64{10}) || st.Failed != 1 {
			t.Errorf("during failure %v %+v; want the previous 10", outputs(got), st)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := p.Scan(epoch); !slices.Equal(outputs(got), []int64{20}) {
			t.Errorf("after recovery %v; the failed signature must not be cached", outputs(got))
		}
	})

	t.Run("modified-since filter", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:853
		f := newFixture(t)
		f.write("old.jsonl", cacheLine("o", 1, tsJuly), time.Now().Add(-10*24*time.Hour))
		f.write("new.jsonl", cacheLine("n", 2, tsJuly), time.Time{})
		if got, _ := f.provider(nil).Scan(time.Now().Add(-24 * time.Hour)); !slices.Equal(outputs(got), []int64{2}) {
			t.Errorf("got %v", outputs(got))
		}
	})
}

func TestCachePersistence(t *testing.T) {
	t0 := wholeSecond(time.Now()).Add(-time.Hour)

	t.Run("disk round trip", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:800
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 42, tsJuly), t0)
		f.provider(nil).Scan(epoch)
		f.write("a.jsonl", cacheLine("1", 43, tsJuly), t0)
		if got, _ := f.provider(nil).Scan(epoch); !slices.Equal(outputs(got), []int64{42}) {
			t.Errorf("new instance %v; want 42 from the saved cache", outputs(got))
		}
	})

	t.Run("prune after 40 days", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:815
		f := newFixture(t)
		f.write("old.jsonl", cacheLine("o", 1, tsJuly), time.Now().Add(-45*24*time.Hour))
		f.write("new.jsonl", cacheLine("n", 2, tsJuly), time.Time{})
		if got, _ := f.provider(nil).Scan(epoch); !slices.Equal(outputs(got), []int64{1, 2}) {
			t.Errorf("the scan still returns both: %v", outputs(got))
		}
		snap := f.snapshotText()
		if strings.Contains(snap, "old.jsonl") || !strings.Contains(snap, "new.jsonl") {
			t.Error("a 45-day-old file must be pruned from the saved cache")
		}
	})

	t.Run("save throttle", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:832
		f := newFixture(t)
		f.now = time.Unix(1_700_000_000, 0)
		p := f.provider(nil)
		f.write("a.jsonl", cacheLine("1", 1, tsJuly), f.now)
		p.Scan(epoch)
		first := f.snapshotText()
		f.now = f.now.Add(30 * time.Second)
		f.write("a.jsonl", cacheLine("1", 2, tsJuly), f.now)
		p.Scan(epoch)
		if f.snapshotText() != first {
			t.Error("a save within 60 s must be skipped")
		}
		f.now = f.now.Add(61 * time.Second)
		p.Scan(epoch)
		if f.snapshotText() == first {
			t.Error("the save must happen once 60 s have passed")
		}
	})

	t.Run("flush ignores the throttle", func(t *testing.T) {
		// Go-only: the daemon flushes on stdin EOF.
		f := newFixture(t)
		p := f.provider(nil)
		f.write("a.jsonl", cacheLine("1", 1, tsJuly), f.now)
		p.Scan(epoch)
		first := f.snapshotText()
		f.write("a.jsonl", cacheLine("1", 2, tsJuly), f.now.Add(time.Second))
		p.Scan(epoch)
		if f.snapshotText() != first {
			t.Fatal("precondition: throttled")
		}
		if err := p.Flush(); err != nil || f.snapshotText() == first {
			t.Errorf("Flush did not save (err %v)", err)
		}
	})

	t.Run("compressed", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:880
		f := newFixture(t)
		var lines []string
		for i := 1; i <= 50; i++ {
			lines = append(lines, cacheLine(fmt.Sprint(i), int64(i), tsJuly))
		}
		f.write("a.jsonl", strings.Join(lines, "\n"), t0)
		f.provider(nil).Scan(epoch)
		raw, _ := os.ReadFile(filepath.Join(f.cache, CacheFileName))
		if plain := f.snapshotText(); len(raw) >= len(plain) {
			t.Errorf("compressed %d bytes >= plain %d", len(raw), len(plain))
		}
	})

	t.Run("parser version mismatch discards the cache", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:1045 (Pi; same rule for Claude, :565-568)
		f := newFixture(t)
		f.write("a.jsonl", cacheLine("1", 10, tsJuly), t0)
		f.provider(nil).Scan(epoch)
		tamper := func(version int) {
			var snap map[string]any
			if err := json.Unmarshal([]byte(f.snapshotText()), &snap); err != nil {
				t.Fatal(err)
			}
			for _, b := range snap["files"].(map[string]any) {
				b.(map[string]any)["entries"].([]any)[0].(map[string]any)["out"] = 999
			}
			snap["version"] = version
			var buf bytes.Buffer
			zw := zlib.NewWriter(&buf)
			json.NewEncoder(zw).Encode(snap)
			zw.Close()
			os.WriteFile(filepath.Join(f.cache, CacheFileName), buf.Bytes(), 0o600)
		}
		tamper(ParserVersion)
		if got, _ := f.provider(nil).Scan(epoch); !slices.Equal(outputs(got), []int64{999}) {
			t.Fatalf("control: the tampered cache must be read, got %v", outputs(got))
		}
		tamper(ParserVersion - 1)
		if got, _ := f.provider(nil).Scan(epoch); !slices.Equal(outputs(got), []int64{10}) {
			t.Errorf("an old parser version must be reparsed, got %v", outputs(got))
		}
	})
}

func TestSonnet55PricesColdWarmAndPersisted(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UsageCostTests.swift:140
	f := newFixture(t)
	f.now = wholeSecond(time.Now())
	stamp := f.now.UTC().Format(time.RFC3339)
	source := `{"type":"assistant","timestamp":"` + stamp + `","requestId":"request-1","message":{"id":"message-1",` +
		`"model":"claude-sonnet-5-5","usage":{"input_tokens":100,"output_tokens":10,` +
		`"cache_creation_input_tokens":40,"cache_read_input_tokens":1000}}}` + "\n"
	path := f.write("session.jsonl", source, f.now)
	check := func(stage string, entries []usage.Entry) {
		t.Helper()
		if len(entries) != 1 {
			t.Fatalf("%s: %d entries", stage, len(entries))
		}
		d := usage.Daily(entries, entries[0].LocalDay)
		if d == nil || d.Tokens != 1_150 || math.Abs(d.Cost.USD-0.0005) > 1e-12 || d.Cost.Coverage.String() != "estimated" {
			t.Errorf("%s: daily %+v", stage, d)
		}
		e := usage.EnrichmentFromEntries(entries, f.now.In(time.Local), time.Monday)
		for name, c := range map[string]usage.Cost{"week": e.Week.Cost, "month": e.Month.Cost,
			"month day": e.MonthDaily[len(e.MonthDaily)-1].Cost, "block": e.ActiveBlock.Cost} {
			if math.Abs(c.USD-0.0005) > 1e-12 {
				t.Errorf("%s: %s cost %v", stage, name, c.USD)
			}
		}
	}
	p := New(Options{Roots: []string{f.root}, CacheDir: f.cache, Now: func() time.Time { return f.now }})
	cold, _ := p.Scan(epoch)
	check("cold", cold)
	// Keep the signature but remove every record: later reads must come from the cache.
	f.write("session.jsonl", strings.Repeat(" ", len(source)), f.now)
	fh, _ := os.Open(path)
	if fresh, _, _ := ParseStream(fh, "s", time.UTC); len(fresh) != 0 {
		t.Fatal("precondition: the rewritten file holds no record")
	}
	fh.Close()
	warm, _ := p.Scan(epoch)
	check("warm", warm)
	persisted, _ := New(Options{Roots: []string{f.root}, CacheDir: f.cache, Now: func() time.Time { return f.now }}).Scan(epoch)
	check("persisted", persisted)
}

func TestWindowsOverRealFiles(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	line := func(id string, out int64, at time.Time) string {
		return fmt.Sprintf(`{"type":"assistant","requestId":"r-%s","timestamp":"%s","message":{"id":"m-%s","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
			id, at.UTC().Format(time.RFC3339), id, out)
	}

	t.Run("week straddling the month boundary", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:958
		var now time.Time
		for m := time.January; m <= time.December; m++ {
			c := time.Date(2026, m, 1, 12, 0, 0, 0, paris)
			if usage.StartOfWeek(c, time.Monday).Before(usage.StartOfMonth(c)) {
				now = c
				break
			}
		}
		weekStart, monthStart := usage.StartOfWeek(now, time.Monday), usage.StartOfMonth(now)
		straddle := weekStart.Add(time.Hour)
		f := newFixture(t)
		f.now = now
		f.write("straddle.jsonl", line("s", 500, straddle), straddle)
		p := f.provider(paris)
		if got, _ := p.Scan(monthStart); len(got) != 0 {
			t.Fatal("precondition: a month-start bound misses last month's file")
		}
		got, _ := p.Scan(usage.EnrichmentScanStart(now, time.Monday))
		if len(got) != 1 || usage.Period(got, usage.DayKey(weekStart), usage.DayKey(now)).Tokens == 0 {
			t.Errorf("the enrichment bound must include this week's file: %+v", got)
		}
	})

	t.Run("cross-month session", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:101
		now := time.Date(2026, 7, 10, 12, 0, 0, 0, paris)
		june := time.Date(2026, 6, 29, 23, 0, 0, 0, paris)
		july := time.Date(2026, 7, 8, 1, 0, 0, 0, paris)
		f := newFixture(t)
		f.now = now
		f.write("straddle.jsonl", line("june", 700, june)+"\n"+line("july", 300, july), july)
		p := f.provider(paris)
		entries, _ := p.Scan(usage.EnrichmentScanStart(now, time.Monday))
		days := map[string]bool{}
		for _, e := range entries {
			days[e.LocalDay] = true
		}
		if !days["2026-06-29"] || !days["2026-07-08"] {
			t.Fatalf("precondition: the scan loads both months: %v", days)
		}
		e := p.FetchEnrichment(now, time.Monday)
		var sum int64
		for _, d := range e.MonthDaily {
			if d.Day[:7] != "2026-07" {
				t.Errorf("last month leaked: %s", d.Day)
			}
			if d.Day == "2026-07-08" && d.Output != 300 {
				t.Errorf("07-08 output %d", d.Output)
			}
			sum += d.Tokens
		}
		if e.MonthDaily[0].Day != "2026-07-01" || e.MonthDaily[len(e.MonthDaily)-1].Day != "2026-07-10" || sum != e.Month.Tokens {
			t.Errorf("series %s..%s sum %d month %d", e.MonthDaily[0].Day, e.MonthDaily[len(e.MonthDaily)-1].Day, sum, e.Month.Tokens)
		}
	})

	t.Run("21-day scan across DST fall-back", func(t *testing.T) {
		// Swift: Tests/PokeTokenBarTests/MonthDailyTrendTests.swift:726
		la, _ := time.LoadLocation("America/Los_Angeles")
		now := time.Date(2026, 11, 2, 7, 59, 0, 0, time.UTC)
		transcript := time.Date(2026, 10, 12, 7, 30, 0, 0, time.UTC)
		f := newFixture(t)
		f.now = now
		f.write("dst-fallback-first-day.jsonl", line("oct12-early", 113, transcript), transcript)
		p := f.provider(la)
		sum := func(es []usage.Entry) (n int64) {
			for _, e := range es {
				n += e.Total()
			}
			return n
		}
		full, _ := p.Scan(epoch)
		selected, _ := p.Scan(usage.EnrichmentScanStart(now.In(la), time.Monday))
		if sum(full) != 123 || sum(selected) != 123 {
			t.Errorf("full %d selected %d; want 123", sum(full), sum(selected))
		}
		e := p.FetchEnrichment(now, time.Monday)
		r := e.RecentDaily
		if len(r) != 21 || r[0].Day != "2026-10-12" || r[0].Tokens != 123 || r[20].Day != "2026-11-01" {
			t.Errorf("recent %s..%s first %d", r[0].Day, r[len(r)-1].Day, r[0].Tokens)
		}
	})
}

func TestDefaultDiscoveryReadsTheEnvironment(t *testing.T) {
	// Go-only: without Options.Roots the provider discovers roots from HOME, CLAUDE_CONFIG_DIR
	// and XDG_CONFIG_HOME at scan time.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	projects := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(filepath.Join(projects, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(projects, "p", "s.jsonl"), []byte(cacheLine("1", 7, tsJuly)), 0o644)
	p := New(Options{})
	real, _ := filepath.EvalSymlinks(home)
	want := []string{filepath.Join(real, ".config", "claude", "projects"), filepath.Join(real, ".claude", "projects")}
	if got := p.Roots(); !slices.Equal(got, want) {
		t.Errorf("roots %v; want %v", got, want)
	}
	if got, _ := p.Scan(epoch); !slices.Equal(outputs(got), []int64{7}) {
		t.Errorf("scan %v", outputs(got))
	}
}

func TestFetchIgnoresZeroTokenRecords(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1080 and MonthDailyTrendTests.swift:172,
	// through the provider with non-empty zero-token input (upstream defect log :266-273).
	f := newFixture(t)
	now := time.Now()
	f.write("s.jsonl", claudeLine("syn", "r", "<synthetic>", now.UTC().Format(time.RFC3339), 0, 0, 0, 0), time.Time{})
	p := New(Options{Roots: []string{f.root}})
	if d, err := p.FetchDaily(now); d != nil || err != nil {
		t.Errorf("FetchDaily = %+v, %v; want nil", d, err)
	}
	e := p.FetchEnrichment(now, time.Monday)
	if e.ActiveBlock != nil || !e.LastUsage.IsZero() || e.Month.Tokens != 0 || !e.BlocksOK || !e.PeriodsOK {
		t.Errorf("enrichment %+v", e)
	}
	// Go-only: a real record today is returned by both.
	f.write("t.jsonl", claudeLine("real", "r", "claude-opus-4-8", now.UTC().Format(time.RFC3339), 10, 5, 0, 0), time.Time{})
	if d, _ := p.FetchDaily(now); d == nil || d.Tokens != 15 {
		t.Errorf("FetchDaily = %+v; want 15 tokens", d)
	}
	if e := p.FetchEnrichment(now, time.Monday); e.ActiveBlock == nil || e.ActiveBlock.Tokens != 15 {
		t.Errorf("block %+v", e.ActiveBlock)
	}
	if p.ID() != "claude_code" || p.DisplayName() != "Claude Code" || !p.ReportsCost() {
		t.Error("provider identity")
	}
}
