/* Daily bar chart: bars, weekend ticks and axis labels in one Cairo repaint
 * (ui.md §2.3). Geometry and labels come from chartModel.js; colors come
 * from the theme node because Cairo ignores CSS:
 *   -ptb-bar-color, -ptb-bar-empty-color, -ptb-bar-hover-color,
 *   -ptb-today-color, -ptb-tick-color, -ptb-label-color, -ptb-future-color.
 * Hovering a column shows the day's total in the shared tooltip, at once.
 */

import Clutter from 'gi://Clutter';
import GObject from 'gi://GObject';
import Pango from 'gi://Pango';
import PangoCairo from 'gi://PangoCairo';
import St from 'gi://St';

import * as Model from './chartModel.js';

const TICK_GAP = 3;
const LABEL_GAP = 2;
const LABEL_HEIGHT = 12;
export const CHART_HEIGHT = Model.TRACK + TICK_GAP + Model.TICK + LABEL_GAP + LABEL_HEIGHT;

function themeColor(node, name) {
    const [found, color] = node.lookup_color(name, false);
    return found ? color : node.get_foreground_color();
}

export const DailyChart = GObject.registerClass(
class PtbDailyChart extends St.DrawingArea {
    /**
     * @param {import('./tooltip.js').Tooltip} tooltip
     */
    _init(tooltip) {
        super._init({
            style_class: 'ptb-chart',
            x_expand: true,
            height: CHART_HEIGHT,
            reactive: true,
        });
        this._tooltip = tooltip;
        this._columns = [];
        this._today = '';
        this._window = '14d';
        this._showsCost = false;
        this._peak = 0;
        this._hovered = -1;

        // MotionController reports actor-relative coordinates (checked in
        // the sandbox: a pointer 204 px into the chart reports x = 204.6).
        const motion = new Clutter.MotionController();
        motion.connect('motion', (_c, _s, x, y) => this._onMotion(x, y));
        motion.connect('leave', () => this._setHovered(-1));
        this.add_action(motion);
        this.connect('style-changed', () => this.queue_repaint());
        this.connect('notify::mapped', () => {
            if (!this.mapped)
                this._setHovered(-1);
        });
    }

    /**
     * @param {object[]} columns  chartModel.columns()
     * @param {string} today
     * @param {string} window
     * @param {boolean} showsCost
     */
    setData(columns, today, window, showsCost) {
        this._columns = columns;
        this._today = today;
        this._window = window;
        this._showsCost = showsCost;
        this._peak = Model.peak(columns);
        if (this._hovered >= columns.length)
            this._hovered = -1;
        this.queue_repaint();
        if (this._hovered >= 0)
            this._showTooltip();
    }

    /** @param {number} x  @param {number} y  actor-relative */
    _onMotion(x, y) {
        if (y > Model.TRACK + TICK_GAP) {
            this._setHovered(-1);
            return;
        }
        let i = Model.columnAt(x, this._columns.length, this.width);
        if (i >= 0 && this._columns[i].isFuture)
            i = -1; // future days are no hover target
        this._setHovered(i);
    }

    _setHovered(index) {
        if (index === this._hovered)
            return;
        this._hovered = index;
        this.queue_repaint();
        if (index < 0)
            this._tooltip.hide(this);
        else
            this._showTooltip();
    }

    _showTooltip() {
        const col = this._columns[this._hovered];
        if (!col || !this.mapped)
            return;
        const rect = Model.barRect(this._hovered, this._columns.length, this.width);
        const [sx, sy] = this.get_transformed_position();
        const scale = this.get_transformed_size()[0] / Math.max(1, this.width);
        const centerX = sx + (rect.x + rect.width / 2) * scale;
        this._tooltip.showAt(this, Model.dayText(col, this._showsCost), centerX, sy - 4, true);
    }

    vfunc_repaint() {
        const cr = this.get_context();
        try {
            this._draw(cr);
        } finally {
            cr.$dispose();
        }
    }

    _draw(cr) {
        const node = this.get_theme_node();
        const [width] = this.get_surface_size();
        const cols = this._columns;
        const n = cols.length;
        if (n === 0)
            return;

        const bar = themeColor(node, '-ptb-bar-color');
        const empty = themeColor(node, '-ptb-bar-empty-color');
        const hover = themeColor(node, '-ptb-bar-hover-color');
        const today = themeColor(node, '-ptb-today-color');
        const tick = themeColor(node, '-ptb-tick-color');
        const labelColor = themeColor(node, '-ptb-label-color');
        const future = themeColor(node, '-ptb-future-color');

        const tickY = Model.TRACK + TICK_GAP;
        const labelY = tickY + Model.TICK + LABEL_GAP;

        const layout = PangoCairo.create_layout(cr);
        layout.set_font_description(node.get_font());
        layout.set_alignment(Pango.Alignment.CENTER);

        cols.forEach((col, i) => {
            const {x, width: w} = Model.barRect(i, n, width);
            const isToday = col.date === this._today;

            if (!col.isFuture) {
                const h = Model.barHeight(col.tokens, this._peak);
                let color = bar;
                if (isToday)
                    color = today;
                else if (i === this._hovered)
                    color = hover;
                else if (col.tokens === 0)
                    color = empty;
                cr.setSourceColor(color);
                const r = Math.min(1, w / 2, h / 2);
                const y = Model.TRACK - h;
                cr.newSubPath();
                cr.arc(x + w - r, y + r, r, -Math.PI / 2, 0);
                cr.lineTo(x + w, Model.TRACK);
                cr.lineTo(x, Model.TRACK);
                cr.arc(x + r, y + r, r, Math.PI, 1.5 * Math.PI);
                cr.closePath();
                cr.fill();
            }

            if (Model.showsWeekendTick(col)) {
                cr.setSourceColor(tick);
                cr.rectangle(x, tickY, w, Model.TICK);
                cr.fill();
            }

            const label = Model.axisLabel(col.date, this._today, this._window);
            if (label !== null) {
                let color = labelColor;
                if (isToday)
                    color = today;
                else if (col.isFuture)
                    color = future;
                layout.set_text(label, -1);
                const [, logical] = layout.get_pixel_extents();
                cr.setSourceColor(color);
                cr.moveTo(x + w / 2 - logical.width / 2, labelY);
                PangoCairo.show_layout(cr, layout);
            }
        });
    }
});
