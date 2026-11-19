/* Floating pet suite for the sandbox driver (PTB_SHOT_SUITE=pet).
 *
 * Drives the pet through the virtual pointer and the sandbox's PRIVATE
 * session bus (dbus-run-session inside scripts/headless-shell.sh), and logs
 * what each step changed:
 *   - hidden in the overview, shown at the default home after it;
 *   - hover callout, petting hearts, a click (popover), a drag (persisted
 *     once, on release), the context menu;
 *   - agent Report/OpenUrl calls (bob, waiting bubble, done run), plus
 *     malformed calls that must be dropped without a JS error;
 *   - the stub's hatch/evolve events (callout, bar flash) and its limitAlert
 *     (bubble), then an injected warn+crit pair (the 250 ms batch picks crit);
 *   - light scheme, size 192, off-screen recovery.
 * With PTB_SHOT_PET_HOLD=1 it only shows the pet, arms agent timers, starts a
 * run and keeps the popover open, for the headless --cycle disable.
 *
 * Use with prepare-sandbox.sh eventsAfter = 26.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

Gio._promisify(Gio.DBusConnection.prototype, 'call');

const TARGET = 'poketokenbar@local';
const BUS = 'io.github.poketokenbar.Agent';
const PATH = '/io/github/poketokenbar/Agent';
const IFACE = 'io.github.poketokenbar.Agent1';

function log(msg) {
    console.log(`ptb-shot: pet: ${msg}`);
}

/** A method call on the sandbox's own session bus. */
async function call(method, args) {
    const sig = method === 'Report' ? '(sss)' : '(s)';
    try {
        await Gio.DBus.session.call(BUS, PATH, IFACE, method, new GLib.Variant(sig, args), null,
            Gio.DBusCallFlags.NO_AUTO_START, 2000, null);
        log(`D-Bus ${method}(${args.map(a => JSON.stringify(a)).join(', ')}) → ok`);
    } catch (e) {
        log(`D-Bus ${method} failed: ${e.message}`);
    }
}

async function elapsedAt(d, seconds) {
    const now = (GLib.get_monotonic_time() - d._startedAt) / 1e6;
    if (seconds > now)
        await d._sleep(Math.round((seconds - now) * 1000));
}

async function leaveOverview(d) {
    d._movePointer(400, 300); // never near the hot corner: (10, 10) reopens the overview
    for (let i = 0; i < 10 && (Main.overview.visible || Main.layoutManager.overviewGroup.visible); i++) {
        Main.overview.hide();
        await d._sleep(1000);
    }
    await d._sleep(300);
}

function petInfo(pet) {
    const r = pet._root;
    const c = pet._controller;
    return `visible ${r.visible} at ${r.x},${r.y} size ${r.width} phase ${c.runPhase} bob ${pet._bobId !== 0} ` +
        `bubble ${c.bubble?.kind ?? '-'} hearts ${c.heartsVisible} callout ${pet._callout.visible}`;
}

function hitCentre(pet) {
    const [x, y] = pet._hit.get_transformed_position();
    const [w, h] = pet._hit.get_transformed_size();
    return [Math.round(x + w / 2), Math.round(y + h / 2)];
}

/** Screen area around the pet and the bubbles above it. */
function petArea(pet, wide = false) {
    const [x, y] = pet._root.get_transformed_position();
    const size = pet._root.width;
    const w = wide ? 420 : 300;
    const left = Math.max(0, Math.min(1280 - w, Math.round(x + size / 2 - w / 2)));
    const top = Math.max(0, Math.round(y - 120));
    return {x: left, y: top, width: w, height: Math.min(800 - top, Math.round(size + 140))};
}

async function pointerTo(d, x, y) {
    for (let i = 0; i < 4; i++) {
        d._movePointer(x, y);
        await d._sleep(120);
        const [px, py] = global.get_pointer();
        if (Math.abs(px - x) <= 2 && Math.abs(py - y) <= 2)
            return;
    }
}

async function button(d, state) {
    d._pointer.notify_button(GLib.get_monotonic_time(), Clutter.BUTTON_PRIMARY, state);
    await d._sleep(60);
}

export async function runPet(d) {
    const ptb = Extension.lookupByUUID(TARGET);
    // Create the virtual pointer while the overview is up: its first motion can
    // trip the hot corner, which would reopen the overview later.
    await d._sleep(1000);
    d._movePointer(640, 400);
    await d._sleep(3000);
    const indicator = Main.panel.statusArea[TARGET];
    const pet = ptb?._pet;
    if (!indicator || !pet) {
        console.error('ptb-shot: PokeTokenBar pet not found');
        return;
    }
    const settings = ptb.getSettings();
    const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
    let originWrites = 0;
    settings.connect('changed::floating-pet-origin', () => originWrites++);

    if (GLib.getenv('PTB_SHOT_PET_HOLD') === '1')
        return hold(d, indicator, pet, settings);

    // 1. Enabled while the shell is still in the overview: hidden there.
    settings.set_boolean('floating-pet-enabled', true);
    await d._sleep(300);
    log(`in overview (${Main.overview.visible}): ${petInfo(pet)}`);
    await leaveOverview(d);
    log(`overview ${Main.overview.visible}, pet sees overview ${pet._inOverview}, display on ${pet._displayOn}`);
    log(`after overview: ${petInfo(pet)}; home ${JSON.stringify(pet._home)} (expect x 1160, bottom 776)`);
    const [ix, iy] = pet._hit.get_transformed_position();
    await d._shot('pet-default-dark.png', petArea(pet)); // a paint refreshes the pick buffer
    const under = global.stage.get_actor_at_pos(Clutter.PickMode.REACTIVE, ix - 6, iy - 20);
    const onSprite = global.stage.get_actor_at_pos(Clutter.PickMode.REACTIVE, ...hitCentre(pet));
    log(`pick beside the sprite: ${under === pet._hit ? 'PET' : under?.constructor?.name}; on it: ` +
        `${onSprite === pet._hit ? 'PET' : onSprite?.constructor?.name}; ` +
        `stacked right above window_group: ${Main.layoutManager.uiGroup.get_children().indexOf(pet._root) === 1}`);

    // 2. Hover: the callout.
    const [hx, hy] = hitCentre(pet);
    await pointerTo(d, hx, hy);
    await d._sleep(400);
    log(`hover: ${petInfo(pet)}; text '${pet._callout.text}'`);
    await d._shot('pet-hover-dark.png', petArea(pet));

    // 3. Petting: side to side with no button held.
    // Strokes of 22 px each way: the first must already travel the minimum stroke (14.4 px at 96).
    for (let i = 0; i < 10; i++) {
        d._movePointer(hx + (i % 2 === 0 ? -22 : 0), hy);
        await d._sleep(60);
    }
    await d._sleep(150);
    log(`petting: ${petInfo(pet)}`);
    await d._shot('pet-hearts-dark.png', petArea(pet));
    await pointerTo(d, 400, 300);
    await d._sleep(1900);
    log(`hearts after 1.8 s: ${pet._controller.heartsVisible}`);

    // 4. Agents over D-Bus: working (bob) and waiting (bubble).
    await call('Report', ['working', 'web', '']);
    await call('Report', ['waiting', 'api', 'Pick a branch to deploy']);
    await d._sleep(1200);
    log(`bubble at ${pet._bubble.x},${pet._bubble.y} ${pet._bubble.width}x${pet._bubble.height}; tail visible ${pet._tail.visible} ` +
        `mapped ${pet._tail.mapped} at ${pet._tail.x},${pet._tail.y} ${pet._tail.width}x${pet._tail.height} opacity ${pet._tail.opacity}; ` +
        `visibleTop ${pet._visibleTop()} image ${JSON.stringify(pet._sprite.imageRect)} frac ${pet._sprite.visibleTopFraction}`);
    log(`agents: ${petInfo(pet)}; body scale ${pet._body.scale_x.toFixed(2)}x${pet._body.scale_y.toFixed(2)}`);
    await d._shot('pet-waiting-dark.png', petArea(pet));
    // Malformed input is dropped silently.
    await call('OpenUrl', ['poketokenbar://agent?state=done&message=%E2%80']);
    await call('OpenUrl', ['not a url']);
    await call('Report', ['sleeping', '', '']);
    log(`after malformed calls: ${petInfo(pet)}`);

    // 5. A click: answers the waiting agent, keeps the bob, opens the popover.
    await pointerTo(d, hx, hy);
    await button(d, Clutter.ButtonState.PRESSED);
    await button(d, Clutter.ButtonState.RELEASED);
    await d._sleep(400);
    if (!indicator.menu.isOpen) { // first virtual click after a move can be dropped (harness)
        log('first click dropped by the harness; clicking again');
        await button(d, Clutter.ButtonState.PRESSED);
        await button(d, Clutter.ButtonState.RELEASED);
        await d._sleep(400);
    }
    log(`click: popover open ${indicator.menu.isOpen}; ${petInfo(pet)}; origin writes ${originWrites}`);
    await d._shot('pet-click-popover-dark.png');
    indicator.menu.close();
    await d._sleep(600);

    // 6. Drag: press, 10 steps of (-12, -6), release. One write, on release.
    await pointerTo(d, hx, hy);
    const before = [pet._root.x, pet._root.y];
    await button(d, Clutter.ButtonState.PRESSED);
    for (let i = 1; i <= 10; i++) {
        d._movePointer(hx - 12 * i, hy - 6 * i);
        await d._sleep(40);
    }
    log(`mid-drag: origin writes ${originWrites}, root ${pet._root.x},${pet._root.y}`);
    await button(d, Clutter.ButtonState.RELEASED);
    await d._sleep(300);
    log(`drag: ${before} → ${pet._root.x},${pet._root.y}; origin writes ${originWrites}; ` +
        `saved ${JSON.stringify(settings.get_value('floating-pet-origin').deepUnpack())}; popover ${indicator.menu.isOpen}`);
    await pointerTo(d, 300, 250);

    // 7. Done: the pet runs next to the cursor, then walks home after 6 s.
    await call('Report', ['done', 'api', 'Tests pass']);
    await d._sleep(800);
    log(`done run: ${petInfo(pet)}`);
    await d._shot('pet-done-run-dark.png', {x: 150, y: 100, width: 600, height: 400});
    await d._sleep(6000);
    log(`after done: ${petInfo(pet)}`);
    await call('Report', ['idle', 'web', '']);

    // 8. Context menu.
    const [mx, my] = hitCentre(pet);
    await pointerTo(d, mx, my);
    d._click(Clutter.BUTTON_SECONDARY);
    await d._sleep(800);
    log(`context menu open ${pet._menu.isOpen}: ${pet._menu._getMenuItems().map(i => i.label?.text).join(' | ')}`);
    await d._shot('pet-menu-dark.png', {x: 800, y: 380, width: 480, height: 420});
    pet._menu.close();
    await pointerTo(d, 400, 300);

    // 9. Stub events: hatch/evolve (flash, callout), then the limitAlert bubble.
    await elapsedAt(d, 26.4);
    log(`hatch: indicator class '${indicator.get_style_class_name()}'`);
    await d._shot('panel-celebrate-dark.png', d._panelArea());
    await elapsedAt(d, 27.5);
    log(`event callout: ${petInfo(pet)}; text '${pet._callout.text}'`);
    await d._shot('pet-event-dark.png', petArea(pet));
    await elapsedAt(d, 31.6);
    log(`stub limitAlert: ${petInfo(pet)}; alert ${JSON.stringify(pet._controller.limitAlert)}`);
    await d._shot('pet-limit-warn-dark.png', petArea(pet));
    pet.handleEvent({kind: 'limitAlert', windowKind: 'fiveHour', label: null, tier: 'warn', utilization: 91});
    pet.handleEvent({kind: 'limitAlert', windowKind: 'sevenDay', label: null, tier: 'crit', utilization: 96});
    await d._sleep(500);
    log(`batched pair: tier ${pet._controller.limitAlert?.tier} ${pet._controller.limitAlert?.utilization}`);
    await d._shot('pet-limit-crit-dark.png', petArea(pet));
    await d._sleep(6200);
    log(`after TTL: alert ${pet._controller.limitAlert}`);

    // 10. Light scheme: callout and a bubble.
    iface.set_string('color-scheme', 'prefer-light');
    await d._sleep(1200);
    await call('OpenUrl', ['poketokenbar://agent?state=waiting&label=my-web-app&message=Allow%20edits%20to%20package.json%3F']);
    await d._sleep(800);
    await d._shot('pet-waiting-light.png', petArea(pet));
    await call('Report', ['idle', 'my-web-app', '']);
    await pointerTo(d, ...hitCentre(pet));
    await d._sleep(500);
    await d._shot('pet-hover-light.png', petArea(pet));
    await pointerTo(d, 400, 300);
    iface.reset('color-scheme');
    await d._sleep(600);

    // 11. Overview, size, off-screen recovery, the switch off and on.
    Main.overview.show();
    await d._sleep(1500);
    log(`overview shown: ${petInfo(pet)}`);
    await leaveOverview(d);
    log(`overview hidden: ${petInfo(pet)}`);
    settings.set_int('floating-pet-size', 192);
    await d._sleep(800);
    log(`size 192: ${petInfo(pet)}; image ${JSON.stringify(pet._sprite.imageRect)}`);
    await d._shot('pet-192-dark.png', petArea(pet, true));
    settings.set_value('floating-pet-origin', new GLib.Variant('m(dd)', [5000, 500]));
    await d._sleep(300);
    log(`off-screen origin recovered to ${JSON.stringify(pet._home)}: ${petInfo(pet)}`);
    settings.reset('floating-pet-size');
    settings.set_boolean('floating-pet-enabled', false);
    await d._sleep(300);
    log(`switched off: ${petInfo(pet)}`);
    await call('Report', ['working', 'x', '']);
    log(`report while off: agents ${pet._controller.tracker.agents.size}`);
    settings.set_boolean('floating-pet-enabled', true);
    await d._sleep(300);
    log(`switched on: ${petInfo(pet)}`);

    // 12. Ctrl+click on the indicator: the secondary menu, not the popover.
    const [bx, by] = indicator.get_transformed_position();
    const [bw, bh] = indicator.get_transformed_size();
    await pointerTo(d, Math.round(bx + bw / 2), Math.round(by + bh / 2));
    d._key(Clutter.KEY_Control_L); // creates the virtual keyboard
    const t = GLib.get_monotonic_time();
    d._keyboard.notify_keyval(t, Clutter.KEY_Control_L, Clutter.KeyState.PRESSED);
    await d._sleep(100);
    d._click(Clutter.BUTTON_PRIMARY);
    await d._sleep(500);
    d._keyboard.notify_keyval(GLib.get_monotonic_time(), Clutter.KEY_Control_L, Clutter.KeyState.RELEASED);
    log(`ctrl+click: secondary ${indicator._secondary.isOpen}, popover ${indicator.menu.isOpen}`);
    if (indicator._secondary.isOpen)
        await d._shot('secondary-ctrl-click-dark.png', d._menuArea({menu: indicator._secondary}));
    indicator._secondary.close();
    indicator.menu.close();
    await d._sleep(300);
    d._click(Clutter.BUTTON_PRIMARY);
    await d._sleep(500);
    log(`plain click: secondary ${indicator._secondary.isOpen}, popover ${indicator.menu.isOpen}`);
    indicator.menu.close();
    log('done');
}

/* --cycle: disable lands with the pet visible, agent timers armed, a run in flight, the popover open. */
async function hold(d, indicator, pet, settings) {
    settings.set_boolean('floating-pet-enabled', true);
    await leaveOverview(d);
    await call('Report', ['working', 'web', '']);
    await pointerTo(d, 300, 250);
    await call('Report', ['done', 'api', 'Tests pass']);
    log(`hold: ${petInfo(pet)}; display on ${pet._displayOn}`);
    for (;;) {
        if (Main.panel.statusArea[TARGET] !== indicator)
            return;
        if (!indicator.menu.isOpen)
            indicator.menu.open();
        await d._sleep(400);
    }
}

/* PTB_SHOT_SUITE=pet-prefs with --prefs poketokenbar@local: the Floating pet
 * group, off then switched on (the prefs process has its own memory GSettings). */
export async function runPetPrefs(d) {
    await d._sleep(9000);
    const prefs = global.get_window_actors().map(a => a.meta_window)
        .find(w => (w.get_title() ?? '').length > 0 && w.get_wm_class() !== 'gnome-shell');
    Main.overview.hide();
    await d._sleep(2000);
    if (!prefs) {
        console.error('ptb-shot: no prefs window');
        return;
    }
    prefs.activate(global.get_current_time());
    await d._sleep(1500);
    const r = prefs.get_frame_rect();
    const area = {x: r.x, y: r.y, width: r.width, height: r.height};
    d._movePointer(r.x + r.width / 2, r.y + r.height / 2);
    await d._sleep(300);
    d._movePointer(r.x + r.width / 2 + 1, r.y + r.height / 2);
    await d._sleep(300);
    Main.overview.hide();
    await d._sleep(1200);
    const notches = Number(GLib.getenv('PTB_SHOT_PREFS_NOTCHES') ?? 6);
    for (let i = 0; i < notches; i++) {
        d._pointer.notify_discrete_scroll(GLib.get_monotonic_time(), Clutter.ScrollDirection.DOWN,
            Clutter.ScrollSource.WHEEL);
        await d._sleep(60);
    }
    await d._sleep(800);
    log(`prefs ${r.x},${r.y} ${r.width}x${r.height}`);
    await d._shot('prefs-pet-off.png', area);
    const click = (GLib.getenv('PTB_SHOT_PREFS_CLICK') ?? '').split(',').map(Number);
    if (click.length === 2 && click.every(Number.isFinite)) {
        await pointerTo(d, r.x + click[0] - 3, r.y + click[1]);
        await pointerTo(d, r.x + click[0], r.y + click[1]);
        d._click(Clutter.BUTTON_PRIMARY);
        await d._sleep(1000);
        await d._shot('prefs-pet-on.png', area);
    }
    log('done');
}
