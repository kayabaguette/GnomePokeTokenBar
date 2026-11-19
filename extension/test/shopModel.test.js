// gjs -m extension/test/shopModel.test.js
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as M from '../lib/shop/shopModel.js';
import {spriteRequestKey} from '../lib/sprites.js';
import {COMMAND_TIMEOUTS_MS} from '../lib/helperClient.js';

const fixtureDir = Gio.File.new_for_uri(import.meta.url).get_parent().get_child('fixtures');

function fixture(name) {
    const [, bytes] = fixtureDir.get_child(name).load_contents(null);
    return JSON.parse(new TextDecoder().decode(bytes));
}

const active = fixture('state-active.json').companion;
const egg = fixture('state-egg.json').companion;

function clone(o) {
    return JSON.parse(JSON.stringify(o));
}

/* ---- Shop ------------------------------------------------------------- */

test('shop entries keep the helper order (default order, protocol §5.4)', () => {
    const e = M.shopEntries(active);
    assert.deepEqual(e.map(x => x.key),
        ['item:mint', 'item:rareCandy', 'egg:plain', 'egg:uncommon', 'item:shinyCharm', 'egg:rare']);
    assert.deepEqual(e.map(x => x.price),
        [100_000_000, 500_000_000, 1_000_000_000, 2_500_000_000, 3_000_000_000, 4_000_000_000]);
});

test('shop entries: names, owned counts, steppers, idle controls on the active fixture', () => {
    const [mint, candy, plain, uncommon, charm, rare] = M.shopEntries(active);
    assert.equal(mint.name, 'Mint');
    assert.equal(mint.showOwned, true, 'Owned ×1');
    assert.equal(mint.showStepper, true, 'maxCount 32 > 1');
    assert.equal(candy.maxCount, 6);
    assert.equal(charm.passive, true);
    assert.equal(charm.showStepper, false, 'passive items never step');
    assert.equal(charm.showOwned, false);
    assert.equal(M.idleControl(charm), 'buy');
    assert.equal(M.idleControl(rare), 'poor', 'rare egg is 4B, wallet 3.2B');
    assert.equal(plain.name, 'Pokémon Egg');
    assert.equal(uncommon.description,
        'Send off your current Pokémon for an egg guaranteed to hatch Uncommon or better.');
    assert.equal(plain.locked, false);
    assert.equal(M.idleControl(plain), 'buy');
});

test('eggs are locked while incubating, even with tokens (PremT:192-200)', () => {
    const c = clone(egg);
    c.wallet = 10_000_000_000;
    for (const e of c.shop.entries) {
        if (e.kind === 'egg') {
            e.canBuy = true; // a stale or wrong flag must not unlock a purchase
            e.maxCount = 1;
        }
    }
    const eggs = M.shopEntries(c).filter(e => e.kind === 'egg');
    assert.equal(eggs.length, 3, 'eggs stay listed while incubating');
    for (const e of eggs) {
        assert.equal(e.locked, true);
        assert.equal(e.canBuy, false);
        assert.equal(M.idleControl(e), 'locked');
    }
});

test('owned Shiny Charm shows Owned and no button', () => {
    const c = clone(active);
    const charm = c.shop.entries.find(e => e.item === 'shinyCharm');
    Object.assign(charm, {owned: 1, purchased: true, canBuy: false, maxCount: 0});
    const e = M.shopEntries(c).find(x => x.item === 'shinyCharm');
    assert.equal(M.idleControl(e), 'owned');
    assert.equal(e.showOwned, false, 'passive items show "Owned", not "Owned ×1"');
});

test('unknown items and unsold tiers are skipped (protocol §12)', () => {
    const c = clone(active);
    c.shop.entries.push({kind: 'item', item: 'masterBall', tier: null, price: 1, passive: false, owned: 0,
        purchased: false, canBuy: true, maxCount: 1});
    c.shop.entries.push({kind: 'egg', item: null, tier: 'legendary', price: 1, passive: false, owned: 0,
        purchased: false, canBuy: true, maxCount: 1});
    c.shop.entries.push({kind: 'bundle'});
    assert.equal(M.shopEntries(c).length, 6);
    assert.deepEqual(M.shopEntries({}), []);
    assert.deepEqual(M.shopEntries(null), []);
});

test('a stepper needs maxCount > 1 and canBuy (SV:65)', () => {
    const c = clone(active);
    const candy = c.shop.entries.find(e => e.item === 'rareCandy');
    candy.maxCount = 1;
    assert.equal(M.shopEntries(c).find(x => x.item === 'rareCandy').showStepper, false);
    candy.maxCount = 2;
    candy.canBuy = false;
    const e = M.shopEntries(c).find(x => x.item === 'rareCandy');
    assert.equal(e.showStepper, false, 'canBuy false');
    assert.equal(M.idleControl(e), 'poor');
});

test('count clamp, totals and labels', () => {
    assert.equal(M.clampCount(5, 3), 3, 'wallet shrank: clamp to the new max');
    assert.equal(M.clampCount(0, 3), 1);
    assert.equal(M.clampCount(-2, 3), 1);
    assert.equal(M.clampCount(4, 0), 1, 'no max keeps 1');
    assert.equal(M.clampCount(2, 6), 2);
    assert.equal(M.totalPrice(500_000_000, 3), 1_500_000_000);
    assert.equal(M.totalPrice(500_000_000, 0), 500_000_000, 'a count below 1 prices one');
    assert.equal(M.buyConfirmText('Rare Candy', 1), 'Buy Rare Candy?');
    assert.equal(M.buyConfirmText('Rare Candy', 3), 'Buy Rare Candy ×3?');
    assert.equal(M.buyButtonLabel(1), 'Buy');
    assert.equal(M.buyButtonLabel(3), 'Buy ×3');
    assert.equal(M.timesText(12), '×12');
});

test('item and egg strings (bag-shop.md §3.5)', () => {
    assert.equal(M.itemDescription('rareCandy'), 'Raises your Pokémon’s EXP by 100M.');
    assert.equal(M.itemName('shinyCharm'), 'Shiny Charm');
    assert.equal(M.itemEmoji('mint'), '🌿');
    assert.equal(M.eggName('rare'), 'Rare Egg');
    assert.equal(M.eggName(null), 'Pokémon Egg');
    assert.equal(M.eggDescription(null), 'Send off your current Pokémon and start fresh with a new egg.');
    assert.equal(M.eggGuaranteeText('rare'), 'Rare or better');
    assert.equal(M.eggGuaranteeText(null), null);
    assert.equal(M.eggGuaranteeText('legendary'), null, 'never a legendary guarantee');
});

/* ---- precious-companion guard (GuardT) ------------------------------- */

test('high value: shiny or legendary, never just rare (GuardT:62-67)', () => {
    const base = {speciesId: 17, displayName: 'Pidgeotto', rarity: 'common', isShiny: false};
    assert.equal(M.isHighValueCompanion(base), false);
    assert.equal(M.isHighValueCompanion({...base, rarity: 'rare'}), false, 'rare is excluded');
    assert.equal(M.isHighValueCompanion({...base, isShiny: true}), true);
    assert.equal(M.isHighValueCompanion({...base, rarity: 'legendary'}), true);
    assert.equal(M.isHighValueCompanion(null), false);
    // A disguised Ditto is sent as displaysShiny=false, rarity common (GuardT:75-87).
    assert.equal(M.isHighValueCompanion({speciesId: 132, rarity: 'common', isShiny: false}), false);
});

test('egg flow: confirm, then precious only for a high-value companion', () => {
    const plain = {rarity: 'common', isShiny: false, displayName: 'Pidgeotto'};
    const shiny = {...plain, isShiny: true};
    assert.equal(M.nextEggStep('idle', plain), 'confirm');
    assert.equal(M.nextEggStep('confirm', plain), 'commit');
    assert.equal(M.nextEggStep('confirm', shiny), 'precious');
    assert.equal(M.nextEggStep('precious', shiny), 'commit');
    assert.equal(M.eggConfirmText(plain, 'rare'), 'Send off Pidgeotto for the Rare Egg?');
    assert.equal(M.eggConfirmText({speciesId: 17}, null), 'Send off #17 for the Pokémon Egg?', 'no name → #id');
});

test('precious warning and button text (SV:248-257)', () => {
    assert.deepEqual(M.preciousConfirm({rarity: 'common', isShiny: true}),
        {warning: '⚠️ This one is shiny! Really send it off?', button: 'Send shiny off'});
    assert.deepEqual(M.preciousConfirm({rarity: 'legendary', isShiny: false}),
        {warning: '⚠️ This is a Legendary Pokémon! Really send it off?', button: 'Send off'});
    assert.deepEqual(M.preciousConfirm({rarity: 'legendary', isShiny: true}),
        {warning: '⚠️ This is a Legendary Pokémon! Really send it off?', button: 'Send off'},
        'legendary wins over shiny');
});

/* ---- Bag ------------------------------------------------------------- */

test('bag lists owned items in the fixed order with counts', () => {
    const b = M.bagEntries(active);
    assert.deepEqual(b.map(x => [x.item, x.count]), [['rareCandy', 3], ['mint', 1]]);
    const [candy, mint] = b;
    assert.equal(candy.usable, true);
    assert.equal(candy.maxUse, 3);
    assert.equal(candy.showStepper, true);
    assert.equal(mint.usable, true);
    assert.equal(mint.showStepper, false);
    const c = clone(active);
    c.inventory = {rareCandy: 2, mint: 0, shinyCharm: 1};
    assert.deepEqual(M.ownedItems(c), ['rareCandy', 'shinyCharm'], 'fixed order, not price order');
    assert.equal(M.bagEntries(c)[1].passive, true);
    assert.deepEqual(M.ownedItems(egg), [], 'empty inventory → empty state');
});

test('candy disabled reasons from bag.candyBlocked, each alone', () => {
    const c = clone(active);
    c.bag = {candyMaxUse: 0, canUseMint: true, candyBlocked: 'lineLoading'};
    let candy = M.bagEntries(c)[0];
    assert.equal(candy.usable, false);
    assert.equal(candy.blockedText, 'Available once data loads', 'decision 10: loading, not "No Pokémon"');
    c.bag.candyBlocked = 'revealPending';
    assert.equal(M.bagEntries(c)[0].blockedText, 'No Pokémon to use it on');
    c.bag.candyBlocked = 'bogus';
    assert.equal(M.bagEntries(c)[0].blockedText, 'Available once data loads', 'unknown → lineLoading fallback');
    c.bag.candyBlocked = null; // maxUse 0 without a reason: still blocked, fallback text
    assert.equal(M.bagEntries(c)[0].blockedText, 'Available once data loads');
    const e = clone(egg);
    e.inventory = {rareCandy: 2, mint: 1, shinyCharm: 0};
    e.bag = {candyMaxUse: 0, canUseMint: false, candyBlocked: 'egg'};
    const [candyE, mintE] = M.bagEntries(e);
    assert.equal(candyE.blockedText, 'Usable after hatching');
    assert.equal(mintE.usable, false);
    assert.equal(mintE.blockedText, 'Usable after hatching');
    candy = M.bagEntries(c)[0];
    assert.equal(candy.showStepper, false, 'no stepper while blocked');
});

test('candy stepper hides at maxUse 1 (mirrors the Shop, §6 item 4)', () => {
    const c = clone(active);
    c.bag.candyMaxUse = 1;
    const candy = M.bagEntries(c)[0];
    assert.equal(candy.usable, true);
    assert.equal(candy.showStepper, false);
});

test('mint usable only with canUseMint and an active Pokémon', () => {
    const c = clone(active);
    c.bag.canUseMint = false;
    assert.equal(M.bagEntries(c)[1].blockedText, 'No Pokémon to use it on');
});

test('effect hints, use labels, confirmations', () => {
    assert.equal(M.effectHint('rareCandy', 3), '+300M XP');
    assert.equal(M.effectHint('rareCandy', 1), '+100M XP');
    assert.equal(M.effectHint('mint'), 'Random nature');
    assert.equal(M.effectHint('shinyCharm'), 'Shiny rate ↑ · active');
    assert.equal(M.useButtonLabel('rareCandy', 3), 'Use ×3');
    assert.equal(M.useButtonLabel('rareCandy', 1), 'Use');
    assert.equal(M.useButtonLabel('mint', 1), 'Use');
    assert.equal(M.useConfirmText(active.active), 'Use on Pidgeotto?');
    assert.equal(M.xpText(1_200_000_000), '+1.2B XP');
});

test('candy preview lines (BV:82-93) from the fixture plan and each branch', () => {
    const plan = fixture('candy-plan.json').data.plan;
    assert.deepEqual(M.candyPreviewLines(plan),
        [{text: 'Expected after evolution: 212.4M XP carried over.', warning: false}]);
    assert.deepEqual(M.candyPreviewLines({count: 1, evolves: false, graduates: false, carryoverXP: 0, discardedXP: 0}), [],
        'progress only: no line');
    assert.deepEqual(M.candyPreviewLines({count: 2, evolves: true, graduates: false, carryoverXP: 0, discardedXP: 0}),
        [{text: 'Expected after evolution: 0 XP carried over.', warning: false}], 'carry-over 0 prints 0 XP');
    assert.deepEqual(M.candyPreviewLines({count: 5, evolves: true, graduates: true, carryoverXP: 0, discardedXP: 37_600_000}), [
        {text: 'Expected to graduate.', warning: false},
        {text: 'On graduation, 37.6M leftover XP will be discarded.', warning: true},
    ]);
    assert.deepEqual(M.candyPreviewLines({count: 1, evolves: false, graduates: true, carryoverXP: 0, discardedXP: 0}),
        [{text: 'Expected to graduate.', warning: false}], 'exact graduation: no discard line, no carry-over');
    assert.deepEqual(M.candyPreviewLines(null), []);
});

test('error text per protocol code', () => {
    assert.equal(M.errorText('insufficientFunds'), 'Not enough tokens.');
    assert.equal(M.errorText('alreadyOwned'), 'You already own this.');
    assert.equal(M.errorText('noActive'), 'Needs a hatched Pokémon.');
    assert.equal(M.errorText('noStock'), 'None left in your bag.');
    assert.equal(M.errorText('notReady'), 'Available once data loads.');
    assert.equal(M.errorText('timeout'), 'The helper did not answer. Try again.');
    assert.equal(M.errorText('helperExited'), 'The helper did not answer. Try again.');
    assert.equal(M.errorText('internal'), 'Something went wrong. Try again.');
    assert.equal(M.errorText(undefined), 'Something went wrong. Try again.');
});

test('feedback from replies: consumed XP, sparkle', () => {
    assert.deepEqual(M.feedbackFor('useCandy', {consumed: 3, xp: 300_000_000, result: 'evolved'}),
        {kind: 'xp', xp: 300_000_000});
    assert.deepEqual(M.feedbackFor('useCandy', {consumed: 0, xp: 0, result: 'progressed'}), null);
    assert.deepEqual(M.feedbackFor('useMint', {nature: 'timid'}), {kind: 'sparkle', nature: 'Timid'});
    assert.equal(M.feedbackFor('buyItem', {}), null);
});

test('Shop and Bag command timeouts (protocol §4)', () => {
    assert.equal(COMMAND_TIMEOUTS_MS.buyItem, 10_000);
    assert.equal(COMMAND_TIMEOUTS_MS.buyEgg, 10_000);
    assert.equal(COMMAND_TIMEOUTS_MS.planCandies, 5_000);
    assert.equal(COMMAND_TIMEOUTS_MS.useCandy, 10_000);
    assert.equal(COMMAND_TIMEOUTS_MS.useMint, 10_000);
});

test('item sprite names (protocol §8.1): Mint has none', () => {
    assert.equal(M.ITEM_SPRITE.rareCandy, 'rare-candy');
    assert.equal(M.ITEM_SPRITE.shinyCharm, 'shiny-charm');
    assert.equal(M.ITEM_SPRITE.mint, undefined);
    assert.equal(M.itemSpriteName('rareCandy'), 'rare-candy');
    assert.equal(M.itemSpriteName('shinyCharm'), 'shiny-charm');
    assert.equal(M.itemSpriteName('mint'), null);
    assert.equal(M.itemSpriteName('toString'), null);
    assert.equal(M.itemEmoji('mint'), '🌿');
});

test('spriteRequestKey: item keys do not collide with egg or species', () => {
    const k = spriteRequestKey({item: 'rare-candy'});
    assert.equal(k, 'item-rare-candy');
    assert.equal(spriteRequestKey({item: 'shiny-charm'}), 'item-shiny-charm');
    assert.equal(k === spriteRequestKey({egg: true}), false);
    assert.equal(k === spriteRequestKey({speciesId: 25}), false);
    assert.equal(spriteRequestKey({egg: true, item: 'rare-candy'}), 'egg');
});

run('shopModel.test.js');
