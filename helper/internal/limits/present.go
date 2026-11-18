package limits

import (
	"fmt"
	"math"
	"time"
)

// KeyPrefix is the window-key prefix of the default Claude account (protocol.md §5.3).
const KeyPrefix = "claude"

// StaleAfter is how long after the last successful fetch the limits count as stale.
const StaleAfter = 15 * time.Minute

// Default alert thresholds.
const (
	DefaultWarn = 80.0
	DefaultCrit = 95.0
)

// minimumPace is the elapsed window fraction below which no pace tier is given.
const minimumPace = 0.1

// WindowKind values (protocol.md §12).
const (
	KindFiveHour       = "fiveHour"
	KindSevenDay       = "sevenDay"
	KindSevenDayOpus   = "sevenDayOpus"
	KindSevenDaySonnet = "sevenDaySonnet"
	KindScoped         = "scoped"
)

// Tier is the six-step pace tier. Its names match the protocol PaceTier enum.
type Tier int

// The tiers, calm to alarming.
const (
	WayUnder Tier = iota
	Under
	OnPace
	SlightlyOver
	Over
	WayOver
)

var tierNames = [...]string{"wayUnder", "under", "onPace", "slightlyOver", "over", "wayOver"}

func (t Tier) String() string { return tierNames[t] }

// MarshalText encodes the tier as its protocol name.
func (t Tier) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// WindowInfo is one display window, shaped like the protocol Window (protocol.md §5.3).
type WindowInfo struct {
	Key         string     `json:"key"`
	Kind        string     `json:"kind"`
	ScopeKind   *string    `json:"scopeKind"`
	Label       *string    `json:"label"`
	Utilization float64    `json:"utilization"`
	ResetsAt    *time.Time `json:"resetsAt"` // UTC, whole seconds, so it marshals as 2026-11-18T13:20:00Z
	NotStarted  bool       `json:"notStarted"`
	Pace        *float64   `json:"pace"`
	PaceDelta   *int       `json:"paceDelta"`
	PaceTier    *Tier      `json:"paceTier"`
	GaugeTier   Tier       `json:"gaugeTier"`
}

// PaceFraction is the elapsed fraction of a window of length span that resets at resetsAt. It is
// false when span <= 0 or the fraction falls outside [0, 1]; it never clamps.
func PaceFraction(resetsAt time.Time, span time.Duration, now time.Time) (float64, bool) {
	if span <= 0 {
		return 0, false
	}
	f := float64(span-resetsAt.Sub(now)) / float64(span)
	if math.IsNaN(f) || f < 0 || f > 1 {
		return 0, false
	}
	return f, true
}

// RoundedDelta is utilization minus pace in percentage points, rounded half away from zero (the
// Swift rounding; JS Math.round rounds -2.5 to -2).
func RoundedDelta(utilization, pace float64) int {
	// The explicit conversion stops the compiler fusing the multiply-add on some architectures.
	return int(math.Round(utilization - float64(pace*100)))
}

// PaceTier returns the pace tier, or false while the pace is unknown or below 0.1 (the early-window
// hold). At or above crit it is always WayOver. Otherwise it follows the rounded delta:
// < -25 wayUnder, < -10 under, < 5 onPace, < 15 slightlyOver, < 30 over, else wayOver.
func PaceTier(utilization float64, pace *float64, crit float64) (Tier, bool) {
	if pace == nil || *pace < minimumPace {
		return 0, false
	}
	if utilization >= crit {
		return WayOver, true
	}
	switch d := RoundedDelta(utilization, *pace); {
	case d < -25:
		return WayUnder, true
	case d < -10:
		return Under, true
	case d < 5:
		return OnPace, true
	case d < 15:
		return SlightlyOver, true
	case d < 30:
		return Over, true
	}
	return WayOver, true
}

// GaugeTier is the tier a gauge is drawn in: the pace tier if there is one, else the absolute
// thresholds (>= crit wayOver, >= warn over, else onPace).
func GaugeTier(utilization float64, pace *float64, warn, crit float64) Tier {
	if t, ok := PaceTier(utilization, pace, crit); ok {
		return t
	}
	switch {
	case utilization >= crit:
		return WayOver
	case utilization >= warn:
		return Over
	}
	return OnPace
}

// DisplayPercent is the percent to show: used as is, or max(0, 100-used) when remaining is true.
func DisplayPercent(utilization float64, remaining bool) float64 {
	if remaining {
		return math.Max(0, 100-utilization)
	}
	return utilization
}

// IsStale reports whether limits last updated at updatedAt are stale at now. A zero updatedAt (no
// successful fetch) is not stale.
func IsStale(updatedAt, now time.Time) bool {
	return !updatedAt.IsZero() && now.Sub(updatedAt) > StaleAfter
}

// IsLimitWarning is true when any window is at or above crit, or the forecast says the 5 h window
// is exhausted before it resets.
func IsLimitWarning(windows []WindowInfo, crit float64, forecastBeforeReset bool) bool {
	for _, w := range windows {
		if w.Utilization >= crit {
			return true
		}
	}
	return forecastBeforeReset
}

// Windows builds the display windows in protocol order: fiveHour, sevenDay, sevenDayOpus,
// sevenDaySonnet, then scoped entries in server order. Windows with null utilization are omitted.
// now is the pace reference; warn and crit feed the gauge tiers.
func (s *Status) Windows(now time.Time, warn, crit float64) []WindowInfo {
	var out []WindowInfo
	add := func(w WindowInfo, reset *string, span time.Duration) {
		t, hasReset := parseTime(reset)
		if hasReset {
			t = t.UTC().Truncate(time.Second)
			w.ResetsAt = &t
			if p, ok := PaceFraction(t, span, now); ok {
				d := RoundedDelta(w.Utilization, p)
				w.Pace, w.PaceDelta = &p, &d
			}
		}
		w.NotStarted = w.Utilization == 0 && !hasReset
		if tier, ok := PaceTier(w.Utilization, w.Pace, crit); ok {
			w.PaceTier = &tier
		}
		w.GaugeTier = GaugeTier(w.Utilization, w.Pace, warn, crit)
		out = append(out, w)
	}
	legacy := []struct {
		win  *Window
		key  string
		kind string
		span time.Duration
	}{
		{s.FiveHour, "fiveHour", KindFiveHour, FiveHour},
		{s.SevenDay, "sevenDay", KindSevenDay, SevenDay},
		{s.SevenDayOpus, "sevenDayOpus", KindSevenDayOpus, SevenDay},
		{s.SevenDaySonnet, "sevenDaySonnet", KindSevenDaySonnet, SevenDay},
	}
	for _, l := range legacy {
		if l.win != nil && l.win.Utilization != nil {
			add(WindowInfo{Key: KeyPrefix + "." + l.key, Kind: l.kind, Utilization: *l.win.Utilization},
				l.win.ResetsAt, l.span)
		}
	}
	for _, i := range s.scopedEntries() {
		e := s.Limits[i]
		if e.Percent == nil {
			continue
		}
		kind, model := e.Kind, e.ModelName()
		w := WindowInfo{Kind: KindScoped, Utilization: *e.Percent}
		if kind != "" {
			w.ScopeKind = &kind
		}
		if model != "" {
			w.Label = &model
		} else {
			model = "any"
		}
		if kind == "" {
			kind = "unknown"
		}
		w.Key = fmt.Sprintf("%s.scoped.%s.%s.%d", KeyPrefix, kind, model, i)
		add(w, e.ResetsAt, e.Span())
	}
	return out
}
