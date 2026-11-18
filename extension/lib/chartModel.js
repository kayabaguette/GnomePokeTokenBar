/* Daily bar chart model: columns, bar geometry, axis labels, readout.
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 * Ported from UI/PopoverView.swift:1480-1669 (DailyTrendMetrics). Day keys are
 * handled as calendar dates in UTC arithmetic, so DST never shifts a column.
 */

import {_} from './i18n.js';
import {compact, costText, dayStamp} from './format.js';

/** Bar track height, in px (DailyTrendMetrics.track). */
export const TRACK = 26;
/** Gap between bars (DailyTrendMetrics.spacing). */
export const SPACING = 1.5;
/** A zero day still draws this tick so the column stays visible. */
export const BASELINE = 1.5;
/** Weekend underline thickness. */
export const TICK = 1.5;

export const WINDOWS = ['7d', '14d', '21d', 'month'];
const ROLLING_DAYS = {'7d': 7, '14d': 14, '21d': 21};

export function normalizeWindow(window) {
    return WINDOWS.includes(window) ? window : '14d';
}

export function windowTitle(window) {
    switch (normalizeWindow(window)) {
    case '7d': return _('Last 7 days');
    case '21d': return _('Last 21 days');
    case 'month': return _('This month');
    default: return _('Last 14 days');
    }
}

/** The next window in the cycle 7d → 14d → 21d → month → 7d. */
export function nextWindow(window) {
    const i = WINDOWS.indexOf(normalizeWindow(window));
    return WINDOWS[(i + 1) % WINDOWS.length];
}

function parse(key) {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(key));
    return m ? {y: Number(m[1]), m: Number(m[2]), d: Number(m[3])} : null;
}

function utcDay(key) {
    const p = parse(key);
    return p ? Date.UTC(p.y, p.m - 1, p.d) : null;
}

function keyOf(utcMs) {
    const d = new Date(utcMs);
    const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
    const dd = String(d.getUTCDate()).padStart(2, '0');
    return `${d.getUTCFullYear()}-${mm}-${dd}`;
}

/** Calendar-day stepping: `addDays('2026-10-31', 1)` is `'2026-11-01'`. */
export function addDays(key, n) {
    const p = parse(key);
    if (!p)
        return key;
    return keyOf(Date.UTC(p.y, p.m - 1, p.d + n));
}

/** Whole calendar days from `from` to `to`, or null for a bad key. */
export function dayDifference(from, to) {
    const a = utcDay(from);
    const b = utcDay(to);
    if (a === null || b === null)
        return null;
    return Math.round((b - a) / 86_400_000);
}

export function daysInMonth(year, month) {
    return new Date(Date.UTC(year, month, 0)).getUTCDate();
}

/** Saturday or Sunday. The locale's weekend is not available in GJS's Intl. */
export function isWeekend(key) {
    const t = utcDay(key);
    if (t === null)
        return false;
    const wd = new Date(t).getUTCDay();
    return wd === 0 || wd === 6;
}

/** A protocol Series as `{date → {tokens, costUsd, coverage}}`. */
function seriesMap(series) {
    const map = new Map();
    if (!series || !Array.isArray(series.tokens))
        return map;
    series.tokens.forEach((tokens, i) => {
        map.set(addDays(series.start, i), {
            tokens: Number(tokens) || 0,
            costUsd: Number(series.costUsd?.[i]) || 0,
            coverage: series.coverage?.[i] ?? 'empty',
        });
    });
    return map;
}

/**
 * The chart columns for `window`: the last 7/14/21 days of `recentDaily`, or
 * the whole month from `monthDaily` with the days after today marked future
 * (DailyTrendMetrics.columns / rollingColumns / monthColumns).
 *
 * @param {object} aggregate  usage Aggregate (`recentDaily`, `monthDaily`)
 * @param {string} today      day key
 * @param {string} window     daily-trend-window
 */
export function columns(aggregate, today, window) {
    const w = normalizeWindow(window);
    if (w === 'month') {
        const p = parse(today);
        if (!p)
            return [];
        const map = seriesMap(aggregate?.monthDaily);
        const month = today.slice(0, 7);
        const out = [];
        for (let day = 1; day <= daysInMonth(p.y, p.m); day++) {
            const date = `${month}-${String(day).padStart(2, '0')}`;
            const v = map.get(date);
            out.push({
                date,
                tokens: v?.tokens ?? 0,
                costUsd: v?.costUsd ?? 0,
                coverage: v?.coverage ?? 'empty',
                isFuture: day > p.d,
            });
        }
        return out;
    }
    const map = seriesMap(aggregate?.recentDaily);
    const count = ROLLING_DAYS[w];
    const out = [];
    for (let offset = count - 1; offset >= 0; offset--) {
        const date = addDays(today, -offset);
        const v = map.get(date);
        out.push({
            date,
            tokens: v?.tokens ?? 0,
            costUsd: v?.costUsd ?? 0,
            coverage: v?.coverage ?? 'empty',
            isFuture: false,
        });
    }
    return out;
}

export function peak(cols) {
    return cols.reduce((max, c) => (c.isFuture ? max : Math.max(max, c.tokens)), 0);
}

/** Peak is only worth printing when more than one day was used (showsPeak). */
export function showsPeak(cols) {
    return cols.filter(c => c.tokens > 0).length > 1;
}

/** Bar height in px: at least the baseline tick, else proportional to the peak. */
export function barHeight(tokens, peakTokens, track = TRACK) {
    if (!(peakTokens > 0) || !(tokens > 0))
        return BASELINE;
    return Math.max(BASELINE, Math.min(1, tokens / peakTokens) * track);
}

/** Horizontal geometry of column `index` in a chart `width` px wide. */
export function barRect(index, count, width) {
    const w = Math.max(0, (width - SPACING * (count - 1)) / count);
    return {x: index * (w + SPACING), width: w};
}

/** The column under x, or -1. Each column owns its gap, so there is no dead zone. */
export function columnAt(x, count, width) {
    if (count <= 0 || width <= 0 || x < 0 || x > width)
        return -1;
    const pitch = (width + SPACING) / count;
    return Math.min(count - 1, Math.floor(x / pitch));
}

/** Weekend underline: past days and today only (showsWeekendTick). */
export function showsWeekendTick(col) {
    return !col.isFuture && isWeekend(col.date);
}

/**
 * The axis label for `date`, or null (DailyTrendMetrics.axisLabel,
 * PopoverView.swift:1590-1644). Today is always labelled; the month window
 * labels the 1st and multiples of 7 away from today; rolling windows label
 * every 7th day back from today plus the 1st, with collision suppression.
 */
export function axisLabel(date, today, window, labelInterval = 7, minimumSeparation = 3) {
    const p = parse(date);
    if (!p)
        return null;
    const dayOfMonth = p.d;
    if (date === today)
        return String(dayOfMonth);

    const w = normalizeWindow(window);
    if (w === 'month') {
        const isRegular = dayOfMonth === 1 || (labelInterval > 0 && dayOfMonth % labelInterval === 0);
        if (!isRegular)
            return null;
        const t = parse(today);
        if (t && Math.abs(dayOfMonth - t.d) < minimumSeparation)
            return null;
        return String(dayOfMonth);
    }

    const diff = dayDifference(date, today);
    if (diff === null || diff <= 0)
        return null;

    if (w === '7d') {
        if (dayOfMonth === 1) {
            // The midpoint of a 7-day window is diff 3; the 1st needs room from it and from today.
            if (!(diff >= minimumSeparation && (diff === 3 || Math.abs(diff - 3) >= minimumSeparation)))
                return null;
            return String(dayOfMonth);
        }
        if (diff === 3 && diff >= minimumSeparation)
            return String(dayOfMonth);
        return null;
    }

    // 14d, 21d
    if (dayOfMonth === 1)
        return diff >= minimumSeparation ? String(dayOfMonth) : null;
    if (diff % 7 === 0 && diff >= minimumSeparation) {
        // The 1st of this month wins as a landmark when it is in the window.
        const diffToFirst = diff - (dayOfMonth - 1);
        if (diffToFirst >= minimumSeparation && Math.abs(dayOfMonth - 1) < minimumSeparation)
            return null;
        const daysToNextFirst = daysInMonth(p.y, p.m) - dayOfMonth + 1;
        const diffToNextFirst = diff - daysToNextFirst;
        if (diffToNextFirst >= minimumSeparation && daysToNextFirst < minimumSeparation)
            return null;
        return String(dayOfMonth);
    }
    return null;
}

/** "Wed, 11/18 187.3M $124.35": a column's day stamp, tokens and optional cost. */
export function dayText(col, showsCost, locale = undefined) {
    const parts = [dayStamp(col.date, locale), compact(col.tokens)];
    if (showsCost)
        parts.push(costText({usd: col.costUsd, coverage: col.coverage}));
    return parts.join(' ');
}

/** Today's readout for the caption row; empty if today is not in the columns. */
export function readout(cols, today, showsCost, locale = undefined) {
    const col = cols.find(c => c.date === today);
    return col ? dayText(col, showsCost, locale) : '';
}
