package claude

import (
	"io"
	"path/filepath"
	"time"

	"poketokenbar/helper/internal/usage"
)

// ParserVersion invalidates cached entries when the entry semantics change. Bump it when the
// record rules or bucket mapping change, not for price-only changes (upstream defect log
// :102-104).
const ParserVersion = 1

// CacheFileName is the cache file of this provider inside Options.CacheDir.
const CacheFileName = "claude-usage-cache.json.zz"

// Options configures a Provider.
type Options struct {
	// Roots overrides root discovery; nil means ProjectRoots(EnvFromOS()) at each scan.
	Roots []string
	// CacheDir persists the parse cache; empty keeps it in memory only.
	CacheDir string
	// Loc is the zone of day keys; nil means time.Local.
	Loc *time.Location
	// Now is the cache clock (pruning, save throttle); nil means time.Now.
	Now func() time.Time
}

// Provider reads Claude Code usage from local transcripts (LocalUsageProvider.swift:4-21).
type Provider struct {
	roots []string
	env   Env
	loc   *time.Location
	cache *usage.FileCache
}

var _ usage.Provider = (*Provider)(nil)

// New returns a provider.
func New(o Options) *Provider {
	p := &Provider{roots: o.Roots, loc: o.Loc}
	if p.loc == nil {
		p.loc = time.Local
	}
	if p.roots == nil {
		p.env = EnvFromOS()
	}
	p.cache = usage.NewFileCache(usage.CacheOptions{
		Dir: o.CacheDir, Name: CacheFileName, Version: ParserVersion, Now: o.Now,
	})
	return p
}

// ID implements usage.Provider.
func (p *Provider) ID() string { return "claude_code" }

// DisplayName implements usage.Provider.
func (p *Provider) DisplayName() string { return "Claude Code" }

// ReportsCost implements usage.Provider.
func (p *Provider) ReportsCost() bool { return true }

// Roots returns the roots a scan reads now.
func (p *Provider) Roots() []string {
	if p.roots != nil {
		return p.roots
	}
	return ProjectRoots(p.env)
}

// Scan returns the entries of every transcript modified at or after since, deduplicated
// across files and roots.
func (p *Provider) Scan(since time.Time) ([]usage.Entry, usage.ScanStats) {
	files := usage.ListFiles(p.Roots(), since, func(name string) bool { return filepath.Ext(name) == ".jsonl" })
	entries, stats := p.cache.Collect(files, p.parse, mergeTail)
	return DedupKeepMax(entries), stats
}

func (p *Provider) parse(r io.Reader, path string) ([]usage.Entry, int64, error) {
	return ParseStream(r, SessionID(path), p.loc)
}

func mergeTail(prior, tail []usage.Entry) []usage.Entry {
	all := make([]usage.Entry, 0, len(prior)+len(tail))
	return DedupKeepMax(append(append(all, prior...), tail...))
}

// FetchDaily implements usage.Provider: it scans the files modified since local midnight and
// returns today's totals, or nil when there are none. It never fails.
func (p *Provider) FetchDaily(now time.Time) (*usage.DailyUsage, error) {
	now = now.In(p.loc)
	entries, _ := p.Scan(usage.StartOfDay(now))
	return usage.Daily(entries, usage.DayKey(now)), nil
}

// FetchEnrichment implements usage.Provider: it scans from usage.EnrichmentScanStart.
func (p *Provider) FetchEnrichment(now time.Time, weekStart time.Weekday) usage.Enrichment {
	now = now.In(p.loc)
	entries, _ := p.Scan(usage.EnrichmentScanStart(now, weekStart))
	return usage.EnrichmentFromEntries(entries, now, weekStart)
}

// Flush saves the parse cache now. Call it before exiting.
func (p *Provider) Flush() error { return p.cache.Flush() }
