package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Window struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    *string `json:"resets_at"`
}

type Limit struct {
	Kind     string   `json:"kind"`
	Group    string   `json:"group"`
	Percent  *float64 `json:"percent"`
	Severity *string  `json:"severity"`
	ResetsAt *string  `json:"resets_at"`
	IsActive *bool    `json:"is_active"`
}

type LimitStatus struct {
	FiveHour       *Window `json:"five_hour"`
	SevenDay       *Window `json:"seven_day"`
	SevenDayOpus   *Window `json:"seven_day_opus"`
	SevenDaySonnet *Window `json:"seven_day_sonnet"`
	Limits         []Limit `json:"limits"`
}

// parseExpiresAt accepts a number or numeric string; >1e10 means milliseconds.
func parseExpiresAt(raw json.RawMessage) (time.Time, bool) {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return time.Time{}, false
		}
		f, err = strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return time.Time{}, false
		}
	}
	if f > 1e10 {
		return time.UnixMilli(int64(f)), true
	}
	return time.Unix(int64(f), 0), true
}

func str(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

func describeWindow(name string, w *Window) string {
	if w == nil {
		return name + ": null"
	}
	return fmt.Sprintf("%s: {utilization: %v, resets_at: %s}", name, w.Utilization, str(w.ResetsAt))
}

func describe(ls LimitStatus) []string {
	out := []string{
		describeWindow("five_hour", ls.FiveHour),
		describeWindow("seven_day", ls.SevenDay),
		describeWindow("seven_day_opus", ls.SevenDayOpus),
		describeWindow("seven_day_sonnet", ls.SevenDaySonnet),
	}
	out = append(out, fmt.Sprintf("limits: %d", len(ls.Limits)))
	for _, l := range ls.Limits {
		pct, act := "null", "null"
		if l.Percent != nil {
			pct = fmt.Sprint(*l.Percent)
		}
		if l.IsActive != nil {
			act = fmt.Sprint(*l.IsActive)
		}
		sev := "null"
		if l.Severity != nil {
			sev = *l.Severity
		}
		out = append(out, fmt.Sprintf("  kind=%s group=%s percent=%s severity=%s resets_at=%s is_active=%s",
			l.Kind, l.Group, pct, sev, str(l.ResetsAt), act))
	}
	return out
}
