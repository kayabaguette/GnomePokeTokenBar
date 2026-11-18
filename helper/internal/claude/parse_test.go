package claude

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	_ "time/tzdata" // hermetic zones

	"poketokenbar/helper/internal/usage"
)

// claudeLine mirrors the Swift fixture helper (LocalUsageReaderTests.swift:86-90).
func claudeLine(id, req, model, ts string, i, o, cw, cr int64) string {
	return fmt.Sprintf(`{"type":"assistant","requestId":"%s","timestamp":"%s","message":{"id":"%s","model":"%s","usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}}`,
		req, ts, id, model, i, o, cw, cr)
}

func TestParseLine(t *testing.T) {
	const ts = `"timestamp":"2026-06-30T10:00:00.000Z"`
	type want struct {
		ok              bool
		id, model       string
		in, out, cw, cr int64
	}
	cases := []struct {
		name string
		line string
		want want
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:88
		{"assistant record", claudeLine("A", "R1", "claude-opus-4-8", "2026-06-30T10:00:00.000Z", 100, 5, 7, 1000),
			want{true, "A|R1", "claude-opus-4-8", 100, 5, 7, 1000}},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1144
		{"absurd counts clamp", `{"type":"assistant","requestId":"r1",` + ts + `,"message":{"id":"m1","model":"claude","usage":{"input_tokens":1e30,"output_tokens":1e30,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
			want{true, "m1|r1", "claude", MaxTokenValue, MaxTokenValue, 0, 0}},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:1179
		{"negative, null, string, absent fold to 0", `{"type":"assistant","requestId":"r2",` + ts + `,"message":{"id":"m2","model":"claude","usage":{"input_tokens":-5,"output_tokens":null,"cache_read_input_tokens":"nope"}}}`,
			want{true, "m2|r2", "claude", 0, 0, 0, 0}},
		// Go-only: fractions truncate; a boolean is an NSNumber upstream (true = 1); an
		// out-of-range number is non-finite there (0) (LocalUsageReader.swift:2216-2227).
		{"fraction, bool, overflow", `{"type":"assistant","requestId":"r",` + ts + `,"message":{"id":"m","usage":{"input_tokens":12.9,"output_tokens":true,"cache_creation_input_tokens":false,"cache_read_input_tokens":1e400}}}`,
			want{true, "m|r", "unknown", 12, 1, 0, 0}},
		// Go-only: missing ids collapse to "|" (spec §2.3 edge case); a non-string model is "unknown".
		{"no ids", `{"type":"assistant",` + ts + `,"message":{"model":7,"usage":{"input_tokens":1}}}`,
			want{true, "|", "unknown", 1, 0, 0, 0}},
		// Go-only: a <synthetic> zero-token record still parses; totals exclude it later (spec §2.4).
		{"synthetic", claudeLine("s", "r", "<synthetic>", "2026-06-30T10:00:00.000Z", 0, 0, 0, 0),
			want{true, "s|r", "<synthetic>", 0, 0, 0, 0}},
		// Go-only: the required fields of LocalUsageReader.swift:564-571.
		{"user record", strings.Replace(claudeLine("A", "R", "m", "2026-06-30T10:00:00.000Z", 1, 1, 1, 1), `"type":"assistant"`, `"type":"user","x":"assistant"`, 1), want{}},
		{"message not an object", `{"type":"assistant",` + ts + `,"message":"usage","requestId":"r"}`, want{}},
		{"message missing", `{"type":"assistant",` + ts + `,"usage":{"input_tokens":1}}`, want{}},
		{"usage null", `{"type":"assistant",` + ts + `,"message":{"id":"m","usage":null}}`, want{}},
		{"usage not an object", `{"type":"assistant",` + ts + `,"message":{"id":"m","usage":[1]}}`, want{}},
		{"timestamp not a string", `{"type":"assistant","timestamp":1,"message":{"id":"m","usage":{}}}`, want{}},
		{"timestamp missing", `{"type":"assistant","message":{"id":"m","usage":{}}}`, want{}},
		{"bad timestamp", `{"type":"assistant","timestamp":"yesterday","message":{"id":"m","usage":{}}}`, want{}},
		{"not JSON", `"assistant" "usage" {`, want{}},
		// Go-only: keys match case-sensitively, like JSONSerialization.
		{"capitalized key", `{"Type":"assistant",` + ts + `,"message":{"id":"m","usage":{}}}`, want{}},
		// Go-only: the byte prefilter runs before decoding: an escaped key never matches it.
		{"escaped usage key", `{"type":"assistant",` + ts + `,"message":{"id":"m","us` + "\\u0061" + `ge":{"input_tokens":1}}}`, want{}},
		// Go-only: invalid UTF-8 rejects the line, like String(data:encoding:.utf8).
		{"invalid UTF-8", `{"type":"assistant",` + ts + `,"message":{"id":"m` + "\xff" + `","usage":{"input_tokens":1}}}`, want{}},
		// Go-only: any line with the bytes "cost-state" is skipped, even a real assistant record
		// (LocalUsageReader.swift:506-512).
		{"cost-state bytes", `{"type":"assistant",` + ts + `,"message":{"id":"m","usage":{"input_tokens":1},"content":[{"type":"tool_use","input":{"pattern":"cost-state"}}]}}`, want{}},
		{"cost-state record", `{"type":"cost-state","sessionId":"s","totalCostUSD":1,"modelUsage":{"claude-opus-5":{"costUSD":1}}}`, want{}},
	}
	for _, c := range cases {
		e, ok := parseLine([]byte(c.line), time.UTC)
		got := want{ok, e.ID, e.Model, e.Input, e.Output, e.CacheWrite, e.CacheRead}
		if !ok {
			got = want{}
		}
		if got != c.want {
			t.Errorf("%s: got %+v; want %+v", c.name, got, c.want)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	cases := []struct {
		in   string
		want string // RFC 3339 in UTC with milliseconds; "" means rejected
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:94 (the Claude log shape)
		{"2026-06-30T10:00:00.000Z", "2026-06-30T10:00:00.000Z"},
		// Swift: Tests/PokeTokenBarTests/AdditionalClaudeAccountsTests.swift:1552 (no fraction)
		{"2026-09-16T10:00:00Z", "2026-09-16T10:00:00.000Z"},
		// Swift: Tests/PokeTokenBarTests/PokeTokenBarTests.swift:179 (microseconds, +00:00)
		{"2026-06-10T11:10:00.034464+00:00", "2026-06-10T11:10:00.034Z"},
		// Go-only: the rest of Models.swift:898-912 — short fraction padded, empty fraction,
		// offset, and the shapes ISO8601DateFormatter rejects.
		{"2026-06-30T10:00:00.5Z", "2026-06-30T10:00:00.500Z"},
		{"2026-06-30T10:00:00.Z", "2026-06-30T10:00:00.000Z"},
		{"2026-06-30T19:00:00.123+09:00", "2026-06-30T10:00:00.123Z"},
		{"2026-06-30T10:00:00,123Z", ""},
		{"2026-06-30T10:00:00.123", ""},
		{"2026-06-30 10:00:00Z", ""},
		{"2026-06-30T10:00:00.12aZ", ""},
	}
	for _, c := range cases {
		got, ok := ParseTimestamp(c.in)
		s := ""
		if ok {
			s = got.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if s != c.want {
			t.Errorf("ParseTimestamp(%q) = %q; want %q", c.in, s, c.want)
		}
	}
}

func TestParseStream(t *testing.T) {
	l1 := claudeLine("1", "r1", "claude-opus-4-8", "2026-06-30T10:00:00.000Z", 1, 0, 0, 0)
	l2 := claudeLine("2", "r2", "claude-opus-4-8", "2026-06-30T10:00:00.000Z", 2, 0, 0, 0)
	big := strings.Replace(l2, `"usage"`, `"content":"`+strings.Repeat("x", 3<<20)+`","usage"`, 1)
	cases := []struct {
		name     string
		data     string
		ids      []string
		consumed int
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:329 (a trailing line is parsed, not consumed)
		{"trailing line", l1 + "\n" + l2, []string{"1|r1", "2|r2"}, len(l1) + 1},
		// Swift: Tests/PokeTokenBarTests/LocalUsageCacheTests.swift:336 (a half-written line is not an entry)
		{"half-written", l1 + "\n" + l2[:len(l2)/2], []string{"1|r1"}, len(l1) + 1},
		// Go-only: empty lines and CRLF endings.
		{"blank and CRLF", "\n" + l1 + "\r\n\n", []string{"1|r1"}, len(l1) + 4},
		// Go-only: a 3 MiB line, longer than the read buffer (bufio.Scanner would stop here).
		{"long line", l1 + "\n" + big + "\n" + l1 + "\n", []string{"1|r1", "2|r2"}, 2*len(l1) + len(big) + 3},
		{"empty", "", nil, 0},
	}
	for _, c := range cases {
		entries, consumed, err := ParseStream(strings.NewReader(c.data), "sess", time.UTC)
		var ids []string
		for _, e := range entries {
			ids = append(ids, e.ID)
			if len(e.Sessions) != 1 || e.Sessions[0] != "sess" {
				t.Errorf("%s: sessions %v", c.name, e.Sessions)
			}
		}
		if err != nil || fmt.Sprint(ids) != fmt.Sprint(c.ids) || consumed != int64(c.consumed) {
			t.Errorf("%s: ids %v consumed %d err %v; want %v %d", c.name, ids, consumed, err, c.ids, c.consumed)
		}
	}
}

func TestParseStreamReadError(t *testing.T) {
	// Go-only: an I/O error is returned, so the cache keeps the previous entries.
	l1 := claudeLine("1", "r1", "m", "2026-06-30T10:00:00.000Z", 1, 0, 0, 0)
	r := io.MultiReader(strings.NewReader(l1+"\n"), iotest.ErrReader(errors.New("EIO")))
	if _, _, err := ParseStream(r, "s", time.UTC); err == nil {
		t.Error("want the read error")
	}
}

func TestLocalDayUsesTheGivenZone(t *testing.T) {
	// Go-only: the day key is the local day of the timestamp (LocalUsageReader.swift:2184-2190).
	seoul, _ := time.LoadLocation("Asia/Seoul")
	line := claudeLine("a", "r", "m", "2026-06-30T20:00:00.000Z", 1, 0, 0, 0)
	if e, _ := parseLine([]byte(line), seoul); e.LocalDay != "2026-07-01" {
		t.Errorf("Seoul day = %s; want 2026-07-01", e.LocalDay)
	}
	if e, _ := parseLine([]byte(line), time.UTC); e.LocalDay != "2026-06-30" {
		t.Errorf("UTC day = %s; want 2026-06-30", e.LocalDay)
	}
}

func TestDedupKeepMax(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	e := func(id string, out int64, minute int, sessions ...string) usage.Entry {
		d := base.Add(time.Duration(minute) * time.Minute)
		return usage.Entry{ID: id, Date: d, LocalDay: usage.DayKey(d), Model: "m", Input: 100, Output: out, CacheRead: 1000, Sessions: sessions}
	}
	type kept struct {
		id       string
		out      int64
		minute   int
		sessions string
	}
	cases := []struct {
		name string
		in   []usage.Entry
		want []kept
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:92 (streamed twice: keep the completed output)
		{"keep max", []usage.Entry{e("A|R1", 5, 0, "s"), e("A|R1", 200, 0, "s"), e("B|R2", 10, 0, "s")},
			[]kept{{"A|R1", 200, 0, "[s]"}, {"B|R2", 10, 0, "[s]"}}},
		// Swift: Tests/PokeTokenBarTests/AdditionalClaudeAccountsTests.swift:1697 (fork replay, both scan orders)
		{"replay later", []usage.Entry{e("T", 0, 5, "source-session"), e("T", 0, 15, "fork-session")},
			[]kept{{"T", 0, 5, "[fork-session source-session]"}}},
		{"replay first", []usage.Entry{e("T", 0, 15, "fork-session"), e("T", 0, 5, "source-session")},
			[]kept{{"T", 0, 5, "[fork-session source-session]"}}},
		// Go-only: the larger total wins even when it is the later copy, and the date stays earliest.
		{"max is later", []usage.Entry{e("T", 1, 0, "a"), e("T", 9, 30, "b")},
			[]kept{{"T", 9, 0, "[a b]"}}},
		// Go-only: on a tie the first seen stays (LocalUsageReader.swift:387).
		{"tie keeps first", []usage.Entry{{ID: "T", Date: base, Model: "first", Output: 1}, {ID: "T", Date: base, Model: "second", Output: 1}},
			[]kept{{"T", 1, 0, "[]"}}},
	}
	for _, c := range cases {
		out := DedupKeepMax(c.in)
		var got []kept
		for _, o := range out {
			got = append(got, kept{o.ID, o.Output, int(o.Date.Sub(base).Minutes()), fmt.Sprint(o.Sessions)})
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: %v; want %v", c.name, got, c.want)
		}
		if c.name == "tie keeps first" && out[0].Model != "first" {
			t.Errorf("tie kept %q", out[0].Model)
		}
	}
	// Go-only: the kept entry's local day follows the earliest date.
	late := e("D", 1, 24*60, "s")
	early := e("D", 9, 0, "s")
	if got := DedupKeepMax([]usage.Entry{late, early}); got[0].LocalDay != early.LocalDay || got[0].Output != 9 {
		t.Errorf("day %s output %d", got[0].LocalDay, got[0].Output)
	}
}

func TestSessionID(t *testing.T) {
	const project = "/home/example/.claude/projects/-home-example-app"
	cases := []struct{ path, want string }{
		// Swift: Tests/PokeTokenBarTests/AdditionalClaudeAccountsTests.swift:1540
		{project + "/abc-123.jsonl", "abc-123"},
		// Swift: Tests/PokeTokenBarTests/AdditionalClaudeAccountsTests.swift:1541
		{project + "/abc-123/subagents/agent-9.jsonl", "abc-123"},
	}
	for _, c := range cases {
		if got := SessionID(c.path); got != c.want {
			t.Errorf("SessionID(%q) = %q; want %q", c.path, got, c.want)
		}
	}
}
