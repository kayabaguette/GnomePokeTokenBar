package usage

import (
	"fmt"
	"time"
)

// DailyUsage is one local day's totals.
type DailyUsage struct {
	Day string `json:"day"`
	Totals
	// Models breaks the day down by model id. Only Daily fills it; series leave it nil.
	Models map[string]Totals `json:"models,omitempty"`
}

// Daily sums the entries of one local day. It returns nil when the day has no tokens
// (LocalUsageReader.swift:2018-2029).
func Daily(entries []Entry, day string) *DailyUsage {
	d := DailyUsage{Day: day, Models: map[string]Totals{}}
	for _, e := range entries {
		if e.LocalDay != day {
			continue
		}
		d.Add(e)
		m := d.Models[e.Model]
		m.Add(e)
		d.Models[e.Model] = m
	}
	if d.Tokens <= 0 {
		return nil
	}
	return &d
}

// Period sums the entries whose local day lies in [from, to], both inclusive
// (LocalUsageReader.swift:2032-2036). Day keys sort like dates.
func Period(entries []Entry, from, to string) Totals {
	var t Totals
	for _, e := range entries {
		if e.LocalDay >= from && e.LocalDay <= to {
			t.Add(e)
		}
	}
	return t
}

// DailySeries returns one DailyUsage per local day from start's day through end's day, both
// inclusive, stepping one calendar day at a time. Days without entries are present with zero
// totals, and entries outside the axis are dropped (LocalUsageReader.swift:2061-2089).
// start and end should share a location.
func DailySeries(entries []Entry, start, end time.Time) []DailyUsage {
	y, m, d := start.Date()
	last := DayKey(StartOfDay(end))
	var days []DailyUsage
	index := map[string]int{}
	for i := 0; ; i++ {
		key := DayKey(midnight(y, m, d+i, start.Location()))
		if key > last {
			break
		}
		index[key] = len(days)
		days = append(days, DailyUsage{Day: key})
	}
	for _, e := range entries {
		if i, ok := index[e.LocalDay]; ok {
			days[i].Add(e)
		}
	}
	return days
}

// Block is the rolling 5-hour usage block behind the burn rate and the depletion forecast.
type Block struct {
	// ID is "block-<unix seconds of the first entry>".
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Totals
	TokensPerMinute float64 `json:"tokensPerMinute"`
}

// ActiveBlock sums the entries with tokens in the BlockWindow ending at now. It returns nil
// when there are none; zero-token records neither create a block nor move its start
// (LocalUsageReader.swift:2114-2132).
func ActiveBlock(entries []Entry, now time.Time) *Block {
	windowStart := now.Add(-BlockWindow)
	var b Block
	found := false
	for _, e := range entries {
		if e.Date.Before(windowStart) || e.Total() <= 0 {
			continue
		}
		if !found || e.Date.Before(b.Start) {
			b.Start = e.Date
		}
		found = true
		b.Add(e)
	}
	if !found {
		return nil
	}
	minutes := max(1, now.Sub(b.Start).Seconds()/60)
	b.TokensPerMinute = float64(b.Tokens) / minutes
	b.ID = fmt.Sprintf("block-%d", b.Start.Unix())
	b.End = b.Start.Add(BlockWindow)
	return &b
}

// BurnTier classifies the combined tokens-per-minute of all active blocks.
type BurnTier string

// Burn tiers (UsageStore.swift:878-884).
const (
	BurnIdle    BurnTier = "idle"
	BurnNormal  BurnTier = "normal"
	BurnFast    BurnTier = "fast"
	BurnBlazing BurnTier = "blazing"
)

// BurnTierFor maps a summed tokens-per-minute rate onto a tier: <= 1,000 idle, < 100,000
// normal, < 400,000 fast, else blazing.
func BurnTierFor(tokensPerMinute float64) BurnTier {
	switch {
	case !(tokensPerMinute > 1_000):
		return BurnIdle
	case tokensPerMinute < 100_000:
		return BurnNormal
	case tokensPerMinute < 400_000:
		return BurnFast
	default:
		return BurnBlazing
	}
}

// Forecast is when the 5-hour limit would reach 100 % at the current burn rate.
type Forecast struct {
	DepletionAt time.Time `json:"depletionAt"`
	BeforeReset bool      `json:"beforeReset"`
}

// ForecastDepletion extrapolates when utilization reaches 100 %, estimating the limit size
// from the block's tokens per percent. It returns false when the estimate is unstable:
// utilization below 5 % or at 100 % or more, no block tokens, a burn below 10,000 tokens per
// minute, or a result a day or more away (UsageStore.swift:713-722).
func ForecastDepletion(blockTokens int64, tokensPerMinute, utilization float64, now time.Time) (time.Time, bool) {
	if !(utilization >= 5) || utilization >= 100 || blockTokens <= 0 || !(tokensPerMinute >= 10_000) {
		return time.Time{}, false
	}
	tokensPerPercent := float64(blockTokens) / utilization
	minutesLeft := (100 - utilization) * tokensPerPercent / tokensPerMinute
	if !(minutesLeft < 24*60) {
		return time.Time{}, false
	}
	return now.Add(time.Duration(minutesLeft * float64(time.Minute))), true
}

// FiveHourForecast combines the 5-hour window's utilization and reset with the active block
// (UsageStore.swift:693-703). At 100 % or more the limit is already reached. It returns nil
// without a block or when ForecastDepletion declines.
func FiveHourForecast(utilization float64, reset time.Time, block *Block, now time.Time) *Forecast {
	if utilization >= 100 {
		return &Forecast{DepletionAt: now, BeforeReset: true}
	}
	if block == nil {
		return nil
	}
	at, ok := ForecastDepletion(block.Tokens, block.TokensPerMinute, utilization, now)
	if !ok {
		return nil
	}
	return &Forecast{DepletionAt: at, BeforeReset: at.Before(reset)}
}

// Enrichment is everything a provider derives from its wider scan. A false OK flag means the
// caller keeps its previous values (UsageProvider.swift:24-39).
type Enrichment struct {
	ActiveBlock *Block
	BlocksOK    bool
	Week        Totals // week start through today
	Month       Totals // month start through today
	MonthDaily  []DailyUsage
	RecentDaily []DailyUsage // RecentDays days ending today
	// LastUsage is the newest entry with tokens; zero if none. It keeps a provider used in
	// the last RecentUseWindow visible on idle days.
	LastUsage time.Time
	PeriodsOK bool
}

// EnrichmentFromEntries assembles the enrichment from one entry set. Every local provider
// shares it (UsageProvider.swift:50-78), with the calendar in now's location.
func EnrichmentFromEntries(entries []Entry, now time.Time, weekStart time.Weekday) Enrichment {
	today := DayKey(now)
	r := Enrichment{
		ActiveBlock: ActiveBlock(entries, now),
		BlocksOK:    true,
		Week:        Period(entries, DayKey(StartOfWeek(now, weekStart)), today),
		Month:       Period(entries, DayKey(StartOfMonth(now)), today),
		MonthDaily:  DailySeries(entries, StartOfMonth(now), now),
		RecentDaily: DailySeries(entries, StartOfRollingWindow(now, RecentDays), now),
		PeriodsOK:   true,
	}
	for _, e := range entries {
		if e.Total() > 0 && e.Date.After(r.LastUsage) {
			r.LastUsage = e.Date
		}
	}
	return r
}

// Provider is a usage source (UsageProvider.swift:4-17). New sources implement it; generic
// aggregation must never branch on a provider id.
type Provider interface {
	ID() string
	DisplayName() string
	// ReportsCost tells whether this provider contributes to cost aggregates.
	ReportsCost() bool
	// FetchDaily returns today's totals, or nil when there is no usage today. It is the
	// critical path: an error stops the "last updated" clock.
	FetchDaily(now time.Time) (*DailyUsage, error)
	// FetchEnrichment is best effort and never fails; see Enrichment's OK flags.
	FetchEnrichment(now time.Time, weekStart time.Weekday) Enrichment
}
