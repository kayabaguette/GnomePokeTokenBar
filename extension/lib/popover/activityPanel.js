/* Activity panel (recap-heatmap.md §2.7, ui.md §2.1 C.4): the daily bar
 * chart or the 16-week heatmap, one at a time, under one caption row.
 *
 * A click anywhere on the panel (chart, caption, gaps) switches the view and
 * writes `activity-view`; two page dots at the right end of the caption are
 * the only hint that it cycles. The buttons inside (period picker, dots) keep
 * their own clicks: the panel's gesture refuses presses that start on them.
 * With nothing used in 16 weeks the heatmap draws no grid but keeps its
 * caption and the click target, so the user can always cycle back (§6 Q1).
 */

import Clutter from 'gi://Clutter';
import St from 'gi://St';

import {_} from '../i18n.js';
import {compact} from '../format.js';
import * as ChartModel from '../chartModel.js';
import * as HeatModel from '../heatmapModel.js';
import {DailyChart} from '../chart.js';
import {HeatmapGrid} from '../heatmap.js';
import {hbox, label, spacer, vbox} from './widgets.js';

function inside(actor, x, y) {
    if (!actor.mapped)
        return false;
    const [ax, ay] = actor.get_transformed_position();
    const [w, h] = actor.get_transformed_size();
    return x >= ax && x < ax + w && y >= ay && y < ay + h;
}

export class ActivityPanel {
    /**
     * @param {object} deps  {tooltip, setTrendWindow(window), setActivityView(view)}
     */
    constructor(deps) {
        this._deps = deps;
        this.actor = vbox('ptb-activity', {x_expand: true, reactive: true});

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
        this._windowButton.connect('clicked',
            () => this._deps.setTrendWindow(ChartModel.nextWindow(this._window)));
        deps.tooltip.attach(this._windowButton, _('Change the period'));
        caption.add_child(this._windowButton);
        this._heatTitle = label(HeatModel.heatmapTitle(), 'ptb-heatmap-title');
        caption.add_child(this._heatTitle);
        this._readout = label('', 'ptb-readout');
        caption.add_child(this._readout);
        caption.add_child(spacer());
        this._peakCaption = label(_('Peak'), 'ptb-tertiary ptb-tiny');
        this._peakValue = label('', 'ptb-peak-value');
        caption.add_child(this._peakCaption);
        caption.add_child(this._peakValue);

        // The dots are a button too, so the cycle is reachable from the keyboard.
        this._dotsButton = new St.Button({
            style_class: 'ptb-page-dots-button',
            can_focus: true,
            y_align: Clutter.ActorAlign.CENTER,
            accessible_name: _('Click to switch the view'),
        });
        const dots = hbox('ptb-page-dots');
        this._dots = HeatModel.ACTIVITY_VIEWS.map(() => {
            const dot = new St.Widget({style_class: 'ptb-page-dot', y_align: Clutter.ActorAlign.CENTER});
            dots.add_child(dot);
            return dot;
        });
        this._dotsButton.set_child(dots);
        this._dotsButton.connect('clicked', () => this._cycle());
        caption.add_child(this._dotsButton);
        this.actor.add_child(caption);

        this._chart = new DailyChart(deps.tooltip);
        this.actor.add_child(this._chart);
        this._heatmap = new HeatmapGrid(date => {
            if (date)
                deps.tooltip.hide(this.actor); // the readout is the hover feedback
            this._renderHeatReadout();
        });
        this.actor.add_child(this._heatmap);

        this._click = new Clutter.ClickGesture();
        this._click.set_required_button(Clutter.BUTTON_PRIMARY);
        this._click.connect('may-recognize', () => {
            const {x, y} = this._click.get_coords_abs();
            return !inside(this._windowButton, x, y) && !inside(this._dotsButton, x, y);
        });
        this._click.connect('recognize', () => this._cycle());
        this.actor.add_action(this._click);
        // The hint gives way to the bar tooltip and to the heatmap readout.
        deps.tooltip.attach(this.actor, () => {
            if ((this._chart.mapped && this._chart._hovered >= 0) || this._heatmap.hoveredDate)
                return null;
            return _('Click to switch the view');
        });

        this._view = 'bars';
        this._window = '14d';
        this._grid = [];
        this._today = '';
        this._monthDaily = null;
    }

    _cycle() {
        this._view = HeatModel.nextActivityView(this._view);
        this._show();
        this._deps.setActivityView(this._view);
    }

    /** @param {object} ctx  {state, settings} */
    update(ctx) {
        const state = ctx.state;
        const combined = state?.usage?.combined;
        const today = state?.usage?.calendar?.today ?? '';
        this._today = today;
        this._view = HeatModel.normalizeActivityView(ctx.settings.activityView);

        this._window = ChartModel.normalizeWindow(ctx.settings.trendWindow);
        this._windowLabel.text = ChartModel.windowTitle(this._window);
        const cols = combined && today ? ChartModel.columns(combined, today, this._window) : [];
        this._bars = {
            readout: ChartModel.readout(cols, today, false), // no cost in readouts (protocol §14 item 20)
            peak: ChartModel.peak(cols),
            showsPeak: ChartModel.showsPeak(cols),
        };
        this._chart.setData(cols, today, this._window, false);

        this._grid = HeatModel.buildGrid(combined?.heatmap, today);
        this._monthDaily = combined?.monthDaily ?? null;
        this._heatPeak = HeatModel.peakOf(this._grid);
        this._heatmap.setData(this._grid, today, HeatModel.weekdayLabels(combined?.heatmap?.start));
        this._show();
    }

    _show() {
        const heat = this._view === 'heatmap';
        this._windowButton.visible = !heat;
        this._heatTitle.visible = heat;
        // Nothing used in the window: keep the caption (and the click target), drop the drawing.
        this._chart.visible = !heat && this._bars?.peak > 0;
        this._heatmap.visible = heat && this._heatPeak > 0;
        const showsPeak = heat ? HeatModel.showsPeak(this._grid) : this._bars?.showsPeak;
        this._peakCaption.visible = Boolean(showsPeak);
        this._peakValue.visible = Boolean(showsPeak);
        this._peakValue.text = compact(heat ? this._heatPeak : this._bars?.peak ?? 0);
        HeatModel.ACTIVITY_VIEWS.forEach((v, i) => {
            if (v === this._view)
                this._dots[i].add_style_pseudo_class('checked');
            else
                this._dots[i].remove_style_pseudo_class('checked');
        });
        if (heat)
            this._renderHeatReadout();
        else
            this._readout.text = this._bars?.readout ?? '';
    }

    /** The readout follows the pointer over the heatmap and falls back to today. */
    _renderHeatReadout() {
        if (this._view !== 'heatmap')
            return;
        const day = this._heatmap.hoveredDate ?? this._today;
        this._readout.text = HeatModel.readout(this._grid, day, this._monthDaily);
    }
}
