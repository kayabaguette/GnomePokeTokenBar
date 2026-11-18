package limits

import "time"

// Candy window kinds.
const (
	CandySession = "session"
	CandyWeekly  = "weekly"
)

// CandyWindow is a window eligible for Rare Candy grants (evaluation lives in the companion package).
type CandyWindow struct {
	Key         string  // "<prefix>.fiveHour" or "<prefix>.sevenDay"
	Kind        string  // CandySession or CandyWeekly
	Utilization float64 // percent used
	// Epoch identifies the window instance: the reset time as ISO-8601 whole-second UTC with a Z, else
	// the raw resets_at string, else "" when the server gave none.
	Epoch string
}

// CandyWindows returns the grant-eligible windows: five_hour (session) then seven_day (weekly),
// each only with non-null utilization. Opus, Sonnet and scoped windows are excluded.
func CandyWindows(s *Status, prefix string) []CandyWindow {
	var out []CandyWindow
	for _, c := range []struct {
		w         *Window
		key, kind string
	}{
		{s.FiveHour, ".fiveHour", CandySession},
		{s.SevenDay, ".sevenDay", CandyWeekly},
	} {
		if c.w == nil || c.w.Utilization == nil {
			continue
		}
		epoch := ""
		if c.w.ResetsAt != nil {
			epoch = *c.w.ResetsAt
		}
		if t, ok := c.w.ResetDate(); ok {
			epoch = t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
		}
		out = append(out, CandyWindow{Key: prefix + c.key, Kind: c.kind, Utilization: *c.w.Utilization, Epoch: epoch})
	}
	return out
}
