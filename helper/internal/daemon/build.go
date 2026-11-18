package daemon

import (
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/usage"
)

// buildState assembles a full state from the in-memory inputs at now. Every time-derived field
// (pace, tiers, stale flags, block and forecast expiry, the calendar, mood) is computed here, so
// the 60 s tick and the cached initial state need nothing else (protocol §2.3, §2.4).
func (d *daemon) buildState(now time.Time) stateMsg {
	now = now.In(d.loc)
	limitsV, forecasts, warning := d.buildLimits(now)
	usageV, burn, todayTokens := d.buildUsage(now, forecasts)
	st := stateMsg{
		Type:            "state",
		Meta:            d.buildMeta(now),
		Usage:           usageV,
		Limits:          limitsV,
		SettingsApplied: d.settings,
	}
	st.Companion = d.store.BuildState(companion.MoodInput{
		Now: now, HasUsageData: d.hasUsageData(now), TodayTokens: int(todayTokens), IdleFor: d.idleFor(now),
		LimitWarning: warning, BurnPerMinute: burn, AlwaysFacesBack: d.settings.AlwaysFacesBack,
	})
	st.Companion.MenuSubject.Sprite = d.menuSpriteRef(st.Companion.MenuSubject)
	return st
}

func (d *daemon) buildMeta(now time.Time) metaView {
	m := metaView{ProtocolVersion: Protocol, HelperVersion: d.cfg.Version, GeneratedAt: ts(now),
		LastUpdated: tsPtr(d.lastUpdated), FromCache: d.fromCache, Refreshing: d.refreshing,
		Errors: []metaError{}, Providers: []providerMeta{}}
	staleAfter := 1800 * time.Second
	if iv := d.settings.interval(); iv > 0 {
		staleAfter = 2 * iv
	}
	m.IsStale = d.lastUpdated.IsZero() || now.Sub(d.lastUpdated) > staleAfter
	for _, src := range d.cfg.Providers {
		id := src.ID()
		m.Providers = append(m.Providers, providerMeta{ID: id, DisplayName: src.DisplayName(),
			ShortName: src.ShortName(), ReportsCost: src.ReportsCost()})
		if err := d.usage[id].scanErr; err != nil {
			m.Errors = append(m.Errors, metaError{Code: "usageScan", Message: "usage scan failed: " + err.Error(), ProviderID: &id})
		}
		if err := d.cacheErr[id]; err != nil {
			m.Errors = append(m.Errors, metaError{Code: "cacheWrite", Message: "usage cache could not be written", ProviderID: &id})
		}
	}
	if d.store.SaveError() != nil || d.persistErr != nil {
		m.Errors = append(m.Errors, metaError{Code: "saveWrite", Message: "a save file could not be written"})
	}
	return m
}

// buildUsage returns the usage section, the summed burn rate of the live blocks and today's
// combined tokens.
func (d *daemon) buildUsage(now time.Time, forecasts map[string]*forecastView) (usageView, float64, int64) {
	ws := d.settings.weekday()
	today := usage.DayKey(now)
	weekStart := usage.DayKey(usage.StartOfWeek(now, ws))
	monthStart := usage.StartOfMonth(now)
	rollStart := usage.StartOfRollingWindow(now, usage.RecentDays)
	v := usageView{
		Calendar:  calendarView{Today: today, WeekStart: weekStart, MonthStart: usage.DayKey(monthStart)},
		Providers: map[string]providerUsageView{},
	}
	var cToday, cWeek, cMonth usage.Totals
	cRecent := (&providerUsage{}).series(rollStart, now)
	cMonthly := (&providerUsage{}).series(monthStart, now)
	burn := 0.0
	for _, src := range d.cfg.Providers {
		id := src.ID()
		p := d.usage[id]
		pt, pw, pm := p.day(today), p.period(weekStart, today), p.period(usage.DayKey(monthStart), today)
		recent, monthly := p.series(rollStart, now), p.series(monthStart, now)
		pv := providerUsageView{Today: toTotalsView(pt), Week: toTotalsView(pw), Month: toTotalsView(pm),
			RecentDaily: toSeriesView(recent), MonthDaily: toSeriesView(monthly), Forecast: forecasts[id]}
		if b := p.activeBlock(now); b != nil {
			pv.ActiveBlock = &blockView{Tokens: clampInt(b.Tokens), TokensPerMinute: round(b.TokensPerMinute, 2),
				Start: ts(b.Start), End: ts(b.End)}
			burn += b.TokensPerMinute
		}
		v.Providers[id] = pv
		cToday.Merge(pt)
		cWeek.Merge(pw)
		cMonth.Merge(pm)
		for i := range cRecent {
			cRecent[i].Merge(recent[i].Totals)
		}
		for i := range cMonthly {
			cMonthly[i].Merge(monthly[i].Totals)
		}
	}
	v.Combined = aggregateView{Today: toTotalsView(cToday), Week: toTotalsView(cWeek), Month: toTotalsView(cMonth),
		RecentDaily: toSeriesView(cRecent), MonthDaily: toSeriesView(cMonthly),
		BurnTier: string(usage.BurnTierFor(burn)), Streak: d.ledger.Streak(now)}
	return v, burn, cToday.Tokens
}

// combinedMonthDaily is the cross-provider month series the history ledger records.
func (d *daemon) combinedMonthDaily(now time.Time) []usage.DailyUsage {
	out := (&providerUsage{}).series(usage.StartOfMonth(now), now)
	for _, src := range d.cfg.Providers {
		for i, day := range d.usage[src.ID()].series(usage.StartOfMonth(now), now) {
			out[i].Merge(day.Totals)
		}
	}
	return out
}

// buildLimits returns the limits section, the 5 h forecast per provider and isLimitWarning.
func (d *daemon) buildLimits(now time.Time) (limitsView, map[string]*forecastView, bool) {
	v := limitsView{Providers: map[string]providerLimitsView{}}
	forecasts := map[string]*forecastView{}
	warn, crit := float64(d.settings.WarnThreshold), float64(d.settings.CritThreshold)
	for _, spec := range d.cfg.Limits {
		l := d.limits[spec.ProviderID]
		pv := providerLimitsView{Available: l.status != nil, UpdatedAt: tsPtr(l.updatedAt), AuthState: l.authState(),
			ErrorCode: strPtr(l.errCode), Windows: []windowView{}}
		pv.IsStale = l.status != nil && limits.IsStale(l.updatedAt, now)
		if l.httpCode != 0 {
			code := l.httpCode
			pv.HTTPStatus = &code
		}
		if l.backoff.Active(now) {
			pv.BackoffUntil = tsPtr(l.backoff.Until())
		}
		var windows []limits.WindowInfo
		if st := l.status; st != nil {
			pv.Plan, pv.HeadlineKey = strPtr(st.Plan()), strPtr(st.HeadlineKey())
			windows = st.Windows(now, warn, crit)
			for _, w := range windows {
				pv.Windows = append(pv.Windows, toWindowView(w))
			}
			if p := d.usage[spec.ProviderID]; p != nil {
				forecasts[spec.ProviderID] = forecast(st, p.activeBlock(now), now)
			}
		}
		f := forecasts[spec.ProviderID]
		if limits.IsLimitWarning(windows, crit, f != nil && f.BeforeReset) {
			v.IsLimitWarning = true
		}
		v.Providers[spec.ProviderID] = pv
	}
	return v, forecasts, v.IsLimitWarning
}

// forecast is the 5 h depletion forecast (usage.md §4): it needs the 5 h window's utilization and
// a reset still ahead of now.
func forecast(st *limits.Status, b *blockState, now time.Time) *forecastView {
	if st.FiveHour == nil || st.FiveHour.Utilization == nil {
		return nil
	}
	reset, ok := st.FiveHour.ResetDate()
	if !ok || !reset.After(now) {
		return nil
	}
	var block *usage.Block
	if b != nil {
		block = &usage.Block{Start: b.Start, End: b.End, Totals: usage.Totals{Tokens: b.Tokens}, TokensPerMinute: b.TokensPerMinute}
	}
	f := usage.FiveHourForecast(*st.FiveHour.Utilization, reset, block, now)
	if f == nil {
		return nil
	}
	return &forecastView{DepletionAt: ts(f.DepletionAt), BeforeReset: f.BeforeReset}
}

func toWindowView(w limits.WindowInfo) windowView {
	v := windowView{Key: w.Key, Kind: w.Kind, ScopeKind: w.ScopeKind, Label: w.Label, Utilization: w.Utilization,
		NotStarted: w.NotStarted, PaceDelta: w.PaceDelta, GaugeTier: w.GaugeTier.String()}
	if w.ResetsAt != nil {
		v.ResetsAt = tsPtr(*w.ResetsAt)
	}
	if w.Pace != nil {
		p := round(*w.Pace, 6)
		v.Pace = &p
	}
	if w.PaceTier != nil {
		t := w.PaceTier.String()
		v.PaceTier = &t
	}
	return v
}

// hasUsageData is upstream "snapshots non-empty": some provider has displayable usage.
func (d *daemon) hasUsageData(now time.Time) bool {
	for _, src := range d.cfg.Providers {
		if d.usage[src.ID()].displayable(now, d.settings.weekday()) {
			return true
		}
	}
	return false
}

// idleFor is upstream timeSinceLastUsage (UsageStore.swift:649-654): nil before any scan, the
// recent-use window when no provider reports a last use, else the time since the newest one.
// Providers whose last scan failed are left out.
func (d *daemon) idleFor(now time.Time) *time.Duration {
	known := false
	var last time.Time
	for _, src := range d.cfg.Providers {
		p := d.usage[src.ID()]
		if !p.Scanned {
			continue
		}
		known = true
		if p.scanErr == nil && p.LastUsage != nil && p.LastUsage.After(last) {
			last = *p.LastUsage
		}
	}
	if !known {
		return nil
	}
	idle := usage.RecentUseWindow
	if !last.IsZero() {
		idle = max(0, now.Sub(last))
	}
	return &idle
}

// usageInput is the companion's view of today (companion.UsageInput): providers with tokens today.
func (d *daemon) usageInput(now time.Time) companion.UsageInput {
	now = now.In(d.loc)
	today := usage.DayKey(now)
	in := companion.UsageInput{TodayByProvider: map[string]int{}, TodayDate: today, HasUsageData: d.hasUsageData(now)}
	for _, src := range d.cfg.Providers {
		if t := d.usage[src.ID()].day(today).Tokens; t > 0 {
			in.TodayByProvider[src.ID()] = int(min(t, maxSafeInt))
		}
	}
	return in
}
