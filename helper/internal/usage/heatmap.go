package usage

import (
	"cmp"
	"time"
)

// Heatmap geometry (ActivityHeatmap.swift:80): 16 week columns of 7 weekday rows.
const (
	HeatmapWeeks = 16
	HeatmapCells = HeatmapWeeks * 7
)

// Heatmap is the 16-week activity window of docs/protocol.md §5.2: totals only, no cost and no
// provider split.
type Heatmap struct {
	// Start is the day of cell (column 0, row 0): the first day of the week, 15 weeks before the
	// week that contains today. Always a weekStart weekday.
	Start string `json:"start"`
	// First is the first day the grid can vouch for, max(Start, recordedSince). A cell before it or
	// after today is outside the range: drawn blank, never hovered.
	First string `json:"first"`
	// Tokens is indexed col*7 + row and covers Start through Start+111. Days after today are 0.
	Tokens [HeatmapCells]int64 `json:"tokens"`
}

// MakeHeatmap builds the window (recentGrid, AH:88-123). The window rolls with today, it does not
// reset on the 1st. Day tokens are max(ledger, live series); the days stay contiguous across DST
// because they step by calendar day.
func MakeHeatmap(h History, now time.Time, weekStart time.Weekday) Heatmap {
	today := DayKey(now)
	y, m, d := StartOfWeek(now, weekStart).Date()
	d -= (HeatmapWeeks - 1) * 7
	hm := Heatmap{Start: civilKey(y, m, d)}
	for i := range hm.Tokens {
		if k := civilKey(y, m, d+i); k <= today {
			hm.Tokens[i] = h.Tokens(k)
		}
	}
	hm.First = max(hm.Start, cmp.Or(h.RecordedSince(), today))
	return hm
}
