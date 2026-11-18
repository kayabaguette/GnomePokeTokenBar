/* Number, cost, percent and time formatters.
 *
 * Pure module: no gi:// imports, so it is unit-tested under plain `gjs -m`.
 * Ported from the upstream Swift sources (spec usage.md §6.9):
 *   Core/TokenFormatter.swift (compact, costCompact, cost, percent),
 *   UI/PopoverView.swift:704-723 (reset clock suffix),
 *   UI/PopoverView.swift:1663 (day stamp).
 * The helper never formats (protocol §1.1); everything shown is built here.
 */

import {_, fmt} from './i18n.js';

const HOUR_S = 3600;
const DAY_S = 86400;
/** A reset this close gets a bare HH:MM suffix (PopoverView.swift:708). */
const RESET_CLOCK_WINDOW_S = 6 * HOUR_S;

/** Coverage values from protocol §12; anything else falls back to `unavailable`. */
const COVERAGE = new Set(['empty', 'reported', 'estimated', 'unavailable', 'partial']);

export function normalizeCoverage(value) {
    return COVERAGE.has(value) ? value : 'unavailable';
}

function trimDecimals(value, decimals) {
    let s = value.toFixed(decimals);
    if (s.includes('.'))
        s = s.replace(/0+$/, '').replace(/\.$/, '');
    return s;
}

/**
 * 987 → "987", 12_345 → "12.3K", 190_612_940 → "190.6M", 1_240_000_000 → "1.24B".
 * K and M keep one decimal, B two; trailing zeros are trimmed; a value that
 * would round to 1000.0 is promoted to the next unit (TokenFormatter.swift:5-19).
 */
export function compact(value) {
    const n = Math.round(Number(value) || 0);
    const v = Math.abs(n);
    const sign = n < 0 ? '-' : '';
    if (v < 1000)
        return String(n);
    if (v < 999_950)
        return `${sign}${trimDecimals(v / 1e3, 1)}K`;
    if (v < 999_950_000)
        return `${sign}${trimDecimals(v / 1e6, 1)}M`;
    return `${sign}${trimDecimals(v / 1e9, 2)}B`;
}

/** Thousands grouping in the session's region (190,612,940 / 190 612 940). */
export function grouped(value, locale = undefined) {
    const n = Math.round(Number(value) || 0);
    try {
        return new Intl.NumberFormat(locale).format(n);
    } catch {
        return String(n);
    }
}

/** "$124.35" (popover and right-click menu). */
export function costFull(usd) {
    return `$${(Number(usd) || 0).toFixed(2)}`;
}

/**
 * Short cost for the top bar: "$9.5" / "$311" / "$1.2K". The band is judged on
 * the rounded string, not the raw value, so 99.96 gives "$100", not "$100.0"
 * (TokenFormatter.swift:40-46).
 */
export function costCompact(usd) {
    const v = Number(usd) || 0;
    const tenths = v.toFixed(1);
    if (Number(tenths) < 100)
        return `$${tenths}`;
    const whole = v.toFixed(0);
    if (Number(whole) < 10_000)
        return `$${whole}`;
    return `$${(v / 1000).toFixed(1)}K`;
}

/**
 * A protocol Cost `{usd, coverage}` as text. `unavailable` coverage (no known
 * price at all) shows `$—` in compact surfaces and "Unavailable" elsewhere;
 * `partial` shows the amount (callers add the hint).
 */
export function costText(cost, compact_ = false) {
    if (normalizeCoverage(cost?.coverage) === 'unavailable')
        return compact_ ? '$—' : _('Unavailable');
    return compact_ ? costCompact(cost?.usd) : costFull(cost?.usd);
}

/** 79.96 → "80%", 88.35 → "88.3%" (TokenFormatter.swift:49-52). */
export function percent(value) {
    let s = (Number(value) || 0).toFixed(1);
    if (s.endsWith('.0'))
        s = s.slice(0, -2);
    if (s === '-0')
        s = '0';
    return `${s}%`;
}

/** Used vs remaining: remaining shows max(0, 100 − u); tiers never flip (protocol §5.3). */
export function displayPercent(utilization, mode) {
    const u = Number(utilization) || 0;
    return mode === 'remaining' ? Math.max(0, 100 - u) : u;
}

/** Popover percent text: "62%" or "38% left" (PopoverView.swift:699-702). */
export function limitPercentText(utilization, mode) {
    const text = percent(displayPercent(utilization, mode));
    return mode === 'remaining' ? fmt(_('%s left'), text) : text;
}

/** Top-bar growth item: "Hatch 42%" for the egg, "Growth 42%" otherwise. */
export function growthText(growthPercent, isEgg) {
    const p = Math.max(0, Math.trunc(Number(growthPercent) || 0));
    return isEgg ? fmt(_('Hatch %d%%'), p) : fmt(_('Growth %d%%'), p);
}

/** RFC 3339 timestamp → epoch ms, or null. */
export function parseTs(ts) {
    if (typeof ts !== 'string' || ts.length === 0)
        return null;
    const ms = Date.parse(ts);
    return Number.isFinite(ms) ? ms : null;
}

function pad2(n) {
    return String(n).padStart(2, '0');
}

/** 24-hour local clock "HH:MM" (protocol §10: clocks are always 24 h). */
export function clock(ms) {
    const d = new Date(ms);
    return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

/**
 * Countdown "4d 2h" / "1h 10m" / "12m" / "<1m". Negative durations (a reset
 * already passed, or a clock skew between helper and shell) clamp to zero.
 */
export function countdown(seconds) {
    const s = Math.max(0, Math.floor(Number(seconds) || 0));
    const d = Math.floor(s / DAY_S);
    const h = Math.floor((s % DAY_S) / HOUR_S);
    const m = Math.floor((s % HOUR_S) / 60);
    if (d > 0)
        return fmt(_('%dd %dh'), d, h);
    if (h > 0)
        return fmt(_('%dh %dm'), h, m);
    if (m > 0)
        return fmt(_('%dm'), m);
    return _('<1m');
}

function sameLocalDay(aMs, bMs) {
    const a = new Date(aMs);
    const b = new Date(bMs);
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() &&
        a.getDate() === b.getDate();
}

/**
 * Absolute reset time after a countdown: bare "HH:MM" when the reset is today
 * or within 6 h, else weekday + day + time so "in 2 days" is unambiguous
 * (PopoverView.swift:704-720, template "EEEEdHHmm").
 */
export function resetClock(resetMs, nowMs, locale = undefined) {
    if (sameLocalDay(resetMs, nowMs) || (resetMs - nowMs) / 1000 <= RESET_CLOCK_WINDOW_S)
        return clock(resetMs);
    let day;
    try {
        day = new Intl.DateTimeFormat(locale, {weekday: 'short', day: 'numeric'}).format(new Date(resetMs));
    } catch {
        day = String(new Date(resetMs).getDate());
    }
    return fmt(_('%s, %s'), day, clock(resetMs));
}

/** "1h 10m (14:30)": countdown plus the absolute reset time. */
export function resetText(resetMs, nowMs, locale = undefined) {
    return `${countdown((resetMs - nowMs) / 1000)} (${resetClock(resetMs, nowMs, locale)})`;
}

/** "just now" / "5 min ago" / "3 h ago" / "2 d ago"; future times clamp to "just now". */
export function relativeAgo(ms, nowMs) {
    const s = Math.max(0, (nowMs - ms) / 1000);
    if (s < 45)
        return _('just now');
    if (s < HOUR_S)
        return fmt(_('%d min ago'), Math.max(1, Math.round(s / 60)));
    if (s < DAY_S)
        return fmt(_('%d h ago'), Math.floor(s / HOUR_S));
    return fmt(_('%d d ago'), Math.floor(s / DAY_S));
}

/** `yyyy-MM-dd` → local Date at midnight, or null. */
export function dayKeyToDate(key) {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(key));
    if (!m)
        return null;
    return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
}

/**
 * Month/day plus weekday in the locale's own order: en "Wed, 11/18",
 * fr "mer. 18/11" (PopoverView.swift:1663, template "MdE").
 */
export function dayStamp(key, locale = undefined) {
    const d = dayKeyToDate(key);
    if (!d)
        return '';
    try {
        return new Intl.DateTimeFormat(locale, {weekday: 'short', month: 'numeric', day: 'numeric'}).format(d);
    } catch {
        return key;
    }
}
