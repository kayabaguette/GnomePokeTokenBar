package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Settings is the forwarded settings set (protocol §9.2) after normalization; it is also
// state.settingsApplied (§5.6) and the content of settings-applied.json.
type Settings struct {
	RefreshInterval  int     `json:"refreshInterval"`
	WarnThreshold    int     `json:"warnThreshold"`
	CritThreshold    int     `json:"critThreshold"`
	GrowthDifficulty float64 `json:"growthDifficulty"`
	WeekStart        int     `json:"weekStart"`
	Language         string  `json:"language"`
	AlwaysFacesBack  bool    `json:"alwaysFacesBack"`
}

// refreshPresets are the refresh-interval choices in seconds; 0 is manual (usage.md §6.1).
var refreshPresets = []int{0, 60, 120, 300, 900}

// DefaultSettings are used before any setSettings and without a settings-applied.json.
func DefaultSettings() Settings {
	return Settings{RefreshInterval: 120, WarnThreshold: 80, CritThreshold: 95, GrowthDifficulty: 1.0,
		WeekStart: 1, Language: "en"}
}

// normalized applies the protocol rules: the nearest refresh preset (the lower one on a tie), the
// threshold rule of §9.1, the difficulty clamp and the language form of §10. weekStart is
// validated by the caller.
func (s Settings) normalized() Settings {
	best := refreshPresets[0]
	for _, p := range refreshPresets {
		if abs(s.RefreshInterval-p) < abs(s.RefreshInterval-best) {
			best = p
		}
	}
	s.RefreshInterval = best
	s.WarnThreshold = min(max(s.WarnThreshold, 50), 95)
	s.CritThreshold = min(max(s.CritThreshold, 80), 100)
	if s.WarnThreshold > s.CritThreshold-5 {
		s.WarnThreshold = s.CritThreshold - 5
	}
	if math.IsNaN(s.GrowthDifficulty) {
		s.GrowthDifficulty = 1
	}
	s.GrowthDifficulty = min(max(s.GrowthDifficulty, 0.1), 2.0)
	s.Language = normalizeLanguage(s.Language)
	if s.WeekStart < 1 || s.WeekStart > 7 {
		s.WeekStart = 1
	}
	return s
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// normalizeLanguage lowercases a locale tag, maps "_" to "-" and drops a codeset or modifier;
// empty, "c" and "posix" become "en" (protocol §10).
func normalizeLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, ".@"); i >= 0 {
		tag = tag[:i]
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	if tag == "" || tag == "c" || tag == "posix" {
		return "en"
	}
	return tag
}

// weekday maps the ISO weekday (1 = Monday … 7 = Sunday) to time.Weekday.
func (s Settings) weekday() time.Weekday { return time.Weekday(s.WeekStart % 7) }

// interval is the automatic refresh period, 0 in manual mode.
func (s Settings) interval() time.Duration { return time.Duration(s.RefreshInterval) * time.Second }

// parseSettings decodes setSettings args strictly: every field is required with its JSON type, and
// weekStart must be 1–7. Errors name the field; nothing is applied on error.
func parseSettings(raw json.RawMessage) (Settings, error) {
	a, err := newArgs(raw)
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	s.RefreshInterval = int(a.int("refreshInterval", true, 0, -1<<31, 1<<31))
	s.WarnThreshold = int(a.int("warnThreshold", true, 0, -1<<31, 1<<31))
	s.CritThreshold = int(a.int("critThreshold", true, 0, -1<<31, 1<<31))
	s.GrowthDifficulty = a.number("growthDifficulty", true, 0)
	s.WeekStart = int(a.int("weekStart", true, 0, 1, 7))
	s.Language = a.str("language", true, "")
	s.AlwaysFacesBack = a.boolean("alwaysFacesBack", true, false)
	if a.err != nil {
		return Settings{}, a.err
	}
	return s.normalized(), nil
}

func (s Settings) String() string {
	return fmt.Sprintf("interval=%ds warn=%d crit=%d difficulty=%g weekStart=%d language=%s back=%t",
		s.RefreshInterval, s.WarnThreshold, s.CritThreshold, s.GrowthDifficulty, s.WeekStart, s.Language, s.AlwaysFacesBack)
}
