// Command ptb-usage-debug scans the local Claude Code transcripts and prints today, this week,
// this month, the 21-day series, the active block, the burn tier and scan timings as JSON.
// It prints aggregate numbers only, never message content.
//
//	go run ./cmd/ptb-usage-debug [-cache DIR] [-week-start monday] [-root DIR]...
package main

import (
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"poketokenbar/helper/internal/claude"
	"poketokenbar/helper/internal/usage"
)

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

type rootsFlag []string

func (r *rootsFlag) String() string     { return strings.Join(*r, ",") }
func (r *rootsFlag) Set(v string) error { *r = append(*r, v); return nil }

type timing struct {
	DailyMs      float64         `json:"dailyMs"`
	EnrichmentMs float64         `json:"enrichmentMs"`
	Daily        usage.ScanStats `json:"daily"`
	Enrichment   usage.ScanStats `json:"enrichment"`
}

type series struct {
	Start    string    `json:"start"`
	Tokens   []int64   `json:"tokens"`
	CostUSD  []float64 `json:"costUsd"`
	Coverage []string  `json:"coverage"`
}

type period struct {
	From string `json:"from"`
	To   string `json:"to"`
	usage.Totals
}

type block struct {
	Tokens          int64      `json:"tokens"`
	TokensPerMinute float64    `json:"tokensPerMinute"`
	Start           time.Time  `json:"start"`
	End             time.Time  `json:"end"`
	Cost            usage.Cost `json:"cost"`
}

type output struct {
	Now          time.Time        `json:"now"`
	Zone         string           `json:"zone"`
	WeekStart    string           `json:"weekStart"`
	Roots        int              `json:"roots"`
	Today        usage.DailyUsage `json:"today"`
	Week         period           `json:"week"`
	Month        period           `json:"month"`
	RecentDaily  series           `json:"recentDaily"`
	MonthDaily   series           `json:"monthDaily"`
	ActiveBlock  *block           `json:"activeBlock"`
	BurnTier     usage.BurnTier   `json:"burnTier"`
	LastUsage    *time.Time       `json:"lastUsage"`
	Cold         timing           `json:"firstPass"`
	Warm         timing           `json:"secondPass"`
	FlushMs      float64          `json:"flushMs"`
	CacheDirTemp bool             `json:"cacheDirTemp"`
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("ptb-usage-debug: ")
	cacheDir := flag.String("cache", "", "cache directory (default: a temporary directory, removed at exit)")
	weekStart := flag.String("week-start", "monday", "first day of the week")
	var roots rootsFlag
	flag.Var(&roots, "root", "projects root to scan instead of discovery (repeatable)")
	flag.Parse()

	ws, ok := weekdays[strings.ToLower(*weekStart)]
	if !ok {
		log.Fatalf("unknown -week-start %q", *weekStart)
	}
	temp := *cacheDir == ""
	if temp {
		dir, err := os.MkdirTemp("", "ptb-usage-debug-")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(dir)
		*cacheDir = dir
	}

	p := claude.New(claude.Options{Roots: roots, CacheDir: *cacheDir})
	now := time.Now()
	out := output{Now: now.UTC().Truncate(time.Second), Zone: now.Format("MST -07:00"), WeekStart: ws.String(),
		Roots: len(p.Roots()), CacheDirTemp: temp}

	pass := func() ([]usage.Entry, []usage.Entry, timing) {
		var t timing
		start := time.Now()
		daily, st := p.Scan(usage.StartOfDay(now))
		t.DailyMs, t.Daily = ms(time.Since(start)), st
		start = time.Now()
		wide, st := p.Scan(usage.EnrichmentScanStart(now, ws))
		t.EnrichmentMs, t.Enrichment = ms(time.Since(start)), st
		return daily, wide, t
	}
	daily, wide, cold := pass()
	_, _, warm := pass()
	out.Cold, out.Warm = cold, warm
	start := time.Now()
	if err := p.Flush(); err != nil {
		log.Printf("flush: %v", err)
	}
	out.FlushMs = ms(time.Since(start))

	today := usage.DayKey(now)
	out.Today = usage.DailyUsage{Day: today}
	if d := usage.Daily(daily, today); d != nil {
		out.Today = *d
	}
	enr := usage.EnrichmentFromEntries(wide, now, ws)
	out.Week = period{From: usage.DayKey(usage.StartOfWeek(now, ws)), To: today, Totals: enr.Week}
	out.Month = period{From: usage.DayKey(usage.StartOfMonth(now)), To: today, Totals: enr.Month}
	out.RecentDaily = toSeries(enr.RecentDaily)
	out.MonthDaily = toSeries(enr.MonthDaily)
	out.BurnTier = usage.BurnTierFor(0)
	if b := enr.ActiveBlock; b != nil {
		out.ActiveBlock = &block{Tokens: b.Tokens, TokensPerMinute: round(b.TokensPerMinute, 1),
			Start: b.Start.UTC(), End: b.End.UTC(), Cost: b.Cost}
		out.BurnTier = usage.BurnTierFor(b.TokensPerMinute)
	}
	if !enr.LastUsage.IsZero() {
		t := enr.LastUsage.UTC()
		out.LastUsage = &t
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		log.Fatal(err)
	}
}

func toSeries(days []usage.DailyUsage) series {
	s := series{Tokens: []int64{}, CostUSD: []float64{}, Coverage: []string{}}
	if len(days) > 0 {
		s.Start = days[0].Day
	}
	for _, d := range days {
		s.Tokens = append(s.Tokens, d.Tokens)
		s.CostUSD = append(s.CostUSD, round(d.Cost.USD, 4))
		s.Coverage = append(s.Coverage, d.Cost.Coverage.String())
	}
	return s
}

func ms(d time.Duration) float64 { return round(float64(d)/float64(time.Millisecond), 1) }

func round(v float64, places int) float64 {
	f := math.Pow(10, float64(places))
	return math.Round(v*f) / f
}
