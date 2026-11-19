/* Recap & heatmap screenshot suite for the sandbox driver (PTB_SHOT_SUITE=recap).
 *
 * Drives the Activity panel click-cycle through the virtual pointer and logs
 * what each click changed, so the log proves the panel's click target leaves
 * the period picker and the bar tooltip alone:
 *   - a click on the period button changes daily-trend-window only;
 *   - a click on the chart, on the caption gap or on the dots flips activity-view;
 *   - a bar hovered for 800 ms still shows its day tooltip, which hides on leave;
 *   - an empty heatmap (tokens zeroed on the rendered state) keeps its caption
 *     and still cycles back to the bars.
 * Then it opens the recap (week, previous week, month, year), goes Back, checks
 * that a reopen lands on Home, and repeats the key shots in the light scheme.
 *
 * With PTB_SHOT_RECAP_HOLD=1 it only opens the recap and keeps it open, so a
 * headless --cycle disable lands with the recap showing.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const TARGET = 'poketokenbar@local';

function log(msg) {
    console.log(`ptb-shot: recap: ${msg}`);
}

async function leaveOverview(d) {
    if (Main.overview.visible) {
        Main.overview.hide();
        await d._sleep(2500);
    }
}

async function openMenu(d, indicator) {
    indicator.menu.open();
    await d._sleep(1200);
    const [mx, my] = indicator.menu.actor.get_transformed_position();
    d._movePointer(mx + 40, my + 300); // warm-up motion
    await d._sleep(300);
}

/** Click at stage (x, y) after making sure the pointer is there. */
async function clickAt(d, x, y) {
    for (let i = 0; i < 4; i++) {
        d._movePointer(x, y);
        await d._sleep(150);
        const [px, py] = global.get_pointer();
        if (Math.abs(px - x) <= 2 && Math.abs(py - y) <= 2)
            break;
    }
    d._click(Clutter.BUTTON_PRIMARY);
    await d._sleep(400);
}

function center(actor) {
    const [x, y] = actor.get_transformed_position();
    const [w, h] = actor.get_transformed_size();
    return [x + w / 2, y + h / 2];
}

/** Scroll Home so `actor` is in view. */
async function reveal(d, popover, actor) {
    const adj = popover._scroll.vadjustment;
    const [, ay] = actor.get_transformed_position();
    const [, sy] = popover._scroll.get_transformed_position();
    adj.value = Math.max(0, Math.min(adj.upper - adj.page_size, adj.value + (ay - sy) - 160));
    await d._sleep(400);
}

export async function runRecap(d, startDelayMs) {
    const ptb = Extension.lookupByUUID(TARGET);
    await d._sleep(startDelayMs);
    const indicator = Main.panel.statusArea[TARGET];
    if (!indicator || !ptb) {
        console.error('ptb-shot: PokeTokenBar indicator not found');
        return;
    }
    d._movePointer(10, 400);
    const settings = ptb.getSettings();
    const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
    const prefix = GLib.getenv('PTB_SHOT_PREFIX') ?? '';
    const popover = indicator._popover;
    const usage = popover._usage;
    const panel = usage._activity;
    const tooltip = ptb.tooltip;
    await leaveOverview(d);

    if (GLib.getenv('PTB_SHOT_RECAP_HOLD') === '1') {
        // Keep the recap open on the year view until the --cycle disable lands.
        for (;;) {
            if (Main.panel.statusArea[TARGET] !== indicator)
                return;
            if (!indicator.menu.isOpen) {
                await openMenu(d, indicator);
                popover.openRecap();
                await d._sleep(500);
                popover._recap._setScope('year');
                log(`hold: recap open ${popover._recap.isOpen}`);
            }
            await d._sleep(500);
        }
    }

    settings.set_string('activity-view', 'bars');
    settings.set_string('daily-trend-window', '14d');
    await openMenu(d, indicator);
    await reveal(d, popover, panel.actor);
    await d._shot(`${prefix}recap-home-bars-dark.png`, d._menuArea(indicator));

    // 1. The period button keeps its own click.
    await clickAt(d, ...center(panel._windowButton));
    log(`after window button: daily-trend-window=${settings.get_string('daily-trend-window')} ` +
        `activity-view=${settings.get_string('activity-view')} (want 21d, bars)`);

    // 2. The bar tooltip survives the panel's hint: hover a bar for 800 ms.
    const chart = panel._chart;
    const [cx, cy] = chart.get_transformed_position();
    const [cw] = chart.get_transformed_size();
    d._movePointer(cx + cw * 0.5, cy + 12);
    await d._sleep(200);
    d._movePointer(cx + cw * 0.52, cy + 13);
    await d._sleep(800);
    log(`bar hover: column ${chart._hovered}, tooltip visible=${tooltip.actor.visible} ` +
        `text='${tooltip.actor.text}' owner is chart=${tooltip._owner === chart}`);
    await d._shot(`${prefix}recap-bar-hover-dark.png`, d._menuArea(indicator));
    d._movePointer(cx + cw * 0.5, cy - 60); // off the chart, above the caption
    await d._sleep(300);
    log(`bar leave: tooltip visible=${tooltip.actor.visible} text='${tooltip.actor.text}'`);

    // 3. A click on the chart body flips to the heatmap.
    await clickAt(d, cx + cw * 0.3, cy + 10);
    log(`after chart click: activity-view=${settings.get_string('activity-view')} (want heatmap)`);
    await d._sleep(600);
    await d._shot(`${prefix}recap-heatmap-dark.png`, d._menuArea(indicator));

    // 4. Hover a heatmap cell: the readout follows the pointer, then falls back to today.
    const heat = panel._heatmap;
    const [hx, hy] = heat.get_transformed_position();
    const [hw] = heat.get_transformed_size();
    const hit = (col, row) => {
        const w = (hw - 14 - 30) / 16;
        return [hx + 14 + col * (w + 2) + w / 2, hy + row * 12 + 5];
    };
    log(`heatmap readout (today) '${panel._readout.text}'`);
    d._movePointer(...hit(10, 4)); // 2026-10-16 on the state-active grid
    await d._sleep(200);
    d._movePointer(...hit(10, 4).map(v => v + 1));
    await d._sleep(500);
    log(`heatmap hover: date=${heat.hoveredDate} readout='${panel._readout.text}' ` +
        `tooltip visible=${tooltip.actor.visible}`);
    await d._shot(`${prefix}recap-heatmap-hover-dark.png`, d._menuArea(indicator));
    d._movePointer(...hit(0, 0)); // outside range (before first): readout back to today
    await d._sleep(400);
    log(`heatmap outside cell: date=${heat.hoveredDate} readout='${panel._readout.text}'`);

    // 5. The caption gap and the dots cycle too.
    const [gx, gy] = center(panel._peakCaption.visible ? panel._peakCaption : panel._readout);
    await clickAt(d, gx - 30, gy);
    log(`after caption-gap click: activity-view=${settings.get_string('activity-view')} (want bars)`);
    await clickAt(d, ...center(panel._dotsButton));
    log(`after dots click: activity-view=${settings.get_string('activity-view')} (want heatmap)`);

    // 6. The empty-heatmap trap: zero the grid on the rendered state.
    const ctx = ptb.context();
    const empty = JSON.parse(JSON.stringify(ctx.state));
    empty.usage.combined.heatmap.tokens = empty.usage.combined.heatmap.tokens.map(() => 0);
    popover.update({...ctx, state: empty});
    await d._sleep(500);
    log(`empty heatmap: grid visible=${heat.visible} caption mapped=${panel._heatTitle.mapped} ` +
        `dots mapped=${panel._dotsButton.mapped}`);
    await d._shot(`${prefix}recap-heatmap-empty-dark.png`, d._menuArea(indicator));
    await clickAt(d, ...center(panel._heatTitle));
    log(`after click on the empty heatmap: activity-view=${settings.get_string('activity-view')} (want bars)`);
    settings.set_string('activity-view', 'heatmap');
    await d._sleep(400);

    // 7. The recap: Details, week, previous week, month, year, Back.
    const adj = popover._scroll.vadjustment;
    adj.value = 0;
    await d._sleep(300);
    await clickAt(d, ...center(usage._details));
    const recap = popover._recap;
    log(`after Details: recap open=${recap.isOpen} scroll visible=${popover._scroll.visible}`);
    await d._sleep(600);
    await d._shot(`${prefix}recap-week-dark.png`, d._menuArea(indicator));

    const meters = recap._meters;
    const [mx, my] = meters.get_transformed_position();
    const [mw] = meters.get_transformed_size();
    d._movePointer(mx + mw * 0.3, my + 20);
    await d._sleep(200);
    d._movePointer(mx + mw * 0.31, my + 21);
    await d._sleep(500);
    log(`meter hover: tooltip '${tooltip.actor.visible ? tooltip.actor.text : ''}'`);
    await d._shot(`${prefix}recap-week-meter-hover-dark.png`, d._menuArea(indicator));

    await clickAt(d, ...center(recap._prev));
    await d._sleep(500);
    log(`previous: offset=${recap._offset} period='${recap._period.text}' next sensitive=${recap._next.reactive}`);
    await d._shot(`${prefix}recap-week-prev-dark.png`, d._menuArea(indicator));
    await clickAt(d, ...center(recap._current));
    log(`this week: offset=${recap._offset}`);

    for (const scope of ['month', 'year']) {
        await clickAt(d, ...center(recap._scopes._buttons.get(scope)));
        await d._sleep(500);
        log(`${scope}: offset=${recap._offset} period='${recap._period.text}' total='${recap._total.text}' ` +
            `chips=${recap._chips.get_n_children()} prev sensitive=${recap._prev.reactive}`);
        await d._shot(`${prefix}recap-${scope}-dark.png`, d._menuArea(indicator));
    }

    const back = recap.actor.get_first_child().get_first_child();
    await clickAt(d, ...center(back));
    log(`after Back: recap open=${recap.isOpen} scroll visible=${popover._scroll.visible} ` +
        `tabs visible=${popover.actor.get_first_child().visible}`);

    // 8. A reopen always lands on Home.
    popover.openRecap();
    indicator.menu.close();
    await d._sleep(600);
    await openMenu(d, indicator);
    log(`after reopen: recap open=${recap.isOpen} scroll visible=${popover._scroll.visible}`);

    // 9. Light scheme: heatmap and recap.
    iface.set_string('color-scheme', 'prefer-light');
    await d._sleep(1500);
    await reveal(d, popover, panel.actor);
    await d._shot(`${prefix}recap-heatmap-light.png`, d._menuArea(indicator));
    settings.set_string('activity-view', 'bars');
    await d._sleep(500);
    await d._shot(`${prefix}recap-home-bars-light.png`, d._menuArea(indicator));
    popover.openRecap();
    await d._sleep(800);
    await d._shot(`${prefix}recap-week-light.png`, d._menuArea(indicator));
    recap._setScope('year');
    await d._sleep(600);
    await d._shot(`${prefix}recap-year-light.png`, d._menuArea(indicator));
    indicator.menu.close();
    iface.reset('color-scheme');
    log('done');
}
