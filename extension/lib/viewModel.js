/* State → display models: top-bar lines, limit labels, status and error text,
 * notification text.
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`. Rules come
 * from spec usage.md §6.9 (UsageStore.swift:354-462), ui.md §1–§2 and §5, and
 * protocol §5–§6, §11–§12.
 */

import {_, fmt} from './i18n.js';
import {
    compact, costText, displayPercent, growthText, percent,
} from './format.js';

export const PACE_TIERS = ['wayUnder', 'under', 'onPace', 'slightlyOver', 'over', 'wayOver'];

/** Unknown enum values take the protocol §12 fallback. */
export function normalizeTier(tier) {
    return PACE_TIERS.includes(tier) ? tier : 'onPace';
}

/** On pace or slower: nothing to act on (PaceTier.isCalm). */
export function isCalm(tier) {
    return PACE_TIERS.indexOf(normalizeTier(tier)) <= PACE_TIERS.indexOf('onPace');
}

export function paceTierName(tier) {
    switch (normalizeTier(tier)) {
    case 'wayUnder': return _('Well under pace');
    case 'under': return _('Under pace');
    case 'slightlyOver': return _('Slightly fast');
    case 'over': return _('Fast');
    case 'wayOver': return _('Very fast');
    default: return _('On pace');
    }
}

/** The pace delta in used points; null at 0 (Localization.paceDelta). */
export function paceDeltaText(points) {
    const n = Math.trunc(Number(points) || 0);
    if (n > 0)
        return fmt(_('%d pts more used'), n);
    if (n < 0)
        return fmt(_('%d pts less used'), -n);
    return null;
}

/**
 * The limit row tooltip (PopoverView.swift:735-744): "<tier> · <delta>" over
 * the pace hint, the hint alone during the early-window hold, nothing without a pace.
 */
export function paceHelp(window, mode) {
    if (typeof window?.pace !== 'number')
        return null;
    const hint = fmt(_('Pace — an even burn across this window would sit at %s now.'),
        percent(displayPercent(window.pace * 100, mode)));
    if (!window.paceTier)
        return hint;
    let head = paceTierName(window.paceTier);
    const delta = paceDeltaText(window.paceDelta);
    if (delta)
        head += ` · ${delta}`;
    return `${head}\n${hint}`;
}

/** Row label for a limit window (Localization.claudeLimitEntry and friends). */
export function windowLabel(window) {
    switch (window?.kind) {
    case 'fiveHour': return _('5-hour session');
    case 'sevenDay': return _('Weekly');
    case 'sevenDayOpus': return _('Weekly Opus');
    case 'sevenDaySonnet': return _('Weekly Sonnet');
    default: return scopedLabel(window?.scopeKind ?? null, window?.label ?? null);
    }
}

function scopedLabel(scopeKind, model) {
    switch (scopeKind) {
    case 'session': return _('5-hour session');
    case 'weekly_all': return _('Weekly');
    case 'weekly_scoped':
        return model ? fmt(_('Weekly %s'), model) : _('Weekly (scoped)');
    default: {
        const base = (scopeKind ?? 'limit').replace(/_/g, ' ');
        return model ? `${base} ${model}` : base;
    }
    }
}

/**
 * Notification window name. `limitAlert.label` is null for the legacy windows,
 * so the name is built from `windowKind` (plan Phase 2 note); a scoped window
 * uses its model label.
 */
export function alertWindowName(event, providerName = 'Claude') {
    switch (event?.windowKind) {
    case 'fiveHour': return fmt(_('%s 5-hour session'), providerName);
    case 'sevenDay': return fmt(_('%s weekly'), providerName);
    case 'sevenDayOpus': return fmt(_('%s weekly Opus'), providerName);
    case 'sevenDaySonnet': return fmt(_('%s weekly Sonnet'), providerName);
    default:
        return event?.label
            ? fmt(_('%s weekly %s'), providerName, event.label)
            : fmt(_('%s limit'), providerName);
    }
}

function providerById(state, id) {
    return state?.meta?.providers?.find(p => p.id === id) ?? null;
}

/** Cost is shown only if some provider reports one (protocol §5.1). */
export function showsCost(state) {
    return (state?.meta?.providers ?? []).some(p => p.reportsCost);
}

/**
 * One top-bar limit item per provider: "<shortName> <percent>", shown only if
 * the provider was used today and has a headline window (usage.md §6.9).
 * Returns `[{text, tier}]`; `tier` is null when the color mode hides it.
 */
export function menuLimitParts(state, settings) {
    if (!settings.showLimit)
        return [];
    const parts = [];
    for (const provider of state?.meta?.providers ?? []) {
        const limits = state?.limits?.providers?.[provider.id];
        const todayTokens = state?.usage?.providers?.[provider.id]?.today?.tokens ?? 0;
        if (!limits?.headlineKey || !(todayTokens > 0))
            continue;
        const window = (limits.windows ?? []).find(w => w.key === limits.headlineKey);
        if (!window || typeof window.utilization !== 'number')
            continue;
        const tier = normalizeTier(window.gaugeTier);
        let shown = tier;
        if (settings.limitColorMode === 'off' || (settings.limitColorMode === 'attention' && isCalm(tier)))
            shown = null;
        parts.push({
            text: `${provider.shortName ?? provider.displayName ?? provider.id} ${percent(displayPercent(window.utilization, settings.limitMode))}`,
            tier: shown,
        });
    }
    return parts;
}

/**
 * The top-bar text lines (UsageStore.menuLines, usage.md §6.9). Each line is
 * a list of runs `{text, tier}`; a run with a tier is drawn in that tier's
 * color, separators and usage text never are.
 *
 * Layout:
 *   - before the first refresh: the growth text, else "—";
 *   - a limit line and ≥ 2 usage items: [usage joined " · ", limit];
 *   - more than 2 usage items: [first n−1 joined, last];
 *   - otherwise one line per item, then the limit;
 *   - all off: [] (sprite only).
 *
 * @param {object} state      last protocol state
 * @param {object} settings   {showTokens, showCost, showGrowth, showLimit, limitMode, limitColorMode}
 */
export function menuLines(state, settings) {
    const companion = state?.companion;
    const growth = settings.showGrowth && companion
        ? growthText(companion.growthPercent, companion.phase === 'egg')
        : null;
    if (!state?.meta?.lastUpdated)
        return [[{text: growth ?? '—', tier: null}]];

    const today = state.usage?.combined?.today;
    const usage = [];
    if (settings.showTokens)
        usage.push(compact(today?.tokens ?? 0));
    if (settings.showCost && showsCost(state))
        usage.push(costText(today?.cost, true));
    if (growth)
        usage.push(growth);

    const parts = menuLimitParts(state, settings);
    const limitLine = [];
    parts.forEach((p, i) => {
        if (i > 0)
            limitLine.push({text: ' · ', tier: null});
        limitLine.push(p);
    });
    const run = text => [{text, tier: null}];

    if (limitLine.length > 0 && usage.length >= 2)
        return [run(usage.join(' · ')), limitLine];
    if (usage.length > 2)
        return [run(usage.slice(0, -1).join(' · ')), run(usage[usage.length - 1])];
    const lines = usage.map(run);
    if (limitLine.length > 0)
        lines.push(limitLine);
    return lines;
}

/** Right-click info row: "Today's tokens 19.4M · $5.09" (ui.md §1.5 item 1). */
export function menuInfoText(state) {
    const today = state?.usage?.combined?.today;
    let text = fmt(_('Today’s tokens %s'), compact(today?.tokens ?? 0));
    if (showsCost(state))
        text += ` · ${costText(today?.cost)}`;
    return text;
}

/** Companion status line from the mood (CompanionView statusLine). */
export function moodText(companion) {
    if (companion?.justEvolvedTo)
        return fmt(_('Evolved into %s!'), companion.justEvolvedTo);
    switch (companion?.mood) {
    case 'egg': return _('Hatching soon.');
    case 'levelUp': return _('It grew!');
    case 'tired': return _('Close to the limit. A short break is fine.');
    case 'sulk': return _('Sulking with its back turned.');
    case 'sleep': return _('Sleeping now.');
    case 'working': return _('Today’s work is piling up.');
    case 'focus': return _('In focus mode now.');
    default: return _('Keeping quiet today.');
    }
}

export function rarityName(rarity) {
    switch (rarity) {
    case 'uncommon': return _('Uncommon');
    case 'rare': return _('Rare');
    case 'legendary': return _('Legendary');
    default: return _('Common');
    }
}

/** The 25 natures, sent as raw ids (protocol §10). */
export function natureName(nature) {
    if (!nature)
        return null;
    const names = {
        hardy: _('Hardy'), lonely: _('Lonely'), brave: _('Brave'), adamant: _('Adamant'), naughty: _('Naughty'),
        bold: _('Bold'), docile: _('Docile'), relaxed: _('Relaxed'), impish: _('Impish'), lax: _('Lax'),
        timid: _('Timid'), hasty: _('Hasty'), serious: _('Serious'), jolly: _('Jolly'), naive: _('Naive'),
        modest: _('Modest'), mild: _('Mild'), quiet: _('Quiet'), bashful: _('Bashful'), rash: _('Rash'),
        calm: _('Calm'), gentle: _('Gentle'), sassy: _('Sassy'), careful: _('Careful'), quirky: _('Quirky'),
    };
    return names[nature] ?? nature;
}

export function regionName(region) {
    switch (region) {
    case 'kanto': return _('Kanto');
    case 'johto': return _('Johto');
    case 'hoenn': return _('Hoenn');
    case 'sinnoh': return _('Sinnoh');
    case 'unova': return _('Unova');
    default: return region ?? '';
    }
}

/** "Stage 2 / 3 · Adamant" or "Final form · Adamant". */
export function stageText(active) {
    const parts = [active?.isFinalStage
        ? _('Final form')
        : fmt(_('Stage %d / %d'), (active?.stageIndex ?? 0) + 1, active?.totalForms ?? 1)];
    const nature = natureName(active?.nature);
    if (nature)
        parts.push(nature);
    return parts.join(' · ');
}

/** "87.6M to next evolution" / "to graduation" / "to hatch". */
export function remainingText(used, threshold, kind) {
    const amount = compact(Math.max(0, (Number(threshold) || 0) - (Number(used) || 0)));
    switch (kind) {
    case 'egg': return fmt(_('%s to hatch'), amount);
    case 'graduation': return fmt(_('%s to graduation'), amount);
    default: return fmt(_('%s to next evolution'), amount);
    }
}

/** "162.4M / 250M · 64%" for the progress bar hover detail. */
export function progressDetail(used, threshold) {
    const t = Number(threshold) || 0;
    const u = Number(used) || 0;
    const pct = t > 0 ? Math.min(100, Math.floor(u * 100 / t)) : 0;
    return `${compact(u)} / ${compact(t)} · ${pct}%`;
}

/** Limits error text by `errorCode` (protocol §11.4; UsageStore.friendlyLimitError). */
export function limitsErrorText(limits) {
    const status = limits?.httpStatus;
    switch (limits?.errorCode) {
    case null:
    case undefined:
        return null;
    case 'credentialsMissing':
        return _('No Claude credentials found in ~/.claude/.credentials.json. Sign in to Claude Code to see limits.');
    case 'accountOAuthMissing':
        return _('Your Claude credentials have no account sign-in. Run /login in Claude Code and limits will appear.');
    case 'tokenExpired':
        return _('The Claude Code sign-in expired. Limits refresh once Claude Code runs again.');
    case 'unauthorized':
        return status
            ? fmt(_('Claude rejected the sign-in (HTTP %d). Check that you are signed in to Claude Code.'), status)
            : _('Claude rejected the sign-in. Check that you are signed in to Claude Code.');
    case 'rateLimited':
        return _('Claude limit checks are rate-limited (429). Retrying automatically.');
    case 'network':
        return _('Couldn’t reach Claude. Check your network connection.');
    case 'httpStatus':
        return status
            ? fmt(_('Couldn’t fetch Claude limits (HTTP %d). Try again shortly.'), status)
            : _('Couldn’t fetch Claude limits. Try again shortly.');
    case 'decode':
        return _('Claude sent a limits response this version can’t read.');
    default:
        return _('Couldn’t fetch Claude limits. Try again shortly.');
    }
}

/** Ongoing-condition text (`meta.errors[]`, protocol §11.3); unknown codes show the helper message. */
export function metaErrorText(error) {
    switch (error?.code) {
    case 'usageScan': return _('Couldn’t read the Claude Code usage logs. Totals stop updating until the next successful scan.');
    case 'saveWrite': return _('Couldn’t save companion progress to disk.');
    case 'cacheWrite': return _('Couldn’t write the usage cache. Scans will be slower.');
    default: return error?.message ?? _('Unknown error');
    }
}

/** Helper-process status text for the footer and the indicator tooltip. */
export function helperStatusText(status) {
    switch (status) {
    case 'mismatch': return _('Helper protocol mismatch; reinstall the extension.');
    case 'down': return _('The background helper stopped. Restarting…');
    default: return null;
    }
}

/**
 * Notification title and body for a protocol event (ui.md §5, protocol §6),
 * or null when the kind posts nothing.
 */
export function notificationText(event) {
    switch (event?.kind) {
    case 'hatch':
        if (event.isShiny) {
            return {
                title: _('✨ Shiny Pokémon!'),
                body: fmt(_('A shiny %s hatched! (1 in %d)'), event.displayName, event.shinyOdds ?? 64),
            };
        }
        return {title: _('🥚 Hatched!'), body: fmt(_('%s hatched from the egg!'), event.displayName)};
    case 'evolve':
        return {
            title: _('✨ Evolved!'),
            body: fmt(_('%s evolved into %s!'), event.fromDisplayName, event.displayName),
        };
    case 'graduate':
        return {
            title: _('🎓 Graduated!'),
            body: fmt(_('%s — saved to your Pokédex! A new egg has arrived.'), event.displayName),
        };
    case 'candyGranted': {
        const window = event.windowKind === 'weekly' ? _('weekly') : _('5-hour');
        return {
            title: fmt(_('🍬 You got %d× Rare Candy!'), event.count ?? 1),
            body: fmt(_('You maxed out your %s token limit. Candies can be used once the Bag arrives.'), window),
        };
    }
    case 'badge':
        return badgeText(event);
    case 'limitAlert': {
        const crit = event.tier === 'crit';
        return {
            title: crit ? _('Limit imminent') : _('Limit warning'),
            body: fmt(_('%s at %s'), alertWindowName(event), percent(event.utilization)),
        };
    }
    case 'authExpired':
        return {
            title: _('Claude limits paused'),
            body: _('The Claude Code sign-in expired. Limits refresh once Claude Code runs again.'),
        };
    default:
        return null;
    }
}

function badgeText(event) {
    const region = regionName(event.region);
    switch (event.badge) {
    case 'region':
        return {title: fmt(_('🏅 %s Dex complete!'), region), body: fmt(_('You’ve caught every Pokémon in %s!'), region)};
    case 'regionShiny':
        return {
            title: fmt(_('✨ Shiny %s Dex complete!'), region),
            body: fmt(_('You’ve caught a shiny of every Pokémon in %s!'), region),
        };
    case 'unown':
        return {title: _('🔤 Unown collection complete!'), body: _('You’ve collected all 28 Unown forms!')};
    case 'national':
        return {title: _('🌍 National Dex complete!'), body: _('You’ve caught every Pokémon from Gen 1–5!')};
    case 'nationalShiny':
        return {title: _('✨ Shiny National Dex complete!'), body: _('You’ve caught a shiny of every Pokémon!')};
    default:
        return null;
    }
}

/** Which notification setting gates an event kind (protocol §6). */
export function notificationGate(kind) {
    switch (kind) {
    case 'limitAlert':
    case 'authExpired':
        return 'limit-notifications';
    case 'hatch':
    case 'evolve':
    case 'graduate':
    case 'candyGranted':
    case 'badge':
        return 'companion-notifications';
    default:
        return null;
    }
}
