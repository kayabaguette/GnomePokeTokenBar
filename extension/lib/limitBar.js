/* Progress bar with an optional pace marker, drawn in one Cairo repaint.
 *
 * Used by the official-limit rows (tier-colored fill + pace tick,
 * PopoverView.swift:1895-1940) and by the companion growth bar. Colors come
 * from the theme node, since Cairo ignores CSS:
 *   color               fill (set per tier class, e.g. .ptb-tier-over)
 *   -ptb-track-color    track
 *   -ptb-marker-color   pace tick
 */

import GObject from 'gi://GObject';
import St from 'gi://St';

const TRACK_HEIGHT = 6;
const MARKER_WIDTH = 2.5;
const MARKER_OVERHANG = 2;
const MARKER_HEIGHT = TRACK_HEIGHT + MARKER_OVERHANG * 2;

function roundedRect(cr, x, y, w, h, r) {
    const rr = Math.min(r, w / 2, h / 2);
    cr.newSubPath();
    cr.arc(x + w - rr, y + rr, rr, -Math.PI / 2, 0);
    cr.arc(x + w - rr, y + h - rr, rr, 0, Math.PI / 2);
    cr.arc(x + rr, y + h - rr, rr, Math.PI / 2, Math.PI);
    cr.arc(x + rr, y + rr, rr, Math.PI, 1.5 * Math.PI);
    cr.closePath();
}

function themeColor(node, name) {
    const [found, color] = node.lookup_color(name, false);
    return found ? color : null;
}

export const LimitBar = GObject.registerClass(
class PtbLimitBar extends St.DrawingArea {
    _init(params = {}) {
        super._init({
            style_class: 'ptb-limit-bar',
            x_expand: true,
            height: MARKER_HEIGHT,
            ...params,
        });
        this._fraction = 0;
        this._marker = null;
        this.connect('style-changed', () => this.queue_repaint());
    }

    /**
     * @param {number} fraction  fill, 0…1 (clamped)
     * @param {number|null} marker  pace tick position 0…1, or null for none
     */
    setValue(fraction, marker = null) {
        const f = Math.max(0, Math.min(1, Number(fraction) || 0));
        const m = typeof marker === 'number' && marker >= 0 && marker <= 1 ? marker : null;
        if (f === this._fraction && m === this._marker)
            return;
        this._fraction = f;
        this._marker = m;
        this.queue_repaint();
    }

    vfunc_repaint() {
        const cr = this.get_context();
        try {
            const node = this.get_theme_node();
            const [w, h] = this.get_surface_size();
            const y0 = (h - TRACK_HEIGHT) / 2;

            const track = themeColor(node, '-ptb-track-color');
            if (track) {
                cr.setSourceColor(track);
                roundedRect(cr, 0, y0, w, TRACK_HEIGHT, TRACK_HEIGHT / 2);
                cr.fill();
            }

            if (this._fraction > 0) {
                cr.save();
                roundedRect(cr, 0, y0, w, TRACK_HEIGHT, TRACK_HEIGHT / 2);
                cr.clip();
                cr.setSourceColor(node.get_foreground_color());
                cr.rectangle(0, y0, Math.max(TRACK_HEIGHT / 2, w * this._fraction), TRACK_HEIGHT);
                cr.fill();
                cr.restore();
            }

            if (this._marker !== null) {
                const marker = themeColor(node, '-ptb-marker-color');
                if (marker) {
                    cr.setSourceColor(marker);
                    const x = (w - MARKER_WIDTH) * this._marker;
                    roundedRect(cr, x, (h - MARKER_HEIGHT) / 2, MARKER_WIDTH, MARKER_HEIGHT, MARKER_WIDTH / 2);
                    cr.fill();
                }
            }
        } finally {
            cr.$dispose();
        }
    }
});
