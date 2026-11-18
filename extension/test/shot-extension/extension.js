/* Sandbox-only screenshot driver for PokeTokenBar (uuid ptb-shot@local).
 *
 * Loaded next to poketokenbar@local by scripts/headless-shell.sh. It opens
 * the PokeTokenBar menus, flips settings and the color scheme (the sandbox
 * uses the memory GSettings backend), moves a virtual pointer over the chart
 * and the panel button, and captures PNGs with the in-process Shell.Screenshot
 * API into $PTB_STATE_DIR/shots/. Never install it in a real session.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Shell from 'gi://Shell';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

Gio._promisify(Shell.Screenshot.prototype, 'screenshot');
Gio._promisify(Shell.Screenshot.prototype, 'screenshot_area');

const TARGET = 'poketokenbar@local';
const START_DELAY_MS = Number(GLib.getenv('PTB_SHOT_DELAY_MS') ?? 4000);

export default class ShotExtension extends Extension {
    enable() {
        this._sources = new Set();
        this._cancelled = false;
        this._startedAt = GLib.get_monotonic_time();
        this._dir = GLib.build_filenamev([GLib.getenv('PTB_STATE_DIR') ?? GLib.get_tmp_dir(), 'shots']);
        GLib.mkdir_with_parents(this._dir, 0o755);
        this._run().catch(e => console.error(`ptb-shot: ${e}\n${e.stack}`));
    }

    disable() {
        this._cancelled = true;
        for (const id of this._sources)
            GLib.source_remove(id);
        this._sources.clear();
        this._pointer = null;
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
        const chart = indicator._popover?._usage?._chart;
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
}
