/* Preferences rules shared by prefs.js and the tests: refresh presets, the
 * warn/crit gap, and the growth-difficulty slider mapping.
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`. It is
 * also imported by prefs.js, which runs in a separate GTK process.
 */

/* Refresh interval presets in seconds; 0 = manual (protocol §9.1, ui.md §4). */
export const REFRESH_PRESETS = [0, 60, 120, 300, 900];

/** The preset closest to `seconds` (the helper normalizes the same way); ties pick the shorter. */
export function nearestPreset(seconds) {
    const v = Number(seconds);
    if (!Number.isFinite(v))
        return 120;
    let best = REFRESH_PRESETS[0];
    for (const p of REFRESH_PRESETS) {
        if (Math.abs(p - v) < Math.abs(best - v))
            best = p;
    }
    return best;
}

/* ---- thresholds (protocol §9.1 "Threshold rule") ----------------------- */

export const WARN_MIN = 50;
export const WARN_MAX = 95;
export const CRIT_MIN = 80;
export const CRIT_MAX = 100;
export const THRESHOLD_STEP = 5;
export const THRESHOLD_GAP = 5;

function clampInt(v, lo, hi, fallback) {
    const n = Math.round(Number(v));
    if (!Number.isFinite(n))
        return fallback;
    return Math.min(hi, Math.max(lo, n));
}

/**
 * Clamp warn to 50–95 and crit to 80–100, then keep warn at least 5 points
 * below crit. Crit wins, as in upstream's load path, so lowering crit pushes
 * warn down and raising warn past crit − 5 is refused.
 */
export function applyThresholdRule(warn, crit) {
    const c = clampInt(crit, CRIT_MIN, CRIT_MAX, 95);
    let w = clampInt(warn, WARN_MIN, WARN_MAX, 80);
    if (w > c - THRESHOLD_GAP)
        w = c - THRESHOLD_GAP;
    return {warn: w, crit: c};
}

/* ---- growth difficulty (spec companion.md §2.4, CompanionModel.swift:135-178) */

export const DIFFICULTY_MIN = 0.1;
export const DIFFICULTY_MAX = 2.0;
export const DIFFICULTY_DEFAULT = 1.0;
const DEFAULT_SNAP_WIDTH = 0.01; // ±1 % of the track snaps to 1.0

/** Swift: CompanionModel.swift:142-145 — non-finite → default, else clamp. */
export function clampDifficulty(value) {
    const v = Number(value);
    if (!Number.isFinite(v))
        return DIFFICULTY_DEFAULT;
    return Math.min(Math.max(v, DIFFICULTY_MIN), DIFFICULTY_MAX);
}

/** Drop binary noise (1.2000000000000002 → 1.2) so stored values stay clean. */
function tidy(x) {
    return Number(x.toPrecision(12));
}

/**
 * Two significant digits (CompanionModel.swift:173-178). The default snap is
 * the position rule's job, not this one's.
 */
export function snapDifficulty(value) {
    const v = Number(value);
    if (!(v > 0))
        return DIFFICULTY_MIN;
    const magnitude = 10 ** (Math.floor(Math.log10(v)) - 1);
    return tidy(Math.round(v / magnitude) * magnitude);
}

/** Multiplier → slider position 0…1 (log scale; CompanionModel.swift:166-169). */
export function difficultyPosition(value) {
    return Math.log(clampDifficulty(value) / DIFFICULTY_MIN) / Math.log(DIFFICULTY_MAX / DIFFICULTY_MIN);
}

/**
 * Slider position 0…1 → multiplier `0.1·20^p`, snapped to two significant
 * digits, and to exactly 1.0 within ±0.01 of its position
 * (CompanionModel.swift:158-164). The result is clamped to 0.1–2.0 so it can
 * go straight into GSettings.
 */
export function difficultyAtPosition(position) {
    const p = Math.min(Math.max(Number(position) || 0, 0), 1);
    if (Math.abs(p - difficultyPosition(DIFFICULTY_DEFAULT)) < DEFAULT_SNAP_WIDTH)
        return DIFFICULTY_DEFAULT;
    return clampDifficulty(snapDifficulty(DIFFICULTY_MIN * (DIFFICULTY_MAX / DIFFICULTY_MIN) ** p));
}

/** "100%", "5.0%", "0.01%" (Localization.difficultyValue, Localization.swift:426-431). */
export function difficultyValueText(value) {
    const percent = Number(value) * 100;
    if (percent >= 10)
        return `${percent.toFixed(0)}%`;
    if (percent >= 1)
        return `${percent.toFixed(1)}%`;
    return `${percent.toFixed(2)}%`;
}

/** Every top-bar text item is off: only the sprite shows (allOffHint). */
export function allMenuItemsOff(flags) {
    return !flags.tokens && !flags.cost && !flags.growth && !flags.limit;
}
