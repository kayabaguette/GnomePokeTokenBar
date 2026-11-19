/* Usage recap screen model (recap-heatmap.md §1).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 * Ported from UI/UsageRecapView.swift. The helper computes the recap
 * (`getRecap`, protocol §7.5); this module turns a reply into labels,
 * meter blocks, the delta chip and the navigation state.
 */

import {_, fmt} from './i18n.js';
import {dayKeyToDate, grouped, percent} from './format.js';

export const SCOPES = ['week', 'month', 'year'];

export function normalizeScope(scope) {
    return SCOPES.includes(scope) ? scope : 'week';
}

/** Segmented picker titles (`recapScopeName`). */
export function scopeName(scope) {
    switch (normalizeScope(scope)) {
    case 'month': return _('Month');
    case 'year': return _('Year');
    default: return _('Week');
    }
}

/** The "back to the running period" button (`recapCurrentPeriod`). */
export function currentPeriodName(scope) {
    switch (normalizeScope(scope)) {
    case 'month': return _('This month');
    case 'year': return _('This year');
    default: return _('This week');
    }
}

/** Caption under the card while the period runs (`recapCompareSoFar`). */
export function compareSoFarText(scope) {
    switch (normalizeScope(scope)) {
    case 'month': return _('Compared with the same days last month');
    case 'year': return _('Compared with the same days last year');
    default: return _('Compared with the same days last week');
    }
}

/* ---- Navigation (UsageRecapView.swift:370-399) ------------------------ */

/** `getRecap` rejects offsets below this (protocol §7.5). */
export const MIN_OFFSET = -1000;

/** Previous is enabled iff the helper says the ledger reaches the period before. */
export function canGoBack(recap) {
    return recap?.canGoBack === true;
}

/** Next is enabled iff the shown period is in the past. */
export function canGoForward(offset) {
    return offset < 0;
}

/** "This week" is enabled iff another period is shown. */
export function canGoToCurrent(offset) {
    return offset !== 0;
}

/**
 * The offset after one Previous (-1) or Next (+1) step; a disabled step
 * keeps the offset. Offsets stay in MIN_OFFSET..0.
 */
export function step(offset, direction, recap) {
    if (direction < 0 && canGoBack(recap))
        return Math.max(MIN_OFFSET, offset - 1);
    if (direction > 0 && canGoForward(offset))
        return Math.min(0, offset + 1);
    return offset;
}

/* ---- Card numbers ----------------------------------------------------- */

/**
 * Change against the previous period, computed here so the chip cannot
 * disagree with the totals (protocol §7.5): null when there is nothing to
 * compare with (no previous total, or a previous total of 0).
 */
export function delta(recap) {
    const prev = recap?.previousTotal;
    if (typeof prev !== 'number' || !(prev > 0))
        return null;
    return ((Number(recap.total) || 0) - prev) / prev;
}

/** "▲150%" / "▼60.9%" / "▲0%", or null for no chip (UsageRecapView.swift:232-245). */
export function chipText(d) {
    if (typeof d !== 'number' || !Number.isFinite(d))
        return null;
    return `${d >= 0 ? '▲' : '▼'}${percent(Math.abs(d) * 100)}`;
}

/** Best-streak value, "6d" (`recapDays`). */
export function daysText(n) {
    return fmt(_('%dd'), n);
}

/** "active/counted" (`UsageRecapView.swift:211`). */
export function activeDaysText(recap) {
    return `${Number(recap?.activeDays) || 0}/${Number(recap?.countedDays) || 0}`;
}

/** Strip chips: at most 4, newest first as sent. */
export const STRIP_MAX = 4;

export function stripGraduates(recap) {
    return Array.isArray(recap?.graduates) ? recap.graduates.slice(0, STRIP_MAX) : [];
}

/* ---- Meters (UsageRecapView.swift:247-293) ---------------------------- */

export const METER_BLOCKS = 8;

/** Column spacing per scope. */
export function meterSpacing(scope) {
    switch (normalizeScope(scope)) {
    case 'month': return 1.5;
    case 'year': return 4;
    default: return 6;
    }
}

/** The meter peak: the largest bucket, at least 1. */
export function meterPeak(buckets) {
    return Math.max(1, ...(buckets ?? []).map(b => Number(b.tokens) || 0));
}

/**
 * Lit blocks, filled from the bottom: a bucket with usage always lights at
 * least one, so "quiet" and "nothing" stay distinct.
 */
export function meterLit(tokens, peak) {
    const t = Number(tokens) || 0;
    if (!(t > 0))
        return 0;
    const p = Math.max(1, Number(peak) || 0);
    return Math.min(METER_BLOCKS, Math.max(1, Math.round((t / p) * METER_BLOCKS)));
}

/* ---- Dates and labels ------------------------------------------------- */

function format(date, options, locale) {
    try {
        return new Intl.DateTimeFormat(locale, options).format(date);
    } catch {
        return '';
    }
}

/** Long day label, template "EEEE d MMM": en "Thursday, Sep 17". */
export function dayLabel(key, locale = undefined) {
    const d = dayKeyToDate(key);
    return d ? format(d, {weekday: 'long', day: 'numeric', month: 'short'}, locale) : '';
}

function monthLabel(key, locale) {
    const d = dayKeyToDate(key);
    return d ? format(d, {month: 'long', year: 'numeric'}, locale) : '';
}

/** Whether this runtime has Intl range formatting (GJS 1.90 does; protocol §14 item 22). */
export const HAS_FORMAT_RANGE = typeof Intl.DateTimeFormat.prototype.formatRange === 'function';

const RANGE_OPTIONS = {day: 'numeric', month: 'short', year: 'numeric'};

/**
 * Week title from two inclusive day keys: the locale's own range format
 * ("Oct 11 – 17, 2026") when available, else two full dates joined by a dash.
 */
export function weekRangeLabel(startKey, endKey, locale = undefined, useRange = HAS_FORMAT_RANGE) {
    const a = dayKeyToDate(startKey);
    const b = dayKeyToDate(endKey);
    if (!a || !b)
        return '';
    try {
        const f = new Intl.DateTimeFormat(locale, RANGE_OPTIONS);
        if (useRange)
            return f.formatRange(a, b);
        return fmt(_('%s – %s'), f.format(a), f.format(b));
    } catch {
        return `${startKey} – ${endKey}`;
    }
}

/** The card header (UsageRecapView.swift:54-69); the view prints it uppercased. */
export function periodLabel(recap, locale = undefined, useRange = HAS_FORMAT_RANGE) {
    if (!recap?.start)
        return '';
    switch (normalizeScope(recap.scope)) {
    case 'month': return monthLabel(recap.start, locale);
    case 'year': return String(recap.start).slice(0, 4);
    default: return weekRangeLabel(recap.start, recap.end, locale, useRange);
    }
}

/**
 * One short label per bucket (UsageRecapView.swift:74-95): week = the
 * weekday's first two letters (one letter collides: Tuesday/Thursday),
 * month = the day number on days 1, 8, 15, 22 and 29, year = the month's
 * initial.
 */
export function bucketLabels(buckets, scope, locale = undefined) {
    const s = normalizeScope(scope);
    return (buckets ?? []).map(b => {
        const d = dayKeyToDate(b.key);
        if (!d)
            return '';
        if (s === 'week')
            return format(d, {weekday: 'short'}, locale).slice(0, 2).toLocaleUpperCase(locale);
        if (s === 'month')
            return [1, 8, 15, 22, 29].includes(d.getDate()) ? String(d.getDate()) : '';
        return format(d, {month: 'narrow'}, locale).toLocaleUpperCase(locale);
    });
}

/**
 * Tooltip of a meter column: "Thursday, Sep 17, 12,345", or "…, no data"
 * for a day the ledger cannot vouch for (a known zero reads 0).
 */
export function bucketDescription(bucket, scope, locale = undefined) {
    const name = normalizeScope(scope) === 'year' ? monthLabel(bucket?.key, locale) : dayLabel(bucket?.key, locale);
    const value = bucket?.hasData ? grouped(bucket.tokens, locale) : _('no data');
    return fmt(_('%s, %s'), name, value);
}

/** First caption line under the card, or null without a best day. */
export function bestDayLine(recap, locale = undefined) {
    const best = recap?.bestDay;
    if (!best?.key)
        return null;
    return fmt(_('Your best day was %s — %s tokens.'), dayLabel(best.key, locale), grouped(best.tokens, locale));
}

/** Second caption line: only while the period runs and a comparison exists. */
export function compareLine(recap) {
    if (!recap?.isInProgress || delta(recap) === null)
        return null;
    return compareSoFarText(recap.scope);
}

/* ---- Sprites ---------------------------------------------------------- */

/** The strip's first sprite: the companion being raised, or the egg (protocol §7.5). */
export function companionSpriteRequest(companion) {
    const a = companion?.active;
    if (!companion || companion.phase === 'egg' || !a || !(a.speciesId > 0))
        return {egg: true};
    return {speciesId: a.speciesId, form: a.form ?? null, shiny: Boolean(a.isShiny), back: false, animated: false};
}

export function graduateSpriteRequest(g) {
    return {speciesId: g.speciesId, form: g.form ?? null, shiny: Boolean(g.isShiny), back: false, animated: false};
}

/**
 * What a new `state` must change for the visible recap to be fetched again
 * (protocol §7.5): today's key or today's tokens.
 */
export function refreshKey(state) {
    const usage = state?.usage;
    return `${usage?.calendar?.today ?? ''}|${usage?.combined?.today?.tokens ?? 0}`;
}
