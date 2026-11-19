/* Shop and Bag screenshot suites for the sandbox driver (ptb-shot@local).
 *
 *   PTB_SHOT_SUITE=shop       the Shop and Bag tabs against the stub and
 *                             state-active.json: wallet, steppers, inline
 *                             confirmations, the precious-companion guard,
 *                             candy preview, Use → Home feedback, a real
 *                             item and egg purchase, locked eggs, an error
 *                             line, the lineLoading and empty Bag states,
 *                             dark and light.
 *   PTB_SHOT_SUITE=shop-cycle keeps the popover open on the Shop tab, with a
 *                             confirmation showing, until the headless
 *                             --cycle disable lands.
 *
 * States the stub cannot produce (a shiny companion, lineLoading, an empty
 * Bag, a stale canBuy) are rendered by calling the view's update() with a
 * patched copy of the current state; the next real state replaces them.
 */

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const TARGET = 'poketokenbar@local';

function* descendants(actor) {
    for (const child of actor.get_children()) {
        yield child;
        yield* descendants(child);
    }
}

/** The first St.Button under `root` whose label is `text`. */
function button(root, text) {
    for (const a of descendants(root)) {
        if (a instanceof St.Button && a.label === text)
            return a;
    }
    return null;
}

function labels(root) {
    const out = [];
    for (const a of descendants(root)) {
        if (a instanceof St.Label && a.visible && a.text)
            out.push(a.text);
    }
    return out;
}

/** Scroll `scroll` so `actor` sits near its top. */
function reveal(scroll, actor, margin = 12) {
    const [, y] = actor.get_transformed_position();
    const [, sy] = scroll.get_transformed_position();
    const adj = scroll.vadjustment;
    adj.value = Math.max(0, Math.min(adj.upper - adj.page_size, adj.value + (y - sy) - margin));
}

class ShopDriver {
    constructor(shot) {
        this.s = shot; // the ShotExtension: _sleep, _shot, _menuArea, _clickActor, _movePointer
        this.prefix = '';
    }

    async start() {
        await this.s._sleep(Number(GLib.getenv('PTB_SHOT_DELAY_MS') ?? 4000));
        this.ptb = Extension.lookupByUUID(TARGET);
        this.indicator = Main.panel.statusArea[TARGET];
        if (!this.indicator || !this.ptb) {
            console.error('ptb-shot: PokeTokenBar indicator not found');
            return false;
        }
        this.s._movePointer(10, 400);
        if (Main.overview.visible) {
            Main.overview.hide();
            await this.s._sleep(2500);
        }
        return true;
    }

    get popover() {
        return this.indicator._popover;
    }

    area() {
        return this.s._menuArea(this.indicator);
    }

    async open() {
        if (!this.indicator.menu.isOpen) {
            this.indicator.menu.open();
            await this.s._sleep(1200);
            const [mx, my] = this.indicator.menu.actor.get_transformed_position();
            this.s._movePointer(mx + 40, my + 300); // warm-up motion
            await this.s._sleep(300);
        }
    }

    async tab(name) {
        await this.open();
        await this.s._clickActor(this.popover._tabButtons[name]);
        await this.s._sleep(600);
        if (this.popover._tab !== name) {
            console.log(`ptb-shot: tab click missed (${this.popover._tab}); switching directly`);
            this.popover.showTab(name);
            await this.s._sleep(400);
        }
    }

    async shot(name) {
        await this.s._shot(`${this.prefix}${name}`, this.area());
    }

    async click(root, text) {
        const b = button(root, text);
        if (!b) {
            console.error(`ptb-shot: no button '${text}' in ${labels(root).join(' | ')}`);
            return false;
        }
        await this.s._clickActor(b);
        await this.s._sleep(350);
        return true;
    }

    patched(fn) {
        const ctx = this.ptb.context();
        const state = JSON.parse(JSON.stringify(ctx.state));
        fn(state.companion);
        return {...ctx, state};
    }

    shopCard(key) {
        return this.popover._shop._cards.get(key);
    }

    bagCard(item) {
        return this.popover._bag._cards.get(item);
    }

    log(msg) {
        console.log(`ptb-shot: ${msg}`);
    }

    wallet() {
        return this.ptb.context().state?.companion?.wallet;
    }
}

export async function runShop(shot) {
    const d = new ShopDriver(shot);
    if (!await d.start())
        return;
    const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
    const sleep = ms => shot._sleep(ms);

    // A. The Shop tab, top and bottom.
    await d.tab('shop');
    const shopScroll = d.popover._shop.actor;
    d.log(`shop entries ${[...d.popover._shop._cards.keys()].join(',')}; wallet ${d.wallet()}`);
    await d.shot('shop-dark.png');
    shopScroll.vadjustment.value = shopScroll.vadjustment.upper;
    await sleep(400);
    await d.shot('shop-dark-bottom.png');
    shopScroll.vadjustment.value = 0;
    await sleep(300);

    // B. Rare Candy ×3, Buy → inline confirmation, then Cancel.
    const candy = d.shopCard('item:rareCandy');
    await shot._clickActor(candy._stepper._plus);
    await shot._clickActor(candy._stepper._plus);
    await sleep(300);
    d.log(`candy stepper ×${candy._stepper.value}, price row '${labels(candy._controls).join(' ')}'`);
    await d.click(candy.actor, 'Buy');
    await d.shot('shop-confirm-candy-dark.png');
    await d.click(candy.actor, 'Cancel');

    // C. Plain egg: confirm; a shiny companion adds the orange second step.
    const plain = d.shopCard('egg:plain');
    reveal(shopScroll, plain.actor);
    await sleep(300);
    await d.click(plain.actor, 'Buy');
    await d.shot('shop-confirm-egg-dark.png');
    await d.click(plain.actor, 'Cancel');
    d.popover._shop.update(d.patched(c => {
        c.active.isShiny = true;
    }));
    await sleep(200);
    await d.click(plain.actor, 'Buy');
    await d.click(plain.actor, 'Buy'); // high value → precious step
    d.log(`precious step: ${plain._step}; text '${labels(plain._controls).join(' | ')}'`);
    await d.shot('shop-precious-shiny-dark.png');
    d.popover._shop.update(d.patched(c => {
        c.active.rarity = 'legendary';
    }));
    await sleep(300);
    d.log(`legendary: step ${plain._step}; text '${labels(plain._controls).join(' | ')}'`);
    await d.click(plain.actor, 'Cancel');
    d.popover._shop.update(d.ptb.context());
    shopScroll.vadjustment.value = 0;
    await sleep(300);

    // D. Light scheme: Shop with a confirmation, precious, Bag.
    iface.set_string('color-scheme', 'prefer-light');
    await sleep(1500);
    await d.click(candy.actor, 'Buy');
    await d.shot('shop-light.png');
    await d.click(candy.actor, 'Cancel');
    d.popover._shop.update(d.patched(c => {
        c.active.isShiny = true;
    }));
    reveal(shopScroll, plain.actor);
    await sleep(300);
    await d.click(plain.actor, 'Buy');
    await d.click(plain.actor, 'Buy');
    await d.shot('shop-precious-light.png');
    await d.click(plain.actor, 'Cancel');
    d.popover._shop.update(d.ptb.context());
    await d.tab('bag');
    await sleep(600);
    await d.shot('bag-light.png');
    iface.reset('color-scheme');
    await sleep(1500);

    // E. Bag: candy ×2 with its preview, Use → confirm → Home "+XP".
    await d.tab('bag');
    await sleep(600);
    const bagCandy = d.bagCard('rareCandy');
    d.log(`bag cards ${[...d.popover._bag._cards.keys()].join(',')}; preview '${labels(bagCandy._preview).join(' | ')}'`);
    await d.shot('bag-dark.png');
    await shot._clickActor(bagCandy._stepper._plus);
    await sleep(700);
    d.log(`bag candy ×${bagCandy.count}; preview '${labels(bagCandy._preview).join(' | ')}'`);
    await d.click(bagCandy.actor, 'Use');
    await d.shot('bag-confirm-dark.png');
    await d.click(bagCandy.actor, 'Use ×2');
    await sleep(150);
    d.log(`after Use ×2: tab ${d.popover._tab}, fx children ${d.popover._companion._fxLayer.get_n_children()}`);
    await d.shot('home-xp-dark.png');
    await sleep(1000);
    await d.shot('home-xp-dark-2.png');
    await sleep(2000);
    d.log(`after the capsule: fx children ${d.popover._companion._fxLayer.get_n_children()}`);

    // F. Mint → Home sparkle.
    await d.tab('bag');
    const mint = d.bagCard('mint');
    const natureBefore = d.ptb.context().state.companion.active?.nature;
    await d.click(mint.actor, 'Use');
    await d.click(mint.actor, 'Use');
    await sleep(250);
    await d.shot('home-sparkle-dark.png');
    d.log(`mint: nature ${natureBefore} → ${d.ptb.context().state.companion.active?.nature}; tab ${d.popover._tab}`);
    await sleep(1800);

    // G. Bag states the stub cannot produce: lineLoading, then empty.
    await d.tab('bag');
    d.popover._bag.update(d.patched(c => {
        c.inventory.rareCandy = 4;
        c.inventory.shinyCharm = 1;
        c.bag = {candyMaxUse: 0, canUseMint: false, candyBlocked: 'lineLoading'};
    }));
    await sleep(500);
    await d.shot('bag-lineloading-dark.png');
    d.popover._bag.update(d.patched(c => {
        c.inventory = {rareCandy: 0, mint: 0, shinyCharm: 0};
    }));
    await sleep(1500);
    await d.shot('bag-empty-dark.png');
    d.popover._bag.update(d.ptb.context());
    await sleep(300);

    // H. A real item purchase: one Rare Candy.
    await d.tab('shop');
    shopScroll.vadjustment.value = 0;
    const before = d.wallet();
    while (candy._stepper.value > 1) // the ×3 from B is kept: back to one
        await shot._clickActor(candy._stepper._minus);
    await d.click(candy.actor, 'Buy');
    await d.click(candy.actor, 'Buy');
    await sleep(600);
    d.log(`bought a Rare Candy: wallet ${before} → ${d.wallet()}, owned ${d.ptb.context().state.companion.inventory.rareCandy}`);
    await d.shot('shop-bought-dark.png');

    // I. A real egg purchase: the Uncommon Egg → Home with the guarantee badge.
    const uncommon = d.shopCard('egg:uncommon');
    reveal(shopScroll, uncommon.actor);
    await sleep(300);
    await d.click(uncommon.actor, 'Buy');
    await d.click(uncommon.actor, 'Buy');
    await sleep(800);
    const c = d.ptb.context().state.companion;
    d.log(`egg bought: phase ${c.phase}, guarantee ${c.egg?.guarantee}, tab ${d.popover._tab}, wallet ${c.wallet}`);
    await d.shot('home-egg-guarantee-dark.png');

    // J. Incubating: eggs locked in the Shop, candy waits for the hatch.
    await d.tab('shop');
    reveal(shopScroll, d.shopCard('egg:plain').actor);
    await sleep(400);
    await d.shot('shop-locked-dark.png');
    await d.tab('bag');
    await d.shot('bag-egg-dark.png');

    // K. An error line: a stale canBuy lets the click through; the stub refuses.
    await d.tab('shop');
    d.popover._shop.update(d.patched(co => {
        const charm = co.shop.entries.find(e => e.item === 'shinyCharm');
        Object.assign(charm, {canBuy: true, maxCount: 1});
    }));
    const charm = d.shopCard('item:shinyCharm');
    reveal(shopScroll, charm.actor);
    await sleep(300);
    await d.click(charm.actor, 'Buy');
    await d.click(charm.actor, 'Buy');
    await sleep(800);
    d.log(`charm error: '${charm._error}'`);
    await d.shot('shop-error-dark.png');

    d.indicator.menu.close();
    d.log('done');
}

/** Hold the popover open on the Shop tab, mid-confirmation, until the disable lands. */
export async function runShopCycle(shot) {
    const d = new ShopDriver(shot);
    if (!await d.start())
        return;
    let opened = 0;
    for (;;) {
        if (Main.panel.statusArea[TARGET] !== d.indicator)
            return; // PokeTokenBar was disabled first: stop here
        if (!d.indicator.menu.isOpen) {
            await d.tab('shop');
            d.popover._shop._cards.get('item:rareCandy')?._stepper._step(1);
            await d.click(d.shopCard('item:rareCandy').actor, 'Buy');
            d.popover._bag.requestPlan();
            if (opened++ === 0)
                d.log(`shop-cycle: menu open ${d.indicator.menu.isOpen}, tab ${d.popover._tab}`);
            else
                d.log(`shop-cycle: menu re-opened (${opened})`);
        }
        await shot._sleep(400);
    }
}
