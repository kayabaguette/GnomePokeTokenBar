package usage

import (
	"log"
	"sync"
)

// Coverage records where a cost amount came from. Aggregation ORs the flags, so an unknown
// part is never hidden by a known one (Swift CostCoverage, UsageCost.swift:5-21).
type Coverage struct {
	Reported  bool // the source recorded the amount
	Estimated bool // the price table estimated it
	Unknown   bool // some tokens could not be priced
}

// Merge ORs other into c.
func (c *Coverage) Merge(other Coverage) {
	c.Reported = c.Reported || other.Reported
	c.Estimated = c.Estimated || other.Estimated
	c.Unknown = c.Unknown || other.Unknown
}

// String returns the wire enum (protocol §12): empty, reported, estimated, unavailable, partial.
func (c Coverage) String() string {
	switch {
	case c.Unknown && (c.Reported || c.Estimated):
		return "partial"
	case c.Unknown:
		return "unavailable"
	case c.Estimated:
		return "estimated"
	case c.Reported:
		return "reported"
	default:
		return "empty"
	}
}

// MarshalText encodes the coverage as its wire enum.
func (c Coverage) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// Cost is an amount in USD with its provenance.
type Cost struct {
	USD      float64  `json:"usd"`
	Coverage Coverage `json:"coverage"`
}

// Totals sums token buckets and cost (Swift LocalUsageReader.Bucket, :52-74).
type Totals struct {
	Tokens     int64 `json:"tokens"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cacheWrite"`
	CacheRead  int64 `json:"cacheRead"`
	Cost       Cost  `json:"cost"`
}

// Add adds one entry. A zero-token entry adds no cost and no coverage, so replays and
// `<synthetic>` records cannot make an unknown-only total look partially priced.
func (t *Totals) Add(e Entry) {
	t.Input += e.Input
	t.Output += e.Output
	t.CacheWrite += e.CacheWrite
	t.CacheRead += e.CacheRead
	total := e.Total()
	t.Tokens += total
	if total <= 0 {
		return
	}
	if usd, ok := EstimatedCost(e.Model, e.Input, e.Output, e.CacheWrite, e.CacheRead); ok {
		t.Cost.USD += usd
		t.Cost.Coverage.Estimated = true
		return
	}
	noteUnpriced(e.Model)
	t.Cost.Coverage.Unknown = true
}

// Merge adds other into t, for cross-provider sums.
func (t *Totals) Merge(other Totals) {
	t.Tokens += other.Tokens
	t.Input += other.Input
	t.Output += other.Output
	t.CacheWrite += other.CacheWrite
	t.CacheRead += other.CacheRead
	t.Cost.USD += other.Cost.USD
	t.Cost.Coverage.Merge(other.Cost.Coverage)
}

var unpricedSeen sync.Map

// noteUnpriced logs each unpriced model once per process, so a new model shows up in the
// journal the first time it is used (Swift ModelPricing.noteUnpriced).
func noteUnpriced(model string) {
	if _, loaded := unpricedSeen.LoadOrStore(ModelKey(model), true); !loaded {
		log.Printf("unpriced model %q: its cost shows as unavailable until the price table has a row", model)
	}
}
