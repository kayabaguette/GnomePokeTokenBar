package usage

import "strings"

// Rate is a standard API price in USD per million tokens. It estimates API-equivalent usage,
// not a subscription bill.
type Rate struct {
	Input      float64
	Output     float64
	CacheWrite float64 // 5-minute cache write; 0 means "no separate write rate", not free
	CacheRead  float64
}

// rates is upstream ModelPricing.table (ModelPricing.swift:23-68) plus the Claude models seen
// in local logs that upstream leaves unpriced. There is no family fallback: an unknown model
// stays unpriced (upstream #289).
var rates = map[string]Rate{
	"claude-opus-5":     {5, 25, 6.25, 0.5},
	"claude-opus-5-5":   {4, 20, 5, 0.2},
	"claude-sonnet-5":   {2, 10, 2.5, 0.2},
	"claude-sonnet-5-5": {2, 10, 2.5, 0.1},
	// Not in upstream. From the claude-api skill (cached 2026-11-03): Mythos 5.1 has Fable 5.1
	// pricing ($10/$50, cache read $0.25, 5-minute write 1.25x input); Mythos 5.2 has Fable 5.2
	// pricing ($8/$40, cache read $0.20, 5-minute write $10); Haiku 5.5 is $0.10/$0.50 with
	// cache read 0.1x and 5-minute write 1.25x input, on the <=100K-prompt rate card (the
	// larger card is in longContext below).
	"claude-mythos-5-1":          {10, 50, 12.5, 0.25},
	"claude-mythos-5-2":          {8, 40, 10, 0.2},
	"claude-haiku-5-5":           {0.1, 0.5, 0.125, 0.01},
	"claude-opus-4-20250514":     {15, 75, 18.75, 1.5},
	"claude-sonnet-4-20250514":   {3, 15, 3.75, 0.3},
	"claude-sonnet-4-5-20250929": {3, 15, 3.75, 0.3},
	"claude-opus-4-8":            {5, 25, 6.25, 0.5},
	"claude-opus-4-7":            {5, 25, 6.25, 0.5},
	"claude-sonnet-4-6":          {3, 15, 3.75, 0.3},
	"claude-haiku-4-5-20251001":  {1, 5, 1.25, 0.1},
	"claude-fable-5":             {10, 50, 12.5, 1.0},
	"claude-fable-5-1":           {10, 50, 12.5, 0.25},
	"gpt-6-astra":                {10, 50, 12.5, 1},
	"gpt-6-sol":                  {2, 10, 2.5, 0.2},
	"gpt-6.1-sol":                {2, 10, 2.5, 0.1},
	"gpt-6-luna":                 {0.1, 0.5, 0.125, 0.01},
	"gpt-5.6-sol":                {4, 20, 5, 0.4},
	"gpt-5.6-terra":              {2, 12, 2.5, 0.2},
	"gpt-5.6-luna":               {0.2, 1.2, 0.25, 0.02},
	"gpt-5":                      {1.25, 10, 0, 0.125},
	"gpt-5-codex":                {1.25, 10, 0, 0.125},
	"gpt-5.1":                    {1.25, 10, 0, 0.125},
	"gpt-5.1-codex":              {1.25, 10, 0, 0.125},
	"gpt-5.2":                    {1.75, 14, 0, 0.175},
	"gpt-5.2-codex":              {1.75, 14, 0, 0.175},
	"gpt-5.3-codex":              {1.75, 14, 0, 0.175},
	"gpt-5.4":                    {2.5, 15, 0, 0.25},
	"gpt-5.5":                    {5, 30, 0, 0.5},
	"gemini-2.5-pro":             {1.25, 10, 0, 0.125},
	"gemini-2.5-flash":           {0.30, 2.5, 0, 0.03},
	"gemini-2.5-flash-lite":      {0.10, 0.40, 0, 0.01},
	"gemini-2.0-flash":           {0.10, 0.4, 0, 0.025},
}

// longContext is a per-request rate card switch: above Threshold prompt tokens
// (input + cache read + cache write), input-side rates are multiplied by In and output by Out.
type longContext struct {
	Threshold int64
	In, Out   float64
}

var (
	gptLong       = longContext{272_000, 2, 1.5}
	longContextOf = map[string]longContext{
		"gpt-6-astra": gptLong, "gpt-6-sol": gptLong, "gpt-6.1-sol": gptLong, "gpt-6-luna": gptLong,
		"gpt-5.6-sol": gptLong, "gpt-5.6-terra": gptLong, "gpt-5.6-luna": gptLong,
		"gpt-5.5": gptLong, "gpt-5.4": gptLong,
		"gemini-2.5-pro": {200_000, 2, 1.5},
		// Not in upstream. Haiku 5.5 bills a prompt over 100K tokens at $0.50/$2.50 (5x), with
		// cache rates staying 0.1x / 1.25x of that card's input rate (claude-api skill).
		"claude-haiku-5-5": {100_000, 5, 5},
	}
)

// aliases maps documented model identities onto table rows (ModelPricing.swift:72-83).
var aliases = map[string]string{
	"gpt-5.6":            "gpt-5.6-sol",
	"claude-sonnet-4":    "claude-sonnet-4-20250514",
	"claude-opus-4":      "claude-opus-4-20250514",
	"claude-sonnet-4-5":  "claude-sonnet-4-5-20250929",
	"claude-haiku-4-5":   "claude-haiku-4-5-20251001",
	"gpt-5-2025-08-07":   "gpt-5",
	"gpt-5.1-2025-11-13": "gpt-5.1",
	"gpt-5.2-2025-12-11": "gpt-5.2",
	"gpt-5.4-2026-03-05": "gpt-5.4",
	"gpt-5.5-2026-04-23": "gpt-5.5",
}

// ModelKey normalizes a model id for the price table: trim, lowercase, strip one provider
// prefix, then apply aliases (ModelPricing.swift:85-92).
func ModelKey(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"openai/", "anthropic/", "google/", "models/"} {
		if strings.HasPrefix(key, prefix) {
			key = key[len(prefix):]
			break
		}
	}
	if alias, ok := aliases[key]; ok {
		return alias
	}
	return key
}

// RateFor returns the table rate of model, and false when the model is unpriced.
func RateFor(model string) (Rate, bool) {
	r, ok := rates[ModelKey(model)]
	return r, ok
}

// EstimatedCost prices one request's token buckets (ModelPricing.swift:103-119). It returns
// false for an unpriced model, a negative bucket, or cache-write tokens on a row without a
// cache-write rate. Never pass aggregates: the long-context switch is per request.
func EstimatedCost(model string, input, output, cacheWrite, cacheRead int64) (float64, bool) {
	key := ModelKey(model)
	r, ok := rates[key]
	if !ok || input < 0 || output < 0 || cacheWrite < 0 || cacheRead < 0 {
		return 0, false
	}
	if cacheWrite != 0 && r.CacheWrite <= 0 {
		return 0, false
	}
	inMult, outMult := 1.0, 1.0
	if lc, ok := longContextOf[key]; ok && input+cacheRead+cacheWrite > lc.Threshold {
		inMult, outMult = lc.In, lc.Out
	}
	const perMillion = 1_000_000
	return (float64(input)*(r.Input/perMillion)+
		float64(cacheWrite)*(r.CacheWrite/perMillion)+
		float64(cacheRead)*(r.CacheRead/perMillion))*inMult +
		float64(output)*(r.Output/perMillion)*outMult, true
}
