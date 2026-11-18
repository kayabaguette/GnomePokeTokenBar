package usage

import (
	"fmt"
	"maps"
	"time"
)

// Ledger keeps day-by-day token totals across months, so history and the streak survive the
// 1st of the month (UsageRecap.swift:9-62). It holds numbers the app already derived and
// rebuilds itself from the next refresh.
type Ledger struct {
	TokensByDay map[string]int64 `json:"tokensByDay"`
	// CoveredSince is the first day the ledger vouches for: from there on a missing day means
	// "no usage"; before it nothing was recorded. Empty when unknown.
	CoveredSince string `json:"coveredSince"`
}

// Tokens returns the recorded tokens of day.
func (l Ledger) Tokens(day string) int64 { return l.TokensByDay[day] }

// Merge records a series. A day never goes down: a rotated or pruned log reports less for a
// day that really happened. The series lists every day of its window, empty ones included, so
// its first day extends the coverage.
func (l *Ledger) Merge(series []DailyUsage) {
	if l.TokensByDay == nil {
		l.TokensByDay = map[string]int64{}
	}
	first := ""
	for _, d := range series {
		if d.Tokens > 0 && d.Tokens > l.TokensByDay[d.Day] {
			l.TokensByDay[d.Day] = d.Tokens
		}
		if first == "" || d.Day < first {
			first = d.Day
		}
	}
	if first != "" && (l.CoveredSince == "" || first < l.CoveredSince) {
		l.CoveredSince = first
	}
}

// Prune drops days before cutoff. The dropped days are unknown again, not empty, so the
// coverage moves up with them.
func (l *Ledger) Prune(cutoff string) {
	maps.DeleteFunc(l.TokensByDay, func(day string, _ int64) bool { return day < cutoff })
	if l.CoveredSince != "" && l.CoveredSince < cutoff {
		l.CoveredSince = cutoff
	}
}

// LedgerCutoff is January 1 of the year before now's: the history keeps this year and the
// last (UsageStore.swift:661-673).
func LedgerCutoff(now time.Time) string { return fmt.Sprintf("%04d-01-01", now.Year()-1) }

// Record merges the month series of a refresh, then prunes. Call it after the enrichment
// phase, which fills the series (upstream defect log :648-654). It reports whether the ledger
// changed and so needs saving.
func (l *Ledger) Record(monthDaily []DailyUsage, now time.Time) bool {
	before := Ledger{TokensByDay: maps.Clone(l.TokensByDay), CoveredSince: l.CoveredSince}
	l.Merge(monthDaily)
	l.Prune(LedgerCutoff(now))
	return l.CoveredSince != before.CoveredSince || !maps.Equal(l.TokensByDay, before.TokensByDay)
}

// Sanitize keeps only days that parse as day keys and hold tokens, and clears a coverage date
// that is not a day key. Call it after loading: a junk key sorting below every real day would
// otherwise backdate the coverage.
func (l *Ledger) Sanitize() {
	maps.DeleteFunc(l.TokensByDay, func(day string, tokens int64) bool {
		_, err := time.Parse(DayLayout, day)
		return err != nil || tokens <= 0
	})
	if _, err := time.Parse(DayLayout, l.CoveredSince); err != nil {
		l.CoveredSince = ""
	}
}

// Streak is the run of consecutive active days that is still going.
type Streak struct {
	Days int `json:"days"`
	// IncludesToday is false while today has no usage yet: the run then ends yesterday.
	IncludesToday bool `json:"includesToday"`
}

// Streak counts consecutive days with tokens, from today if today is active, else from
// yesterday, stepping back one calendar day at a time in now's location
// (UsageRecap.swift:83-97).
func (l Ledger) Streak(now time.Time) Streak {
	y, m, d := now.Date()
	active := func(offset int) bool { return l.Tokens(DayKey(midnight(y, m, d+offset, now.Location()))) > 0 }
	s := Streak{IncludesToday: active(0)}
	offset := -1
	if s.IncludesToday {
		offset = 0
	}
	for ; active(offset); offset-- {
		s.Days++
	}
	return s
}
