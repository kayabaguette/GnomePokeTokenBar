package usage

import (
	"math"
	"testing"
)

const mtok = 1_000_000

// Expected dollars are typed from the price sources (upstream table, claude-api skill), never
// recomputed from the Go table, so a wrong row cannot confirm itself.
func TestEstimatedCost(t *testing.T) {
	cases := []struct {
		name            string
		model           string
		in, out, cw, cr int64
		want            float64
		wantOK          bool
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:61
		{"opus 4.8 input", "claude-opus-4-8", mtok, 0, 0, 0, 5.0, true},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:62
		{"opus 4.8 output", "claude-opus-4-8", 0, mtok, 0, 0, 25.0, true},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:63
		{"haiku 4.5 dated id", "claude-haiku-4-5-20251001", mtok, 0, 0, 0, 1.0, true},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:64
		{"fable 5 all buckets", "claude-fable-5", mtok, mtok, mtok, mtok, 73.5, true},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:67
		{"fable 5.1 cache read", "claude-fable-5-1", 0, 0, 0, mtok, 0.25, true},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:72
		{"no family fallback", "claude-opus-4-99", mtok, 0, 0, 0, 0, false},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:73
		{"unverified fable 6", "claude-fable-6", mtok, 0, 0, 0, 0, false},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:80
		{"unverified opus 6", "claude-opus-6", mtok, 0, 0, 0, 0, false},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:81
		{"unknown model", "totally-unknown", mtok, 0, 0, 0, 0, false},
		// Swift: Tests/PokeTokenBarTests/UsageCostTests.swift:168 (100*2 + 10*10 + 40*2.5 + 1000*0.1 per MTok)
		{"sonnet 5.5 bucket split", "claude-sonnet-5-5", 100, 10, 40, 1000, 0.0005, true},
		// Go-only: skill price, Mythos 5.1 = Fable 5.1 ($10 / $50, write 1.25x = $12.50, read $0.25).
		{"mythos 5.1 all buckets", "claude-mythos-5-1", mtok, mtok, mtok, mtok, 72.75, true},
		// Go-only: skill price, Mythos 5.2 = Fable 5.2 ($8 / $40, 5m write $10, read $0.20).
		{"mythos 5.2 all buckets", "claude-mythos-5-2", mtok, mtok, mtok, mtok, 58.2, true},
		// Go-only: Haiku 5.5, prompt of exactly 100K stays on the $0.10 card.
		{"haiku 5.5 input at 100K", "claude-haiku-5-5", 100_000, 0, 0, 0, 0.01, true},
		// Go-only: one more prompt token moves to the $0.50 card.
		{"haiku 5.5 input above 100K", "claude-haiku-5-5", 100_001, 0, 0, 0, 0.0500005, true},
		// Go-only: output $0.50 on the small card.
		{"haiku 5.5 output small prompt", "claude-haiku-5-5", 0, mtok, 0, 0, 0.5, true},
		// Go-only: output $2.50 once the prompt is over 100K.
		{"haiku 5.5 output large prompt", "claude-haiku-5-5", 100_001, mtok, 0, 0, 2.5500005, true},
		// Go-only: cache write 1.25x of $0.10, small card.
		{"haiku 5.5 cache write small", "claude-haiku-5-5", 0, 0, 100_000, 0, 0.0125, true},
		// Go-only: cache write 1.25x of $0.50, large card.
		{"haiku 5.5 cache write large", "claude-haiku-5-5", 0, 0, 100_001, 0, 0.062500625, true},
		// Go-only: cache read 0.1x of $0.10, small card.
		{"haiku 5.5 cache read small", "claude-haiku-5-5", 0, 0, 0, 100_000, 0.001, true},
		// Go-only: cache read counts toward the prompt: 1M reads bill at 0.1x of $0.50.
		{"haiku 5.5 cache read large", "claude-haiku-5-5", 0, 0, 0, mtok, 0.05, true},
		// Go-only: GPT long context starts above 272K prompt tokens (ModelPricing.swift:110-116).
		{"gpt 5.5 at 272K", "gpt-5.5", 272_000, 1_000, 0, 0, 1.39, true},
		{"gpt 5.5 above 272K", "gpt-5.5", 272_001, 1_000, 0, 0, 2.76501, true},
		{"gemini 2.5 pro above 200K", "gemini-2.5-pro", 200_001, 0, 0, 0, 0.5000025, true},
		// Go-only: a zero write rate means "no write rate", not free writes (ModelPricing.swift:108).
		{"write tokens without write rate", "gpt-5", 0, 0, 1, 0, 0, false},
		{"no write tokens on such a row", "gpt-5", mtok, 0, 0, 0, 1.25, true},
		{"negative bucket", "claude-opus-4-8", -1, 0, 0, 0, 0, false},
		// Go-only: key normalization (ModelPricing.swift:85-92): trim, lowercase, one prefix, alias.
		{"prefix and case", " Anthropic/Claude-Opus-4-8 ", mtok, 0, 0, 0, 5.0, true},
		{"alias", "claude-haiku-4-5", mtok, 0, 0, 0, 1.0, true},
		{"only one prefix stripped", "anthropic/models/claude-opus-4-8", mtok, 0, 0, 0, 0, false},
	}
	for _, c := range cases {
		got, ok := EstimatedCost(c.model, c.in, c.out, c.cw, c.cr)
		if ok != c.wantOK || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: EstimatedCost = %v, %v; want %v, %v", c.name, got, ok, c.want, c.wantOK)
		}
	}
}

func TestRates(t *testing.T) {
	cases := []struct {
		model string
		want  Rate
	}{
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:20
		{"claude-opus-5", Rate{5, 25, 6.25, 0.5}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:21
		{"claude-opus-5-5", Rate{4, 20, 5, 0.2}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:22
		{"claude-sonnet-5", Rate{2, 10, 2.5, 0.2}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:23
		{"claude-sonnet-5-5", Rate{2, 10, 2.5, 0.1}},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:66
		{"claude-fable-5-1", Rate{10, 50, 12.5, 0.25}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:6
		{"gpt-6-astra", Rate{10, 50, 12.5, 1}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:9
		{"gpt-6-luna", Rate{0.1, 0.5, 0.125, 0.01}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:12
		{"gpt-5.6-luna", Rate{0.2, 1.2, 0.25, 0.02}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:13
		{"gpt-5.5", Rate{5, 30, 0, 0.5}},
		// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:14
		{"gpt-5.3-codex", Rate{1.75, 14, 0, 0.175}},
	}
	for _, c := range cases {
		if got, ok := RateFor(c.model); !ok || got != c.want {
			t.Errorf("RateFor(%q) = %v, %v; want %v", c.model, got, ok, c.want)
		}
	}
}

// Every model read from local logs must be priced: a table that only asserts the rows it
// holds cannot catch a missing one (upstream #303).
func TestModelsSeenInLogsArePriced(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ModelPricingTests.swift:32 (+ the three models Go adds)
	for _, model := range []string{
		"claude-opus-5", "claude-opus-5-5", "claude-sonnet-5", "claude-sonnet-5-5",
		"claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5",
		"claude-fable-5", "claude-fable-5-1",
		"claude-mythos-5-1", "claude-mythos-5-2", "claude-haiku-5-5",
	} {
		cost, ok := EstimatedCost(model, 2, 175, 26_939, 42_676)
		if !ok || cost <= 0 {
			t.Errorf("%s is unpriced in a real Claude Code bucket split", model)
		}
	}
}

func TestCoverage(t *testing.T) {
	// Go-only: protocol §12 enum over upstream's three flags (UsageCost.swift:5-41).
	cases := []struct {
		c    Coverage
		want string
	}{
		{Coverage{}, "empty"},
		{Coverage{Reported: true}, "reported"},
		{Coverage{Estimated: true}, "estimated"},
		{Coverage{Reported: true, Estimated: true}, "estimated"},
		{Coverage{Unknown: true}, "unavailable"},
		{Coverage{Unknown: true, Estimated: true}, "partial"},
		{Coverage{Unknown: true, Reported: true}, "partial"},
	}
	for _, c := range cases {
		if got := c.c.String(); got != c.want {
			t.Errorf("%+v.String() = %q; want %q", c.c, got, c.want)
		}
	}
	var merged Coverage
	merged.Merge(Coverage{Estimated: true})
	merged.Merge(Coverage{Unknown: true})
	if merged.String() != "partial" {
		t.Errorf("merge = %v; want partial", merged)
	}
}

func TestTotalsCostProvenance(t *testing.T) {
	e := func(model string, in, out, cw, cr int64) Entry {
		return Entry{ID: model, LocalDay: "2026-09-15", Model: model, Input: in, Output: out, CacheWrite: cw, CacheRead: cr}
	}
	cases := []struct {
		name     string
		entries  []Entry
		usd      float64
		coverage string
	}{
		// Swift: Tests/PokeTokenBarTests/ClaudeReportedCostTests.swift:208
		{"priced estimate", []Entry{e("claude-haiku-4-5-20251001", mtok, 0, 0, 0)}, 1.0, "estimated"},
		// Swift: Tests/PokeTokenBarTests/ClaudeReportedCostTests.swift:216
		{"unpriced", []Entry{e("claude-future-unpriced-model", mtok, 0, 0, 0)}, 0, "unavailable"},
		// Swift: Tests/PokeTokenBarTests/UsageCostTests.swift:73
		{"zero usage keeps unknown unavailable", []Entry{e("gpt-5.5", 0, 0, 0, 0), e("not-priced", 1_000, 100, 0, 500)}, 0, "unavailable"},
		// Swift: Tests/PokeTokenBarTests/UsageCostTests.swift:48 (priced + unpriced, without a source amount)
		{"priced and unpriced", []Entry{e("claude-opus-4-8", mtok, 0, 0, 0), e("new-model", 10, 0, 0, 0)}, 5.0, "partial"},
		// Go-only: a <synthetic> zero-token record adds neither cost nor coverage.
		{"synthetic only", []Entry{e("<synthetic>", 0, 0, 0, 0)}, 0, "empty"},
	}
	for _, c := range cases {
		var tot Totals
		for _, en := range c.entries {
			tot.Add(en)
		}
		if math.Abs(tot.Cost.USD-c.usd) > 1e-9 || tot.Cost.Coverage.String() != c.coverage {
			t.Errorf("%s: cost = %v %s; want %v %s", c.name, tot.Cost.USD, tot.Cost.Coverage, c.usd, c.coverage)
		}
	}
}

func TestTotalsMerge(t *testing.T) {
	// Go-only: cross-provider sums keep every bucket and OR the coverage.
	a := Totals{Tokens: 3, Input: 1, Output: 2, Cost: Cost{USD: 1, Coverage: Coverage{Estimated: true}}}
	b := Totals{Tokens: 4, CacheWrite: 3, CacheRead: 1, Cost: Cost{USD: 2, Coverage: Coverage{Unknown: true}}}
	a.Merge(b)
	want := Totals{Tokens: 7, Input: 1, Output: 2, CacheWrite: 3, CacheRead: 1,
		Cost: Cost{USD: 3, Coverage: Coverage{Estimated: true, Unknown: true}}}
	if a != want {
		t.Errorf("Merge = %+v; want %+v", a, want)
	}
}
