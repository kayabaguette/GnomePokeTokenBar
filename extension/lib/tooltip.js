/* One shared hover tooltip (St has none; ui.md §7).
 *
 * attach(actor, text) shows `text` after a 600 ms hover (the macOS `.help`
 * delay), below the pointer and clamped to the monitor. Charts call
 * showAt()/hide() directly to show a card without delay. The label lives in
 * Main.uiGroup, raised above the open menu each time it is shown.
 */

import GLib from 'gi://GLib';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';

const HOVER_DELAY_MS = 600;
const POINTER_GAP = 18;
const EDGE = 6;

export class Tooltip {
    constructor() {
        this._label = new St.Label({
            style_class: 'ptb-tooltip',
            visible: false,
            reactive: false,
        });
        this._label.clutter_text.line_wrap = true;
        Main.uiGroup.add_child(this._label);
        this._timerId = 0;
        this._owner = null;
        this._destroyed = false;
    }

    /** The label actor (for light/dark class tracking). */
    get actor() {
        return this._label;
    }

    /**
     * Show `text` when the pointer rests on `actor`. `text` may be a function
     * evaluated at show time; a null/empty result shows nothing.
     *
     * @param {St.Widget} actor
     * @param {string|(() => string|null)} text
     */
    attach(actor, text) {
        actor.reactive = true;
        actor.track_hover = true;
        actor._ptbTooltipText = text;
        if (actor._ptbTooltipAttached)
            return;
        actor._ptbTooltipAttached = true;
        actor.connect('notify::hover', () => {
            if (this._destroyed)
                return;
            if (actor.hover)
                this._schedule(actor);
            else if (this._owner === actor)
                this.hide();
        });
        actor.connect('destroy', () => {
            if (!this._destroyed && this._owner === actor)
                this.hide();
        });
    }

    /** Change an attached actor's text (shown text updates on the next hover). */
    setText(actor, text) {
        actor._ptbTooltipText = text;
        if (this._owner === actor && this._label.visible) {
            const value = resolve(text);
            if (value)
                this._label.text = value;
            else
                this.hide();
        }
    }

    _schedule(actor) {
        this._cancelTimer();
        this._owner = actor;
        this._timerId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, HOVER_DELAY_MS, () => {
            this._timerId = 0;
            if (!this._destroyed && actor.hover && actor.mapped) {
                const text = resolve(actor._ptbTooltipText);
                if (text) {
                    const [x, y] = global.get_pointer();
                    this._showText(text, x, y + POINTER_GAP, false);
                }
            }
            return GLib.SOURCE_REMOVE;
        });
    }

    /**
     * Show immediately at stage coordinates. With `above`, (x, y) is the
     * bottom centre of the tooltip; else its top-left corner, centred on x.
     */
    showAt(owner, text, x, y, above = true) {
        if (this._destroyed)
            return;
        this._cancelTimer();
        this._owner = owner;
        this._showText(text, x, y, above);
    }

    _showText(text, x, y, above) {
        const label = this._label;
        label.text = text;
        Main.uiGroup.set_child_above_sibling(label, null);
        label.show();
        const [, natW] = label.get_preferred_width(-1);
        const [, natH] = label.get_preferred_height(natW);
        const monitor = Main.layoutManager.findMonitorForPoint?.(x, y) ?? Main.layoutManager.primaryMonitor;
        let left = Math.round(x - natW / 2);
        let top = Math.round(above ? y - natH : y);
        if (monitor) {
            left = Math.max(monitor.x + EDGE, Math.min(left, monitor.x + monitor.width - natW - EDGE));
            top = Math.max(monitor.y + EDGE, Math.min(top, monitor.y + monitor.height - natH - EDGE));
        }
        label.set_position(left, top);
    }

    hide(owner = null) {
        if (owner && owner !== this._owner)
            return;
        this._cancelTimer();
        this._owner = null;
        this._label.hide();
    }

    _cancelTimer() {
        if (this._timerId) {
            GLib.source_remove(this._timerId);
            this._timerId = 0;
        }
    }

    destroy() {
        this._destroyed = true;
        this._cancelTimer();
        this._owner = null;
        this._label.destroy();
        this._label = null;
    }
}

function resolve(text) {
    const value = typeof text === 'function' ? text() : text;
    return typeof value === 'string' && value.length > 0 ? value : null;
}
