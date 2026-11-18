package claude

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"poketokenbar/helper/internal/usage"
)

// MaxTokenValue caps a parsed token count. Upstream maxParsedTokenValue is
// 1_000_000_000_000_000 (LocalUsageReader.swift:2204): far above real use, yet sums of capped
// values stay inside int64.
const MaxTokenValue = 1_000_000_000_000_000

var (
	needleCostState = []byte(`"cost-state"`)
	needleUsage     = []byte(`"usage"`)
	needleAssistant = []byte(`"assistant"`)
)

// SessionID is the session a transcript belongs to, from its path:
// <project>/<session>.jsonl, or <project>/<session>/subagents/<agent>.jsonl for a subagent,
// which counts with its parent session (LocalUsageReader.swift:538-544).
func SessionID(path string) string {
	parent := filepath.Dir(path)
	if filepath.Base(parent) == "subagents" {
		return filepath.Base(filepath.Dir(parent))
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// ParseStream reads JSONL transcript records from r (LocalUsageReader.swift:498-534). It
// returns the assistant entries, deduplicated with DedupKeepMax and tagged with session, and
// the bytes through the last newline. A trailing line without a newline is parsed but not
// counted as consumed. Lines of any length are supported.
func ParseStream(r io.Reader, session string, loc *time.Location) ([]usage.Entry, int64, error) {
	var sessions []string
	if session != "" {
		sessions = []string{session}
	}
	var entries []usage.Entry
	handle := func(line []byte) {
		if e, ok := parseLine(line, loc); ok {
			e.Sessions = sessions
			entries = append(entries, e)
		}
	}
	br := bufio.NewReaderSize(r, 256<<10)
	var long []byte
	var consumed int64
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long, chunk...)
			continue
		}
		line := chunk
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		if err == nil {
			consumed += int64(len(line))
			handle(line[:len(line)-1])
			long = long[:0]
			continue
		}
		if err == io.EOF {
			if len(line) > 0 {
				handle(line)
			}
			return DedupKeepMax(entries), consumed, nil
		}
		return nil, 0, err
	}
}

type rawRecord struct {
	Type      jsontext.Value `json:"type"`
	Timestamp jsontext.Value `json:"timestamp"`
	RequestID jsontext.Value `json:"requestId"`
	Message   *rawMessage    `json:"message"`
}

type rawMessage struct {
	ID    jsontext.Value `json:"id"`
	Model jsontext.Value `json:"model"`
	Usage *rawUsage      `json:"usage"`
}

type rawUsage struct {
	Input      jsontext.Value `json:"input_tokens"`
	Output     jsontext.Value `json:"output_tokens"`
	CacheWrite jsontext.Value `json:"cache_creation_input_tokens"`
	CacheRead  jsontext.Value `json:"cache_read_input_tokens"`
}

// parseLine applies the line rules of LocalUsageReader.swift:505-580. A line containing the
// bytes "cost-state" is never an assistant line, whether or not it parses as a ledger (the
// ledger itself is not used in M1). Other lines must contain "usage" and "assistant", be a
// JSON object with type "assistant", an object message with an object usage, and a
// parseable timestamp.
func parseLine(line []byte, loc *time.Location) (usage.Entry, bool) {
	if bytes.Contains(line, needleCostState) || len(line) == 0 ||
		!bytes.Contains(line, needleUsage) || !bytes.Contains(line, needleAssistant) {
		return usage.Entry{}, false
	}
	var rec rawRecord
	// JSON keys match case-sensitively; invalid UTF-8 rejects the line, as upstream's
	// String(data:encoding:.utf8) does.
	if json.Unmarshal(line, &rec, jsontext.AllowDuplicateNames(true)) != nil {
		return usage.Entry{}, false
	}
	if typ, ok := jsonString(rec.Type); !ok || typ != "assistant" || rec.Message == nil || rec.Message.Usage == nil {
		return usage.Entry{}, false
	}
	ts, ok := jsonString(rec.Timestamp)
	if !ok {
		return usage.Entry{}, false
	}
	date, ok := ParseTimestamp(ts)
	if !ok {
		return usage.Entry{}, false
	}
	model, ok := jsonString(rec.Message.Model)
	if !ok {
		model = "unknown"
	}
	msgID, _ := jsonString(rec.Message.ID)
	reqID, _ := jsonString(rec.RequestID)
	u := rec.Message.Usage
	return usage.Entry{
		ID:         msgID + "|" + reqID,
		Date:       date,
		LocalDay:   usage.DayKey(date.In(loc)),
		Model:      model,
		Input:      tokenValue(u.Input),
		Output:     tokenValue(u.Output),
		CacheWrite: tokenValue(u.CacheWrite),
		CacheRead:  tokenValue(u.CacheRead),
	}, true
}

func jsonString(v jsontext.Value) (string, bool) {
	if len(v) == 0 || v.Kind() != '"' {
		return "", false
	}
	var s string
	_ = json.Unmarshal(v, &s) // cannot fail: v is a string the record decode already validated
	return s, true
}

// tokenValue converts a JSON value to a token count (LocalUsageReader.swift:2212-2227):
// non-numbers and null give 0, negatives give 0, fractions truncate, and huge values clamp to
// MaxTokenValue. A JSON boolean counts as 1 or 0, because upstream reads it as an NSNumber.
func tokenValue(v jsontext.Value) int64 {
	if len(v) == 0 {
		return 0
	}
	switch v.Kind() {
	case 't':
		return 1
	case '0':
	default:
		return 0
	}
	// A range error is an overflow to ±Inf (non-finite upstream) or an underflow to 0.
	d, err := strconv.ParseFloat(string(v), 64)
	if err != nil || d <= 0 {
		return 0
	}
	if d >= MaxTokenValue {
		return MaxTokenValue
	}
	return int64(d)
}

// ParseTimestamp parses an ISO-8601 timestamp the way upstream's ISO8601Parser does
// (Models.swift:874-914): a date-time with Z or ±HH:MM, an optional fraction truncated or
// padded to milliseconds.
func ParseTimestamp(s string) (time.Time, bool) {
	// Go also accepts a comma before the fraction; ISO8601DateFormatter does not.
	if strings.ContainsRune(s, ',') {
		return time.Time{}, false
	}
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		if tz := strings.IndexAny(s[dot+1:], "Z+-"); tz >= 0 {
			tz += dot + 1
			frac := s[dot+1 : tz]
			if len(frac) > 3 {
				frac = frac[:3]
			}
			frac += strings.Repeat("0", 3-len(frac))
			s = s[:dot+1] + frac + s[tz:]
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// DedupKeepMax merges entries that share an ID, as a streamed or replayed request is logged
// several times (LocalUsageReader.swift:380-397):
//   - the entry with the larger total wins (the completed one); on a tie the first seen stays;
//   - the date and local day become the earliest of the copies (a fork replays a turn later);
//   - the sessions become the sorted union.
//
// Order of first appearance is kept. Apply it per file, on tail merges, and across all files.
func DedupKeepMax(entries []usage.Entry) []usage.Entry {
	index := make(map[string]int, len(entries))
	out := make([]usage.Entry, 0, len(entries))
	for _, e := range entries {
		i, ok := index[e.ID]
		if !ok {
			index[e.ID] = len(out)
			out = append(out, e)
			continue
		}
		cur := out[i]
		kept := cur
		if e.Total() > cur.Total() {
			kept = e
		}
		if e.Date.Before(cur.Date) {
			kept.Date, kept.LocalDay = e.Date, e.LocalDay
		} else {
			kept.Date, kept.LocalDay = cur.Date, cur.LocalDay
		}
		kept.Sessions = unionSorted(cur.Sessions, e.Sessions)
		out[i] = kept
	}
	return out
}

// unionSorted returns a new sorted slice of the distinct values of a and b.
func unionSorted(a, b []string) []string {
	u := make([]string, 0, len(a)+len(b))
	u = append(append(u, a...), b...)
	slices.Sort(u)
	return slices.Compact(u)
}
