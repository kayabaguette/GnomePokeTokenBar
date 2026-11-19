/* 16-week activity heatmap and Activity panel model (recap-heatmap.md §2).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 * Ported from UI/ActivityHeatmap.swift and UI/ActivityVisualization.swift.
 * The helper sends the window as `usage.combined.heatmap {start, first,
 * tokens[112]}` (protocol §5.2), so the week math and the ledger/series
 * overlay stay in Go; this module only lays the cells out and reads them.
 */

import {_} from './i18n.js';
import {compact, dayKeyToDate, dayStamp} from './format.js';
import {addDays, dayDifference, dayText} from './chartModel.js';

/* ---- Activity panel views (ActivityVisualization.swift:15-31) --------- */

/** The enum order is the click cycle: bars first (the default), then the heatmap. */
export const ACTIVITY_VIEWS = ['bars', 'heatmap'];

/** GSettings `activity-view`; an unknown value takes the default (protocol §9.1). */
export function normalizeActivityView(view) {
    return ACTIVITY_VIEWS.includes(view) ? view : 'bars';
}

/** The next view, wrapping, so a future view joins the cycle by being listed. */
export function nextActivityView(view) {
    const i = ACTIVITY_VIEWS.indexOf(normalizeActivityView(view));
    return ACTIVITY_VIEWS[(i + 1) % ACTIVITY_VIEWS.length];
}

/** Caption title of the heatmap view (`recentHeatmap`). */
export function heatmapTitle() {
    return _('16 weeks');
}

/* ---- Geometry (ActivityHeatmap.swift:21-22, 254-287) ------------------ */

export const WEEKS = 16;
export const DAYS = 7;
export const CELL_COUNT = WEEKS * DAYS;
export const CELL_HEIGHT = 10;
export const GAP = 2;
/** The weekday-letter column and its gap to the grid (`spacing + 2`). */
export const LABEL_WIDTH = 10;
export const LABEL_GAP = 4;
export const GRID_X = LABEL_WIDTH + LABEL_GAP;
export const HEIGHT = DAYS * CELL_HEIGHT + (DAYS - 1) * GAP;
/** Continuous corner radius of a cell. */
export const RADIUS = 2;

function cellWidth(width) {
    return Math.max(0, (width - GRID_X - GAP * (WEEKS - 1)) / WEEKS);
}

/** Rectangle of the cell at (col, row) in a widget `width` px wide. */
export function cellRect(col, row, width) {
    const w = cellWidth(width);
    return {x: GRID_X + col * (w + GAP), y: row * (CELL_HEIGHT + GAP), width: w, height: CELL_HEIGHT};
}

/**
 * The cell under (x, y), or null. Each cell owns the gap after it, so the
 * readout does not flicker back to today between cells.
 */
export function cellAt(x, y, width) {
    if (!(width > GRID_X) || x < GRID_X || x > width || y < 0 || y > HEIGHT)
        return null;
    const pitchX = (width - GRID_X + GAP) / WEEKS;
    const pitchY = (HEIGHT + GAP) / DAYS;
    const col = Math.min(WEEKS - 1, Math.floor((x - GRID_X) / pitchX));
    const row = Math.min(DAYS - 1, Math.floor(y / pitchY));
    return {col, row};
}

/* ---- Cells ------------------------------------------------------------ */

const KEY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * The grid as 16 columns of 7 cells `{date, tokens, outside}`, oldest week
 * on the left; index `col*7 + row` is day `start + index` (protocol §5.2).
 * A cell is outside the range before `first` and after `today`: it keeps
 * its place and draws nothing (ActivityHeatmap.swift:58-74, 100-110).
 *
 * @param {{start: string, first: string, tokens: number[]}} heatmap
 * @param {string} today  usage.calendar.today
 * @returns {Array<Array<{date: string, tokens: number, outside: boolean}>>}  [] for a bad payload
 */
export function buildGrid(heatmap, today) {
    const start = heatmap?.start;
    if (typeof start !== 'string' || !KEY.test(start))
        return [];
    const tokens = Array.isArray(heatmap.tokens) ? heatmap.tokens : [];
    const first = typeof heatmap.first === 'string' && KEY.test(heatmap.first) && heatmap.first > start
        ? heatmap.first : start;
    const end = typeof today === 'string' && KEY.test(today) ? today : '';
    const columns = [];
    for (let col = 0; col < WEEKS; col++) {
        const cells = [];
        for (let row = 0; row < DAYS; row++) {
            const i = col * DAYS + row;
            const date = addDays(start, i);
            cells.push({date, tokens: Math.max(0, Number(tokens[i]) || 0), outside: date < first || date > end});
        }
        columns.push(cells);
    }
    return columns;
}

function inRange(grid) {
    return grid.flat().filter(c => !c.outside);
}

/** The window peak over in-range cells only (an outside cell is never data). */
export function peakOf(grid) {
    return inRange(grid).reduce((max, c) => Math.max(max, c.tokens), 0);
}

/** In-range days with usage. */
export function usedCount(grid) {
    return inRange(grid).filter(c => c.tokens > 0).length;
}

/** Peak is hidden for a single used day, as for the bars (ActivityHeatmap.swift:205-206). */
export function showsPeak(grid) {
    return usedCount(grid) > 1;
}

/** The in-range cell for `date`, or null. */
export function cellFor(grid, date) {
    return inRange(grid).find(c => c.date === date) ?? null;
}

/* ---- Intensity (ActivityHeatmap.swift:135-165) ------------------------ */

/** Levels 0..4; level 0 is a drawn, unused day. Outside cells are level -1 (clear). */
export const LEVEL_COUNT = 5;

/**
 * Quartiles of the window peak, boundaries inclusive. The view only draws
 * when peak > 0; the guard keeps a zero peak from dividing by zero.
 */
export function level(tokens, peak) {
    if (!(tokens > 0) || !(peak > 0))
        return 0;
    const ratio = tokens / peak;
    if (ratio <= 0.25)
        return 1;
    if (ratio <= 0.5)
        return 2;
    if (ratio <= 0.75)
        return 3;
    return 4;
}

/** The CSS color property of a level (`-ptb-heat-0..4`), or null for clear (-1). */
export function levelColorProperty(lvl) {
    return Number.isInteger(lvl) && lvl >= 0 && lvl < LEVEL_COUNT ? `-ptb-heat-${lvl}` : null;
}

/* ---- Labels and readout ----------------------------------------------- */

/**
 * One narrow weekday letter per row, starting from `start`'s weekday (the
 * applied week start): en Monday-first gives M T W T F S S.
 */
export function weekdayLabels(start, locale = undefined) {
    const d = dayKeyToDate(start);
    if (!d)
        return [];
    let f;
    try {
        f = new Intl.DateTimeFormat(locale, {weekday: 'narrow'});
    } catch {
        return [];
    }
    const out = [];
    for (let i = 0; i < DAYS; i++)
        out.push(f.format(new Date(d.getFullYear(), d.getMonth(), d.getDate() + i, 12)));
    return out;
}

function seriesTokens(series, date) {
    if (!series || !Array.isArray(series.tokens))
        return null;
    const i = dayDifference(series.start, date);
    if (i === null || i < 0 || i >= series.tokens.length)
        return null;
    return Number(series.tokens[i]) || 0;
}

/**
 * The caption readout for `date` (the hovered day, else today), tokens only
 * (no cost in readouts, protocol §14 item 20). A day of this month's live
 * series reads like the bars; any other in-range day reads the grid; a day
 * outside the range reads nothing (ActivityHeatmap.swift:245-251).
 *
 * @param {Array} grid           buildGrid()
 * @param {string} date
 * @param {object} [monthDaily]  usage.combined.monthDaily
 */
export function readout(grid, date, monthDaily = null, locale = undefined) {
    const cell = cellFor(grid, date);
    if (!cell)
        return '';
    const live = seriesTokens(monthDaily, date);
    if (live !== null)
        return dayText({date, tokens: live}, false, locale);
    return `${dayStamp(date, locale)} ${compact(cell.tokens)}`;
}
