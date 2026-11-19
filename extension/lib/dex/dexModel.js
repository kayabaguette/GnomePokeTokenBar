/* Pokédex display logic (ui.md §3, protocol §7): filters and command args,
 * paging, wheel/trackpad paging, the short result cache, species-detail
 * selection rules and text helpers.
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 */

import {_, fmt, ngettext} from '../i18n.js';
import {relativeAgo} from '../format.js';

export const PAGE_SIZE = 16; // 4 × 4 grid (CompanionView.swift:1481-1483)
export const COLUMNS = 4;
export const LOG_PAGE_SIZE = 10;
export const MAX_QUERY_LENGTH = 64;

export const RARITY_ORDER = ['legendary', 'rare', 'uncommon', 'common']; // rarityDisplayOrder
export const REGIONS = ['kanto', 'johto', 'hoenn', 'sinnoh', 'unova'];
export const DEX_SORTS = ['numberAsc', 'numberDesc', 'nameAsc', 'nameDesc', 'rarityDesc'];
export const LOG_SORTS = ['recentFirst', 'oldestFirst', 'numberAsc', 'numberDesc', 'nameAsc', 'nameDesc', 'rarityDesc'];
export const STAT_KEYS = ['hp', 'attack', 'defense', 'specialAttack', 'specialDefense', 'speed'];

/* ---- filters and command args ------------------------------------------ */

export function defaultFilters() {
    return {query: '', rarity: null, region: null, shinyOnly: false, dexSort: 'numberAsc', logSort: 'recentFirst'};
}

export function isDefaultFilters(f) {
    const d = defaultFilters();
    return normalizeQuery(f.query) === d.query && f.rarity === d.rarity && f.region === d.region &&
        f.shinyOnly === d.shinyOnly && f.dexSort === d.dexSort && f.logSort === d.logSort;
}

/** The sort is non-default for the segment (the sort button is accent-tinted then). */
export function isSortCustom(filters, segment) {
    return segment === 'log' ? filters.logSort !== 'recentFirst' : filters.dexSort !== 'numberAsc';
}

export function normalizeQuery(text) {
    return String(text ?? '').trim().slice(0, MAX_QUERY_LENGTH);
}

/** Tapping the selected chip clears the filter (CompanionView.swift:1573). */
export function toggleValue(current, value) {
    return current === value ? null : value;
}

/**
 * getDexPage args (protocol §7.1). With an anchor the helper serves the page
 * holding that species and ignores `page` (Back from the detail view).
 */
export function dexPageArgs(filters, page, anchorSpeciesId = null) {
    return {
        page: Math.max(1, Math.trunc(page) || 1),
        pageSize: PAGE_SIZE,
        query: normalizeQuery(filters.query),
        rarity: RARITY_ORDER.includes(filters.rarity) ? filters.rarity : null,
        region: REGIONS.includes(filters.region) ? filters.region : null,
        shinyOnly: Boolean(filters.shinyOnly),
        sort: DEX_SORTS.includes(filters.dexSort) ? filters.dexSort : 'numberAsc',
        anchorSpeciesId: Number.isInteger(anchorSpeciesId) ? anchorSpeciesId : null,
    };
}

/** getCatchLog args (protocol §7.3); the log has no region filter. */
export function catchLogArgs(filters, page) {
    const p = Math.max(1, Math.trunc(page) || 1);
    return {
        offset: (p - 1) * LOG_PAGE_SIZE,
        limit: LOG_PAGE_SIZE,
        query: normalizeQuery(filters.query),
        rarity: RARITY_ORDER.includes(filters.rarity) ? filters.rarity : null,
        shinyOnly: Boolean(filters.shinyOnly),
        sort: LOG_SORTS.includes(filters.logSort) ? filters.logSort : 'recentFirst',
    };
}

export function logPageCount(total) {
    return Math.max(1, Math.ceil((Number(total) || 0) / LOG_PAGE_SIZE));
}

/** The 1-based log page holding `offset`. */
export function logPageOf(offset) {
    return Math.floor(Math.max(0, Number(offset) || 0) / LOG_PAGE_SIZE) + 1;
}

/** Next page after a pager or wheel step, clamped (DexScrollPager.pageAfterScroll, 1-based). */
export function pageAfterStep(current, pageCount, step) {
    const count = Math.max(1, Math.trunc(pageCount) || 1);
    return Math.min(Math.max(1, (Math.trunc(current) || 1) + Math.sign(step)), count);
}

/* ---- wheel and trackpad paging (DexScrollPager, CompanionView.swift:1381) */

/* Clutter reports touchpad scrolling in "scroll units" (about 10 px of finger
 * travel each), not points, so the 24 pt macOS threshold does not carry over.
 * Two units is a short deliberate flick. */
export const SMOOTH_THRESHOLD = 2;
/* A smooth gesture without an end event (no finish flag, no zero delta) is
 * considered over after this much silence. */
export const GESTURE_IDLE_MS = 250;

/**
 * One page per wheel notch; a smooth (touchpad) gesture accumulates its
 * deltas to a threshold and pages once, then is latched until it ends.
 *
 * Input events are plain objects so the rules are testable:
 *   {direction: 'up'|'down'|'left'|'right'|'smooth', dx, dy,
 *    source: 'wheel'|'finger'|'continuous'|'unknown',
 *    emulated: bool, finished: bool, timeMs: number}
 * `emulated` events are the copies Mutter synthesizes for the other API
 * (a smooth copy of a wheel notch, a discrete copy of a touchpad scroll); the
 * real event already counted, so they are ignored.
 */
export class ScrollPager {
    constructor({threshold = SMOOTH_THRESHOLD, idleMs = GESTURE_IDLE_MS} = {}) {
        this._threshold = threshold;
        this._idleMs = idleMs;
        this.reset();
    }

    reset() {
        this._acc = 0;
        this._fired = false;
        this._lastMs = null;
    }

    /** @returns {number} +1 next page, −1 previous page, 0 nothing */
    step(ev) {
        if (!ev || ev.emulated)
            return 0;
        if (ev.direction === 'down' || ev.direction === 'up') {
            this.reset();
            return ev.direction === 'down' ? 1 : -1;
        }
        if (ev.direction !== 'smooth')
            return 0; // horizontal notches never page
        const dx = Number(ev.dx) || 0;
        const dy = Number(ev.dy) || 0;
        if (ev.finished || (dx === 0 && dy === 0)) {
            this.reset(); // the gesture ended (finger lifted)
            return 0;
        }
        const t = Number(ev.timeMs) || 0;
        if (this._lastMs !== null && (t - this._lastMs > this._idleMs || t < this._lastMs)) {
            this._acc = 0;
            this._fired = false;
        }
        this._lastMs = t;

        if (ev.source === 'wheel') {
            // A high-resolution wheel: fractions of a notch add up to one page per notch.
            this._acc += dy;
            if (Math.abs(this._acc) < 1 - 1e-9)
                return 0;
            const dir = Math.sign(this._acc);
            this._acc = 0;
            return dir;
        }
        if (this._fired)
            return 0;
        this._acc += dy;
        if (Math.abs(this._acc) < this._threshold)
            return 0;
        const dir = Math.sign(this._acc);
        this._acc = 0;
        this._fired = true;
        return dir;
    }
}

/* ---- short-lived result cache ------------------------------------------ */

export const CACHE_TTL_MS = 30_000;
const CACHE_MAX = 24;

export function cacheKey(cmd, args) {
    return `${cmd}:${JSON.stringify(args)}`;
}

/** Page results for a few seconds, newest kept (LRU by insertion). `now` is injectable for tests. */
export class ResultCache {
    constructor({ttlMs = CACHE_TTL_MS, max = CACHE_MAX, now = () => Date.now()} = {}) {
        this._ttl = ttlMs;
        this._max = max;
        this._now = now;
        this._map = new Map();
    }

    get(key) {
        const hit = this._map.get(key);
        if (!hit)
            return null;
        if (this._now() - hit.at > this._ttl) {
            this._map.delete(key);
            return null;
        }
        return hit.value;
    }

    set(key, value) {
        this._map.delete(key);
        this._map.set(key, {value, at: this._now()});
        while (this._map.size > this._max)
            this._map.delete(this._map.keys().next().value);
    }

    clear() {
        this._map.clear();
    }

    get size() {
        return this._map.size;
    }
}

/**
 * What in `state` changes dex pages: the summary, the pin and the companion
 * being raised. A new value invalidates the cache; the 60 s states that only
 * move the clock keep it.
 */
export function dexSignature(state) {
    const c = state?.companion;
    if (!c)
        return null;
    const a = c.active;
    return JSON.stringify([
        c.dexSummary ?? null,
        c.representative ?? null,
        a ? [a.speciesId, a.form ?? null, a.stageIndex, Boolean(a.isShiny)] : null,
    ]);
}

/* ---- species detail ----------------------------------------------------- */

export function formCollected(form) {
    return Boolean(form?.hasNormal || form?.isShiny);
}

/**
 * The form the detail opens on: the requested one if collected, else the
 * pinned form, else the first collected form, else the first (the species
 * default) (CompanionView.swift:1741-1746).
 */
export function initialForm(species, preferred = null) {
    const forms = species?.forms ?? [];
    if (forms.length === 0)
        return null;
    if (preferred !== null && forms.some(f => f.form === preferred && formCollected(f)))
        return preferred;
    const pinned = forms.find(f => f.isRepresentative && formCollected(f));
    if (pinned)
        return pinned.form;
    return (forms.find(formCollected) ?? forms[0]).form;
}

/** {hasNormal, isShiny} of the displayed form, or of the species without forms. */
export function appearanceOf(species, form) {
    const forms = species?.forms ?? [];
    if (forms.length === 0)
        return {hasNormal: Boolean(species?.hasNormal), isShiny: Boolean(species?.isShiny)};
    const f = forms.find(x => x.form === form);
    return {hasNormal: Boolean(f?.hasNormal), isShiny: Boolean(f?.isShiny)};
}

/**
 * Individuals of the displayed form. The helper returns every form's
 * individuals (plan Phase 3 note), so the extension filters; a species
 * without forms keeps them all.
 */
export function individualsForForm(species, form) {
    const all = species?.individuals ?? [];
    if ((species?.forms ?? []).length === 0)
        return all;
    return all.filter(i => (i.form ?? null) === form);
}

/**
 * Normal or shiny shown: the requested look when the form has it, else the
 * first individual's, else shiny only when nothing normal was collected
 * (displayedShiny, CompanionView.swift:1752-1756).
 */
export function displayedShiny(species, form, requested = null) {
    const look = appearanceOf(species, form);
    if (requested === true && look.isShiny)
        return true;
    if (requested === false && look.hasNormal)
        return false;
    const first = individualsForForm(species, form)[0];
    if (first)
        return Boolean(first.isShiny);
    return look.isShiny && !look.hasNormal;
}

/** Individuals listed in the picker: the displayed form and look (CompanionView.swift:1760-1762). */
export function visibleIndividuals(species, form, shiny) {
    return individualsForForm(species, form).filter(i => Boolean(i.isShiny) === Boolean(shiny));
}

/** The selected individual: the requested id if visible, else the first. */
export function pickIndividual(list, id = null) {
    return list.find(i => i.id === id) ?? list[0] ?? null;
}

/** Form picker columns (PokemonDetailView.formPickerColumns, CompanionView.swift:1815). */
export function formPickerColumns(count) {
    const n = Math.max(1, Math.trunc(count) || 1);
    if (n > 20)
        return 7;
    if (n > 8)
        return 6;
    return Math.min(n, 4);
}

/** Stat bar scale: at least 300, rounded up to a hundred (PokemonProfile.swift:219-223). */
export function statScaleMaximum(values) {
    const nums = (values ?? []).map(v => Number(v) || 0);
    const highest = Math.max(300, ...nums);
    const rem = highest % 100;
    return rem === 0 ? highest : highest + (100 - rem);
}

export function baseStatTotal(stats) {
    return STAT_KEYS.reduce((sum, k) => sum + (Number(stats?.[k]) || 0), 0);
}

export function statLabel(key) {
    switch (key) {
    case 'hp': return _('HP');
    case 'attack': return _('Attack');
    case 'defense': return _('Defense');
    case 'specialAttack': return _('Sp. Atk');
    case 'specialDefense': return _('Sp. Def');
    case 'speed': return _('Speed');
    default: return key;
    }
}

export function genderLabel(gender) {
    switch (gender) {
    case 'male': return _('Male');
    case 'female': return _('Female');
    case 'genderless': return _('Genderless');
    default: return '—';
    }
}

/** One learn method (Localization.moveMethod, Localization.swift:1339-1348). */
export function moveMethodLabel(m) {
    switch (m?.method) {
    case 'level-up':
        return Number(m.level) > 0 ? fmt(_('Lv. %d'), m.level) : _('Start');
    case 'machine': return _('TM');
    case 'egg': return _('Egg');
    case 'tutor': return _('Tutor');
    case 'light-ball-egg': return _('Light Ball breeding');
    case 'form-change': return _('Form change');
    default: return String(m?.method ?? '').replace(/-/g, ' ');
    }
}

/** Learn methods of a move, de-duplicated in order, joined " · ". */
export function moveMethodsText(methods) {
    const out = [];
    for (const m of methods ?? []) {
        const label = moveMethodLabel(m);
        if (label && !out.includes(label))
            out.push(label);
    }
    return out.join(' · ');
}

/** "Gastrodon [East Sea]" for a non-default form, else the species name. */
export function formTitle(species, form) {
    const forms = species?.forms ?? [];
    const name = species?.displayName ?? `#${species?.speciesId ?? '?'}`;
    if (forms.length === 0 || form === null || form === forms[0].form)
        return name;
    const f = forms.find(x => x.form === form);
    return f?.displayName ? `${name} [${f.displayName}]` : name;
}

/** Alternate forms reuse the default form's details (protocol §7.2 `details`). */
export function isDefaultForm(species, form) {
    const forms = species?.forms ?? [];
    return forms.length === 0 || form === null || form === forms[0].form;
}

/** Whether the displayed species/form is the pinned representative. */
export function isPinned(species, form, representative) {
    if (!representative || representative.speciesId !== species?.speciesId)
        return false;
    if ((species?.forms ?? []).length === 0)
        return true;
    return (representative.form ?? null) === form;
}

/**
 * Individual picker label "#1 · Normal · Lv. 100 · 30/10/2026"
 * (CompanionView.swift:1882-1889); "Raising" or "—" without a catch date.
 */
export function individualLabel(ind, index, locale = undefined) {
    const look = ind?.isShiny ? `✨ ${_('Shiny')}` : _('Normal');
    let date;
    if (ind?.caughtAt) {
        try {
            date = new Intl.DateTimeFormat(locale, {year: 'numeric', month: 'numeric', day: 'numeric'})
                .format(new Date(ind.caughtAt));
        } catch {
            date = String(ind.caughtAt).slice(0, 10);
        }
    } else {
        date = ind?.isRaising ? _('Raising') : '—';
    }
    return `#${index + 1} · ${look} · ${fmt(_('Lv. %d'), ind?.level ?? 5)} · ${date}`;
}

/** Catch date for the individual card: a short date, "Raising" for the companion, else "—". */
export function caughtDateText(ind, locale = undefined) {
    if (!ind?.caughtAt)
        return ind?.isRaising ? _('Raising') : '—';
    try {
        return new Intl.DateTimeFormat(locale, {year: 'numeric', month: 'short', day: 'numeric'})
            .format(new Date(ind.caughtAt));
    } catch {
        return String(ind.caughtAt).slice(0, 10);
    }
}

/**
 * The individual card's prayer lines (CV:1947-1953), or null when the egg was
 * never prayed over. ngettext for the count (M3 decision 40; upstream has no
 * singular).
 */
export function prayerLines(ind) {
    const n = ind?.prayers;
    if (!(Number.isInteger(n) && n > 0))
        return null;
    return {
        born: fmt(ngettext('Born after %d prayer', 'Born after %d prayers', n), n),
        caption: _('Your prayers reached the heavens. Probably.'),
    };
}

/* ---- grid and log text ---------------------------------------------------- */

/** "Kanto 11/151" (dexRegionProgressLabel). */
export function regionProgressText(region, name) {
    return `${name} ${region?.owned ?? 0}/${region?.total ?? 0}`;
}

/** 🏅 for a complete region, ✨ when it is shiny-complete too. */
export function regionMedal(region) {
    if (region?.shinyComplete)
        return '✨';
    if (region?.complete)
        return '🏅';
    return null;
}

export function regionFraction(region) {
    const t = Number(region?.total) || 0;
    return t > 0 ? Math.min(1, (Number(region?.owned) || 0) / t) : 0;
}

/**
 * Header medals: the national and Unown tiers only; region medals already
 * show on the region chips (CompanionView.swift:1614-1622).
 */
export function headerMedals(badges) {
    const out = [];
    for (const key of badges ?? []) {
        switch (key) {
        case 'unown': out.push({key, emoji: '🔤', label: _('Unown collection complete')}); break;
        case 'national': out.push({key, emoji: '🌍', label: _('National Dex complete')}); break;
        case 'nationalShiny': out.push({key, emoji: '✨', label: _('Shiny National Dex complete')}); break;
        default: break;
        }
    }
    return out;
}

/** "1/2" collected forms of a form species, or null. */
export function formCountText(cell) {
    const f = cell?.forms;
    return f && Number.isInteger(f.total) ? `${f.owned ?? 0}/${f.total}` : null;
}

/** Owned at all: in normal colors or shiny. A cell that is neither is drawn locked. */
export function cellOwned(cell) {
    return Boolean(cell?.hasNormal || cell?.isShiny);
}

/**
 * Cell tooltip "#25 Pikachu · Rare · Forms 1/2 · Shiny · Raising · Representative"
 * (DexSpeciesCell.tooltip, CompanionView.swift:2196-2204).
 */
export function cellTooltip(cell, rarityName) {
    const parts = [`#${cell.speciesId} ${cell.displayName ?? ''}`.trim(), rarityName];
    if (cell.forms)
        parts.push(fmt(_('Forms %d/%d'), cell.forms.owned ?? 0, cell.forms.total ?? 0));
    if (cell.isShiny)
        parts.push(_('Shiny'));
    if (cell.isRaising)
        parts.push(_('Raising'));
    if (cell.isRepresentative)
        parts.push(_('Representative'));
    return parts.join(' · ');
}

export function sortLabel(sort) {
    switch (sort) {
    case 'numberAsc': return _('Number, low to high');
    case 'numberDesc': return _('Number, high to low');
    case 'nameAsc': return _('Name, A to Z');
    case 'nameDesc': return _('Name, Z to A');
    case 'rarityDesc': return _('Rarest first');
    case 'recentFirst': return _('Newest first');
    case 'oldestFirst': return _('Oldest first');
    default: return sort;
    }
}

const DAY_MS = 86_400_000;

/** Catch date as a relative time: "5 h ago", "12 d ago", "3 mo ago", "2 yr ago". */
export function caughtText(ms, nowMs) {
    if (!Number.isFinite(ms))
        return '';
    const days = Math.max(0, (nowMs - ms) / DAY_MS);
    if (days < 30)
        return relativeAgo(ms, nowMs);
    if (days < 365)
        return fmt(_('%d mo ago'), Math.floor(days / 30.44));
    return fmt(_('%d yr ago'), Math.floor(days / 365.25));
}

/** Text for a failed dex command (protocol §11.2 plus the local codes). */
export function dexErrorText(code) {
    switch (code) {
    case 'notReady': return _('Pokémon data is still loading. It appears once it is ready.');
    case 'helperExited': return _('The background helper is restarting. The Pokédex reloads when it is back.');
    case 'timeout': return _('The background helper did not answer in time.');
    case 'unavailable': return _('Pokémon data could not be downloaded. Check your connection.');
    default: return _('The Pokédex could not be loaded.');
    }
}

/** Codes that clear by themselves: retry on the next state instead of offering a button. */
export function retriesOnState(code) {
    return code === 'notReady' || code === 'helperExited';
}

export function heightText(m) {
    return fmt(_('%s m'), (Number(m) || 0).toFixed(1));
}

export function weightText(kg) {
    return fmt(_('%s kg'), (Number(kg) || 0).toFixed(1));
}

/**
 * Evolution line for an individual (protocol §7.2 has none): the companion
 * being raised uses `active.evoLine` (with stages still ahead), others the
 * `chain` of their catch-log entry (protocol §7.3, same id as Individual).
 * Returns [{speciesId, form, displayName, reached, current}] or null.
 */
export function evoLineFor(individual, active, chainsById) {
    if (!individual)
        return null;
    if (individual.isRaising && Array.isArray(active?.evoLine) && active.evoLine.length > 0)
        return active.evoLine;
    const chain = chainsById?.get?.(individual.id);
    if (!Array.isArray(chain) || chain.length === 0)
        return null;
    return chain.map((n, i) => ({
        speciesId: n.speciesId,
        form: n.form ?? null,
        displayName: n.displayName ?? null,
        reached: true,
        current: i === chain.length - 1,
    }));
}
