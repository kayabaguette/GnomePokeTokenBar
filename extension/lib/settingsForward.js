/* The GSettings → `setSettings` mapping (protocol §9).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`. The
 * extension passes in the values it reads from GSettings, GLib and the shell.
 */

/** GSettings keys whose change triggers a (debounced) `setSettings`. */
export const FORWARDED_KEYS = [
    'refresh-interval', 'warn-threshold', 'crit-threshold', 'growth-difficulty',
    'week-start', 'always-faces-back', 'shop-difficulty',
];

/**
 * Protocol §10: first entry of GLib.get_language_names(), codeset and
 * modifier stripped, `_` → `-`, lowercased; `C` and `POSIX` become `en`.
 *
 * @param {string[]} names
 */
export function languageFromNames(names) {
    const first = Array.isArray(names) && names.length > 0 ? String(names[0]) : 'C';
    const bare = first.split('.')[0].split('@')[0];
    if (bare === '' || bare === 'C' || bare === 'POSIX')
        return 'en';
    return bare.replace(/_/g, '-').toLowerCase();
}

/**
 * The locale's first weekday from Intl, as an ISO weekday (1 = Monday …
 * 7 = Sunday), or null. Tries `getWeekInfo()` then the `weekInfo` getter
 * (protocol §9.2). Neither exists in GJS 1.90 (checked 2026-11-18).
 */
export function intlFirstDay(localeTag, IntlLocale = globalThis.Intl?.Locale) {
    if (typeof IntlLocale !== 'function')
        return null;
    try {
        const locale = new IntlLocale(localeTag);
        const info = typeof locale.getWeekInfo === 'function' ? locale.getWeekInfo() : locale.weekInfo;
        const day = info?.firstDay;
        return Number.isInteger(day) && day >= 1 && day <= 7 ? day : null;
    } catch {
        return null;
    }
}

/**
 * The ISO weekday the week starts on, for `setSettings.weekStart`.
 *
 * Explicit settings map directly (monday 1, sunday 7, saturday 6). `locale`
 * resolves, in order:
 *   1. Intl week info (protocol §9.2);
 *   2. GNOME's own `org.gnome.desktop.calendar week-start-day` when it is not
 *      'default' (enum 1 = Monday … 7 = Sunday), as the shell calendar does;
 *   3. the locale's first weekday as the shell calendar reads it,
 *      `Shell.util_get_week_start()` (0 = Sunday … 6 = Saturday);
 *   4. Monday, the protocol fallback.
 *
 * @param {string} setting  week-start value
 * @param {{intlDay?: number|null, desktopDay?: number|null, shellWeekStart?: number|null}} sources
 */
export function resolveWeekStart(setting, sources = {}) {
    switch (setting) {
    case 'monday': return 1;
    case 'sunday': return 7;
    case 'saturday': return 6;
    }
    const {intlDay = null, desktopDay = null, shellWeekStart = null} = sources;
    if (Number.isInteger(intlDay) && intlDay >= 1 && intlDay <= 7)
        return intlDay;
    if (Number.isInteger(desktopDay) && desktopDay >= 1 && desktopDay <= 7)
        return desktopDay;
    if (Number.isInteger(shellWeekStart) && shellWeekStart >= 0 && shellWeekStart <= 6)
        return shellWeekStart === 0 ? 7 : shellWeekStart;
    return 1;
}

/**
 * The full forwarded set (protocol §9.2). Every M1 field is required by the
 * helper; the optional M2 `shopDifficulty` is always sent too.
 *
 * @param {(key: string) => any} get  reads a GSettings value by key
 * @param {string} language
 * @param {number} weekStart  ISO weekday
 */
export function forwardedSettings(get, language, weekStart) {
    return {
        refreshInterval: Math.trunc(Number(get('refresh-interval'))),
        warnThreshold: Math.trunc(Number(get('warn-threshold'))),
        critThreshold: Math.trunc(Number(get('crit-threshold'))),
        growthDifficulty: Number(get('growth-difficulty')),
        weekStart,
        language,
        alwaysFacesBack: Boolean(get('always-faces-back')),
        shopDifficulty: Number(get('shop-difficulty')),
    };
}
