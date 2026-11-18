package companion

import (
	"strconv"
	"time"
)

// Rare Candy grants (upstream CompanionStore.swift:1419-1531). Candies accrue in M1; using them is deferred.

// Candy window kinds.
const (
	CandySession = "session" // one candy
	CandyWeekly  = "weekly"  // five candies
)

// CandyWindow is one official-limit window eligible for Rare Candy.
type CandyWindow struct {
	Key         string  // stable id, e.g. "claude.fiveHour"; never contains the reset time
	Kind        string  // CandySession or CandyWeekly
	Utilization float64 // percent used
	Epoch       string  // reset epoch (Unix s/ms or ISO-8601); "" when the window has none
	ProviderID  string  // copied into the candyGranted event, e.g. "claude_code"
}

// CandyGrant is one grant decided by EvaluateCandyGrants.
type CandyGrant struct {
	Window CandyWindow
	Count  int
}

// parseEpoch reads Unix seconds (> 1e8), milliseconds (> 1e12) or an ISO-8601 time (CS:1420-1429).
func parseEpoch(s string) (time.Time, bool) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		switch {
		case f > 1e12:
			return time.UnixMilli(int64(f)), true
		case f > 1e8:
			return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9)), true
		}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// isNewEpoch reports a real window reset rather than jitter: the epoch must advance by ≥ 30 min
// (session) or ≥ 24 h (weekly). Unparseable epochs reset on any change (CS:1436-1444).
func isNewEpoch(previous, current, kind string) bool {
	if previous == current {
		return false
	}
	p, ok1 := parseEpoch(previous)
	c, ok2 := parseEpoch(current)
	if !ok1 || !ok2 {
		return true
	}
	minAdvance := 30 * time.Minute
	if kind == CandyWeekly {
		minAdvance = 24 * time.Hour
	}
	return c.Sub(p) >= minAdvance
}

// EvaluateCandyGrants is the pure, edge-triggered grant rule (CS:1454-1488). A window grants once
// when it reaches 100 %; it re-arms after a reading below 100 % or a new epoch. The first epoch
// seen for a key is only recorded. grantTier and windowEpoch are updated in place and must be non-nil.
func EvaluateCandyGrants(windows []CandyWindow, grantTier map[string]int, windowEpoch map[string]string) []CandyGrant {
	var grants []CandyGrant
	for _, w := range windows {
		if w.Epoch != "" {
			if prev, ok := windowEpoch[w.Key]; !ok {
				windowEpoch[w.Key] = w.Epoch
			} else if isNewEpoch(prev, w.Epoch, w.Kind) {
				delete(grantTier, w.Key)
				windowEpoch[w.Key] = w.Epoch
			}
		}
		if w.Utilization < 100 {
			delete(grantTier, w.Key)
			if w.Epoch != "" {
				windowEpoch[w.Key] = w.Epoch
			}
			continue
		}
		if grantTier[w.Key] >= 1 {
			continue
		}
		grantTier[w.Key] = 1
		if w.Epoch != "" {
			windowEpoch[w.Key] = w.Epoch
		}
		count := 1
		if w.Kind == CandyWeekly {
			count = weeklyCandyGrant
		}
		grants = append(grants, CandyGrant{Window: w, Count: count})
	}
	return grants
}

// GrantCandies applies EvaluateCandyGrants to the inventory and persists the edge state, even when
// nothing was granted. Nothing happens until limitsReady; the first run only records windows
// already at 100 % as granted (CS:1493-1531).
func (s *Store) GrantCandies(windows []CandyWindow, limitsReady bool) []Event {
	if !limitsReady {
		return nil
	}
	st := &s.st
	if !st.CandyFeatureSeeded {
		for _, w := range windows {
			if w.Utilization >= 100 {
				st.CandyGrantTier[w.Key] = 1
				if w.Epoch != "" {
					st.CandyWindowEpoch[w.Key] = w.Epoch
				}
			}
		}
		st.CandyFeatureSeeded = true
		s.save()
		return s.drain()
	}
	epochBefore := make(map[string]string, len(st.CandyWindowEpoch))
	for k, v := range st.CandyWindowEpoch {
		epochBefore[k] = v
	}
	tierBefore := copyLedger(st.CandyGrantTier)
	grants := EvaluateCandyGrants(windows, st.CandyGrantTier, st.CandyWindowEpoch)
	for _, g := range grants {
		st.Inventory["rareCandy"] += g.Count
		s.emit(CandyGrantedEvent{
			Type: "event", Kind: "candyGranted", Item: "rareCandy", Count: g.Count,
			ProviderID: g.Window.ProviderID, WindowKey: g.Window.Key, WindowKind: g.Window.Kind,
		})
	}
	if len(grants) > 0 || !mapsEqual(tierBefore, st.CandyGrantTier) || !mapsEqual(epochBefore, st.CandyWindowEpoch) {
		s.save()
	}
	return s.drain()
}

func mapsEqual[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
