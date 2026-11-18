package daemon

import (
	"slices"
	"time"

	"poketokenbar/helper/internal/usage"
)

// dayTotals is one local day's buckets, with the coverage flags kept explicit so the persisted
// inputs decode again (usage.Coverage only marshals).
type dayTotals struct {
	Input      int64   `json:"in"`
	Output     int64   `json:"out"`
	CacheWrite int64   `json:"cw"`
	CacheRead  int64   `json:"cr"`
	USD        float64 `json:"usd"`
	Reported   bool    `json:"rep,omitempty"`
	Estimated  bool    `json:"est,omitempty"`
	Unknown    bool    `json:"unk,omitempty"`
}

func fromTotals(t usage.Totals) dayTotals {
	return dayTotals{Input: t.Input, Output: t.Output, CacheWrite: t.CacheWrite, CacheRead: t.CacheRead,
		USD: t.Cost.USD, Reported: t.Cost.Coverage.Reported, Estimated: t.Cost.Coverage.Estimated,
		Unknown: t.Cost.Coverage.Unknown}
}

func (d dayTotals) tokens() int64 { return d.Input + d.Output + d.CacheWrite + d.CacheRead }

func (d dayTotals) totals() usage.Totals {
	return usage.Totals{Tokens: d.tokens(), Input: d.Input, Output: d.Output, CacheWrite: d.CacheWrite,
		CacheRead: d.CacheRead, Cost: usage.Cost{USD: d.USD,
			Coverage: usage.Coverage{Reported: d.Reported, Estimated: d.Estimated, Unknown: d.Unknown}}}
}

// blockState is the rolling 5 h block as of the last scan.
type blockState struct {
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	Tokens          int64     `json:"tokens"`
	TokensPerMinute float64   `json:"tokensPerMinute"`
}

// providerUsage is everything the daemon keeps per usage provider. Every period is derived from the
// per-day buckets at build time, so today, week, month and both series always agree, across
// midnight, a week-start change and a rebuild from the persisted inputs (protocol §2.3, §5.2).
type providerUsage struct {
	Days      map[string]dayTotals `json:"days"`
	Block     *blockState          `json:"block,omitempty"`
	LastUsage *time.Time           `json:"lastUsage,omitempty"`
	Scanned   bool                 `json:"scanned"` // an enrichment was applied at least once

	scanErr error // the last scan failed; not persisted
}

func newProviderUsage() *providerUsage { return &providerUsage{Days: map[string]dayTotals{}} }

// apply replaces the provider's data with one scan. Days outside the scan's windows are dropped.
func (p *providerUsage) apply(e usage.Enrichment) {
	days := map[string]dayTotals{}
	for _, series := range [][]usage.DailyUsage{e.MonthDaily, e.RecentDaily} {
		for _, d := range series {
			if d.Tokens > 0 {
				days[d.Day] = fromTotals(d.Totals)
			}
		}
	}
	p.Days = days
	p.Block = nil
	if b := e.ActiveBlock; b != nil {
		p.Block = &blockState{Start: b.Start, End: b.End, Tokens: b.Tokens, TokensPerMinute: b.TokensPerMinute}
	}
	p.LastUsage = nil
	if !e.LastUsage.IsZero() {
		t := e.LastUsage
		p.LastUsage = &t
	}
	p.Scanned = true
	p.scanErr = nil
}

// day returns the totals of one day key.
func (p *providerUsage) day(key string) usage.Totals { return p.Days[key].totals() }

// period sums the days in [from, to], in day order so float sums are reproducible.
func (p *providerUsage) period(from, to string) usage.Totals {
	var keys []string
	for k := range p.Days {
		if k >= from && k <= to {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var t usage.Totals
	for _, k := range keys {
		t.Merge(p.day(k))
	}
	return t
}

// series returns one entry per calendar day from start's day through end's day.
func (p *providerUsage) series(start, end time.Time) []usage.DailyUsage {
	keys := dayKeys(start, end)
	out := make([]usage.DailyUsage, len(keys))
	for i, k := range keys {
		out[i] = usage.DailyUsage{Day: k, Totals: p.day(k)}
	}
	return out
}

// dayKeys lists the day keys from start's day through end's day, stepping one calendar day.
func dayKeys(start, end time.Time) []string {
	last := usage.DayKey(end)
	var keys []string
	y, m, d := start.Date()
	for i := 0; ; i++ {
		k := usage.DayKey(time.Date(y, m, d+i, 12, 0, 0, 0, start.Location()))
		if k > last {
			return keys
		}
		keys = append(keys, k)
	}
}

// activeBlock returns the block if it has not ended at now.
func (p *providerUsage) activeBlock(now time.Time) *blockState {
	if p.Block == nil || !p.Block.End.After(now) {
		return nil
	}
	return p.Block
}

// displayable is upstream hasDisplayableUsage (UsageStore.swift:2305-2315): usage today, in the
// block, week, month or either series, or a last use within the recent-use window.
func (p *providerUsage) displayable(now time.Time, weekStart time.Weekday) bool {
	today := usage.DayKey(now)
	from := usage.DayKey(usage.StartOfRollingWindow(now, usage.RecentDays))
	if m := usage.DayKey(usage.StartOfMonth(now)); m < from {
		from = m
	}
	if w := usage.DayKey(usage.StartOfWeek(now, weekStart)); w < from {
		from = w
	}
	for k, d := range p.Days {
		if k >= from && k <= today && d.tokens() > 0 {
			return true
		}
	}
	if b := p.activeBlock(now); b != nil && b.Tokens > 0 {
		return true
	}
	return p.LastUsage != nil && now.Sub(*p.LastUsage) <= usage.RecentUseWindow
}
