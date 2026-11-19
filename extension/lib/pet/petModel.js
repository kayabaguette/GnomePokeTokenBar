/* Floating pet rules (floating-pet.md §1, §5.1 item 2; upstream
 * FloatingPetPanel.swift, cited as FPP).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 *
 * Coordinates are Clutter stage coordinates: origin top-left, y DOWN. A "pet
 * origin" is the pet box's BOTTOM-left corner {x, y} (what upstream persists,
 * FPP:32-33), so a size change grows the sprite up and right. A "panel origin"
 * is the panel's TOP-left corner. Upstream is y-up; every geometry function
 * here is mirrored (floating-pet.md §5.2 B).
 */

import {_, fmt, ngettext} from '../i18n.js';
import {displayPercent, grouped, percent} from '../format.js';

/* ---- constants (FPP:36-55, :1263-1264, :1302-1304) ----------------------- */

export const CLICK_THRESHOLD_SQ = 16;
export const BUBBLE_HEADROOM = 72;
export const BUBBLE_MIN_WIDTH = 180;
export const BUBBLE_H_PADDING = 8;
export const BUBBLE_CONTENT_WIDTH = BUBBLE_MIN_WIDTH - BUBBLE_H_PADDING * 2;
export const BUBBLE_BODY_LINE_LIMIT = 2;
export const RUN_DURATION_MS = 600;
export const RUN_CURSOR_GAP = 16;
export const BOB_INTERVAL_MS = 500;
export const HEART_DURATION_MS = 1800;
export const EVENT_POP_MS = 6000;
export const BUBBLE_TTL_MS = 6000;
/* The helper writes one limits pass's alerts back to back: collect them this
 * long, then show the strongest (floating-pet.md §4.3). */
export const ALERT_BATCH_MS = 250;
export const HOME_INSET = 24;
export const SIZE_MIN = 48;
export const SIZE_MAX = 384;
export const SIZE_STEP = 8;
export const SIZE_DEFAULT = 96;

/* Prayers are deferred (M2 decision 24): an egg click opens the popover. */
export const PRAYERS_ENABLED = false;

/** Swift `.rounded()`: half away from zero. */
export function swiftRound(v) {
    return Math.sign(v) * Math.round(Math.abs(v));
}

/** The size setting snapped to 48–384 in steps of 8 (SV:377). */
export function snapSize(value) {
    const n = Number(value);
    if (!Number.isFinite(n))
        return SIZE_DEFAULT;
    const stepped = SIZE_MIN + Math.round((n - SIZE_MIN) / SIZE_STEP) * SIZE_STEP;
    return Math.min(SIZE_MAX, Math.max(SIZE_MIN, stepped));
}

/* ---- moves and runs ------------------------------------------------------ */

/**
 * A move persists the origin only at home; a user drag during a run cancels
 * the return and becomes the new home (FPP:67-70).
 * @param {'home'|'away'|'returning'} phase
 */
export function resolveMove(phase, userDragging) {
    if (userDragging)
        return {phase: 'home', persist: true};
    return {phase, persist: phase === 'home'};
}

/**
 * Panel top-left next to the cursor: below-right, flipped left/up at the edges
 * so the panel does not cover the pointer, then clamped to the work area. When
 * the panel is larger than the work area the bottom-left (the pet) stays
 * visible (FPP:75-83, mirrored to y-down).
 */
export function runPanelOrigin(cursor, panel, wa, gap = RUN_CURSOR_GAP) {
    const fitsRight = cursor.x + gap + panel.width <= wa.x + wa.width;
    const fitsBelow = cursor.y + gap + panel.height <= wa.y + wa.height;
    let x = fitsRight ? cursor.x + gap : cursor.x - gap - panel.width;
    let y = fitsBelow ? cursor.y + gap : cursor.y - gap - panel.height;
    x = Math.max(Math.min(x, wa.x + wa.width - panel.width), wa.x);
    y = Math.min(Math.max(y, wa.y), wa.y + wa.height - panel.height);
    return {x, y};
}

/** Motion (bob, run) needs animations on and power saver off (FPP:86). */
export function allowsMotion(animated, reduceMotion) {
    return animated && !reduceMotion;
}

/** A done runs to the cursor unless the pet is already there (FPP:89-91). */
export function runsToCursor(phase, motionAllowed, visible) {
    return phase !== 'away' && motionAllowed && visible;
}

export function shouldBob(working, motionAllowed) {
    return motionAllowed && working;
}

export function isClick(start, end, thresholdSq = CLICK_THRESHOLD_SQ) {
    const dx = end.x - start.x, dy = end.y - start.y;
    return dx * dx + dy * dy < thresholdSq;
}

/* ---- panel geometry (FPP:516-532) ---------------------------------------- */

/** `scale` converts the logical chrome constants to stage px (St scale factor). */
export function panelSize(petSize, showingBubble, scale = 1) {
    if (showingBubble)
        return {width: Math.max(petSize, BUBBLE_MIN_WIDTH * scale), height: petSize + BUBBLE_HEADROOM * scale};
    return {width: petSize, height: petSize};
}

/** Panel top-left for a pet bottom-left. */
export function panelOrigin(pet, petSize, panel) {
    const inset = Math.max(0, (panel.width - petSize) / 2);
    return {x: pet.x - inset, y: pet.y - panel.height};
}

/** Pet bottom-left for a panel top-left. */
export function petOrigin(panelTopLeft, petSize, panel) {
    const inset = Math.max(0, (panel.width - petSize) / 2);
    return {x: panelTopLeft.x + inset, y: panelTopLeft.y + panel.height};
}

/* ---- home: default, clamping, recovery (FPP:786-796) --------------------- */

/** The bottom-right corner of a work area with a 24 px inset. */
export function defaultHome(wa, size) {
    return {x: wa.x + wa.width - size - HOME_INSET, y: wa.y + wa.height - HOME_INSET};
}

/** Keep the pet box inside a work area; left and bottom win when it is too big. */
export function clampHome(home, wa, size) {
    return {
        x: Math.max(Math.min(home.x, wa.x + wa.width - size), wa.x),
        y: Math.min(Math.max(home.y, wa.y + size), wa.y + wa.height),
    };
}

function overlap(a, b) {
    const w = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x);
    const h = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y);
    return w > 0 && h > 0 ? w * h : 0;
}

/**
 * The home to show: the saved origin clamped into the work area it overlaps
 * most, or the default home on the primary work area when it overlaps none
 * (off-screen recovery) or nothing is saved.
 *
 * @param {{x: number, y: number}|null} saved
 * @param {Array<{x, y, width, height}>} workAreas
 * @param {number} primary  index into workAreas
 */
export function resolveHome(saved, workAreas, primary, size) {
    const fallback = workAreas[primary] ?? workAreas[0];
    if (!fallback)
        return saved ?? {x: 120, y: 120};
    if (saved && Number.isFinite(saved.x) && Number.isFinite(saved.y)) {
        const rect = {x: saved.x, y: saved.y - size, width: size, height: size};
        let best = null, bestArea = 0;
        for (const wa of workAreas) {
            const a = overlap(rect, wa);
            if (a > bestArea) {
                best = wa;
                bestArea = a;
            }
        }
        if (best)
            return clampHome(saved, best, size);
    }
    return defaultHome(fallback, size);
}

/* ---- bubbles (FPP:97-112, :186-199, :311-316) ---------------------------- */

/**
 * The one bubble the pet shows: a limit alert, then the latest agent done,
 * then the latest waiting agent.
 * @returns {{kind: 'limit', alert}|{kind: 'agentDone', label, message}|
 *           {kind: 'agentWaiting', label, message, others}|null}
 */
export function bubble(limitAlert, agent) {
    if (limitAlert)
        return {kind: 'limit', alert: limitAlert};
    if (agent?.done)
        return {kind: 'agentDone', label: agent.done.label, message: agent.done.message};
    if (agent?.waiting) {
        return {kind: 'agentWaiting', label: agent.waiting.label, message: agent.waiting.message,
            others: agent.otherWaitingCount};
    }
    return null;
}

const NO_AGENT = {done: null, waiting: null, otherWaitingCount: 0, working: false};

/** Speech outranks hearts: a stroke shows hearts only with no bubble up. */
export function heartsShowing(alertActive, agent = NO_AGENT) {
    return !alertActive && bubble(null, agent) === null;
}

/** Headroom for speech or hearts, agent-only speech included. */
export function isShowingBubble(alertActive, hearts, agent = NO_AGENT) {
    return alertActive || bubble(null, agent) !== null || hearts;
}

/** Pop the callout only for a new event while visible with no bubble (FPP:311-316). */
export function shouldPopEvent(eventSeq, lastSeenSeq, petVisible, bubbleShowing) {
    if (eventSeq === null || eventSeq === undefined || eventSeq === lastSeenSeq)
        return false;
    return petVisible && !bubbleShowing;
}

/** What a primary click does; prayers are deferred, so the egg opens the popover too. */
export function clickAction(subjectIsEgg) {
    return subjectIsEgg && PRAYERS_ENABLED ? 'pray' : 'openPopover';
}

/* ---- limit alert bubble (US:2153-2163) ----------------------------------- */

/**
 * The strongest of one limits pass's `limitAlert` events: critical first, then
 * the higher utilisation; the first wins a tie (Swift `max(by:)`).
 */
export function pickBubbleAlert(alerts) {
    let best = null;
    for (const a of alerts ?? []) {
        if (!a)
            continue;
        if (best === null) {
            best = a;
            continue;
        }
        const ac = a.tier === 'crit', bc = best.tier === 'crit';
        if ((!bc && ac) || (bc === ac && (best.utilization ?? 0) < (a.utilization ?? 0)))
            best = a;
    }
    return best;
}

export function shouldDismissBubble(shownAtMs, nowMs, ttlMs = BUBBLE_TTL_MS) {
    return nowMs - shownAtMs >= ttlMs;
}

/* ---- hover text (FPP:1086-1101, US:749-770) ------------------------------- */

/** The highest limit utilisation over every provider used today, or null. */
export function highestLimitUtilization(state) {
    let max = null;
    for (const [id, limits] of Object.entries(state?.limits?.providers ?? {})) {
        if (!((state?.usage?.providers?.[id]?.today?.tokens ?? 0) > 0))
            continue;
        for (const w of limits?.windows ?? []) {
            if (typeof w?.utilization === 'number' && (max === null || w.utilization > max))
                max = w.utilization;
        }
    }
    return max;
}

/**
 * "Today: 1,234 tokens (limit 83%) · Sulking for 5 hours" plus the event line.
 * Prayers are deferred, so there is no prayer line.
 */
export function hoverText({todayTokens, limitUtilization, mode, sulkIdleSeconds = 0, eventLine = null}) {
    const usage = grouped(todayTokens ?? 0);
    let base;
    if (typeof limitUtilization === 'number') {
        const p = percent(displayPercent(limitUtilization, mode));
        base = fmt(_('Today: %s tokens (limit %s)'), usage, mode === 'remaining' ? fmt(_('%s left'), p) : p);
    } else {
        base = fmt(_('Today: %s tokens'), usage);
    }
    if (sulkIdleSeconds > 0) {
        const hours = Math.trunc(sulkIdleSeconds / 3600);
        if (hours < 48) {
            base += ` · ${fmt(ngettext('Sulking for %d hour', 'Sulking for %d hours', hours), hours)}`;
        } else {
            const days = Math.trunc(hours / 24);
            base += ` · ${fmt(ngettext('Sulking for %d day', 'Sulking for %d days', days), days)}`;
        }
    }
    return eventLine ? `${base}\n${eventLine}` : base;
}

/* ---- petting (FPP:1254-1295) --------------------------------------------- */

/** The stroke length for a pet size (FPP:632). */
export function minStroke(petSize) {
    return Math.max(10, petSize * 0.15);
}

export class PettingDetector {
    static REQUIRED_REVERSALS = 3;
    static WINDOW_S = 1.2;

    constructor() {
        this.reset();
    }

    reset() {
        this._direction = 0;
        this._extremeX = 0;
        this._lastTime = null;
        this._reversals = [];
    }

    /**
     * Feed one horizontal sample (x in px, time in seconds). True when this
     * sample completes a reversal and the recent wiggle has at least three.
     */
    feed(x, time, stroke) {
        const last = this._lastTime;
        if (last === null || time - last > PettingDetector.WINDOW_S) {
            this.reset();
            this._lastTime = time;
            this._extremeX = x;
            return false;
        }
        this._lastTime = time;
        this._reversals = this._reversals.filter(t => !(time - t > PettingDetector.WINDOW_S));
        if (this._direction === 0) {
            if (Math.abs(x - this._extremeX) < stroke)
                return false;
            this._direction = x > this._extremeX ? 1 : -1;
            this._extremeX = x;
            return false;
        }
        if ((x - this._extremeX) * this._direction > 0) {
            this._extremeX = x;
            return false;
        }
        if ((this._extremeX - x) * this._direction < stroke)
            return false;
        this._direction = -this._direction;
        this._extremeX = x;
        this._reversals.push(time);
        return this._reversals.length >= PettingDetector.REQUIRED_REVERSALS;
    }
}

/* ---- heart bubble art (FPP:1297-1342) ------------------------------------ */

export const HEART_PIXEL = 2;
export const HEART_TAIL_OVERLAP = 3;
export const HEART_ROWS = [
    '..OOOOOOOOOOOOO..',
    '.OWWWWWWWWWWWWWO.',
    'OWWWWWWWWWWWWWWWO',
    'OWWWWRRRWRRRWWWWO',
    'OWWWRHHRRRRRRWWWO',
    'OWWWRHRRRRRRDWWWO',
    'OWWWRRRRRRRRDWWWO',
    'OWWWWRRRRRRDWWWWO',
    'OWWWWWRRRRDWWWWWO',
    'OWWWWWWRRDWWWWWWO',
    'OWWWWWWWDWWWWWWWO',
    'OWWWWWWWWWWWWWWWO',
    '.OWWWWWWWWWWWWWO.',
    '..OOOOOWWWOOOOO..',
    '.......OWO.......',
    '........O........',
];
/* RGB per cell; the same in light and dark mode (game style). */
export const HEART_COLORS = {
    O: [0x20, 0x20, 0x20],
    W: [0xFF, 0xFF, 0xFF],
    R: [0xE0, 0x30, 0x3C],
    D: [0xA8, 0x20, 0x2C],
    H: [0xFF, 0x9A, 0xA2],
};
export const HEART_HEIGHT = HEART_ROWS.length * HEART_PIXEL;
export const HEART_WIDTH = HEART_ROWS[0].length * HEART_PIXEL;

/* The speech bubble's tail in the same pixel style: its top row covers the
 * bubble's 2 px bottom border, so the tail opens into the bubble. */
export const TAIL_ROWS = [
    'OWWWWWWWO',
    '.OWWWWWO.',
    '..OWWWO..',
    '...OWO...',
    '....O....',
];

/** Pixel art as non-premultiplied RGBA bytes, one byte quad per cell. */
export function pixelArtRgba(rows) {
    const w = rows[0].length;
    const bytes = new Uint8Array(w * rows.length * 4);
    rows.forEach((row, y) => {
        for (let x = 0; x < w; x++) {
            const c = HEART_COLORS[row[x]];
            if (!c)
                continue;
            const i = (y * w + x) * 4;
            bytes.set([...c, 0xFF], i);
        }
    });
    return bytes;
}

/** The 17×16 heart art. */
export function heartRgba() {
    return pixelArtRgba(HEART_ROWS);
}

/**
 * Y of the bubble's top edge from the pet box top, so its tail tip lands
 * `overlap` px inside the sprite's visible top; whole pixels (SL:440-442).
 */
export function bubbleTopOffset(visibleTop, bubbleHeight, overlap) {
    return swiftRound(visibleTop + overlap - bubbleHeight);
}
