package usage

import (
	"time"
)

// History is what the recap and the heatmap read: the day ledger with the live month series laid
// over it. Per day, tokens = max(ledger, series), so the two views agree with the bars even when
// the ledger trails a refresh (AH:117-123; docs/protocol.md §5.2 Heatmap, §7.5).
type History struct {
	tokens map[string]int64
	since  string
}

// NewHistory overlays series (the combined month series) on the ledger. RecordedSince is the
// earlier of the ledger's coverage and the series' first day, which is the live series' start even
// when that day had no usage: the series lists every day of its window.
func NewHistory(l Ledger, series []DailyUsage) History {
	h := History{tokens: make(map[string]int64, len(l.TokensByDay)+len(series)), since: l.CoveredSince}
	for day, t := range l.TokensByDay {
		h.tokens[day] = t
	}
	for i, d := range series {
		if d.Tokens > h.tokens[d.Day] {
			h.tokens[d.Day] = d.Tokens
		}
		if i == 0 && (h.since == "" || d.Day < h.since) {
			h.since = d.Day
		}
	}
	return h
}

// RecordedSince is the first day the history vouches for, or "" when nothing was ever recorded.
func (h History) RecordedSince() string { return h.since }

// Tokens returns the day's tokens (0 for an unrecorded day).
func (h History) Tokens(day string) int64 { return h.tokens[day] }

// Covers reports whether a missing day means "no usage" (UsageRecap.swift:20).
func (h History) Covers(day string) bool { return h.since != "" && day >= h.since }

// Known is a day the history can speak for: it has usage or lies in the covered range (UR:206).
func (h History) Known(day string) bool { return h.Tokens(day) > 0 || h.Covers(day) }

// civilKey is the day key of the civil date (y, m, d); d may overflow.
func civilKey(y int, m time.Month, d int) string {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Format(DayLayout)
}

// RecapScope is the length of a recap period.
type RecapScope string

// The three scopes of the usage recap (UR:100).
const (
	ScopeWeek  RecapScope = "week"
	ScopeMonth RecapScope = "month"
	ScopeYear  RecapScope = "year"
)

// RecapPeriod is a calendar week, month or year in local time (UR:112-143). Start is the first instant
// and End the first instant after, both local midnights.
type RecapPeriod struct {
	Scope      RecapScope
	Start, End time.Time
}

// NewRecapPeriod returns the running period of the scope that contains t. Weeks start on first.
func NewRecapPeriod(scope RecapScope, t time.Time, first time.Weekday) RecapPeriod {
	var start time.Time
	switch scope {
	case ScopeWeek:
		start = StartOfWeek(t, first)
	case ScopeMonth:
		start = StartOfMonth(t)
	default:
		start = midnight(t.Year(), time.January, 1, t.Location())
	}
	return RecapPeriod{Scope: scope, Start: start}.at(0)
}

// Shifted returns the period n scopes away (negative is earlier). A month shifts from its 1st, so
// "31 March - 1 month" is February, never March 3 (URT:212-224).
func (p RecapPeriod) Shifted(n int) RecapPeriod { return p.at(n) }

// at recomputes Start and End from p.Start's civil date shifted by n scopes.
func (p RecapPeriod) at(n int) RecapPeriod {
	loc := p.Start.Location()
	y, m, d := p.Start.Date()
	switch p.Scope {
	case ScopeWeek:
		p.Start, p.End = midnight(y, m, d+7*n, loc), midnight(y, m, d+7*(n+1), loc)
	case ScopeMonth:
		p.Start, p.End = midnight(y, m+time.Month(n), 1, loc), midnight(y, m+time.Month(n+1), 1, loc)
	default:
		p.Start, p.End = midnight(y+n, time.January, 1, loc), midnight(y+n+1, time.January, 1, loc)
	}
	return p
}

// Days lists the day keys of the period, in order.
func (p RecapPeriod) Days() []string {
	last := DayKey(p.End)
	var keys []string
	y, m, d := p.Start.Date()
	for i := 0; ; i++ {
		k := civilKey(y, m, d+i)
		if k >= last {
			return keys
		}
		keys = append(keys, k)
	}
}

// Bucket is one meter column of the recap: a day (week, month) or a month (year).
type Bucket struct {
	// Key is the day, or the first day of the month for a year bucket.
	Key       string `json:"key"`
	Tokens    int64  `json:"tokens"`
	IsCurrent bool   `json:"isCurrent"`
	HasData   bool   `json:"hasData"`
}

// BestDay is the busiest day of a period.
type BestDay struct {
	Key    string `json:"key"`
	Tokens int64  `json:"tokens"`
}

// Recap is UsageRecap.make's result (UR:190-268) without the graduates, which come from the
// companion. Start and End are inclusive day keys.
type Recap struct {
	Scope        RecapScope `json:"scope"`
	Start        string     `json:"start"`
	End          string     `json:"end"`
	Total        int64      `json:"total"`
	IsInProgress bool       `json:"isInProgress"`
	// PreviousTotal is nil when the compared range is not covered (judged on its oldest day).
	PreviousTotal *int64   `json:"previousTotal"`
	BestDay       *BestDay `json:"bestDay"`
	ActiveDays    int      `json:"activeDays"`
	CountedDays   int      `json:"countedDays"`
	BestStreak    int      `json:"bestStreak"`
	CanGoBack     bool     `json:"canGoBack"`
	Buckets       []Bucket `json:"buckets"`
}

// MakeRecap computes the recap of period p as of now (UR:190-268), all in civil-date space.
func MakeRecap(h History, p RecapPeriod, now time.Time) Recap {
	today := DayKey(now)
	days := p.Days()
	r := Recap{Scope: p.Scope, Start: days[0], End: days[len(days)-1], Buckets: []Bucket{}}
	r.IsInProgress = days[0] <= today && today < DayKey(p.End)

	var elapsed []string
	for _, k := range days {
		if k <= today {
			elapsed = append(elapsed, k)
		}
	}
	run := 0
	for _, k := range elapsed {
		t := h.Tokens(k)
		r.Total += t
		if h.Known(k) {
			r.CountedDays++
		}
		if t <= 0 {
			run = 0
			continue
		}
		r.ActiveDays++
		run++
		r.BestStreak = max(r.BestStreak, run)
		// Strictly greater: the earliest day wins a tie (the port's decision, protocol §7.5).
		if r.BestDay == nil || t > r.BestDay.Tokens {
			r.BestDay = &BestDay{Key: k, Tokens: t}
		}
	}

	if p.Scope == ScopeYear {
		r.Buckets = yearBuckets(h, p, today)
	} else {
		for _, k := range days {
			r.Buckets = append(r.Buckets, Bucket{Key: k, Tokens: h.Tokens(k), IsCurrent: k == today,
				HasData: k <= today && h.Known(k)})
		}
	}

	prev := p.Shifted(-1).Days()
	compared := prev
	if r.IsInProgress {
		compared = prev[:min(len(elapsed), len(prev))]
	}
	if len(compared) > 0 && h.Covers(compared[0]) {
		var sum int64
		for _, k := range compared {
			sum += h.Tokens(k)
		}
		r.PreviousTotal = &sum
	}
	r.CanGoBack = h.Covers(prev[len(prev)-1])
	return r
}

// yearBuckets is one bucket per month; a month with no elapsed day has no data (UR:229-237).
func yearBuckets(h History, p RecapPeriod, today string) []Bucket {
	out := make([]Bucket, 0, 12)
	for i := 0; i < 12; i++ {
		m := RecapPeriod{Scope: ScopeMonth, Start: p.Start}.at(i)
		start, end := DayKey(m.Start), DayKey(m.End)
		b := Bucket{Key: start, IsCurrent: start <= today && today < end}
		for _, k := range m.Days() {
			if k > today {
				break
			}
			b.Tokens += h.Tokens(k)
			b.HasData = b.HasData || h.Known(k)
		}
		out = append(out, b)
	}
	return out
}
