package limits

// Alert is a rising-tier crossing of one window, shaped like the protocol limitAlert event fields
// (the daemon adds providerId).
type Alert struct {
	WindowKey   string  `json:"windowKey"`
	WindowKind  string  `json:"windowKind"`
	Label       *string `json:"label"`
	Tier        string  `json:"tier"` // "warn" or "crit"
	Utilization float64 `json:"utilization"`
}

// EvaluateAlerts is the edge-triggered alert rule, per window key. Tier 0 is below warn (the key is
// absent from the map), 1 is >= warn, 2 is >= crit. An alert fires only when the tier rises above
// prevTiers[key]. prevTiers is not modified; the returned map holds exactly the windows currently at
// tier >= 1, so keys missing from windows are pruned (callers pass only a successful fetch).
func EvaluateAlerts(prevTiers map[string]int, windows []WindowInfo, warn, crit float64) (map[string]int, []Alert) {
	next := make(map[string]int)
	var alerts []Alert
	for _, w := range windows {
		tier := 0
		switch {
		case w.Utilization >= crit:
			tier = 2
		case w.Utilization >= warn:
			tier = 1
		}
		if tier == 0 {
			continue
		}
		// A duplicate key is judged against the higher of the persisted and the in-pass tier.
		inPass := next[w.Key]
		known := max(prevTiers[w.Key], inPass)
		next[w.Key] = max(tier, inPass)
		if tier > known {
			name := "warn"
			if tier == 2 {
				name = "crit"
			}
			alerts = append(alerts, Alert{WindowKey: w.Key, WindowKind: w.Kind, Label: w.Label, Tier: name,
				Utilization: w.Utilization})
		}
	}
	return next, alerts
}
