package limits

import (
	"encoding/json"
	"strings"
	"time"
)

// Window spans.
const (
	FiveHour = 5 * time.Hour
	SevenDay = 7 * 24 * time.Hour
)

// Window is one legacy limit window ("five_hour", "seven_day", ...). A nil *Window is a JSON null.
type Window struct {
	Utilization *float64 `json:"utilization"` // percent used, nil when null
	ResetsAt    *string  `json:"resets_at"`
}

// ResetDate parses ResetsAt; ok is false when it is absent or unparseable.
func (w *Window) ResetDate() (time.Time, bool) {
	if w == nil {
		return time.Time{}, false
	}
	return parseTime(w.ResetsAt)
}

// Entry is one element of the newer limits[] list.
type Entry struct {
	Kind     string   `json:"kind"`
	Group    string   `json:"group"`
	Percent  *float64 `json:"percent"`
	Severity string   `json:"severity"`
	ResetsAt *string  `json:"resets_at"`
	IsActive *bool    `json:"is_active"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// ModelName returns scope.model.display_name, or "".
func (e Entry) ModelName() string {
	if e.Scope == nil || e.Scope.Model == nil {
		return ""
	}
	return e.Scope.Model.DisplayName
}

// Span returns the window length implied by Kind (session 5 h, weekly_* 7 d), or 0 if unknown.
func (e Entry) Span() time.Duration {
	switch e.Kind {
	case "session":
		return FiveHour
	case "weekly_all", "weekly_scoped":
		return SevenDay
	}
	return 0
}

// Status is the decoded usage response plus the plan fields taken from the credentials.
// Unknown response keys are ignored.
type Status struct {
	FiveHour       *Window `json:"five_hour"`
	SevenDay       *Window `json:"seven_day"`
	SevenDayOpus   *Window `json:"seven_day_opus"`
	SevenDaySonnet *Window `json:"seven_day_sonnet"`
	Limits         []Entry `json:"limits"`

	// Filled by Client.Fetch from the credentials file, not from the response.
	SubscriptionType string `json:"-"`
	RateLimitTier    string `json:"-"`
}

// ParseStatus decodes a usage response body.
func ParseStatus(body []byte) (*Status, error) {
	var s Status
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, &Error{Code: CodeDecode, Err: err}
	}
	return &s, nil
}

// parseTime parses an ISO-8601 timestamp with optional fractional seconds (any digit count) and a Z
// or +hh:mm offset.
func parseTime(s *string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	return t, err == nil
}

// HeadlineKey is the key of the window narrow surfaces show: the 5 h window if its utilization is
// present, else the 7 d one, else "".
func (s *Status) HeadlineKey() string {
	if s.FiveHour != nil && s.FiveHour.Utilization != nil {
		return KeyPrefix + ".fiveHour"
	}
	if s.SevenDay != nil && s.SevenDay.Utilization != nil {
		return KeyPrefix + ".sevenDay"
	}
	return ""
}

// scopedEntries returns the limits[] entries shown beyond the legacy fields, with their original
// index: everything if both five_hour and seven_day are null, else all but session and weekly_all.
func (s *Status) scopedEntries() (idx []int) {
	both := s.FiveHour == nil && s.SevenDay == nil
	for i, e := range s.Limits {
		if both || (e.Kind != "session" && e.Kind != "weekly_all") {
			idx = append(idx, i)
		}
	}
	return idx
}

// Plan returns the plan display string derived from subscriptionType and rateLimitTier, or "" when
// the credentials carry no subscription. Max 5x displays as "Max", Max 20x as "Ultra", other
// multipliers as "<Type> <N>x".
func (s *Status) Plan() string {
	if s.SubscriptionType == "" {
		return ""
	}
	r := []rune(s.SubscriptionType)
	base := strings.ToUpper(string(r[:1])) + string(r[1:])
	mult := tierMultiplier(s.RateLimitTier)
	if mult == "" {
		return base
	}
	if strings.ToLower(s.SubscriptionType) != "max" {
		return base + " " + mult
	}
	switch mult {
	case "5x":
		return "Max"
	case "20x":
		return "Ultra"
	}
	return "Max " + mult
}

// tierMultiplier finds a "<digits>x" token among the "_"-separated parts of the tier.
func tierMultiplier(tier string) string {
	for _, part := range strings.Split(tier, "_") {
		digits, ok := strings.CutSuffix(part, "x")
		if !ok || digits == "" {
			continue
		}
		numeric := true
		for _, r := range digits {
			if r < '0' || r > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			return part
		}
	}
	return ""
}
