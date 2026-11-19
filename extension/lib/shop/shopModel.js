/* Shop and Bag display models (spec bag-shop.md §3, protocol §5.4, §7.4).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`. The
 * helper sends `companion.shop` already sorted and `companion.bag` with the
 * availability flags; this module only turns them into what the cards show:
 * names, prices, counts, which control row applies, the disabled reason, the
 * candy preview lines and the error text of a failed command.
 */

import {_, fmt} from '../i18n.js';
import {compact} from '../format.js';
import {natureName, rarityName} from '../viewModel.js';

/** Item enumeration in ItemKind.allCases order (protocol §12); also the Bag order. */
export const ITEMS = ['rareCandy', 'mint', 'shinyCharm'];
export const EGG_TIERS = [null, 'uncommon', 'rare'];
/** XP per Rare Candy, never scaled by difficulty (bag-shop.md §2.2). */
export const CANDY_XP = 100_000_000;

/* Mint has no sprite at all, and getSprite has no item request shape
 * (protocol §8.1), so every item shows its fallback emoji (bag-shop.md §1.2). */
const ITEM_EMOJI = {rareCandy: '🍬', mint: '🌿', shinyCharm: '✨'};

export function itemName(item) {
    switch (item) {
    case 'rareCandy': return _('Rare Candy');
    case 'mint': return _('Mint');
    case 'shinyCharm': return _('Shiny Charm');
    default: return String(item ?? '');
    }
}

export function itemDescription(item) {
    switch (item) {
    case 'rareCandy': return fmt(_('Raises your Pokémon’s EXP by %s.'), compact(CANDY_XP));
    case 'mint': return _('Randomly changes your Pokémon’s nature.');
    case 'shinyCharm': return _('While owned, raises the chance of hatching a shiny.');
    default: return '';
    }
}

export function itemEmoji(item) {
    return ITEM_EMOJI[item] ?? '❔';
}

/** Egg tiers on sale: null (plain), uncommon, rare. Anything else is not an egg we sell. */
export function isShopTier(tier) {
    return tier === null || tier === undefined || tier === 'uncommon' || tier === 'rare';
}

export function eggName(tier) {
    switch (tier) {
    case 'uncommon': return _('Uncommon Egg');
    case 'rare': return _('Rare Egg');
    default: return _('Pokémon Egg');
    }
}

export function eggDescription(tier) {
    if (tier === 'uncommon' || tier === 'rare')
        return fmt(_('Send off your current Pokémon for an egg guaranteed to hatch %s or better.'), rarityName(tier));
    return _('Send off your current Pokémon and start fresh with a new egg.');
}

/** Home egg badge for a premium egg (`egg.guarantee`), or null for a plain egg. */
export function eggGuaranteeText(tier) {
    if (tier !== 'uncommon' && tier !== 'rare')
        return null;
    return fmt(_('%s or better'), rarityName(tier));
}

function nonNegInt(v) {
    const n = Math.trunc(Number(v));
    return Number.isFinite(n) && n > 0 ? n : 0;
}

/**
 * The Shop list as the helper sent it (never re-sorted, protocol §5.4), one
 * display entry per ShopEntry. Unknown items and unsold tiers are skipped
 * (protocol §12). An egg is `locked` while an egg incubates.
 *
 * @param {object|null} companion  state.companion
 */
export function shopEntries(companion) {
    const entries = companion?.shop?.entries;
    if (!Array.isArray(entries))
        return [];
    const incubating = companion.phase !== 'active';
    const out = [];
    for (const e of entries) {
        if (e?.kind === 'item') {
            if (!ITEMS.includes(e.item))
                continue;
            const passive = Boolean(e.passive);
            const owned = Math.trunc(Number(e.owned) || 0);
            const maxCount = nonNegInt(e.maxCount);
            const canBuy = Boolean(e.canBuy) && maxCount > 0;
            out.push({
                key: `item:${e.item}`,
                kind: 'item',
                item: e.item,
                tier: null,
                name: itemName(e.item),
                description: itemDescription(e.item),
                emoji: itemEmoji(e.item),
                price: nonNegInt(e.price),
                passive,
                owned,
                purchased: Boolean(e.purchased),
                canBuy,
                maxCount,
                locked: false,
                showOwned: !passive && owned > 0,
                showStepper: !passive && canBuy && maxCount > 1,
            });
        } else if (e?.kind === 'egg') {
            if (!isShopTier(e.tier))
                continue;
            const tier = e.tier ?? null;
            const maxCount = nonNegInt(e.maxCount);
            out.push({
                key: `egg:${tier ?? 'plain'}`,
                kind: 'egg',
                item: null,
                tier,
                name: eggName(tier),
                description: eggDescription(tier),
                emoji: '🥚',
                price: nonNegInt(e.price),
                passive: false,
                owned: 0,
                purchased: false,
                canBuy: Boolean(e.canBuy) && !incubating,
                maxCount: incubating ? 0 : maxCount,
                locked: incubating,
                showOwned: false,
                showStepper: false,
            });
        }
    }
    return out;
}

/**
 * The idle control row of a Shop card (bag-shop.md §3.2):
 * `owned` (a purchased passive item), `locked` (an egg while incubating:
 * a disabled Buy plus the hint), `buy`, or `poor` ("Not enough tokens").
 */
export function idleControl(entry) {
    if (entry.purchased)
        return 'owned';
    if (entry.locked)
        return 'locked';
    return entry.canBuy ? 'buy' : 'poor';
}

/** Stepper value in 1…max; a max below 1 keeps 1 (nothing to step). */
export function clampCount(n, max) {
    const hi = Math.max(1, Math.trunc(Number(max)) || 1);
    const v = Math.trunc(Number(n)) || 1;
    return Math.min(hi, Math.max(1, v));
}

/** The selected total, `price × N` (protocol §5.4: the extension multiplies). */
export function totalPrice(unitPrice, n) {
    return nonNegInt(unitPrice) * Math.max(1, Math.trunc(Number(n)) || 1);
}

/** "×3" for the steppers and the count suffixes. */
export function timesText(n) {
    return `×${Math.trunc(Number(n)) || 0}`;
}

/** "Buy Rare Candy?" / "Buy Rare Candy ×3?" (SV:108-110). */
export function buyConfirmText(name, n) {
    return fmt(_('Buy %s?'), n > 1 ? `${name} ${timesText(n)}` : name);
}

/** "Buy" / "Buy ×3" (SV:126). */
export function buyButtonLabel(n) {
    return n > 1 ? `${_('Buy')} ${timesText(n)}` : _('Buy');
}

/* ---- egg purchase and the precious-companion guard -------------------- */

/** The name the confirmations use: the localized display name, `#id` without names (bag-shop.md §6 item 8). */
export function companionName(active) {
    if (!active)
        return '';
    return active.displayName ?? `#${active.speciesId}`;
}

/**
 * Sending this companion off needs a second confirmation: it is shiny (as
 * displayed) or legendary. Rare is excluded on purpose (CS:193-195, GuardT:62-67).
 */
export function isHighValueCompanion(active) {
    return Boolean(active) && (Boolean(active.isShiny) || active.rarity === 'legendary');
}

/** "Send off Pidgeotto for the Rare Egg?" (eggConfirm). */
export function eggConfirmText(active, tier) {
    return fmt(_('Send off %s for the %s?'), companionName(active), eggName(tier));
}

/**
 * The second, orange confirmation (SV:248-257): the warning and the button
 * label. Legendary wins over shiny for the warning; the button says "Send
 * shiny off" only for a shiny that is not legendary.
 */
export function preciousConfirm(active) {
    const legendary = active?.rarity === 'legendary';
    const shiny = Boolean(active?.isShiny);
    return {
        warning: legendary
            ? _('⚠️ This is a Legendary Pokémon! Really send it off?')
            : _('⚠️ This one is shiny! Really send it off?'),
        button: shiny && !legendary ? _('Send shiny off') : _('Send off'),
    };
}

/**
 * Egg Buy flow: idle → confirm → (preciousConfirm when high value) → commit.
 * Returns the next step after the button of `step` is pressed.
 */
export function nextEggStep(step, active) {
    if (step === 'idle')
        return 'confirm';
    if (step === 'confirm')
        return isHighValueCompanion(active) ? 'precious' : 'commit';
    if (step === 'precious')
        return 'commit';
    return 'idle';
}

/* ---- Bag ------------------------------------------------------------- */

/** Owned items in the fixed order rareCandy, mint, shinyCharm (CS:1164-1169). */
export function ownedItems(companion) {
    const inv = companion?.inventory ?? {};
    return ITEMS.filter(item => Number(inv[item]) > 0);
}

/**
 * Why Rare Candy cannot be used, from `bag.candyBlocked` (protocol §5.4,
 * decision 10). An unknown value takes the `lineLoading` fallback (§12).
 */
export function candyBlockedText(blocked) {
    switch (blocked) {
    case 'egg': return _('Usable after hatching');
    case 'noStock':
    case 'revealPending': return _('No Pokémon to use it on');
    default: return _('Available once data loads');
    }
}

/**
 * One Bag card per owned item. `usable` drives the Use button; `blockedText`
 * is the caption shown instead.
 */
export function bagEntries(companion) {
    const inv = companion?.inventory ?? {};
    const bag = companion?.bag ?? {};
    const incubating = companion?.phase !== 'active';
    return ownedItems(companion).map(item => {
        const base = {
            item,
            name: itemName(item),
            description: itemDescription(item),
            emoji: itemEmoji(item),
            count: Math.trunc(Number(inv[item])),
            passive: item === 'shinyCharm',
            usable: false,
            maxUse: 0,
            blockedText: null,
            showStepper: false,
        };
        if (item === 'rareCandy') {
            const maxUse = nonNegInt(bag.candyMaxUse);
            base.usable = maxUse > 0;
            base.maxUse = maxUse;
            // A stepper with the single value 1 is noise: mirror the Shop (bag-shop.md §6 item 4).
            base.showStepper = maxUse > 1;
            if (!base.usable)
                base.blockedText = candyBlockedText(incubating ? 'egg' : (bag.candyBlocked ?? 'lineLoading'));
        } else if (item === 'mint') {
            base.usable = Boolean(bag.canUseMint) && !incubating;
            base.maxUse = base.usable ? 1 : 0;
            if (!base.usable)
                base.blockedText = incubating ? _('Usable after hatching') : _('No Pokémon to use it on');
        }
        return base;
    });
}

/** "+300M XP" for N candies: the idle hint and the Home capsule (BV:115, CV:880). */
export function xpText(xp) {
    return `+${compact(xp)} XP`;
}

export function candyEffectHint(n) {
    return xpText(Math.max(1, Math.trunc(Number(n)) || 1) * CANDY_XP);
}

/** Idle hint of a usable item. */
export function effectHint(item, n = 1) {
    if (item === 'rareCandy')
        return candyEffectHint(n);
    if (item === 'mint')
        return _('Random nature');
    return _('Shiny rate ↑ · active');
}

/** "Use on Pidgeotto?" (useOnCurrent). */
export function useConfirmText(active) {
    return fmt(_('Use on %s?'), companionName(active));
}

/** "Use" / "Use ×3" (BV:143). */
export function useButtonLabel(item, n) {
    return item === 'rareCandy' && n > 1 ? `${_('Use')} ${timesText(n)}` : _('Use');
}

/**
 * The candy preview under the description (BV:82-93), from a CandyPlan:
 * graduation first, then either the discarded XP (orange) or the carry-over.
 * `null` (no plan) shows nothing.
 *
 * @returns {{text: string, warning: boolean}[]}
 */
export function candyPreviewLines(plan) {
    if (!plan || typeof plan !== 'object')
        return [];
    const lines = [];
    if (plan.graduates)
        lines.push({text: _('Expected to graduate.'), warning: false});
    if (Number(plan.discardedXP) > 0) {
        lines.push({
            text: fmt(_('On graduation, %s leftover XP will be discarded.'), compact(plan.discardedXP)),
            warning: true,
        });
    } else if (plan.evolves && !plan.graduates) {
        lines.push({
            text: fmt(_('Expected after evolution: %s XP carried over.'), compact(plan.carryoverXP ?? 0)),
            warning: false,
        });
    }
    return lines;
}

/* ---- command results ------------------------------------------------ */

/** Inline text for a failed Shop or Bag command (protocol §11.2). */
export function errorText(code) {
    switch (code) {
    case 'insufficientFunds': return _('Not enough tokens.');
    case 'alreadyOwned': return _('You already own this.');
    case 'noActive': return _('Needs a hatched Pokémon.');
    case 'noStock': return _('None left in your bag.');
    case 'notReady': return _('Available once data loads.');
    case 'timeout':
    case 'helperExited': return _('The helper did not answer. Try again.');
    default: return _('Something went wrong. Try again.');
    }
}

/**
 * What the Home card plays after a successful use (bag-shop.md §2.5):
 * `{kind: 'xp', xp}` for candy (the consumed XP, which can be less than the
 * selection), `{kind: 'sparkle'}` for a Mint, null otherwise.
 */
export function feedbackFor(cmd, data) {
    if (cmd === 'useCandy') {
        const xp = nonNegInt(data?.xp);
        return xp > 0 ? {kind: 'xp', xp} : null;
    }
    if (cmd === 'useMint')
        return {kind: 'sparkle', nature: natureName(data?.nature ?? null)};
    return null;
}
