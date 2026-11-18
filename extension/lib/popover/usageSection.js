/* Usage totals and the daily chart (ui.md §2.1 C and §2.3):
 * today's big number with the grouped value and cost, this week / this month,
 * the streak badge, the token-type breakdown and the daily bar chart.
 */

import Clutter from 'gi://Clutter';
import St from 'gi://St';

import {_, fmt} from '../i18n.js';
import {compact, costText, grouped, normalizeCoverage} from '../format.js';
import * as ChartModel from '../chartModel.js';
import {DailyChart} from '../chart.js';
import * as V from '../viewModel.js';
import {hbox, label, spacer, vbox} from './widgets.js';

const PARTIAL_HINT = () => _('Some models have no known price; this cost leaves them out.');

export class UsageSection {
    /**
     * @param {object} deps  {tooltip, setTrendWindow(window)}
     */
    constructor(deps) {
        this._deps = deps;
        this.actor = vbox('ptb-usage', {x_expand: true});

        // Caption row: the streak badge sits at its right end, out of the
        // period table's way (the table needs the full 332 px).
        const head = hbox('ptb-usage-head', {x_expand: true});
        head.add_child(label(_('Today’s tokens'), 'ptb-caption'));
        head.add_child(spacer());
        this._streak = label('', 'ptb-streak', {y_align: Clutter.ActorAlign.CENTER});
        deps.tooltip.attach(this._streak, () => this._streakHint);
        head.add_child(this._streak);
        this.actor.add_child(head);

        const big = hbox('ptb-big-row', {x_expand: true});
        this._big = label('0', 'ptb-big-number', {y_align: Clutter.ActorAlign.END});
        this._grouped = label('', 'ptb-tertiary ptb-small ptb-grouped', {y_align: Clutter.ActorAlign.END});
        this._todayCost = label('', 'ptb-secondary ptb-today-cost', {y_align: Clutter.ActorAlign.END});
        deps.tooltip.attach(this._todayCost, () => this._todayCostHint);
        big.add_child(this._big);
        big.add_child(this._grouped);
        big.add_child(spacer());
        big.add_child(this._todayCost);
        this.actor.add_child(big);

        // This week / this month as a two-row table with aligned columns
        // (the macOS row falls back to a vertical layout when it does not fit).
        const periods = vbox('ptb-period-table', {x_expand: true});
        this._week = this._period(periods, _('This week'));
        this._month = this._period(periods, _('This month'));
        this.actor.add_child(periods);

        const breakdown = vbox('ptb-breakdown');
        this._breakdown1 = label('', 'ptb-tertiary ptb-small');
        this._breakdown2 = label('', 'ptb-tertiary ptb-small');
        breakdown.add_child(this._breakdown1);
        breakdown.add_child(this._breakdown2);
        this.actor.add_child(breakdown);

        const caption = hbox('ptb-chart-caption', {x_expand: true});
        this._windowButton = new St.Button({
            style_class: 'ptb-window-button',
            can_focus: true,
            y_align: Clutter.ActorAlign.CENTER,
        });
        const wbBox = hbox('ptb-window-button-box');
        this._windowLabel = label('', 'ptb-window-label');
        wbBox.add_child(this._windowLabel);
        wbBox.add_child(new St.Icon({icon_name: 'pan-down-symbolic', style_class: 'ptb-window-icon'}));
        this._windowButton.set_child(wbBox);
        this._windowButton.connect('clicked', () => this._deps.setTrendWindow(ChartModel.nextWindow(this._window)));
        deps.tooltip.attach(this._windowButton, _('Change the period'));
        caption.add_child(this._windowButton);
        this._readout = label('', 'ptb-readout');
        caption.add_child(this._readout);
        caption.add_child(spacer());
        this._peakCaption = label(_('Peak'), 'ptb-tertiary ptb-tiny');
        this._peakValue = label('', 'ptb-peak-value');
        caption.add_child(this._peakCaption);
        caption.add_child(this._peakValue);
        this.actor.add_child(caption);

        this._chart = new DailyChart(deps.tooltip);
        this.actor.add_child(this._chart);

        this._window = '14d';
        this._todayCostHint = null;
        this._streakHint = null;
    }

    _period(table, title) {
        const row = hbox('ptb-period');
        row.add_child(label(title, 'ptb-tertiary ptb-small ptb-period-title'));
        const value = label('', 'ptb-period-value');
        const cost = label('', 'ptb-secondary ptb-small ptb-period-cost');
        row.add_child(value);
        row.add_child(cost);
        table.add_child(row);
        return {value, cost};
    }

    /** @param {object} ctx  {state, settings} */
    update(ctx) {
        const state = ctx.state;
        const combined = state?.usage?.combined;
        const today = combined?.today;
        const showCost = V.showsCost(state);

        const tokens = today?.tokens ?? 0;
        this._big.text = compact(tokens);
        this._grouped.text = tokens >= 1000 ? grouped(tokens) : '';
        this._grouped.visible = tokens >= 1000;
        this._todayCost.visible = showCost;
        this._todayCost.text = showCost ? costText(today?.cost) : '';
        this._todayCostHint = normalizeCoverage(today?.cost?.coverage) === 'partial' ? PARTIAL_HINT() : null;

        for (const [slot, totals] of [[this._week, combined?.week], [this._month, combined?.month]]) {
            slot.value.text = compact(totals?.tokens ?? 0);
            slot.cost.text = showCost ? costText(totals?.cost) : '';
            slot.cost.visible = showCost;
        }

        const streak = combined?.streak;
        if (streak && streak.days >= 2) {
            this._streak.text = `🔥 ${streak.days}`;
            this._streak.opacity = streak.includesToday ? 255 : 110;
            this._streakHint = streak.includesToday
                ? fmt(_('%d-day streak'), streak.days)
                : fmt(_('%d-day streak — use it today to keep it going'), streak.days);
            this._streak.show();
        } else {
            this._streak.hide();
        }

        this._breakdown1.text = fmt(_('Input %s   Output %s'), compact(today?.input ?? 0), compact(today?.output ?? 0));
        this._breakdown2.text = fmt(_('Cache write %s   Cache read %s'),
            compact(today?.cacheWrite ?? 0), compact(today?.cacheRead ?? 0));

        this._window = ChartModel.normalizeWindow(ctx.settings.trendWindow);
        this._windowLabel.text = ChartModel.windowTitle(this._window);
        const dayKey = state?.usage?.calendar?.today ?? '';
        const cols = combined && dayKey ? ChartModel.columns(combined, dayKey, this._window) : [];
        const peak = ChartModel.peak(cols);
        this._readout.text = ChartModel.readout(cols, dayKey, false); // cost is on the big row
        const showsPeak = ChartModel.showsPeak(cols);
        this._peakCaption.visible = showsPeak;
        this._peakValue.visible = showsPeak;
        this._peakValue.text = compact(peak);
        // Nothing used in this window: keep the caption (the period can still change), drop the bars.
        this._chart.visible = peak > 0;
        this._chart.setData(cols, dayKey, this._window, showCost);
    }
}
