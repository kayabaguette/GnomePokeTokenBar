package daemon

import (
	"context"
	"time"

	"poketokenbar/helper/internal/claude"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/usage"
)

// UsageSource is a usage provider as the daemon drives it. Scan does ONE scan per refresh and
// derives everything from it, so today's totals (the last day of RecentDaily) and the enrichment
// can never disagree (protocol §5.2). Generic code never branches on ID.
type UsageSource interface {
	ID() string
	DisplayName() string
	// ShortName labels the top-bar limit item ("Claude 62%").
	ShortName() string
	ReportsCost() bool
	// Scan returns the provider's enrichment at now. An error stops the "last updated" clock.
	Scan(now time.Time, weekStart time.Weekday) (usage.Enrichment, error)
	// Flush saves the provider's parse cache.
	Flush() error
}

// ClaudeSource adapts the Claude Code provider.
type ClaudeSource struct{ P *claude.Provider }

var _ UsageSource = ClaudeSource{}

// ID implements UsageSource.
func (c ClaudeSource) ID() string { return c.P.ID() }

// DisplayName implements UsageSource.
func (c ClaudeSource) DisplayName() string { return c.P.DisplayName() }

// ShortName implements UsageSource.
func (ClaudeSource) ShortName() string { return "Claude" }

// ReportsCost implements UsageSource.
func (c ClaudeSource) ReportsCost() bool { return c.P.ReportsCost() }

// Scan implements UsageSource with a single scan from the enrichment lower bound, which also
// covers every file modified since local midnight (the daily scan's range).
func (c ClaudeSource) Scan(now time.Time, weekStart time.Weekday) (usage.Enrichment, error) {
	now = now.Local()
	entries, _ := c.P.Scan(usage.EnrichmentScanStart(now, weekStart))
	return usage.EnrichmentFromEntries(entries, now, weekStart), nil
}

// Flush implements UsageSource.
func (c ClaudeSource) Flush() error { return c.P.Flush() }

// LimitsFetcher fetches official limits; *limits.Client implements it.
type LimitsFetcher interface {
	Fetch(ctx context.Context) (*limits.Status, error)
}

// LimitsSpec attaches an official-limits source to the usage provider it belongs to.
type LimitsSpec struct {
	ProviderID string
	Fetcher    LimitsFetcher
}
