/* Sandbox-only screenshot driver for PokeTokenBar (uuid ptb-shot@local).
 *
 * Loaded next to poketokenbar@local by scripts/headless-shell.sh. It opens
 * the PokeTokenBar menus, flips settings and the color scheme (the sandbox
 * uses the memory GSettings backend), drives a virtual pointer and keyboard,
 * and captures PNGs with the in-process Shell.Screenshot API into
 * $PTB_STATE_DIR/shots/. Never install it in a real session.
 *
 * PTB_SHOT_SUITE selects what runs:
 *   home  (default) the Phase 6 home, chart, panel and right-click shots;
 *   dex   the Pokédex: search typing, wheel paging, detail, catch log, light;
 *   recap the Activity click-cycle, heatmap and usage recap (recapSuite.js);
 *   cycle opens the Pokédex on a species whose getSpecies the stub delays and
 *         leaves the popover open, so the headless --cycle disable lands with
 *         the menu open and a command pending (the screen-lock case);
 *   shop, shop-cycle, shop-prefs  the Shop and Bag tabs (shopSuite.js);
 *   pet, pet-prefs  the floating pet, its bubbles, the agent D-Bus object and
 *         the Floating pet prefs group (petSuite.js);
 *   pet-pray, pet-reveal  prayers over the egg pet and the Ditto reveal's card
 *         effect (praySuite.js);
 *   backup, backup-prefs  the Save1 relay on the sandbox bus and the Backup &
 *         Transfer prefs page (backupSuite.js).
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Shell from 'gi://Shell';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

import {runBackup, runBackupPrefs} from './backupSuite.js';
import {runPet, runPetPrefs} from './petSuite.js';
import {runPetPray, runPetReveal} from './praySuite.js';
import {runRecap} from './recapSuite.js';
import {runShop, runShopCycle, runShopPrefs} from './shopSuite.js';

Gio._promisify(Shell.Screenshot.prototype, 'screenshot');
Gio._promisify(Shell.Screenshot.prototype, 'screenshot_area');

const TARGET = 'poketokenbar@local';
const START_DELAY_MS = Number(GLib.getenv('PTB_SHOT_DELAY_MS') ?? 4000);

export default class ShotExtension extends Extension {
    enable() {
        this._loggedOpen = false;
        this._sources = new Set();
        this._cancelled = false;
        this._startedAt = GLib.get_monotonic_time();
        this._dir = GLib.build_filenamev([GLib.getenv('PTB_STATE_DIR') ?? GLib.get_tmp_dir(), 'shots']);
        GLib.mkdir_with_parents(this._dir, 0o755);
        // A disable cancels the pending sleeps; that is expected, not an error.
        this._run().catch(e => {
            if (!this._cancelled)
                console.error(`ptb-shot: ${e}\n${e.stack}`);
        });
    }

    disable() {
        this._cancelled = true;
        for (const id of this._sources)
            GLib.source_remove(id);
        this._sources.clear();
        this._pointer = null;
        this._keyboard = null;
    }

    _sleep(ms) {
        return new Promise((resolve, reject) => {
            const id = GLib.timeout_add(GLib.PRIORITY_DEFAULT, ms, () => {
                this._sources.delete(id);
                if (this._cancelled)
                    reject(new Error('cancelled'));
                else
                    resolve();
                return GLib.SOURCE_REMOVE;
            });
            this._sources.add(id);
        });
    }

    async _shot(name, area = null) {
        const path = GLib.build_filenamev([this._dir, name]);
        const stream = Gio.File.new_for_path(path).replace(null, false, Gio.FileCreateFlags.NONE, null);
        const shooter = new Shell.Screenshot();
        if (area)
            await shooter.screenshot_area(area.x, area.y, area.width, area.height, stream);
        else
            await shooter.screenshot(false, stream);
        stream.close(null);
        console.log(`ptb-shot: wrote ${path}`);
    }

    _movePointer(x, y) {
        if (!this._pointer) {
            const seat = global.stage.context.get_backend().get_default_seat();
            this._pointer = seat.create_virtual_device(Clutter.InputDeviceType.POINTER_DEVICE);
        }
        this._pointer.notify_absolute_motion(GLib.get_monotonic_time(), x, y);
    }

    _click(button) {
        const t = GLib.get_monotonic_time();
        this._pointer.notify_button(t, button, Clutter.ButtonState.PRESSED);
        this._pointer.notify_button(t + 1000, button, Clutter.ButtonState.RELEASED);
    }

    _menuArea(indicator) {
        const actor = indicator.menu.actor;
        const [x, y] = actor.get_transformed_position();
        const [w, h] = actor.get_transformed_size();
        return {x: Math.max(0, Math.floor(x) - 8), y: 0, width: Math.ceil(w) + 16, height: Math.ceil(y + h) + 8};
    }

    _panelArea() {
        const box = Main.panel._rightBox;
        const [x] = box.get_transformed_position();
        const [w, h] = box.get_transformed_size();
        return {x: Math.max(0, Math.floor(x) - 4), y: 0, width: Math.ceil(w) + 8, height: Math.ceil(h)};
    }

    async _run() {
        const suite = GLib.getenv('PTB_SHOT_SUITE') ?? 'home';
        if (suite === 'dex')
            return this._runDex();
        if (suite === 'cycle')
            return this._runCycle();
        if (suite === 'recap')
            return runRecap(this, START_DELAY_MS);
        if (suite === 'pet')
            return runPet(this);
        if (suite === 'pet-prefs')
            return runPetPrefs(this);
        if (suite === 'pet-pray')
            return runPetPray(this);
        if (suite === 'pet-reveal')
            return runPetReveal(this);
        if (suite === 'shop')
            return runShop(this);
        if (suite === 'shop-cycle')
            return runShopCycle(this);
        if (suite === 'shop-prefs')
            return runShopPrefs(this);
        if (suite === 'backup')
            return runBackup(this);
        if (suite === 'backup-prefs')
            return runBackupPrefs(this);
        return this._runHome();
    }

    async _runHome() {
        const ptb = Extension.lookupByUUID(TARGET);
        await this._sleep(START_DELAY_MS);
        const indicator = Main.panel.statusArea[TARGET];
        if (!indicator || !ptb) {
            console.error('ptb-shot: PokeTokenBar indicator not found');
            return;
        }
        this._movePointer(10, 400); // create the virtual pointer early; its first event only enters
        const settings = ptb.getSettings();
        const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
        const prefix = GLib.getenv('PTB_SHOT_PREFIX') ?? '';

        // 1. Defaults, dark: panel + popover.
        await this._shot(`${prefix}panel-dark-default.png`, this._panelArea());
        indicator.menu.open();
        await this._sleep(1500);
        await this._shot(`${prefix}popover-dark.png`, this._menuArea(indicator));

        // 1b. Scrolled to the limits section.
        const scroll = indicator._popover?._scroll;
        if (scroll) {
            const adj = scroll.vadjustment;
            adj.value = adj.upper - adj.page_size;
            await this._sleep(500);
            await this._shot(`${prefix}popover-dark-limits.png`, this._menuArea(indicator));
            adj.value = 0;
            await this._sleep(300);
        }

        // 2. Hover the chart: today's bar is the last column.
        const chart = indicator._popover?._usage?._activity?._chart;
        if (chart?.mapped) {
            let probeMotions = 0;
            let probeXY = '';
            const probe = new Clutter.MotionController();
            probe.connect('motion', (_c, _s, x, y) => {
                probeMotions++;
                probeXY = `${x},${y}`;
            });
            chart.add_action(probe);
            const [cx, cy] = chart.get_transformed_position();
            const [cw] = chart.get_transformed_size();
            this._movePointer(cx + cw * 0.6, cy + 14);
            await this._sleep(300);
            this._movePointer(cx + cw * 0.62, cy + 15);
            await this._sleep(800);
            const [px, py] = global.get_pointer();
            const under = global.stage.get_actor_at_pos(Clutter.PickMode.REACTIVE, px, py);
            console.log(`ptb-shot: chart at ${cx},${cy} w=${cw}; pointer ${px},${py}; ` +
                `actor under pointer ${under?.constructor?.name} ${under?.style_class ?? ''}; ` +
                `hovered column ${chart._hovered}; probe motions ${probeMotions} last at ${probeXY}`);
            chart.remove_action(probe);
            if (chart._hovered < 0) {
                // The virtual pointer did not deliver motion: drive the same handler directly.
                chart._onMotion(px - cx, py - cy);
                await this._sleep(300);
                console.log(`ptb-shot: direct _onMotion → hovered column ${chart._hovered}`);
            }
            await this._shot(`${prefix}chart-hover-dark.png`, this._menuArea(indicator));
        }
        indicator.menu.close();
        await this._sleep(600);

        // 3. Two-line panel with cost, growth and the colored limit item.
        settings.set_boolean('show-cost-in-menu', true);
        settings.set_boolean('show-limit-in-menu', true);
        await this._sleep(800);
        await this._shot(`${prefix}panel-dark-limit.png`, this._panelArea());
        settings.set_boolean('show-cost-in-menu', false);
        settings.set_boolean('show-growth-in-menu', true);
        await this._sleep(800);
        await this._shot(`${prefix}panel-dark-growth-limit.png`, this._panelArea());

        // 4. Light scheme, remaining mode, month chart.
        iface.set_string('color-scheme', 'prefer-light');
        settings.set_string('limit-display-mode', 'remaining');
        settings.set_string('daily-trend-window', 'month');
        await this._sleep(1500);
        await this._shot(`${prefix}panel-light.png`, this._panelArea());
        const fg = indicator.get_theme_node().get_foreground_color();
        console.log(`ptb-shot: panel fg ${fg.red},${fg.green},${fg.blue}; lines class ` +
            `'${indicator._lines.get_style_class_name()}'; overview ${Main.overview.visible}`);
        indicator.menu.open();
        await this._sleep(1500);
        await this._shot(`${prefix}popover-light.png`, this._menuArea(indicator));
        indicator.menu.close();
        await this._sleep(600);

        // 5. Right click through the virtual pointer: the secondary menu.
        const [ix, iy] = indicator.get_transformed_position();
        const [iw, ih] = indicator.get_transformed_size();
        this._movePointer(ix + iw / 2, iy + ih / 2);
        await this._sleep(300);
        this._click(Clutter.BUTTON_SECONDARY);
        await this._sleep(1200);
        const secondary = indicator._secondary;
        console.log(`ptb-shot: secondary open=${secondary?.isOpen} main open=${indicator.menu.isOpen}`);
        if (secondary)
            await this._shot(`${prefix}secondary-light.png`, this._menuArea({menu: secondary}));
        secondary?.close();

        iface.reset('color-scheme');

        // Optional: a full-screen shot once the stub's events have produced banners.
        const bannerAt = Number(GLib.getenv('PTB_SHOT_BANNER_AT_MS') ?? 0);
        if (bannerAt > 0) {
            const elapsed = (GLib.get_monotonic_time() - this._startedAt) / 1000;
            await this._sleep(Math.max(100, bannerAt - elapsed));
            await this._shot(`${prefix}banner.png`);
            console.log(`ptb-shot: tray sources ${Main.messageTray.getSources().map(s => `${s.title}:${s.count}`).join(', ')}`);
        }
        console.log('ptb-shot: done');
    }

    /* ---- Pokédex suite ------------------------------------------------- */

    _key(keyval) {
        if (!this._keyboard) {
            const seat = global.stage.context.get_backend().get_default_seat();
            this._keyboard = seat.create_virtual_device(Clutter.InputDeviceType.KEYBOARD_DEVICE);
        }
        const t = GLib.get_monotonic_time();
        this._keyboard.notify_keyval(t, keyval, Clutter.KeyState.PRESSED);
        this._keyboard.notify_keyval(t + 1000, keyval, Clutter.KeyState.RELEASED);
    }

    async _type(text) {
        for (const ch of text) {
            this._key(Clutter.unicode_to_keysym(ch.codePointAt(0)));
            await this._sleep(60);
        }
    }

    async _clickActor(actor, button = Clutter.BUTTON_PRIMARY) {
        const [x, y] = actor.get_transformed_position();
        const [w, h] = actor.get_transformed_size();
        // A virtual motion is occasionally clamped (seen once at y = 0 right
        // after the overview closed): re-issue it until the pointer is there.
        let [px, py] = [-1, -1];
        for (let i = 0; i < 4; i++) {
            this._movePointer(x + w / 2, y + h / 2);
            await this._sleep(150);
            [px, py] = global.get_pointer();
            if (Math.abs(px - (x + w / 2)) <= 2 && Math.abs(py - (y + h / 2)) <= 2)
                break;
        }
        const under = global.stage.get_actor_at_pos(Clutter.PickMode.REACTIVE, px, py);
        console.log(`ptb-shot: click ${actor.constructor.name}.${actor.style_class ?? ''} at ${Math.round(x + w / 2)},` +
            `${Math.round(y + h / 2)} (pointer ${px},${py}; under ${under?.constructor?.name}.${under?.style_class ?? ''})`);
        this._click(button);
        await this._sleep(250);
    }

    _focusInfo(entry) {
        const focus = global.stage.get_key_focus();
        return `focus ${focus === entry.clutter_text ? 'entry' : focus?.constructor?.name ?? 'none'}`;
    }

    async _openDexTab(indicator) {
        // The headless shell starts in the overview, which owns key focus and
        // closes menus as it animates out: leave it first.
        if (Main.overview.visible) {
            Main.overview.hide();
            await this._sleep(2500);
        }
        indicator._openTab = 'dex'; // the right-click "Pokédex" path
        indicator.menu.open();
        await this._sleep(1500);
        // Warm-up: the first virtual motion after the overview can be dropped.
        const [mx, my] = indicator.menu.actor.get_transformed_position();
        this._movePointer(mx + 40, my + 300);
        await this._sleep(300);
        console.log(`ptb-shot: menu open ${indicator.menu.isOpen}, overview ${Main.overview.visible}`);
        return indicator._popover._dex;
    }

    async _runDex() {
        const ptb = Extension.lookupByUUID(TARGET);
        await this._sleep(START_DELAY_MS);
        const indicator = Main.panel.statusArea[TARGET];
        if (!indicator || !ptb) {
            console.error('ptb-shot: PokeTokenBar indicator not found');
            return;
        }
        this._movePointer(10, 400);
        const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
        const prefix = GLib.getenv('PTB_SHOT_PREFIX') ?? '';
        const area = () => this._menuArea(indicator);

        // 1. The grid, opened from the right-click path.
        const dex = await this._openDexTab(indicator);
        console.log(`ptb-shot: dex page ${dex._gridPage}/${dex._gridData?.pageCount} total ${dex._gridData?.total}`);
        await this._shot(`${prefix}dex-grid-dark.png`, area());

        // 2. Typing in the search field inside the open menu.
        const entry = dex._search;
        await this._clickActor(entry);
        if (!indicator.menu.isOpen) {
            // Harness flake (first virtual click after the overview): log it and retry once.
            console.log('ptb-shot: first click closed the menu; reopening and retrying');
            indicator._openTab = 'dex';
            indicator.menu.open();
            await this._sleep(1200);
            await this._clickActor(entry);
        }
        console.log(`ptb-shot: after click: ${this._focusInfo(entry)}, menu open ${indicator.menu.isOpen}`);
        await this._type('pi');
        await this._sleep(700);
        console.log(`ptb-shot: typed 'pi' → text '${entry.get_text()}', ${this._focusInfo(entry)}, ` +
            `menu open ${indicator.menu.isOpen}, total ${dex._gridData?.total}`);
        await this._shot(`${prefix}dex-search-dark.png`, area());
        for (const [name, sym] of [['Left', Clutter.KEY_Left], ['Left', Clutter.KEY_Left], ['Left', Clutter.KEY_Left],
            ['Right', Clutter.KEY_Right], ['Right', Clutter.KEY_Right], ['Right', Clutter.KEY_Right],
            ['space', Clutter.KEY_space], ['Return', Clutter.KEY_Return]]) {
            this._key(sym);
            await this._sleep(120);
            console.log(`ptb-shot: key ${name}: text '${entry.get_text()}' cursor ${entry.clutter_text.cursor_position}, ` +
                `${this._focusInfo(entry)}, menu open ${indicator.menu.isOpen}`);
        }
        for (let i = 0; i < 4; i++) {
            this._key(Clutter.KEY_BackSpace);
            await this._sleep(80);
        }
        await this._sleep(600);
        console.log(`ptb-shot: after BackSpace: text '${entry.get_text()}', ${this._focusInfo(entry)}, ` +
            `menu open ${indicator.menu.isOpen}, total ${dex._gridData?.total}`);

        // 3. Wheel and trackpad paging over the grid.
        const grid = dex._grid._grid;
        const seen = [];
        const probeId = grid.connect('captured-event', (_a, ev) => {
            if (ev.type() === Clutter.EventType.SCROLL) {
                const [dx, dy] = ev.get_scroll_direction() === Clutter.ScrollDirection.SMOOTH ? ev.get_scroll_delta() : [0, 0];
                seen.push(`dir=${ev.get_scroll_direction()} src=${ev.get_scroll_source()} emu=${ev.is_pointer_emulated()} ` +
                    `d=${dx.toFixed(2)},${dy.toFixed(2)} fin=${ev.get_scroll_finish_flags()}`);
            }
            return Clutter.EVENT_PROPAGATE;
        });
        const [gx, gy] = grid.get_transformed_position();
        const [gw, gh] = grid.get_transformed_size();
        this._movePointer(gx + gw / 2, gy + gh / 2);
        await this._sleep(300);
        const page = () => dex._gridPage;
        const p0 = page();
        this._pointer.notify_discrete_scroll(GLib.get_monotonic_time(), Clutter.ScrollDirection.DOWN,
            Clutter.ScrollSource.WHEEL);
        await this._sleep(500);
        const p1 = page();
        console.log(`ptb-shot: wheel notch down: page ${p0} → ${p1}; events: ${seen.join(' | ')}`);
        seen.length = 0;
        await this._shot(`${prefix}dex-grid-page2-dark.png`, area());
        // Virtual continuous scroll is in pixels; Mutter reports px / 10 to
        // actors (measured: 0.6 → 0.06). 8 × 6 px = 4.8 units: one page, latched.
        for (let i = 0; i < 8; i++) {
            this._pointer.notify_scroll_continuous(GLib.get_monotonic_time(), 0, 6, Clutter.ScrollSource.FINGER,
                Clutter.ScrollFinishFlags.NONE);
            await this._sleep(16);
        }
        this._pointer.notify_scroll_continuous(GLib.get_monotonic_time(), 0, 0, Clutter.ScrollSource.FINGER,
            Clutter.ScrollFinishFlags.VERTICAL);
        await this._sleep(500);
        const p2 = page();
        console.log(`ptb-shot: trackpad gesture (8 × 6 px down): page ${p1} → ${p2} (one page expected); ` +
            `events: ${seen.length} first ${seen[0] ?? '-'} last ${seen[seen.length - 1] ?? '-'}`);
        seen.length = 0;
        this._pointer.notify_discrete_scroll(GLib.get_monotonic_time(), Clutter.ScrollDirection.UP,
            Clutter.ScrollSource.WHEEL);
        await this._sleep(200);
        this._pointer.notify_discrete_scroll(GLib.get_monotonic_time(), Clutter.ScrollDirection.UP,
            Clutter.ScrollSource.WHEEL);
        await this._sleep(500);
        console.log(`ptb-shot: two notches up: page ${p2} → ${page()}; popover scroll ${indicator._popover._scroll.vadjustment.value}`);
        grid.disconnect(probeId);

        // 4. Search a number, open the cell with a real click, then Back.
        await this._clickActor(entry);
        await this._type('423');
        await this._sleep(800);
        console.log(`ptb-shot: '423' → ${dex._gridData?.total} result(s)`);
        await this._shot(`${prefix}dex-forms-cell-dark.png`, area()); // "Gastrodon 1/2"
        await this._clickActor(dex._grid._cells[0].actor);
        await this._sleep(1500);
        const detail = dex._detail;
        console.log(`ptb-shot: detail open ${Boolean(detail)} species ${detail?._speciesId} form ${detail?._form} ` +
            `individual ${detail?._data?.individuals?.[0]?.id ?? '-'}`);
        await this._shot(`${prefix}dex-detail-dark.png`, area());
        if (detail) {
            const adj = detail._scroll.vadjustment;
            adj.value = Math.min(adj.upper - adj.page_size, 330);
            await this._sleep(400);
            await this._shot(`${prefix}dex-detail-dark-2.png`, area());
            adj.value = adj.upper - adj.page_size;
            await this._sleep(400);
            await this._shot(`${prefix}dex-detail-dark-3.png`, area());
            const back = detail.actor.get_child_at_index(0).get_child_at_index(0);
            await this._clickActor(back);
            await this._sleep(800);
        }
        console.log(`ptb-shot: after Back: detail ${Boolean(dex._detail)}, page ${dex._gridPage}, ` +
            `search '${entry.get_text()}'`);
        entry.set_text('');
        await this._sleep(700);
        // Back returns to the page it came from: page 2, cell 6.
        this._movePointer(gx + gw / 2, gy + gh / 2);
        await this._sleep(200);
        this._pointer.notify_discrete_scroll(GLib.get_monotonic_time(), Clutter.ScrollDirection.DOWN,
            Clutter.ScrollSource.WHEEL);
        await this._sleep(600);
        const fromPage = dex._gridPage;
        const picked = dex._grid._cells[5]._cell?.speciesId;
        await this._clickActor(dex._grid._cells[5].actor);
        await this._sleep(1200);
        const opened = dex._detail?._speciesId;
        const back2 = dex._detail?.actor.get_child_at_index(0).get_child_at_index(0);
        if (back2)
            await this._clickActor(back2);
        await this._sleep(900);
        console.log(`ptb-shot: page-${fromPage} cell #${picked} → detail ${opened} → Back → page ${dex._gridPage} ` +
            `(${dex._gridPage === fromPage ? 'kept' : 'LOST'})`);

        // 5. Sort card, then the catch log.
        await this._clickActor(dex._sortButton);
        await this._sleep(300);
        await this._shot(`${prefix}dex-sort-dark.png`, area());
        await this._clickActor(dex._sortButton);
        const logButton = dex._segmented._buttons.get('log');
        await this._clickActor(logButton);
        await this._sleep(1200);
        console.log(`ptb-shot: log total ${dex._logData?.total} page ${dex._logPage}`);
        await this._shot(`${prefix}dex-log-dark.png`, area());
        // A lower row: Back must scroll the log back to it.
        const low = (dex._logData?.entries ?? [])[8];
        if (low) {
            dex._openFromLog(low, low.chain[low.chain.length - 1]);
            await this._sleep(1000);
            dex._detail?._deps.onBack();
            await this._sleep(900);
            const adj = dex._log._scroll.vadjustment;
            console.log(`ptb-shot: log row 9 (${low.id}) → detail → Back: scroll ${Math.round(adj.value)} of ` +
                `${Math.round(adj.upper - adj.page_size)} (${adj.value > 0 ? 'revealed' : 'NOT revealed'})`);
            await this._shot(`${prefix}dex-log-revealed-dark.png`, area());
        }
        // Open Gastrodon from its log row, then Back to the row.
        const gastro = (dex._logData?.entries ?? []).find(e => e.speciesId === 423);
        if (gastro) {
            dex._openFromLog(gastro, gastro.chain[gastro.chain.length - 1]);
            await this._sleep(1200);
            console.log(`ptb-shot: from log: species ${dex._detail?._speciesId} form ${dex._detail?._form} ` +
                `evo line ${dex._chains.has(gastro.id)}`);
            dex._detail?._deps.onBack();
            await this._sleep(800);
            console.log(`ptb-shot: back to log: segment ${dex._segment} page ${dex._logPage}`);
        }

        // 6. Representative: Butterfree is pinned in the fixture; follow the companion again.
        dex.openSpecies(12);
        await this._sleep(1200);
        await this._shot(`${prefix}dex-detail-pinned-dark.png`, area());
        const repBefore = JSON.stringify(ptb.context().state?.companion?.representative);
        dex._detail?._toggleRepresentative(true);
        await this._sleep(800);
        console.log(`ptb-shot: representative ${repBefore} → ${JSON.stringify(ptb.context().state?.companion?.representative)}`);
        dex._detail?._deps.onBack();
        await this._sleep(600);

        // 7. Light scheme.
        iface.set_string('color-scheme', 'prefer-light');
        await this._sleep(1200);
        await this._clickActor(dex._segmented._buttons.get('grid'));
        await this._sleep(1000);
        await this._shot(`${prefix}dex-grid-light.png`, area());
        dex.openSpecies(423);
        await this._sleep(1200);
        await this._shot(`${prefix}dex-detail-light.png`, area());
        dex._detail?._deps.onBack();
        await this._clickActor(dex._segmented._buttons.get('log'));
        await this._sleep(1000);
        await this._shot(`${prefix}dex-log-light.png`, area());
        iface.reset('color-scheme');
        await this._sleep(800);

        // 8. Home: the companion sprite opens its detail.
        indicator._popover.showTab('home');
        await this._sleep(600);
        await this._clickActor(indicator._popover._companion._tile);
        await this._sleep(1200);
        console.log(`ptb-shot: home sprite → tab ${indicator._popover._tab}, detail ${dex._detail?._speciesId}`);
        await this._shot(`${prefix}dex-from-home-dark.png`, area());
        indicator.menu.close();
        console.log('ptb-shot: done');
    }

    /* ---- cycle suite: disable with the popover open ---------------------- */

    async _runCycle() {
        await this._sleep(START_DELAY_MS);
        const indicator = Main.panel.statusArea[TARGET];
        if (!indicator) {
            console.error('ptb-shot: PokeTokenBar indicator not found');
            return;
        }
        // Keep the popover open on a pending species detail until the disable
        // lands (the stub holds getSpecies); re-open if anything closes it.
        let reopened = 0;
        for (;;) {
            // PokeTokenBar is disabled a moment before this driver: stop then.
            if (Main.panel.statusArea[TARGET] !== indicator)
                return;
            if (!indicator.menu.isOpen) {
                const dex = await this._openDexTab(indicator);
                dex.openSpecies(25);
                if (reopened++ > 0)
                    console.log(`ptb-shot: cycle: menu re-opened (${reopened})`);
            }
            await this._sleep(400);
            const dex = indicator._popover?._dex;
            if (indicator.menu.isOpen && dex?._detail?._seq > 0 && !this._loggedOpen) {
                this._loggedOpen = true;
                console.log(`ptb-shot: cycle: menu open true, detail ${dex._detail._speciesId} pending`);
            }
        }
    }
}
