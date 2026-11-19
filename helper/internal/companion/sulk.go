package companion

import "time"

// idleDuration is how long the user has been idle for the mood: the scan's idle time, shortened
// by a rise in today's total that Update saw (CS:1974-1985). ok is false when it is unknown, which
// never sulks.
func (s *Store) idleDuration(in MoodInput) (idle time.Duration, ok bool) {
	if !in.HasUsageData || in.IdleFor == nil {
		return 0, false
	}
	idle = *in.IdleFor
	if !s.lastObservedUsage.IsZero() {
		idle = min(idle, max(0, in.Now.Sub(s.lastObservedUsage)))
	}
	return idle, true
}

// sulkIdleSeconds is state.companion.sulkIdleSeconds (protocol §5.4): the idle time in whole hours,
// expressed in seconds and floored, while the mood is sulk; 0 otherwise. Whole hours keep the 60 s
// tick from emitting a new state every minute of a sulk.
func (s *Store) sulkIdleSeconds(in MoodInput, mood string) int {
	if mood != "sulk" {
		return 0
	}
	idle, _ := s.idleDuration(in)
	return int(idle/time.Hour) * 3600
}
