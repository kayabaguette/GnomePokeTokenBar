package daemon

import (
	"math"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/usage"
)

// Wire messages of protocol §3, §5 and §6. Every documented field is always present; a missing
// value is null. Enums are strings here, so the fixtures decode into these types for the
// contract-drift test.

// Protocol is the wire protocol version.
const Protocol = 1

type helloMsg struct {
	Type          string `json:"type"`
	Protocol      int    `json:"protocol"`
	HelperVersion string `json:"helperVersion"`
	PID           int    `json:"pid"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type resultMsg struct {
	Type  string     `json:"type"`
	ID    int64      `json:"id"`
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *errorBody `json:"error,omitempty"`
}

type fatalMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// stateMsg is one `state` line (§5).
type stateMsg struct {
	Type            string                  `json:"type"`
	Meta            metaView                `json:"meta"`
	Usage           usageView               `json:"usage"`
	Limits          limitsView              `json:"limits"`
	Companion       companion.CompanionView `json:"companion"`
	SettingsApplied Settings                `json:"settingsApplied"`
}

type metaView struct {
	ProtocolVersion int            `json:"protocolVersion"`
	HelperVersion   string         `json:"helperVersion"`
	GeneratedAt     string         `json:"generatedAt"`
	LastUpdated     *string        `json:"lastUpdated"`
	IsStale         bool           `json:"isStale"`
	FromCache       bool           `json:"fromCache"`
	Refreshing      bool           `json:"refreshing"`
	Errors          []metaError    `json:"errors"`
	Providers       []providerMeta `json:"providers"`
}

type metaError struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	ProviderID *string `json:"providerId"`
}

type providerMeta struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	ShortName   string `json:"shortName"`
	ReportsCost bool   `json:"reportsCost"`
}

type usageView struct {
	Calendar  calendarView                 `json:"calendar"`
	Combined  aggregateView                `json:"combined"`
	Providers map[string]providerUsageView `json:"providers"`
}

type calendarView struct {
	Today      string `json:"today"`
	WeekStart  string `json:"weekStart"`
	MonthStart string `json:"monthStart"`
}

type costView struct {
	USD      float64 `json:"usd"`
	Coverage string  `json:"coverage"`
}

type totalsView struct {
	Tokens     int64    `json:"tokens"`
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheWrite int64    `json:"cacheWrite"`
	CacheRead  int64    `json:"cacheRead"`
	Cost       costView `json:"cost"`
}

type seriesView struct {
	Start    string    `json:"start"`
	Tokens   []int64   `json:"tokens"`
	CostUSD  []float64 `json:"costUsd"`
	Coverage []string  `json:"coverage"`
}

type aggregateView struct {
	Today       totalsView   `json:"today"`
	Week        totalsView   `json:"week"`
	Month       totalsView   `json:"month"`
	RecentDaily seriesView   `json:"recentDaily"`
	MonthDaily  seriesView   `json:"monthDaily"`
	BurnTier    string       `json:"burnTier"`
	Streak      usage.Streak `json:"streak"`
}

type providerUsageView struct {
	Today       totalsView    `json:"today"`
	Week        totalsView    `json:"week"`
	Month       totalsView    `json:"month"`
	RecentDaily seriesView    `json:"recentDaily"`
	MonthDaily  seriesView    `json:"monthDaily"`
	ActiveBlock *blockView    `json:"activeBlock"`
	Forecast    *forecastView `json:"forecast"`
}

type blockView struct {
	Tokens          int64   `json:"tokens"`
	TokensPerMinute float64 `json:"tokensPerMinute"`
	Start           string  `json:"start"`
	End             string  `json:"end"`
}

type forecastView struct {
	DepletionAt string `json:"depletionAt"`
	BeforeReset bool   `json:"beforeReset"`
}

type limitsView struct {
	IsLimitWarning bool                          `json:"isLimitWarning"`
	Providers      map[string]providerLimitsView `json:"providers"`
}

type providerLimitsView struct {
	Available    bool         `json:"available"`
	UpdatedAt    *string      `json:"updatedAt"`
	IsStale      bool         `json:"isStale"`
	AuthState    string       `json:"authState"`
	ErrorCode    *string      `json:"errorCode"`
	HTTPStatus   *int         `json:"httpStatus"`
	BackoffUntil *string      `json:"backoffUntil"`
	Plan         *string      `json:"plan"`
	Account      *string      `json:"account"`
	HeadlineKey  *string      `json:"headlineKey"`
	Windows      []windowView `json:"windows"`
}

type windowView struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	ScopeKind   *string  `json:"scopeKind"`
	Label       *string  `json:"label"`
	Utilization float64  `json:"utilization"`
	ResetsAt    *string  `json:"resetsAt"`
	NotStarted  bool     `json:"notStarted"`
	Pace        *float64 `json:"pace"`
	PaceDelta   *int     `json:"paceDelta"`
	PaceTier    *string  `json:"paceTier"`
	GaugeTier   string   `json:"gaugeTier"`
}

// Events the daemon emits itself (§6); the companion package marshals its own.

type limitAlertEvent struct {
	Type        string  `json:"type"`
	Kind        string  `json:"kind"`
	ProviderID  string  `json:"providerId"`
	WindowKey   string  `json:"windowKey"`
	WindowKind  string  `json:"windowKind"`
	Label       *string `json:"label"`
	Tier        string  `json:"tier"`
	Utilization float64 `json:"utilization"`
}

type authExpiredEvent struct {
	Type       string `json:"type"`
	Kind       string `json:"kind"`
	ProviderID string `json:"providerId"`
	ErrorCode  string `json:"errorCode"`
}

type errorEvent struct {
	Type    string `json:"type"`
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// spriteReply is the getSprite reply data (§8.1).
type spriteReply struct {
	Key          string `json:"key"`
	Status       string `json:"status"`
	ManifestPath string `json:"manifestPath"`
}

// ---------------------------------------------------------------- value helpers

// ts formats a timestamp as RFC 3339 UTC, whole seconds (§1.1).
func ts(t time.Time) string { return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z") }

func tsPtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := ts(t)
	return &v
}

func round(v float64, places int) float64 {
	f := math.Pow(10, float64(places))
	return math.Round(v*f) / f
}

// clampInt keeps integers inside the JSON-safe range (§1.1).
func clampInt(v int64) int64 { return min(max(v, 0), maxSafeInt) }

func toTotalsView(t usage.Totals) totalsView {
	usd := round(t.Cost.USD, 4)
	cov := t.Cost.Coverage.String()
	if cov == "empty" || cov == "unavailable" {
		usd = 0
	}
	return totalsView{Tokens: clampInt(t.Tokens), Input: clampInt(t.Input), Output: clampInt(t.Output),
		CacheWrite: clampInt(t.CacheWrite), CacheRead: clampInt(t.CacheRead), Cost: costView{USD: usd, Coverage: cov}}
}

func toSeriesView(days []usage.DailyUsage) seriesView {
	s := seriesView{Tokens: make([]int64, len(days)), CostUSD: make([]float64, len(days)), Coverage: make([]string, len(days))}
	if len(days) > 0 {
		s.Start = days[0].Day
	}
	for i, d := range days {
		t := toTotalsView(d.Totals)
		s.Tokens[i], s.CostUSD[i], s.Coverage[i] = t.Tokens, t.Cost.USD, t.Cost.Coverage
	}
	return s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
