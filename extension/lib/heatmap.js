/* 16-week activity heatmap: weekday letters and cells in one Cairo repaint
 * (recap-heatmap.md §2). Geometry comes from heatmapModel.js; colors come
 * from the theme node because Cairo ignores CSS:
 *   -ptb-heat-0 … -ptb-heat-4, -ptb-heat-today-color, -ptb-heat-label-color.
 * There is no popup tooltip (the cells are too small): hovering a cell
 * reports its day to the caption readout through `onHover(date | null)`.
 */

import Clutter from 'gi://Clutter';
import GObject from 'gi://GObject';
import Pango from 'gi://Pango';
import PangoCairo from 'gi://PangoCairo';
import St from 'gi://St';

import * as Model from './heatmapModel.js';

function themeColor(node, name) {
    const [found, color] = node.lookup_color(name, false);
    return found ? color : node.get_foreground_color();
}

function roundedRect(cr, x, y, w, h, r) {
    const rr = Math.min(r, w / 2, h / 2);
    cr.newSubPath();
    cr.arc(x + w - rr, y + rr, rr, -Math.PI / 2, 0);
    cr.arc(x + w - rr, y + h - rr, rr, 0, Math.PI / 2);
    cr.arc(x + rr, y + h - rr, rr, Math.PI / 2, Math.PI);
    cr.arc(x + rr, y + rr, rr, Math.PI, 1.5 * Math.PI);
    cr.closePath();
}

export const HeatmapGrid = GObject.registerClass(
class PtbHeatmapGrid extends St.DrawingArea {
    /** @param {(date: string|null) => void} onHover */
    _init(onHover) {
        super._init({
            style_class: 'ptb-heatmap',
            x_expand: true,
            height: Model.HEIGHT,
            reactive: true,
        });
        this._onHover = onHover;
        this._grid = [];
        this._labels = [];
        this._today = '';
        this._peak = 0;
        this._hovered = null;

        const motion = new Clutter.MotionController();
        motion.connect('motion', (_c, _s, x, y) => this._onMotion(x, y));
        motion.connect('leave', () => this._setHovered(null));
        this.add_action(motion);
        this.connect('style-changed', () => this.queue_repaint());
        this.connect('notify::mapped', () => {
            if (!this.mapped)
                this._setHovered(null);
        });
    }

    get hoveredDate() {
        return this._hovered;
    }

    /**
     * @param {Array} grid        heatmapModel.buildGrid()
     * @param {string} today
     * @param {string[]} labels   heatmapModel.weekdayLabels()
     */
    setData(grid, today, labels) {
        this._grid = grid;
        this._today = today;
        this._labels = labels;
        this._peak = Model.peakOf(grid);
        if (this._hovered && !Model.cellFor(grid, this._hovered))
            this._setHovered(null);
        this.queue_repaint();
    }

    /** @param {number} x  @param {number} y  actor-relative */
    _onMotion(x, y) {
        const at = Model.cellAt(x, y, this.width);
        const cell = at ? this._grid[at.col]?.[at.row] : null;
        // Outside-range cells are no hover target: the readout stays on today.
        this._setHovered(cell && !cell.outside ? cell.date : null);
    }

    _setHovered(date) {
        if (date === this._hovered)
            return;
        this._hovered = date;
        this._onHover?.(date);
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
        if (this._grid.length === 0 || !(this._peak > 0))
            return;
        const node = this.get_theme_node();
        const [width] = this.get_surface_size();
        const levels = [];
        for (let l = 0; l < Model.LEVEL_COUNT; l++)
            levels.push(themeColor(node, Model.levelColorProperty(l)));
        const todayColor = themeColor(node, '-ptb-heat-today-color');
        const labelColor = themeColor(node, '-ptb-heat-label-color');

        const layout = PangoCairo.create_layout(cr);
        layout.set_font_description(node.get_font());
        layout.set_alignment(Pango.Alignment.CENTER);
        cr.setSourceColor(labelColor);
        this._labels.forEach((text, row) => {
            layout.set_text(text, -1);
            const [, logical] = layout.get_pixel_extents();
            const y = row * (Model.CELL_HEIGHT + Model.GAP) + (Model.CELL_HEIGHT - logical.height) / 2;
            cr.moveTo((Model.LABEL_WIDTH - logical.width) / 2, y);
            PangoCairo.show_layout(cr, layout);
        });

        this._grid.forEach((cells, col) => {
            cells.forEach((cell, row) => {
                if (cell.outside)
                    return; // level -1: holds its place, draws nothing
                const r = Model.cellRect(col, row, width);
                cr.setSourceColor(levels[Model.level(cell.tokens, this._peak)]);
                roundedRect(cr, r.x, r.y, r.width, r.height, Model.RADIUS);
                cr.fill();
                if (cell.date === this._today) {
                    cr.setSourceColor(todayColor);
                    cr.setLineWidth(1);
                    roundedRect(cr, r.x + 0.5, r.y + 0.5, r.width - 1, r.height - 1, Model.RADIUS - 0.5);
                    cr.stroke();
                }
            });
        });
    }
});
